// Package cash follows money from the register to the bank: expected tenders
// from the POS, drawer counts and over/short, deposits and bank matching, and
// reconciliation of card and platform settlements.
package cash

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/ingest"
	"github.com/selectdev/purros/api/internal/refs"
	"github.com/shopspring/decimal"
)

const (
	feature = "cash"
	tag     = "Cash"
)

// ---------------------------------------------------------------------------
// Ingestion
// ---------------------------------------------------------------------------

type TenderInput struct {
	ExternalID string `json:"externalId" validate:"required,extid"`
	refs.LocationRef
	BusinessDate httpx.Date       `json:"businessDate" validate:"required"`
	RegisterID   string           `json:"registerId,omitempty" validate:"max=100"`
	Type         string           `json:"type" validate:"required,oneof=cash card digital_wallet gift_card voucher account platform other"`
	Platform     string           `json:"platform,omitempty" validate:"max=100"`
	Amount       *decimal.Decimal `json:"amount" validate:"required"`
	Currency     string           `json:"currency,omitempty" validate:"omitempty,currency"`
}

type TenderBatch struct {
	Source  string        `json:"source" validate:"required"`
	Tenders []TenderInput `json:"tenders" validate:"required"`
}

type SettlementInput struct {
	ExternalID         string           `json:"externalId" validate:"required,extid"`
	LocationID         string           `json:"locationId,omitempty"`
	LocationExternalID string           `json:"locationExternalId,omitempty"`
	Provider           string           `json:"provider" validate:"required,max=100" doc:"Card processor or platform name"`
	TenderType         string           `json:"tenderType" validate:"required,oneof=card digital_wallet gift_card voucher platform other"`
	BusinessDate       httpx.Date       `json:"businessDate" validate:"required" doc:"The sales day the payout covers"`
	Gross              *decimal.Decimal `json:"gross" validate:"required"`
	Fees               *decimal.Decimal `json:"fees,omitempty"`
	Net                *decimal.Decimal `json:"net,omitempty" doc:"Defaults to gross − fees"`
	Currency           string           `json:"currency" validate:"required,currency"`
	PaidOn             *httpx.Date      `json:"paidOn,omitempty"`
}

type SettlementBatch struct {
	Source      string            `json:"source" validate:"required"`
	Settlements []SettlementInput `json:"settlements" validate:"required"`
}

type BankTxnInput struct {
	ExternalID string           `json:"externalId" validate:"required,extid"`
	Account    string           `json:"account,omitempty" validate:"max=100"`
	BookedOn   httpx.Date       `json:"bookedOn" validate:"required"`
	Amount     *decimal.Decimal `json:"amount" validate:"required" doc:"Positive for money in"`
	Reference  string           `json:"reference,omitempty" validate:"max=500" doc:"Bank reference; include the deposit bag number to help matching"`
	Currency   string           `json:"currency" validate:"required,currency"`
}

type BankTxnBatch struct {
	Source       string         `json:"source" validate:"required"`
	Transactions []BankTxnInput `json:"transactions" validate:"required"`
}

// ---------------------------------------------------------------------------
// Counts, deposits, business days
// ---------------------------------------------------------------------------

type CountInput struct {
	refs.LocationRef
	BusinessDate  httpx.Date       `json:"businessDate" validate:"required"`
	RegisterID    string           `json:"registerId,omitempty" validate:"max=100"`
	Kind          string           `json:"kind" validate:"required,oneof=open skim shift_change close safe" doc:"open = starting float; skim = cash moved to the safe"`
	Counted       *decimal.Decimal `json:"counted" validate:"required"`
	Denominations map[string]int   `json:"denominations,omitempty" doc:"e.g. {\"20.00\": 12, \"5.00\": 4}"`
	CountedBy     string           `json:"countedBy,omitempty" validate:"max=100"`
	Note          string           `json:"note,omitempty" validate:"max=500"`
}

