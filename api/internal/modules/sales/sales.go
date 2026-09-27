// Package sales ingests sales feeds from POS systems, online stores and other
// channels: individual transactions and hourly/daily summaries.
package sales

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/modules/organization"
	"github.com/shopspring/decimal"
)

const (
	feature      = "sales"
	feedsFeature = "sales.feeds"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type Tender struct {
	Type     string           `json:"type" validate:"required,oneof=cash card digital_wallet gift_card voucher account platform other"`
	Platform string           `json:"platform,omitempty" validate:"max=100" doc:"Platform name when type is platform"`
	Amount   *decimal.Decimal `json:"amount" validate:"required"`
	Change   *decimal.Decimal `json:"change,omitempty" doc:"Cash change given back"`
}

type LineInput struct {
	ItemID         string           `json:"itemId,omitempty"`
	ItemSKU        string           `json:"itemSku,omitempty" validate:"max=100"`
	ItemBarcode    string           `json:"itemBarcode,omitempty" validate:"max=100"`
	ItemExternalID string           `json:"itemExternalId,omitempty" validate:"max=200"`
	Name           string           `json:"name,omitempty" validate:"max=200"`
	Quantity       *decimal.Decimal `json:"quantity" validate:"required"`
	UnitPrice      *decimal.Decimal `json:"unitPrice" validate:"required"`
	Discount       *decimal.Decimal `json:"discount,omitempty"`
	Tax            *decimal.Decimal `json:"tax,omitempty"`
}

type TransactionInput struct {
	ExternalID         string           `json:"externalId" validate:"required,extid"`
	LocationID         string           `json:"locationId,omitempty"`
	LocationExternalID string           `json:"locationExternalId,omitempty"`
	OccurredAt         time.Time        `json:"occurredAt" validate:"required"`
	Type               string           `json:"type,omitempty" validate:"omitempty,oneof=sale refund"`
	Status             string           `json:"status,omitempty" validate:"omitempty,oneof=completed voided"`
	Channel            string           `json:"channel,omitempty" validate:"max=50" doc:"e.g. in_store, online, delivery"`
	RegisterID         string           `json:"registerId,omitempty" validate:"max=100"`
	EmployeeID         string           `json:"employeeId,omitempty"`
	EmployeeExternalID string           `json:"employeeExternalId,omitempty"`
	RefundOf           string           `json:"refundOf,omitempty" validate:"max=200" doc:"externalId of the original sale"`
	Lines              []LineInput      `json:"lines,omitempty"`
	Tenders            []Tender         `json:"tenders,omitempty"`
	Subtotal           *decimal.Decimal `json:"subtotal,omitempty"`
	Tax                *decimal.Decimal `json:"tax,omitempty"`
	Total              *decimal.Decimal `json:"total" validate:"required"`
	Currency           string           `json:"currency,omitempty" validate:"omitempty,currency"`
}

type TransactionBatch struct {
	Source       string             `json:"source" validate:"required" doc:"Stable label for the sending system, e.g. pos:store-101 or web-store"`
	Transactions []TransactionInput `json:"transactions" validate:"required"`
}

type Line struct {
	LineNo         int             `json:"lineNo"`
	ItemID         *string         `json:"itemId"`
	ItemSKU        *string         `json:"itemSku"`
	ItemBarcode    *string         `json:"itemBarcode"`
	ItemExternalID *string         `json:"itemExternalId"`
	Name           *string         `json:"name"`
	Quantity       decimal.Decimal `json:"quantity"`
	UnitPrice      decimal.Decimal `json:"unitPrice"`
	Discount       decimal.Decimal `json:"discount"`
	Tax            decimal.Decimal `json:"tax"`
}

type Transaction struct {
	ID           string           `json:"id"`
	Source       string           `json:"source"`
	ExternalID   string           `json:"externalId"`
	LocationID   string           `json:"locationId"`
	BusinessDate httpx.Date       `json:"businessDate"`
	OccurredAt   time.Time        `json:"occurredAt"`
	Type         string           `json:"type"`
	Status       string           `json:"status"`
	Channel      *string          `json:"channel"`
	RegisterID   *string          `json:"registerId"`
	EmployeeID   *string          `json:"employeeId"`
	RefundOf     *string          `json:"refundOf"`
	Subtotal     *decimal.Decimal `json:"subtotal"`
	Tax          decimal.Decimal  `json:"tax"`
	Total        decimal.Decimal  `json:"total"`
	Currency     string           `json:"currency"`
	Tenders      []Tender         `json:"tenders"`
	Lines        []Line           `json:"lines"`
	Version      int              `json:"version"`
	CreatedAt    time.Time        `json:"createdAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
}

type ChannelSummary struct {
	Channel      string           `json:"channel" validate:"required,max=50"`
	NetSales     *decimal.Decimal `json:"netSales" validate:"required"`
	Transactions *int             `json:"transactions,omitempty"`
}

type SummaryInput struct {
	ExternalID         string           `json:"externalId" validate:"required,extid" doc:"e.g. 2026-09-27T12 for an hourly summary"`
	LocationID         string           `json:"locationId,omitempty"`
	LocationExternalID string           `json:"locationExternalId,omitempty"`
	PeriodStart        time.Time        `json:"periodStart" validate:"required"`
	PeriodEnd          time.Time        `json:"periodEnd" validate:"required,gtfield=PeriodStart"`
	NetSales           *decimal.Decimal `json:"netSales" validate:"required"`
	GrossSales         *decimal.Decimal `json:"grossSales,omitempty"`
	Discounts          *decimal.Decimal `json:"discounts,omitempty"`
	Tax                *decimal.Decimal `json:"tax,omitempty"`
	Transactions       *int             `json:"transactions,omitempty" validate:"omitempty,min=0"`
	Guests             *int             `json:"guests,omitempty" validate:"omitempty,min=0"`
	ByChannel          []ChannelSummary `json:"byChannel,omitempty" validate:"dive"`
	Currency           string           `json:"currency,omitempty" validate:"omitempty,currency"`
}

type SummaryBatch struct {
	Source    string         `json:"source" validate:"required"`
	Summaries []SummaryInput `json:"summaries" validate:"required"`
}

type Summary struct {
	ID           string           `json:"id"`
	Source       string           `json:"source"`
	ExternalID   string           `json:"externalId"`
	LocationID   string           `json:"locationId"`
	BusinessDate httpx.Date       `json:"businessDate"`
	PeriodStart  time.Time        `json:"periodStart"`
	PeriodEnd    time.Time        `json:"periodEnd"`
	NetSales     decimal.Decimal  `json:"netSales"`
	GrossSales   *decimal.Decimal `json:"grossSales"`
	Discounts    *decimal.Decimal `json:"discounts"`
	Tax          *decimal.Decimal `json:"tax"`
	Transactions *int             `json:"transactions"`
	Guests       *int             `json:"guests"`
	ByChannel    []ChannelSummary `json:"byChannel"`
	Currency     string           `json:"currency"`
	CreatedAt    time.Time        `json:"createdAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
}

type ListQuery struct {
	httpx.ListParams
	Source     string     `json:"source,omitempty"`
	LocationID string     `json:"locationId,omitempty"`
	From       httpx.Date `json:"from,omitempty" doc:"First business date (inclusive)"`
	To         httpx.Date `json:"to,omitempty" doc:"Last business date (inclusive)"`
}

type UnmappedItem struct {
	Source    string    `json:"source"`
	Ref       string    `json:"ref" doc:"How the sales feed identified the item, e.g. externalId:pos-775 or sku:LATTE-12"`
	Name      *string   `json:"name"`
	Lines     int       `json:"lines"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

type MapInput struct {
	Source string `json:"source" validate:"required"`
	Ref    string `json:"ref" validate:"required,max=300"`
	ItemID string `json:"itemId" validate:"required"`
}

type MapResult struct {
	Source       string `json:"source"`
	Ref          string `json:"ref"`
	ItemID       string `json:"itemId"`
	LinesUpdated int64  `json:"linesUpdated"`
}

// ---------------------------------------------------------------------------
// Item matching
// ---------------------------------------------------------------------------

// refOf is the stable key for how a feed identified an item. It must match
// refSQL below.
func refOf(l LineInput) string {
	switch {
	case l.ItemExternalID != "":
		return "externalId:" + l.ItemExternalID
	case l.ItemSKU != "":
		return "sku:" + l.ItemSKU
	case l.ItemBarcode != "":
		return "barcode:" + l.ItemBarcode
	default:
		return "name:" + l.Name
	}
}

const refSQL = `CASE WHEN l.item_external_id IS NOT NULL THEN 'externalId:' || l.item_external_id
	WHEN l.item_sku IS NOT NULL THEN 'sku:' || l.item_sku
	WHEN l.item_barcode IS NOT NULL THEN 'barcode:' || l.item_barcode
	ELSE 'name:' || coalesce(l.name, '') END`

type itemMatcher struct {
	q      db.Querier
	source string
	cache  map[string]*string
}

func (m *itemMatcher) match(ctx context.Context, l LineInput) (*string, error) {
	key := "id:" + l.ItemID
	if l.ItemID == "" {
		key = refOf(l)
	}
	if v, ok := m.cache[key]; ok {
		return v, nil
	}
	var id string
	var err error
	switch {
	case l.ItemID != "":
		err = m.q.QueryRow(ctx, `SELECT id FROM items WHERE id = $1`, l.ItemID).Scan(&id)
	default:
		err = m.q.QueryRow(ctx, `
			SELECT id FROM (
				SELECT id, 1 AS pri FROM items WHERE $1 <> '' AND external_id = $1
				UNION ALL SELECT id, 2 FROM items WHERE $2 <> '' AND sku = $2
				UNION ALL SELECT id, 3 FROM items WHERE $3 <> '' AND barcode = $3
				UNION ALL SELECT item_id, 4 FROM item_mappings WHERE source = $4 AND external_ref = $5
			) m ORDER BY pri LIMIT 1`,
			l.ItemExternalID, l.ItemSKU, l.ItemBarcode, m.source, refOf(l)).Scan(&id)
	}
	var out *string
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		out = nil
	case err != nil:
		return nil, err
	default:
		out = &id
	}
	m.cache[key] = out
	return out, nil
}

// ---------------------------------------------------------------------------
// Ingestion
// ---------------------------------------------------------------------------

func sumTenders(ts []Tender) decimal.Decimal {
	sum := decimal.Zero
	for _, t := range ts {
		sum = sum.Add(*t.Amount)
		if t.Change != nil {
			sum = sum.Sub(*t.Change)
		}
	}
	return sum
}

func decOrZero(d *decimal.Decimal) decimal.Decimal {
	if d == nil {
		return decimal.Zero
	}
	return *d
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ingestTransactions(c *httpx.Ctx) (any, error) {
	var batch TransactionBatch
	if err := c.Decode(&batch); err != nil {
		return nil, err
	}
	if !httpx.ValidSource(batch.Source) {
		return nil, httpx.Validation(httpx.FieldError{Path: "source", Message: "Must be 1–100 characters without spaces or '/'"})
	}
	if err := c.CheckBatchSize("transactions", len(batch.Transactions)); err != nil {
		return nil, err
	}

	res := httpx.BatchResult{BatchID: ids.New(ids.Batch), Received: len(batch.Transactions), Results: []httpx.RecordResult{}}
	err := c.InTx(func(tx pgx.Tx) error {
		locs := organization.NewLocationResolver(tx)
		items := &itemMatcher{q: tx, source: batch.Source, cache: map[string]*string{}}
		empByExt := map[string]*string{}
		unmapped := map[string]string{} // ref → sample name

		for i, in := range batch.Transactions {
			if errs := httpx.ValidateItem(in); errs != nil {
				res.Add(httpx.RecordResult{Index: i, ExternalID: in.ExternalID, Status: "rejected", Errors: errs})
				continue
			}
			var lineErrs []httpx.FieldError
			for j, l := range in.Lines {
				for _, fe := range httpx.ValidateItem(l) {
					fe.Path = "lines[" + itoa(j) + "]." + fe.Path
					lineErrs = append(lineErrs, fe)
				}
			}
			for j, t := range in.Tenders {
				for _, fe := range httpx.ValidateItem(t) {
					fe.Path = "tenders[" + itoa(j) + "]." + fe.Path
					lineErrs = append(lineErrs, fe)
				}
			}
			if lineErrs != nil {
				res.Add(httpx.RecordResult{Index: i, ExternalID: in.ExternalID, Status: "rejected", Errors: lineErrs})
				continue
			}
			if len(in.Tenders) > 0 && !sumTenders(in.Tenders).Equal(*in.Total) {
				res.Add(httpx.Reject(i, in.ExternalID, "tenders",
					"Tender total "+sumTenders(in.Tenders).StringFixed(2)+" does not match transaction total "+in.Total.StringFixed(2)))
				continue
			}
			loc, err := locs.Resolve(c, in.LocationID, in.LocationExternalID)
			if err != nil {
				res.Add(httpx.Reject(i, in.ExternalID, "location", err.Error()))
				continue
			}

			var warnings []httpx.FieldError
			var empID *string
			switch {
			case in.EmployeeID != "":
				empID = &in.EmployeeID
			case in.EmployeeExternalID != "":
				cached, ok := empByExt[in.EmployeeExternalID]
				if !ok {
					var id string
					err := tx.QueryRow(c, `SELECT id FROM employees WHERE external_id = $1`, in.EmployeeExternalID).Scan(&id)
					if err != nil && !errors.Is(err, pgx.ErrNoRows) {
						return err
					}
					if err == nil {
						cached = &id
					}
					empByExt[in.EmployeeExternalID] = cached
				}
				empID = cached
				if empID == nil {
					warnings = append(warnings, httpx.FieldError{Path: "employeeExternalId", Message: "Unknown employee; stored without one"})
				}
			}

			lineItems := make([]*string, len(in.Lines))
			for j, l := range in.Lines {
				id, err := items.match(c, l)
				if err != nil {
					return err
				}
				lineItems[j] = id
				if id == nil {
					unmapped[refOf(l)] = l.Name
				}
			}

			txType, status, currency := in.Type, in.Status, in.Currency
			if txType == "" {
				txType = "sale"
			}
			if status == "" {
				status = "completed"
			}
			if currency == "" {
				currency = loc.Currency
			}
			tenders := in.Tenders
			if tenders == nil {
				tenders = []Tender{}
			}
			tendersJSON, _ := json.Marshal(tenders)

			var id string
			var inserted bool
			err = httpx.Savepoint(c, tx, func(sp pgx.Tx) error {
				err := sp.QueryRow(c, `
					INSERT INTO sales_transactions (id, source, external_id, location_id, business_date, occurred_at, type,
						status, channel, register_id, employee_id, refund_of, subtotal, tax, total, currency, tenders, batch_id)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
					ON CONFLICT (source, external_id) DO UPDATE SET
						location_id=EXCLUDED.location_id, business_date=EXCLUDED.business_date, occurred_at=EXCLUDED.occurred_at,
						type=EXCLUDED.type, status=EXCLUDED.status, channel=EXCLUDED.channel, register_id=EXCLUDED.register_id,
						employee_id=EXCLUDED.employee_id, refund_of=EXCLUDED.refund_of, subtotal=EXCLUDED.subtotal,
						tax=EXCLUDED.tax, total=EXCLUDED.total, currency=EXCLUDED.currency, tenders=EXCLUDED.tenders,
						batch_id=EXCLUDED.batch_id, version=sales_transactions.version+1, updated_at=now()
					RETURNING id, (xmax = 0)`,
					ids.New(ids.SalesTxn), batch.Source, in.ExternalID, loc.ID, loc.BusinessDate(in.OccurredAt),
					in.OccurredAt.UTC(), txType, status, nilIfEmpty(in.Channel), nilIfEmpty(in.RegisterID), empID,
					nilIfEmpty(in.RefundOf), in.Subtotal, decOrZero(in.Tax), *in.Total, currency, tendersJSON, res.BatchID,
				).Scan(&id, &inserted)
				if err != nil {
					return err
				}
				if !inserted {
					if _, err := sp.Exec(c, `DELETE FROM sales_transaction_lines WHERE transaction_id = $1`, id); err != nil {
						return err
					}
				}
				for j, l := range in.Lines {
					_, err := sp.Exec(c, `
						INSERT INTO sales_transaction_lines (transaction_id, line_no, item_id, item_sku, item_barcode,
							item_external_id, name, quantity, unit_price, discount, tax)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
						id, j+1, lineItems[j], nilIfEmpty(l.ItemSKU), nilIfEmpty(l.ItemBarcode), nilIfEmpty(l.ItemExternalID),
						nilIfEmpty(l.Name), *l.Quantity, *l.UnitPrice, decOrZero(l.Discount), decOrZero(l.Tax))
					if err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				if db.IsForeignKeyViolation(err) {
					res.Add(httpx.Reject(i, in.ExternalID, "", "Unknown employeeId or itemId"))
					continue
				}
				return err
			}
			status = "updated"
			if inserted {
				status = "created"
			}
			res.Add(httpx.RecordResult{Index: i, ExternalID: in.ExternalID, Status: status, ID: id, Errors: warnings})
		}

		for ref, name := range unmapped {
			if err := c.Record(tx, httpx.Change{
				Action: "sales.unmapped_item", EventType: "sales.unmapped_item", Feature: feedsFeature,
				EntityType: "unmapped_item", EntityID: batch.Source + "|" + ref,
				After: map[string]any{"source": batch.Source, "ref": ref, "name": nilIfEmpty(name), "batchId": res.BatchID},
			}); err != nil {
				return err
			}
		}
		_, err := tx.Exec(c, `
			INSERT INTO ingest_batches (id, api_key_id, integration_id, kind, source, received, created, updated, rejected)
			VALUES ($1, $2, nullif($3, ''), 'sales_transactions', $4, $5, $6, $7, $8)`,
			res.BatchID, c.Principal.KeyID, c.Principal.IntegrationID, batch.Source, res.Received, res.Created, res.Updated, res.Rejected)
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func ingestSummaries(c *httpx.Ctx) (any, error) {
	var batch SummaryBatch
	if err := c.Decode(&batch); err != nil {
		return nil, err
	}
	if !httpx.ValidSource(batch.Source) {
		return nil, httpx.Validation(httpx.FieldError{Path: "source", Message: "Must be 1–100 characters without spaces or '/'"})
	}
	if err := c.CheckBatchSize("summaries", len(batch.Summaries)); err != nil {
		return nil, err
	}
	res := httpx.BatchResult{BatchID: ids.New(ids.Batch), Received: len(batch.Summaries), Results: []httpx.RecordResult{}}
	err := c.InTx(func(tx pgx.Tx) error {
		locs := organization.NewLocationResolver(tx)
		for i, in := range batch.Summaries {
			if errs := httpx.ValidateItem(in); errs != nil {
				res.Add(httpx.RecordResult{Index: i, ExternalID: in.ExternalID, Status: "rejected", Errors: errs})
				continue
			}
			loc, err := locs.Resolve(c, in.LocationID, in.LocationExternalID)
			if err != nil {
				res.Add(httpx.Reject(i, in.ExternalID, "location", err.Error()))
				continue
			}
			currency := in.Currency
			if currency == "" {
				currency = loc.Currency
			}
			byChannel := in.ByChannel
			if byChannel == nil {
				byChannel = []ChannelSummary{}
			}
			chJSON, _ := json.Marshal(byChannel)
			var id string
			var inserted bool
			err = tx.QueryRow(c, `
				INSERT INTO sales_summaries (id, source, external_id, location_id, business_date, period_start, period_end,
					net_sales, gross_sales, discounts, tax, transactions, guests, by_channel, currency, batch_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
				ON CONFLICT (source, external_id) DO UPDATE SET
					location_id=EXCLUDED.location_id, business_date=EXCLUDED.business_date,
					period_start=EXCLUDED.period_start, period_end=EXCLUDED.period_end, net_sales=EXCLUDED.net_sales,
					gross_sales=EXCLUDED.gross_sales, discounts=EXCLUDED.discounts, tax=EXCLUDED.tax,
					transactions=EXCLUDED.transactions, guests=EXCLUDED.guests, by_channel=EXCLUDED.by_channel,
					currency=EXCLUDED.currency, batch_id=EXCLUDED.batch_id, updated_at=now()
				RETURNING id, (xmax = 0)`,
				ids.New(ids.SalesSummary), batch.Source, in.ExternalID, loc.ID, loc.BusinessDate(in.PeriodStart),
				in.PeriodStart.UTC(), in.PeriodEnd.UTC(), *in.NetSales, in.GrossSales, in.Discounts, in.Tax,
				in.Transactions, in.Guests, chJSON, currency, res.BatchID).Scan(&id, &inserted)
			if err != nil {
				return err
			}
			status := "updated"
			if inserted {
				status = "created"
			}
			res.Add(httpx.RecordResult{Index: i, ExternalID: in.ExternalID, Status: status, ID: id})
		}
		_, err := tx.Exec(c, `
			INSERT INTO ingest_batches (id, api_key_id, integration_id, kind, source, received, created, updated, rejected)
			VALUES ($1, $2, nullif($3, ''), 'sales_summaries', $4, $5, $6, $7, $8)`,
			res.BatchID, c.Principal.KeyID, c.Principal.IntegrationID, batch.Source, res.Received, res.Created, res.Updated, res.Rejected)
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

func parseDateRange(c *httpx.Ctx) (from, to *httpx.Date, err error) {
	for name, dst := range map[string]**httpx.Date{"from": &from, "to": &to} {
		if s := c.Query(name); s != "" {
			d, perr := httpx.ParseDate(s)
			if perr != nil {
				return nil, nil, httpx.Validation(httpx.FieldError{Path: name, Message: "Must be a date in YYYY-MM-DD format"})
			}
			*dst = &d
		}
	}
	return from, to, nil
}

func listTransactions(c *httpx.Ctx) (any, error) {
	lp, err := c.ParseList()
	if err != nil {
		return nil, err
	}
	from, to, err := parseDateRange(c)
	if err != nil {
		return nil, err
	}
	rows, err := c.App.Pool.Query(c, `
		SELECT id, source, external_id, location_id, business_date, occurred_at, type, status, channel, register_id,
		       employee_id, refund_of, subtotal, tax, total, currency, tenders, version, created_at, updated_at
		FROM sales_transactions
		WHERE id > $1 AND ($2::timestamptz IS NULL OR updated_at >= $2)
		  AND ($3 = '' OR source = $3) AND ($4 = '' OR location_id = $4)
		  AND ($5::date IS NULL OR business_date >= $5) AND ($6::date IS NULL OR business_date <= $6)
		ORDER BY id LIMIT $7`,
		lp.AfterID, lp.UpdatedSince, c.Query("source"), c.Query("locationId"), from, to, lp.Limit+1)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Transaction, error) {
		var t Transaction
		var bd time.Time
		err := r.Scan(&t.ID, &t.Source, &t.ExternalID, &t.LocationID, &bd, &t.OccurredAt, &t.Type, &t.Status,
			&t.Channel, &t.RegisterID, &t.EmployeeID, &t.RefundOf, &t.Subtotal, &t.Tax, &t.Total, &t.Currency,
			&t.Tenders, &t.Version, &t.CreatedAt, &t.UpdatedAt)
		t.BusinessDate = httpx.NewDate(bd)
		t.Lines = []Line{}
		return t, err
	})
	if err != nil {
		return nil, err
	}
	page := httpx.NewPage(list, lp.Limit, func(t Transaction) string { return t.ID })
	if len(page.Data) == 0 {
		return page, nil
	}
	idx := map[string]int{}
	txIDs := make([]string, len(page.Data))
	for i, t := range page.Data {
		idx[t.ID] = i
		txIDs[i] = t.ID
	}
	lrows, err := c.App.Pool.Query(c, `
		SELECT transaction_id, line_no, item_id, item_sku, item_barcode, item_external_id, name, quantity, unit_price, discount, tax
		FROM sales_transaction_lines WHERE transaction_id = ANY($1) ORDER BY transaction_id, line_no`, txIDs)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var txID string
		var l Line
		if err := lrows.Scan(&txID, &l.LineNo, &l.ItemID, &l.ItemSKU, &l.ItemBarcode, &l.ItemExternalID, &l.Name,
			&l.Quantity, &l.UnitPrice, &l.Discount, &l.Tax); err != nil {
			return nil, err
		}
		t := &page.Data[idx[txID]]
		t.Lines = append(t.Lines, l)
	}
	return page, lrows.Err()
}