type Count struct {
	ID            string           `json:"id"`
	LocationID    string           `json:"locationId"`
	BusinessDate  httpx.Date       `json:"businessDate"`
	RegisterID    string           `json:"registerId"`
	Kind          string           `json:"kind"`
	Counted       decimal.Decimal  `json:"counted"`
	Expected      *decimal.Decimal `json:"expected" doc:"For shift_change and close counts: float + cash sales − skims"`
	OverShort     *decimal.Decimal `json:"overShort"`
	Denominations map[string]int   `json:"denominations"`
	CountedBy     *string          `json:"countedBy"`
	Note          *string          `json:"note"`
	CreatedAt     time.Time        `json:"createdAt"`
}

type DepositInput struct {
	refs.LocationRef
	BusinessDate httpx.Date       `json:"businessDate" validate:"required"`
	BagNumber    string           `json:"bagNumber,omitempty" validate:"max=50"`
	Amount       *decimal.Decimal `json:"amount" validate:"required"`
	Currency     string           `json:"currency,omitempty" validate:"omitempty,currency"`
}

type Deposit struct {
	ID                string           `json:"id"`
	LocationID        string           `json:"locationId"`
	BusinessDate      httpx.Date       `json:"businessDate"`
	BagNumber         *string          `json:"bagNumber"`
	Amount            decimal.Decimal  `json:"amount"`
	Currency          string           `json:"currency"`
	Status            string           `json:"status" doc:"recorded, verified or mismatch"`
	BankTransactionID *string          `json:"bankTransactionId"`
	Difference        *decimal.Decimal `json:"difference"`
	CreatedAt         time.Time        `json:"createdAt"`
	VerifiedAt        *time.Time       `json:"verifiedAt"`
}

type TenderTotal struct {
	Type   string          `json:"type"`
	Amount decimal.Decimal `json:"amount"`
}

type BusinessDay struct {
	LocationID   string          `json:"locationId"`
	BusinessDate httpx.Date      `json:"businessDate"`
	Status       string          `json:"status" doc:"open or closed"`
	Tenders      []TenderTotal   `json:"tenders"`
	CashExpected decimal.Decimal `json:"cashExpected" doc:"Cash tenders for the day"`
	OverShort    decimal.Decimal `json:"overShort" doc:"Sum of over/short from closing counts"`
	Deposited    decimal.Decimal `json:"deposited"`
	Settled      decimal.Decimal `json:"settled" doc:"Card and platform settlements (gross)"`
	NonCash      decimal.Decimal `json:"nonCash" doc:"Card and platform tenders from the POS"`
	Unsettled    decimal.Decimal `json:"unsettled" doc:"nonCash − settled"`
	ClosedAt     *time.Time      `json:"closedAt"`
}

const countCols = `id, location_id, business_date, register_id, kind, counted, expected, over_short, coalesce(denominations, '{}'), counted_by, note, created_at`

func scanCount(r pgx.Row) (Count, error) {
	var c Count
	var d time.Time
	err := r.Scan(&c.ID, &c.LocationID, &d, &c.RegisterID, &c.Kind, &c.Counted, &c.Expected, &c.OverShort, &c.Denominations, &c.CountedBy, &c.Note, &c.CreatedAt)
	c.BusinessDate = httpx.NewDate(d)
	return c, err
}

const depCols = `id, location_id, business_date, bag_number, amount, currency, status, bank_transaction_id, difference, created_at, verified_at`

func scanDeposit(r pgx.Row) (Deposit, error) {
	var d Deposit
	var bd time.Time
	err := r.Scan(&d.ID, &d.LocationID, &bd, &d.BagNumber, &d.Amount, &d.Currency, &d.Status, &d.BankTransactionID, &d.Difference, &d.CreatedAt, &d.VerifiedAt)
	d.BusinessDate = httpx.NewDate(bd)
	return d, err
}

func tolerance(ctx context.Context, q db.Querier) decimal.Decimal {
	var t decimal.Decimal
	if err := q.QueryRow(ctx, `SELECT over_short_tolerance FROM company LIMIT 1`).Scan(&t); err != nil {
		return decimal.NewFromInt(5)
	}
	return t
}

// expectedCash = opening float + cash tenders − skims for a register and day.
func expectedCash(ctx context.Context, q db.Querier, loc string, day httpx.Date, register string) (decimal.Decimal, error) {
	var float, sales, skims decimal.Decimal
	err := q.QueryRow(ctx, `SELECT
		coalesce((SELECT counted FROM cash_counts WHERE location_id=$1 AND business_date=$2 AND register_id=$3 AND kind='open' ORDER BY created_at DESC LIMIT 1), 0),
		coalesce((SELECT sum(amount) FROM cash_tenders WHERE location_id=$1 AND business_date=$2 AND register_id=$3 AND tender_type='cash'), 0),
		coalesce((SELECT sum(counted) FROM cash_counts WHERE location_id=$1 AND business_date=$2 AND register_id=$3 AND kind='skim'), 0)`,
		loc, day, register).Scan(&float, &sales, &skims)
	return float.Add(sales).Sub(skims), err
}

func dayStatus(ctx context.Context, q db.Querier, loc string, day httpx.Date) (string, *time.Time, error) {
	var status string
	var closed *time.Time
	err := q.QueryRow(ctx, `SELECT status, closed_at FROM business_days WHERE location_id=$1 AND business_date=$2`, loc, day).Scan(&status, &closed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "open", nil, nil
	}
	return status, closed, err
}

// IsDayClosed reports whether a location's business day has been closed.
func IsDayClosed(ctx context.Context, q db.Querier, loc string, day httpx.Date) (bool, error) {
	s, _, err := dayStatus(ctx, q, loc, day)
	return s == "closed", err
}

func summarize(ctx context.Context, q db.Querier, loc string, day httpx.Date) (BusinessDay, error) {
	bd := BusinessDay{LocationID: loc, BusinessDate: day, Tenders: []TenderTotal{}}
	var err error
	bd.Status, bd.ClosedAt, err = dayStatus(ctx, q, loc, day)
	if err != nil {
		return bd, err
	}
	rows, err := q.Query(ctx, `SELECT tender_type, sum(amount) FROM cash_tenders WHERE location_id=$1 AND business_date=$2 GROUP BY 1 ORDER BY 1`, loc, day)
	if err != nil {
		return bd, err
	}
	for rows.Next() {
		var t TenderTotal
		if err := rows.Scan(&t.Type, &t.Amount); err != nil {
			rows.Close()
			return bd, err
		}
		bd.Tenders = append(bd.Tenders, t)
		if t.Type == "cash" {
			bd.CashExpected = bd.CashExpected.Add(t.Amount)
		} else {
			bd.NonCash = bd.NonCash.Add(t.Amount)
		}
	}
	rows.Close()
	err = q.QueryRow(ctx, `SELECT
		coalesce((SELECT sum(over_short) FROM cash_counts WHERE location_id=$1 AND business_date=$2 AND kind IN ('close', 'shift_change')), 0),
		coalesce((SELECT sum(amount) FROM bank_deposits WHERE location_id=$1 AND business_date=$2), 0),
		coalesce((SELECT sum(gross) FROM cash_settlements WHERE location_id=$1 AND business_date=$2), 0)`,
		loc, day).Scan(&bd.OverShort, &bd.Deposited, &bd.Settled)
	bd.Unsettled = bd.NonCash.Sub(bd.Settled)
	return bd, err
}

// matchDeposits tries to match unverified deposits with a bank transaction:
// same amount (within tolerance) booked within 7 days, preferring a reference
// that contains the bag number.
func matchDeposits(c *httpx.Ctx, tx pgx.Tx, btxID string, booked time.Time, amount decimal.Decimal, reference string) error {
	var dep Deposit
	var err error
	dep, err = scanDeposit(tx.QueryRow(c, `SELECT `+depCols+` FROM bank_deposits
		WHERE status <> 'verified' AND bank_transaction_id IS NULL AND business_date BETWEEN $1::date - 7 AND $1::date
		  AND abs(amount - $2) <= 1
		ORDER BY (bag_number IS NOT NULL AND $3 ILIKE '%' || bag_number || '%') DESC, abs(amount - $2), business_date DESC
		LIMIT 1 FOR UPDATE`, booked, amount, reference))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	diff := amount.Sub(dep.Amount)
	status, event := "verified", ""
	if !diff.IsZero() {
		status, event = "mismatch", "cash.deposit_mismatch"
	}
	after, err := scanDeposit(tx.QueryRow(c, `UPDATE bank_deposits SET status=$2, bank_transaction_id=$3, difference=$4, verified_at=now()
		WHERE id=$1 RETURNING `+depCols, dep.ID, status, btxID, diff))
	if err != nil {
		return err
	}
	return c.Record(tx, httpx.Change{Action: "deposit." + status, EventType: event, Feature: "cash.deposit_verification",
		EntityType: "bank_deposit", EntityID: dep.ID, LocationID: dep.LocationID, Before: dep, After: after})
}