func listSummaries(c *httpx.Ctx) (any, error) {
	lp, err := c.ParseList()
	if err != nil {
		return nil, err
	}
	from, to, err := parseDateRange(c)
	if err != nil {
		return nil, err
	}
	rows, err := c.App.Pool.Query(c, `
		SELECT id, source, external_id, location_id, business_date, period_start, period_end, net_sales, gross_sales,
		       discounts, tax, transactions, guests, by_channel, currency, created_at, updated_at
		FROM sales_summaries
		WHERE id > $1 AND ($2::timestamptz IS NULL OR updated_at >= $2)
		  AND ($3 = '' OR source = $3) AND ($4 = '' OR location_id = $4)
		  AND ($5::date IS NULL OR business_date >= $5) AND ($6::date IS NULL OR business_date <= $6)
		ORDER BY id LIMIT $7`,
		lp.AfterID, lp.UpdatedSince, c.Query("source"), c.Query("locationId"), from, to, lp.Limit+1)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Summary, error) {
		var s Summary
		var bd time.Time
		err := r.Scan(&s.ID, &s.Source, &s.ExternalID, &s.LocationID, &bd, &s.PeriodStart, &s.PeriodEnd, &s.NetSales,
			&s.GrossSales, &s.Discounts, &s.Tax, &s.Transactions, &s.Guests, &s.ByChannel, &s.Currency, &s.CreatedAt, &s.UpdatedAt)
		s.BusinessDate = httpx.NewDate(bd)
		return s, err
	})
	if err != nil {
		return nil, err
	}
	return httpx.NewPage(list, lp.Limit, func(s Summary) string { return s.ID }), nil
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

type unmappedList struct {
	Data []UnmappedItem `json:"data"`
}

func Routes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "POST", Path: "/sales/transactions:batch", Tag: "Sales", Feature: feedsFeature, Scope: "sales:write",
			Ingest: true, Status: 202, Summary: "Send sales transactions from a POS or online store",
			Description: "Records are unique on (source, externalId): re-sending updates instead of duplicating. " +
				"Lines whose item can't be matched are stored and listed under unmapped items.",
			Body: TransactionBatch{}, Response: httpx.BatchResult{}, Handler: ingestTransactions,
		},
		{
			Method: "GET", Path: "/sales/transactions", Tag: "Sales", Feature: feedsFeature, Scope: "sales:read",
			Summary: "List sales transactions", Query: ListQuery{}, Response: httpx.Page[Transaction]{},
			Handler: listTransactions,
		},
		{
			Method: "POST", Path: "/sales-summaries", Tag: "Sales", Feature: feedsFeature, Scope: "sales:write",
			Ingest: true, Status: 202, Summary: "Send hourly or daily sales totals",
			Description: "For systems that can't send individual transactions. Don't send both for the same source.",
			Body:        SummaryBatch{}, Response: httpx.BatchResult{}, Handler: ingestSummaries,
		},
		{
			Method: "GET", Path: "/sales-summaries", Tag: "Sales", Feature: feedsFeature, Scope: "sales:read",
			Summary: "List sales summaries", Query: ListQuery{}, Response: httpx.Page[Summary]{},
			Handler: listSummaries,
		},
		{
			Method: "GET", Path: "/sales/unmapped-items", Tag: "Sales", Feature: feedsFeature, Scope: "sales:read",
			Summary: "Items in sales feeds that aren't linked to a PurrOS item", Response: unmappedList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `
					SELECT t.source, `+refSQL+` AS ref, max(l.name), count(*)::int, min(t.occurred_at), max(t.occurred_at)
					FROM sales_transaction_lines l JOIN sales_transactions t ON t.id = l.transaction_id
					WHERE l.item_id IS NULL AND ($1 = '' OR t.source = $1)
					GROUP BY t.source, ref ORDER BY count(*) DESC LIMIT 500`, c.Query("source"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (UnmappedItem, error) {
					var u UnmappedItem
					err := r.Scan(&u.Source, &u.Ref, &u.Name, &u.Lines, &u.FirstSeen, &u.LastSeen)
					return u, err
				})
				if list == nil {
					list = []UnmappedItem{}
				}
				return unmappedList{Data: list}, err
			},
		},
		{
			Method: "POST", Path: "/sales/unmapped-items:map", Tag: "Sales", Feature: feedsFeature, Scope: "sales:write",
			Summary:     "Link an unmapped item reference to a PurrOS item",
			Description: "Past lines with this reference are updated, and future ones are matched automatically.",
			Body:        MapInput{}, Response: MapResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in MapInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out MapResult
				err := c.InTx(func(tx pgx.Tx) error {
					_, err := tx.Exec(c, `
						INSERT INTO item_mappings (source, external_ref, item_id) VALUES ($1, $2, $3)
						ON CONFLICT (source, external_ref) DO UPDATE SET item_id = EXCLUDED.item_id`,
						in.Source, in.Ref, in.ItemID)
					if db.IsForeignKeyViolation(err) {
						return httpx.Validation(httpx.FieldError{Path: "itemId", Message: "Unknown item"})
					}
					if err != nil {
						return err
					}
					tag, err := tx.Exec(c, `
						UPDATE sales_transaction_lines l SET item_id = $3
						FROM sales_transactions t
						WHERE t.id = l.transaction_id AND l.item_id IS NULL AND t.source = $1 AND `+refSQL+` = $2`,
						in.Source, in.Ref, in.ItemID)
					if err != nil {
						return err
					}
					out = MapResult{Source: in.Source, Ref: in.Ref, ItemID: in.ItemID, LinesUpdated: tag.RowsAffected()}
					return c.Record(tx, httpx.Change{Action: "sales.map_item", EntityType: "item_mapping",
						EntityID: in.Source + "|" + in.Ref, After: out})
				})
				return out, err
			},
		},
	}
}