type countList struct {
	Data []Count `json:"data"`
}

type depositList struct {
	Data []Deposit `json:"data"`
}

type dayList struct {
	Data []BusinessDay `json:"data"`
}

// Routes returns the cash routes.
func Routes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "POST", Path: "/cash/tenders", Tag: tag, Feature: feature, Scope: "cash:write", Ingest: true, Status: 202,
			Summary:     "Send tender totals per drawer or shift from the POS",
			Description: "Sets the expected cash for drawer counts and the card/platform totals to reconcile. Unique on (source, externalId).",
			Body:        TenderBatch{}, Response: httpx.BatchResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return ingest.Run(c, "cash_tenders", "tenders", func(e *ingest.Env, _ int, in TenderInput) (ingest.Outcome, error) {
					loc, err := e.Locs.Resolve(c, in.LocationID, in.LocationExternalID)
					if err != nil {
						return ingest.Outcome{}, ingest.Rejectf("location", "%s", err.Error())
					}
					currency := in.Currency
					if currency == "" {
						currency = loc.Currency
					}
					var warnings []httpx.FieldError
					if closed, err := IsDayClosed(c, e.Tx, loc.ID, in.BusinessDate); err != nil {
						return ingest.Outcome{}, err
					} else if closed {
						warnings = append(warnings, httpx.FieldError{Path: "businessDate", Message: "This business day is closed; the change is flagged for review"})
					}
					var id string
					var inserted bool
					err = e.Tx.QueryRow(c, `INSERT INTO cash_tenders (id, source, external_id, location_id, business_date, register_id, tender_type, platform, amount, currency)
						VALUES ($1,$2,$3,$4,$5,$6,$7,nullif($8,''),$9,$10)
						ON CONFLICT (source, external_id) DO UPDATE SET location_id=EXCLUDED.location_id, business_date=EXCLUDED.business_date,
							register_id=EXCLUDED.register_id, tender_type=EXCLUDED.tender_type, platform=EXCLUDED.platform,
							amount=EXCLUDED.amount, currency=EXCLUDED.currency, updated_at=now()
						RETURNING id, (xmax = 0)`,
						ids.New(ids.CashTender), e.Source, in.ExternalID, loc.ID, in.BusinessDate, in.RegisterID, in.Type, in.Platform, *in.Amount, currency).
						Scan(&id, &inserted)
					return ingest.Outcome{Status: ingest.Status(inserted), ID: id, Warnings: warnings}, err
				})
			},
		},
		{
			Method: "POST", Path: "/cash/settlements", Tag: tag, Feature: feature, Scope: "cash:write", Ingest: true, Status: 202,
			Summary:     "Send card processor and platform payouts",
			Description: "With tender reconciliation on, each settlement is compared with the POS tenders of the same type and day; differences raise cash.settlement_mismatch.",
			Body:        SettlementBatch{}, Response: httpx.BatchResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				reconcile, err := c.App.Features.IsEnabled(c, "cash.tender_reconciliation")
				if err != nil {
					return nil, err
				}
				return ingest.Run(c, "cash_settlements", "settlements", func(e *ingest.Env, _ int, in SettlementInput) (ingest.Outcome, error) {
					var locID *string
					if in.LocationID != "" || in.LocationExternalID != "" {
						loc, err := e.Locs.Resolve(c, in.LocationID, in.LocationExternalID)
						if err != nil {
							return ingest.Outcome{}, ingest.Rejectf("location", "%s", err.Error())
						}
						locID = &loc.ID
					}
					fees := decimal.Zero
					if in.Fees != nil {
						fees = *in.Fees
					}
					net := in.Gross.Sub(fees)
					if in.Net != nil {
						net = *in.Net
					}
					var id string
					var inserted bool
					err := e.Tx.QueryRow(c, `INSERT INTO cash_settlements (id, source, external_id, location_id, provider, tender_type, business_date, gross, fees, net, currency, paid_on)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
						ON CONFLICT (source, external_id) DO UPDATE SET location_id=EXCLUDED.location_id, provider=EXCLUDED.provider,
							tender_type=EXCLUDED.tender_type, business_date=EXCLUDED.business_date, gross=EXCLUDED.gross, fees=EXCLUDED.fees,
							net=EXCLUDED.net, currency=EXCLUDED.currency, paid_on=EXCLUDED.paid_on, updated_at=now()
						RETURNING id, (xmax = 0)`,
						ids.New(ids.Settlement), e.Source, in.ExternalID, locID, in.Provider, in.TenderType, in.BusinessDate, *in.Gross, fees, net, in.Currency, in.PaidOn).
						Scan(&id, &inserted)
					if err != nil {
						return ingest.Outcome{}, err
					}
					if reconcile && locID != nil {
						var pos, settled decimal.Decimal
						if err := e.Tx.QueryRow(c, `SELECT
							coalesce((SELECT sum(amount) FROM cash_tenders WHERE location_id=$1 AND business_date=$2 AND tender_type=$3), 0),
							coalesce((SELECT sum(gross) FROM cash_settlements WHERE location_id=$1 AND business_date=$2 AND tender_type=$3), 0)`,
							*locID, in.BusinessDate, in.TenderType).Scan(&pos, &settled); err != nil {
							return ingest.Outcome{}, err
						}
						if diff := settled.Sub(pos); diff.Abs().GreaterThan(tolerance(c, e.Tx)) {
							if err := c.Record(e.Tx, httpx.Change{Action: "settlement.mismatch", EventType: "cash.settlement_mismatch",
								Feature: "cash.tender_reconciliation", EntityType: "cash_settlement", EntityID: id, LocationID: *locID,
								After: map[string]any{"locationId": *locID, "businessDate": in.BusinessDate, "tenderType": in.TenderType,
									"posTotal": pos, "settledTotal": settled, "difference": diff}}); err != nil {
								return ingest.Outcome{}, err
							}
						}
					}
					return ingest.Outcome{Status: ingest.Status(inserted), ID: id}, nil
				})
			},
		},
		{
			Method: "POST", Path: "/cash/bank-transactions", Tag: tag, Feature: feature, Scope: "cash:write", Ingest: true, Status: 202,
			Summary:     "Send bank transactions for deposit verification",
			Description: "Credits are matched to recorded deposits (amount, date and bag number in the reference).",
			Body:        BankTxnBatch{}, Response: httpx.BatchResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				verify, err := c.App.Features.IsEnabled(c, "cash.deposit_verification")
				if err != nil {
					return nil, err
				}
				return ingest.Run(c, "bank_transactions", "transactions", func(e *ingest.Env, _ int, in BankTxnInput) (ingest.Outcome, error) {
					var id string
					var inserted bool
					err := e.Tx.QueryRow(c, `INSERT INTO bank_transactions (id, source, external_id, account, booked_on, amount, reference, currency)
						VALUES ($1,$2,$3,nullif($4,''),$5,$6,nullif($7,''),$8)
						ON CONFLICT (source, external_id) DO UPDATE SET account=EXCLUDED.account, booked_on=EXCLUDED.booked_on,
							amount=EXCLUDED.amount, reference=EXCLUDED.reference, currency=EXCLUDED.currency
						RETURNING id, (xmax = 0)`,
						ids.New(ids.BankTxn), e.Source, in.ExternalID, in.Account, in.BookedOn, *in.Amount, in.Reference, in.Currency).Scan(&id, &inserted)
					if err != nil {
						return ingest.Outcome{}, err
					}
					if verify && inserted && in.Amount.IsPositive() {
						if err := matchDeposits(c, e.Tx, id, in.BookedOn.Time, *in.Amount, in.Reference); err != nil {
							return ingest.Outcome{}, err
						}
					}
					return ingest.Outcome{Status: ingest.Status(inserted), ID: id}, nil
				})
			},
		},
		{
			Method: "POST", Path: "/cash/counts", Tag: tag, Feature: feature, Scope: "cash:write",
			Summary:     "Record a drawer, skim or safe count",
			Description: "Closing and shift-change counts are compared with the expected cash; over/short beyond the company tolerance raises cash.over_short_exceeded.",
			Body:        CountInput{}, Response: Count{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in CountInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if in.Counted.IsNegative() {
					return nil, httpx.Validation(httpx.FieldError{Path: "counted", Message: "Must be zero or more"})
				}
				var out Count
				err := c.InTx(func(tx pgx.Tx) error {
					loc, err := refs.Location(c, tx, in.LocationRef, "")
					if err != nil {
						return err
					}
					if closed, err := IsDayClosed(c, tx, loc, in.BusinessDate); err != nil {
						return err
					} else if closed {
						return httpx.PeriodLocked("This business day is closed.")
					}
					var expected, overShort *decimal.Decimal
					if in.Kind == "close" || in.Kind == "shift_change" {
						e, err := expectedCash(c, tx, loc, in.BusinessDate, in.RegisterID)
						if err != nil {
							return err
						}
						os := in.Counted.Sub(e)
						expected, overShort = &e, &os
					}
					var denoms map[string]int
					if len(in.Denominations) > 0 {
						denoms = in.Denominations
					}
					out, err = scanCount(tx.QueryRow(c, `INSERT INTO cash_counts (id, location_id, business_date, register_id, kind, counted, expected,
						over_short, denominations, counted_by, note) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,nullif($10,''),nullif($11,'')) RETURNING `+countCols,
						ids.New(ids.CashCount), loc, in.BusinessDate, in.RegisterID, in.Kind, *in.Counted, expected, overShort, denoms, in.CountedBy, in.Note))
					if err != nil {
						return err
					}
					if err := c.Record(tx, httpx.Change{Action: "cash.count", EventType: "cash.count_completed", Feature: feature,
						EntityType: "cash_count", EntityID: out.ID, LocationID: loc, After: out}); err != nil {
						return err
					}
					if overShort != nil && overShort.Abs().GreaterThan(tolerance(c, tx)) {
						return c.Record(tx, httpx.Change{Action: "cash.over_short", EventType: "cash.over_short_exceeded", Feature: feature,
							EntityType: "cash_count", EntityID: out.ID, LocationID: loc, After: out})
					}
					return nil
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/cash/counts", Tag: tag, Feature: feature, Scope: "cash:read",
			Summary: "List counts", Response: countList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT `+countCols+` FROM cash_counts WHERE ($1 = '' OR location_id = $1)
					AND ($2 = '' OR business_date = $2::date) ORDER BY created_at DESC LIMIT 500`, c.Query("locationId"), c.Query("businessDate"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Count, error) { return scanCount(r) })
				if list == nil {
					list = []Count{}
				}
				return countList{Data: list}, err
			},
		},
		{
			Method: "POST", Path: "/cash/deposits", Tag: tag, Feature: feature, Scope: "cash:write",
			Summary: "Record a bank deposit", Body: DepositInput{}, Response: Deposit{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in DepositInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if !in.Amount.IsPositive() {
					return nil, httpx.Validation(httpx.FieldError{Path: "amount", Message: "Must be greater than zero"})
				}
				var out Deposit
				err := c.InTx(func(tx pgx.Tx) error {
					loc, err := refs.Location(c, tx, in.LocationRef, "")
					if err != nil {
						return err
					}
					currency := in.Currency
					if currency == "" {
						if err := tx.QueryRow(c, `SELECT currency FROM locations WHERE id=$1`, loc).Scan(&currency); err != nil {
							return err
						}
					}
					out, err = scanDeposit(tx.QueryRow(c, `INSERT INTO bank_deposits (id, location_id, business_date, bag_number, amount, currency)
						VALUES ($1,$2,$3,nullif($4,''),$5,$6) RETURNING `+depCols, ids.New(ids.Deposit), loc, in.BusinessDate, in.BagNumber, *in.Amount, currency))
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "deposit.record", EventType: "cash.deposit_recorded", Feature: feature,
						EntityType: "bank_deposit", EntityID: out.ID, LocationID: loc, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/cash/deposits", Tag: tag, Feature: feature, Scope: "cash:read",
			Summary: "List deposits", Response: depositList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT `+depCols+` FROM bank_deposits WHERE ($1 = '' OR location_id = $1)
					AND ($2 = '' OR status = $2) ORDER BY business_date DESC, created_at DESC LIMIT 500`, c.Query("locationId"), c.Query("status"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Deposit, error) { return scanDeposit(r) })
				if list == nil {
					list = []Deposit{}
				}
				return depositList{Data: list}, err
			},
		},
		{
			Method: "GET", Path: "/cash/business-days", Tag: tag, Feature: feature, Scope: "cash:read",
			Summary:     "Daily cash summary for a location",
			Description: "Query with locationId, from and to (at most 62 days).",
			Response:    dayList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				loc, err := refs.Location(c, c.App.Pool, refs.LocationRef{LocationID: c.Query("locationId"), LocationExternalID: c.Query("locationExternalId")}, "")
				if err != nil {
					return nil, err
				}
				from, err := httpx.ParseDate(c.Query("from"))
				if err != nil {
					return nil, httpx.Validation(httpx.FieldError{Path: "from", Message: "Required date in YYYY-MM-DD format"})
				}
				to, err := httpx.ParseDate(c.Query("to"))
				if err != nil || to.Before(from.Time) || to.Sub(from.Time) > 62*24*time.Hour {
					return nil, httpx.Validation(httpx.FieldError{Path: "to", Message: "Required, on or after from, at most 62 days later"})
				}
				out := dayList{Data: []BusinessDay{}}
				for d := from.Time; !d.After(to.Time); d = d.AddDate(0, 0, 1) {
					bd, err := summarize(c, c.App.Pool, loc, httpx.NewDate(d))
					if err != nil {
						return nil, err
					}
					out.Data = append(out.Data, bd)
				}
				return out, nil
			},
		},
		{
			Method: "POST", Path: "/cash/business-days/{locationId}/{date}:close", Tag: tag, Feature: feature, Scope: "cash:write",
			Summary:     "Close a business day",
			Description: "After closing, counts are refused and late POS data for the day is flagged for review.",
			Response:    BusinessDay{},
			Handler: func(c *httpx.Ctx) (any, error) {
				day, err := httpx.ParseDate(c.Param("date"))
				if err != nil {
					return nil, httpx.Validation(httpx.FieldError{Path: "date", Message: "Must be YYYY-MM-DD"})
				}
				var out BusinessDay
				err = c.InTx(func(tx pgx.Tx) error {
					loc, err := refs.Location(c, tx, refs.LocationRef{LocationID: c.Param("locationId")}, "")
					if err != nil {
						return err
					}
					tag, err := tx.Exec(c, `INSERT INTO business_days (location_id, business_date, status, closed_at) VALUES ($1,$2,'closed',now())
						ON CONFLICT (location_id, business_date) DO UPDATE SET status='closed', closed_at=now() WHERE business_days.status <> 'closed'`, loc, day)
					if err != nil {
						return err
					}
					if tag.RowsAffected() == 0 {
						return httpx.Conflict("This business day is already closed.")
					}
					out, err = summarize(c, tx, loc, day)
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "business_day.close", EventType: "cash.business_day_closed", Feature: feature,
						EntityType: "business_day", EntityID: fmt.Sprintf("%s:%s", loc, day), LocationID: loc, After: out})
				})
				return out, err
			},
		},
	}
}
