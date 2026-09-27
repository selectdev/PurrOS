// Package purchasing serves suppliers, catalogs, purchase orders, receiving,
// supplier invoice matching and suggested orders.
package purchasing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/crud"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/modules/inventory"
	"github.com/selectdev/purros/api/internal/refs"
	"github.com/shopspring/decimal"
)

const (
	feature = "purchasing"
	tag     = "Purchasing"
)

// ---------------------------------------------------------------------------
// Suppliers and catalogs
// ---------------------------------------------------------------------------

type Supplier struct {
	ID            string           `json:"id" db:"id"`
	Name          string           `json:"name" db:"name"`
	Email         *string          `json:"email" db:"email"`
	Phone         *string          `json:"phone" db:"phone"`
	ContactName   *string          `json:"contactName" db:"contact_name"`
	Currency      string           `json:"currency" db:"currency"`
	LeadTimeDays  int              `json:"leadTimeDays" db:"lead_time_days"`
	OrderDays     []int32          `json:"orderDays" db:"order_days" doc:"Weekdays orders are placed, 0 = Sunday"`
	MinOrderValue *decimal.Decimal `json:"minOrderValue" db:"min_order_value"`
	ExternalID    *string          `json:"externalId" db:"external_id"`
	Version       int              `json:"version" db:"version"`
	CreatedAt     time.Time        `json:"createdAt" db:"created_at"`
	UpdatedAt     time.Time        `json:"updatedAt" db:"updated_at"`
	ArchivedAt    *time.Time       `json:"archivedAt" db:"archived_at"`
}

type SupplierInput struct {
	Name          string           `json:"name" db:"name" validate:"required,max=200"`
	Email         *string          `json:"email,omitempty" db:"email" validate:"omitempty,email"`
	Phone         *string          `json:"phone,omitempty" db:"phone" validate:"omitempty,max=40"`
	ContactName   *string          `json:"contactName,omitempty" db:"contact_name" validate:"omitempty,max=100"`
	Currency      string           `json:"currency,omitempty" db:"currency" validate:"omitempty,currency"`
	LeadTimeDays  int              `json:"leadTimeDays,omitempty" db:"lead_time_days" validate:"min=0,max=365"`
	OrderDays     []int32          `json:"orderDays,omitempty" db:"order_days" validate:"dive,min=0,max=6"`
	MinOrderValue *decimal.Decimal `json:"minOrderValue,omitempty" db:"min_order_value"`
	ExternalID    *string          `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
}

var suppliers = &crud.Resource[Supplier, SupplierInput]{
	Path: "/suppliers", Table: "suppliers", Prefix: ids.Supplier, Noun: "supplier", Tag: tag,
	Feature: feature, ReadScope: "purchasing:read", WriteScope: "purchasing:write", External: true, Archive: true,
	Defaults: func(in *SupplierInput) {
		if in.Currency == "" {
			in.Currency = "USD"
		}
		if in.OrderDays == nil {
			in.OrderDays = []int32{}
		}
		if in.LeadTimeDays == 0 {
			in.LeadTimeDays = 2
		}
	},
	Check: func(c *httpx.Ctx, q db.Querier, in *SupplierInput, _ *Supplier) error {
		if in.OrderDays == nil {
			in.OrderDays = []int32{}
		}
		return nil
	},
}

type CatalogEntry struct {
	ItemID      string          `json:"itemId"`
	SKU         string          `json:"sku"`
	Name        string          `json:"name"`
	SupplierSKU *string         `json:"supplierSku"`
	PackSize    decimal.Decimal `json:"packSize" doc:"Base units per pack"`
	Price       decimal.Decimal `json:"price" doc:"Price per pack"`
	MinPacks    decimal.Decimal `json:"minPacks"`
}

type CatalogEntryInput struct {
	refs.ItemRef
	SupplierSKU string           `json:"supplierSku,omitempty" validate:"max=100"`
	PackSize    *decimal.Decimal `json:"packSize,omitempty"`
	Price       *decimal.Decimal `json:"price" validate:"required"`
	MinPacks    *decimal.Decimal `json:"minPacks,omitempty"`
}

type CatalogInput struct {
	Items []CatalogEntryInput `json:"items" validate:"dive"`
}

type catalogList struct {
	Data []CatalogEntry `json:"data"`
}

func loadCatalog(ctx context.Context, q db.Querier, supplierID string) (catalogList, error) {
	rows, err := q.Query(ctx, `SELECT c.item_id, i.sku, i.name, c.supplier_sku, c.pack_size, c.price, c.min_packs
		FROM supplier_catalog c JOIN items i ON i.id = c.item_id WHERE c.supplier_id = $1 ORDER BY i.name`, supplierID)
	if err != nil {
		return catalogList{}, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CatalogEntry, error) {
		var e CatalogEntry
		return e, r.Scan(&e.ItemID, &e.SKU, &e.Name, &e.SupplierSKU, &e.PackSize, &e.Price, &e.MinPacks)
	})
	if list == nil {
		list = []CatalogEntry{}
	}
	return catalogList{Data: list}, err
}

// ---------------------------------------------------------------------------
// Purchase orders
// ---------------------------------------------------------------------------

type POLine struct {
	LineNo      int             `json:"lineNo"`
	ItemID      string          `json:"itemId"`
	Quantity    decimal.Decimal `json:"quantity" doc:"In base units"`
	UnitPrice   decimal.Decimal `json:"unitPrice" doc:"Per base unit"`
	ReceivedQty decimal.Decimal `json:"receivedQty"`
	Amount      decimal.Decimal `json:"amount"`
}

type PurchaseOrder struct {
	ID         string          `json:"id"`
	Number     string          `json:"number"`
	SupplierID string          `json:"supplierId"`
	LocationID string          `json:"locationId"`
	Status     string          `json:"status" doc:"draft, awaiting_approval, approved, sent, partially_received, received or cancelled"`
	ExpectedOn *httpx.Date     `json:"expectedOn"`
	Total      decimal.Decimal `json:"total"`
	Currency   string          `json:"currency"`
	Notes      *string         `json:"notes"`
	ExternalID *string         `json:"externalId"`
	ApprovedAt *time.Time      `json:"approvedAt"`
	SentAt     *time.Time      `json:"sentAt"`
	Version    int             `json:"version"`
	Lines      []POLine        `json:"lines"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

type POLineInput struct {
	refs.ItemRef
	Quantity  *decimal.Decimal `json:"quantity" validate:"required" doc:"In base units"`
	UnitPrice *decimal.Decimal `json:"unitPrice,omitempty" doc:"Per base unit; defaults to the catalog price ÷ pack size"`
}

type POInput struct {
	SupplierID string `json:"supplierId" validate:"required"`
	refs.LocationRef
	ExpectedOn *httpx.Date   `json:"expectedOn,omitempty"`
	Notes      string        `json:"notes,omitempty" validate:"max=1000"`
	ExternalID string        `json:"externalId,omitempty" validate:"omitempty,extid"`
	Lines      []POLineInput `json:"lines" validate:"required,min=1,dive"`
}

type ReceiptLineInput struct {
	refs.ItemRef
	Quantity *decimal.Decimal `json:"quantity" validate:"required" doc:"Good units received"`
	Damaged  *decimal.Decimal `json:"damaged,omitempty" doc:"Units refused as damaged (not added to stock)"`
	Note     string           `json:"note,omitempty" validate:"max=200"`
}

type ReceiveInput struct {
	Lines []ReceiptLineInput `json:"lines,omitempty" validate:"dive" doc:"Omit to receive everything outstanding"`
	Note  string             `json:"note,omitempty" validate:"max=500"`
}

const poCols = `id, number, supplier_id, location_id, status, expected_on, total, currency, notes, external_id,
	approved_at, sent_at, version, created_at, updated_at`

func loadPO(ctx context.Context, q db.Querier, id string, lock bool) (PurchaseOrder, error) {
	sql := `SELECT ` + poCols + ` FROM purchase_orders WHERE id=$1`
	if lock {
		sql += ` FOR UPDATE`
	}
	var p PurchaseOrder
	var exp *time.Time
	err := q.QueryRow(ctx, sql, id).Scan(&p.ID, &p.Number, &p.SupplierID, &p.LocationID, &p.Status, &exp, &p.Total, &p.Currency,
		&p.Notes, &p.ExternalID, &p.ApprovedAt, &p.SentAt, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, httpx.NotFound("Purchase order not found.")
	}
	if err != nil {
		return p, err
	}
	if exp != nil {
		d := httpx.NewDate(*exp)
		p.ExpectedOn = &d
	}
	rows, err := q.Query(ctx, `SELECT line_no, item_id, quantity, unit_price, received_qty FROM purchase_order_lines WHERE po_id=$1 ORDER BY line_no`, id)
	if err != nil {
		return p, err
	}
	p.Lines, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (POLine, error) {
		var l POLine
		err := r.Scan(&l.LineNo, &l.ItemID, &l.Quantity, &l.UnitPrice, &l.ReceivedQty)
		l.Amount = l.Quantity.Mul(l.UnitPrice).Round(2)
		return l, err
	})
	return p, err
}

func transition(from []string, to, event, stamp string) func(c *httpx.Ctx) (any, error) {
	return func(c *httpx.Ctx) (any, error) {
		var out PurchaseOrder
		err := c.InTx(func(tx pgx.Tx) error {
			before, err := loadPO(c, tx, c.Param("id"), true)
			if err != nil {
				return err
			}
			if !slices.Contains(from, before.Status) {
				return httpx.Conflict(fmt.Sprintf("A %s purchase order can't be %s.", before.Status, to))
			}
			set := ""
			if stamp != "" {
				set = ", " + stamp + " = now()"
			}
			if _, err := tx.Exec(c, `UPDATE purchase_orders SET status=$2`+set+`, version=version+1, updated_at=now() WHERE id=$1`, before.ID, to); err != nil {
				return err
			}
			out, err = loadPO(c, tx, before.ID, false)
			if err != nil {
				return err
			}
			return c.Record(tx, httpx.Change{Action: "purchase_order." + to, EventType: event, Feature: feature,
				EntityType: "purchase_order", EntityID: out.ID, LocationID: out.LocationID, Before: before, After: out})
		})
		return out, err
	}
}

// ---------------------------------------------------------------------------
// Supplier invoices
// ---------------------------------------------------------------------------

type InvoiceLineInput struct {
	refs.ItemRef
	Quantity  *decimal.Decimal `json:"quantity" validate:"required"`
	UnitPrice *decimal.Decimal `json:"unitPrice" validate:"required"`
}

type SupplierInvoiceInput struct {
	SupplierID    string             `json:"supplierId" validate:"required"`
	POID          string             `json:"purchaseOrderId,omitempty"`
	InvoiceNumber string             `json:"invoiceNumber" validate:"required,max=100"`
	InvoiceDate   httpx.Date         `json:"invoiceDate" validate:"required"`
	Total         *decimal.Decimal   `json:"total" validate:"required"`
	Currency      string             `json:"currency,omitempty" validate:"omitempty,currency"`
	ExternalID    string             `json:"externalId,omitempty" validate:"omitempty,extid"`
	Lines         []InvoiceLineInput `json:"lines,omitempty" validate:"dive"`
}

type InvoiceIssue struct {
	ItemID  string `json:"itemId,omitempty"`
	Code    string `json:"code" doc:"quantity_not_received, price_changed, not_on_order, total_mismatch"`
	Message string `json:"message"`
}

type SupplierInvoice struct {
	ID            string          `json:"id"`
	SupplierID    string          `json:"supplierId"`
	POID          *string         `json:"purchaseOrderId"`
	InvoiceNumber string          `json:"invoiceNumber"`
	InvoiceDate   httpx.Date      `json:"invoiceDate"`
	Total         decimal.Decimal `json:"total"`
	Currency      string          `json:"currency"`
	Lines         json.RawMessage `json:"lines"`
	Status        string          `json:"status" doc:"matched, mismatch, approved or disputed"`
	Issues        []InvoiceIssue  `json:"issues"`
	ExternalID    *string         `json:"externalId"`
	CreatedAt     time.Time       `json:"createdAt"`
}

const sinCols = `id, supplier_id, po_id, invoice_number, invoice_date, total, currency, lines, status, issues, external_id, created_at`

func scanSIN(r pgx.Row) (SupplierInvoice, error) {
	var s SupplierInvoice
	var d time.Time
	err := r.Scan(&s.ID, &s.SupplierID, &s.POID, &s.InvoiceNumber, &d, &s.Total, &s.Currency, &s.Lines, &s.Status, &s.Issues, &s.ExternalID, &s.CreatedAt)
	s.InvoiceDate = httpx.NewDate(d)
	return s, err
}

// ---------------------------------------------------------------------------
// Suggested orders
// ---------------------------------------------------------------------------

type SuggestedLine struct {
	ItemID       string          `json:"itemId"`
	SKU          string          `json:"sku"`
	Name         string          `json:"name"`
	OnHand       decimal.Decimal `json:"onHand"`
	OnOrder      decimal.Decimal `json:"onOrder"`
	ParLevel     decimal.Decimal `json:"parLevel"`
	DailyUsage   decimal.Decimal `json:"dailyUsage" doc:"Average over the last 28 days"`
	CoverDays    int             `json:"coverDays" doc:"Days until the delivery after this one"`
	SuggestedQty decimal.Decimal `json:"suggestedQty" doc:"Base units, rounded up to whole packs"`
	Packs        decimal.Decimal `json:"packs"`
	PackPrice    decimal.Decimal `json:"packPrice"`
	Amount       decimal.Decimal `json:"amount"`
}

type Suggestion struct {
	SupplierID   string          `json:"supplierId"`
	LocationID   string          `json:"locationId"`
	NextOrderOn  httpx.Date      `json:"nextOrderOn"`
	Lines        []SuggestedLine `json:"lines"`
	Total        decimal.Decimal `json:"total"`
	BelowMinimum bool            `json:"belowMinimum" doc:"Total is below the supplier's minimum order value"`
}

// daysUntilNext returns days from `from` to the next order day (1..7), or 7.
func daysUntilNext(from time.Time, orderDays []int32) int {
	if len(orderDays) == 0 {
		return 7
	}
	for d := 1; d <= 7; d++ {
		if slices.Contains(orderDays, int32(from.AddDate(0, 0, d).Weekday())) {
			return d
		}
	}
	return 7
}

func suggest(c *httpx.Ctx, q db.Querier, sup Supplier, locID string, today time.Time) (Suggestion, error) {
	out := Suggestion{SupplierID: sup.ID, LocationID: locID, NextOrderOn: httpx.NewDate(today), Lines: []SuggestedLine{}, Total: decimal.Zero}
	cover := sup.LeadTimeDays + daysUntilNext(today, sup.OrderDays)
	rows, err := q.Query(c, `
		SELECT c.item_id, i.sku, i.name, c.pack_size, c.price, c.min_packs,
		       coalesce(l.on_hand, 0), coalesce(l.par_level, 0),
		       coalesce((SELECT sum(pl.quantity - pl.received_qty) FROM purchase_order_lines pl
		                 JOIN purchase_orders p ON p.id = pl.po_id
		                 WHERE pl.item_id = c.item_id AND p.location_id = $2
		                   AND p.status IN ('approved', 'sent', 'partially_received', 'awaiting_approval')), 0),
		       coalesce((SELECT -sum(m.quantity) FROM stock_movements m
		                 WHERE m.item_id = c.item_id AND m.location_id = $2 AND m.type IN ('sale', 'shipment', 'waste', 'reversal')
		                   AND m.occurred_at >= now() - interval '28 days'), 0) / 28
		FROM supplier_catalog c JOIN items i ON i.id = c.item_id
		LEFT JOIN stock_levels l ON l.item_id = c.item_id AND l.location_id = $2
		WHERE c.supplier_id = $1 AND i.archived_at IS NULL ORDER BY i.name`, sup.ID, locID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var l SuggestedLine
		var pack, minPacks decimal.Decimal
		if err := rows.Scan(&l.ItemID, &l.SKU, &l.Name, &pack, &l.PackPrice, &minPacks, &l.OnHand, &l.ParLevel, &l.OnOrder, &l.DailyUsage); err != nil {
			return out, err
		}
		l.DailyUsage = decimal.Max(l.DailyUsage, decimal.Zero).Round(4)
		l.CoverDays = cover
		need := l.ParLevel.Add(l.DailyUsage.Mul(decimal.NewFromInt(int64(cover)))).Sub(l.OnHand).Sub(l.OnOrder)
		if !need.IsPositive() {
			continue
		}
		l.Packs = decimal.Max(need.Div(pack).Ceil(), minPacks)
		l.SuggestedQty = l.Packs.Mul(pack)
		l.Amount = l.Packs.Mul(l.PackPrice).Round(2)
		out.Total = out.Total.Add(l.Amount)
		out.Lines = append(out.Lines, l)
	}
	if sup.MinOrderValue != nil && out.Total.LessThan(*sup.MinOrderValue) && len(out.Lines) > 0 {
		out.BelowMinimum = true
	}
	return out, rows.Err()
}

type poList struct {
	httpx.ListParams
	Status     string `json:"status,omitempty"`
	SupplierID string `json:"supplierId,omitempty"`
	LocationID string `json:"locationId,omitempty"`
}

type sinList struct {
	Data []SupplierInvoice `json:"data"`
}

// Routes returns the purchasing routes.
func Routes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "GET", Path: "/suppliers/{id}/catalog", Tag: tag, Feature: feature, Scope: "purchasing:read",
			Summary: "A supplier's catalog (order guide)", Response: catalogList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				if _, err := suppliers.Get(c, c.App.Pool, "id", c.Param("id"), false); err != nil {
					return nil, err
				}
				return loadCatalog(c, c.App.Pool, c.Param("id"))
			},
		},
		{
			Method: "PUT", Path: "/suppliers/{id}/catalog", Tag: tag, Feature: feature, Scope: "purchasing:write",
			Summary: "Replace a supplier's catalog", Body: CatalogInput{}, Response: catalogList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in CatalogInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out catalogList
				err := c.InTx(func(tx pgx.Tx) error {
					sup, err := suppliers.Get(c, tx, "id", c.Param("id"), true)
					if err != nil {
						return err
					}
					if _, err := tx.Exec(c, `DELETE FROM supplier_catalog WHERE supplier_id=$1`, sup.ID); err != nil {
						return err
					}
					one := decimal.NewFromInt(1)
					for i, e := range in.Items {
						path := fmt.Sprintf("items[%d]", i)
						item, err := refs.Item(c, tx, e.ItemRef, path)
						if err != nil {
							return err
						}
						pack, minPacks := one, one
						if e.PackSize != nil {
							pack = *e.PackSize
						}
						if e.MinPacks != nil {
							minPacks = *e.MinPacks
						}
						if !pack.IsPositive() || e.Price.IsNegative() {
							return httpx.Validation(httpx.FieldError{Path: path, Message: "packSize must be > 0 and price ≥ 0"})
						}
						if _, err := tx.Exec(c, `INSERT INTO supplier_catalog (supplier_id, item_id, supplier_sku, pack_size, price, min_packs)
							VALUES ($1,$2,nullif($3,''),$4,$5,$6)`, sup.ID, item, e.SupplierSKU, pack, *e.Price, minPacks); err != nil {
							if db.IsUniqueViolation(err) {
								return httpx.Validation(httpx.FieldError{Path: path, Message: "Item listed twice"})
							}
							return err
						}
					}
					out, err = loadCatalog(c, tx, sup.ID)
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "supplier.catalog", EntityType: "supplier", EntityID: sup.ID, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/suggested-orders", Tag: tag, Feature: "purchasing.suggested_orders", Scope: "purchasing:read",
			Summary:     "Suggested order for a supplier and location",
			Description: "par level + daily usage × (lead time + days to the next order day) − on hand − on order, rounded up to whole packs.",
			Response:    Suggestion{},
			Handler: func(c *httpx.Ctx) (any, error) {
				sup, err := suppliers.Get(c, c.App.Pool, "id", c.Query("supplierId"), false)
				if err != nil {
					return nil, err
				}
				loc, err := refs.Location(c, c.App.Pool, refs.LocationRef{LocationID: c.Query("locationId"), LocationExternalID: c.Query("locationExternalId")}, "")
				if err != nil {
					return nil, err
				}
				return suggest(c, c.App.Pool, sup, loc, time.Now())
			},
		},
		{
			Method: "POST", Path: "/purchase-orders", Tag: tag, Feature: feature, Scope: "purchasing:write",
			Summary:     "Create a purchase order",
			Description: "Orders above the company's approval limit start as awaiting_approval; others are approved straight away.",
			Body:        POInput{}, Response: PurchaseOrder{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in POInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out PurchaseOrder
				err := c.InTx(func(tx pgx.Tx) error {
					sup, err := suppliers.Get(c, tx, "id", in.SupplierID, false)
					if err != nil {
						return httpx.Validation(httpx.FieldError{Path: "supplierId", Message: "Unknown supplier"})
					}
					loc, err := refs.Location(c, tx, in.LocationRef, "")
					if err != nil {
						return err
					}
					id := ids.New(ids.PurchaseOrder)
					if _, err := tx.Exec(c, `INSERT INTO purchase_orders (id, supplier_id, location_id, expected_on, currency, notes, external_id)
						VALUES ($1,$2,$3,$4,$5,nullif($6,''),nullif($7,''))`, id, sup.ID, loc, in.ExpectedOn, sup.Currency, in.Notes, in.ExternalID); err != nil {
						if db.IsUniqueViolation(err) {
							return httpx.Conflict("Another purchase order has this externalId.")
						}
						return err
					}
					total := decimal.Zero
					for i, l := range in.Lines {
						path := fmt.Sprintf("lines[%d]", i)
						if !l.Quantity.IsPositive() {
							return httpx.Validation(httpx.FieldError{Path: path + ".quantity", Message: "Must be greater than zero"})
						}
						item, err := refs.Item(c, tx, l.ItemRef, path)
						if err != nil {
							return err
						}
						price := l.UnitPrice
						if price == nil {
							var pack, packPrice decimal.Decimal
							err := tx.QueryRow(c, `SELECT pack_size, price FROM supplier_catalog WHERE supplier_id=$1 AND item_id=$2`, sup.ID, item).Scan(&pack, &packPrice)
							if errors.Is(err, pgx.ErrNoRows) {
								return httpx.Validation(httpx.FieldError{Path: path + ".unitPrice", Message: "Not in the supplier's catalog; send unitPrice"})
							}
							if err != nil {
								return err
							}
							p := packPrice.Div(pack).Round(4)
							price = &p
						}
						if _, err := tx.Exec(c, `INSERT INTO purchase_order_lines (po_id, line_no, item_id, quantity, unit_price) VALUES ($1,$2,$3,$4,$5)`,
							id, i+1, item, *l.Quantity, *price); err != nil {
							return err
						}
						total = total.Add(l.Quantity.Mul(*price))
					}
					var limit *decimal.Decimal
					if err := tx.QueryRow(c, `SELECT po_approval_limit FROM company LIMIT 1`).Scan(&limit); err != nil {
						return err
					}
					status, approvedAt := "approved", "now()"
					if limit != nil && total.GreaterThan(*limit) {
						status, approvedAt = "awaiting_approval", "NULL"
					}
					if _, err := tx.Exec(c, `UPDATE purchase_orders SET total=$2, status=$3, approved_at=`+approvedAt+` WHERE id=$1`, id, total.Round(2), status); err != nil {
						return err
					}
					out, err = loadPO(c, tx, id, false)
					if err != nil {
						return err
					}
					if err := c.Record(tx, httpx.Change{Action: "purchase_order.create", EventType: "purchase_order.created", Feature: feature,
						EntityType: "purchase_order", EntityID: id, LocationID: loc, After: out}); err != nil {
						return err
					}
					if status == "approved" {
						return c.Record(tx, httpx.Change{Action: "purchase_order.approve", EventType: "purchase_order.approved", Feature: feature,
							EntityType: "purchase_order", EntityID: id, LocationID: loc, After: out})
					}
					return nil
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/purchase-orders", Tag: tag, Feature: feature, Scope: "purchasing:read",
			Summary: "List purchase orders", Query: poList{}, Response: httpx.Page[PurchaseOrder]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				var out httpx.Page[PurchaseOrder]
				err = c.InTx(func(tx pgx.Tx) error {
					rows, err := tx.Query(c, `SELECT id FROM purchase_orders WHERE id > $1 AND ($2 = '' OR status = $2)
						AND ($3 = '' OR supplier_id = $3) AND ($4 = '' OR location_id = $4) ORDER BY id LIMIT $5`,
						lp.AfterID, c.Query("status"), c.Query("supplierId"), c.Query("locationId"), lp.Limit+1)
					if err != nil {
						return err
					}
					idList, err := pgx.CollectRows(rows, pgx.RowTo[string])
					if err != nil {
						return err
					}
					var list []PurchaseOrder
					for _, id := range idList {
						p, err := loadPO(c, tx, id, false)
						if err != nil {
							return err
						}
						list = append(list, p)
					}
					out = httpx.NewPage(list, lp.Limit, func(p PurchaseOrder) string { return p.ID })
					return nil
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/purchase-orders/{id}", Tag: tag, Feature: feature, Scope: "purchasing:read",
			Summary: "Get a purchase order", Response: PurchaseOrder{},
			Handler: func(c *httpx.Ctx) (any, error) { return loadPO(c, c.App.Pool, c.Param("id"), false) },
		},
		{
			Method: "POST", Path: "/purchase-orders/{id}:approve", Tag: tag, Feature: feature, Scope: "purchasing:write",
			Summary: "Approve a purchase order", Response: PurchaseOrder{},
			Handler: transition([]string{"awaiting_approval", "draft"}, "approved", "purchase_order.approved", "approved_at"),
		},
		{
			Method: "POST", Path: "/purchase-orders/{id}:send", Tag: tag, Feature: feature, Scope: "purchasing:write",
			Summary:     "Mark a purchase order as sent to the supplier",
			Description: "Emits purchase_order.sent so a supplier integration can submit it.",
			Response:    PurchaseOrder{},
			Handler:     transition([]string{"approved"}, "sent", "purchase_order.sent", "sent_at"),
		},
		{
			Method: "POST", Path: "/purchase-orders/{id}:cancel", Tag: tag, Feature: feature, Scope: "purchasing:write",
			Summary: "Cancel a purchase order", Response: PurchaseOrder{},
			Handler: transition([]string{"draft", "awaiting_approval", "approved", "sent"}, "cancelled", "", ""),
		},
		{
			Method: "POST", Path: "/purchase-orders/{id}:receive", Tag: tag, Feature: feature, Scope: "purchasing:write",
			Summary:     "Receive a delivery",
			Description: "Adds received stock at the order's unit price (updating average cost). Partial deliveries leave the order partially_received.",
			Body:        ReceiveInput{}, Response: PurchaseOrder{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in ReceiveInput
				if err := c.DecodeOptional(&in); err != nil {
					return nil, err
				}
				var out PurchaseOrder
				err := c.InTx(func(tx pgx.Tx) error {
					po, err := loadPO(c, tx, c.Param("id"), true)
					if err != nil {
						return err
					}
					if !slices.Contains([]string{"approved", "sent", "partially_received"}, po.Status) {
						return httpx.Conflict("A " + po.Status + " purchase order can't be received.")
					}
					type rec struct {
						qty, damaged decimal.Decimal
						note         string
					}
					receipts := map[string]rec{}
					if len(in.Lines) == 0 {
						for _, l := range po.Lines {
							if out := l.Quantity.Sub(l.ReceivedQty); out.IsPositive() {
								receipts[l.ItemID] = rec{qty: out}
							}
						}
					}
					for i, l := range in.Lines {
						path := fmt.Sprintf("lines[%d]", i)
						item, err := refs.Item(c, tx, l.ItemRef, path)
						if err != nil {
							return err
						}
						found := false
						for _, pl := range po.Lines {
							found = found || pl.ItemID == item
						}
						if !found {
							return httpx.Validation(httpx.FieldError{Path: path + ".itemId", Message: "Not on this purchase order"})
						}
						if l.Quantity.IsNegative() {
							return httpx.Validation(httpx.FieldError{Path: path + ".quantity", Message: "Must be zero or more"})
						}
						r := receipts[item]
						r.qty = r.qty.Add(*l.Quantity)
						if l.Damaged != nil {
							r.damaged = r.damaged.Add(*l.Damaged)
						}
						r.note = l.Note
						receipts[item] = r
					}
					var logLines []map[string]any
					for _, pl := range po.Lines {
						r, ok := receipts[pl.ItemID]
						if !ok || r.qty.IsZero() && r.damaged.IsZero() {
							continue
						}
						delete(receipts, pl.ItemID) // apply once, to the first line with this item
						price := pl.UnitPrice
						if _, err := inventory.Move(c, tx, inventory.Movement{ItemID: pl.ItemID, LocationID: po.LocationID, Quantity: r.qty,
							Type: "receipt", UnitCost: &price, SourceType: "purchase_order", SourceID: po.ID, Note: r.note}); err != nil {
							return err
						}
						if _, err := tx.Exec(c, `UPDATE purchase_order_lines SET received_qty = received_qty + $3 WHERE po_id=$1 AND line_no=$2`,
							po.ID, pl.LineNo, r.qty); err != nil {
							return err
						}
						logLines = append(logLines, map[string]any{"itemId": pl.ItemID, "quantity": r.qty, "damaged": r.damaged, "note": r.note})
					}
					if len(logLines) == 0 {
						return httpx.Validation(httpx.FieldError{Path: "lines", Message: "Nothing to receive"})
					}
					linesJSON, _ := json.Marshal(logLines)
					if _, err := tx.Exec(c, `INSERT INTO goods_receipts (id, po_id, location_id, lines, note) VALUES ($1,$2,$3,$4,nullif($5,''))`,
						ids.New(ids.GoodsReceipt), po.ID, po.LocationID, linesJSON, in.Note); err != nil {
						return err
					}
					var outstanding bool
					if err := tx.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM purchase_order_lines WHERE po_id=$1 AND received_qty < quantity)`, po.ID).Scan(&outstanding); err != nil {
						return err
					}
					status := "received"
					if outstanding {
						status = "partially_received"
					}
					if _, err := tx.Exec(c, `UPDATE purchase_orders SET status=$2, version=version+1, updated_at=now() WHERE id=$1`, po.ID, status); err != nil {
						return err
					}
					out, err = loadPO(c, tx, po.ID, false)
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "purchase_order.receive", EventType: "purchase_order.received", Feature: feature,
						EntityType: "purchase_order", EntityID: po.ID, LocationID: po.LocationID, Before: po, After: out})
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/supplier-invoices", Tag: tag, Feature: feature, Scope: "purchasing:write",
			Summary:     "Record a supplier invoice and match it",
			Description: "With invoice matching on, the invoice is compared with the purchase order and goods received; differences are listed as issues.",
			Body:        SupplierInvoiceInput{}, Response: SupplierInvoice{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in SupplierInvoiceInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out SupplierInvoice
				err := c.InTx(func(tx pgx.Tx) error {
					sup, err := suppliers.Get(c, tx, "id", in.SupplierID, false)
					if err != nil {
						return httpx.Validation(httpx.FieldError{Path: "supplierId", Message: "Unknown supplier"})
					}
					currency := in.Currency
					if currency == "" {
						currency = sup.Currency
					}
					type line struct {
						ItemID    string          `json:"itemId"`
						Quantity  decimal.Decimal `json:"quantity"`
						UnitPrice decimal.Decimal `json:"unitPrice"`
					}
					var lines []line
					for i, l := range in.Lines {
						item, err := refs.Item(c, tx, l.ItemRef, fmt.Sprintf("lines[%d]", i))
						if err != nil {
							return err
						}
						lines = append(lines, line{ItemID: item, Quantity: *l.Quantity, UnitPrice: *l.UnitPrice})
					}
					issues := []InvoiceIssue{}
					matchOn, err := c.App.Features.IsEnabled(c, "purchasing.invoice_matching")
					if err != nil {
						return err
					}
					var poID *string
					if in.POID != "" {
						po, err := loadPO(c, tx, in.POID, false)
						if err != nil {
							return httpx.Validation(httpx.FieldError{Path: "purchaseOrderId", Message: "Unknown purchase order"})
						}
						if po.SupplierID != sup.ID {
							return httpx.Validation(httpx.FieldError{Path: "purchaseOrderId", Message: "Belongs to another supplier"})
						}
						poID = &po.ID
						if matchOn {
							byItem := map[string]POLine{}
							for _, pl := range po.Lines {
								byItem[pl.ItemID] = pl
							}
							sum := decimal.Zero
							for _, l := range lines {
								sum = sum.Add(l.Quantity.Mul(l.UnitPrice))
								pl, ok := byItem[l.ItemID]
								switch {
								case !ok:
									issues = append(issues, InvoiceIssue{ItemID: l.ItemID, Code: "not_on_order", Message: "Billed but not on the purchase order"})
								case l.Quantity.GreaterThan(pl.ReceivedQty):
									issues = append(issues, InvoiceIssue{ItemID: l.ItemID, Code: "quantity_not_received",
										Message: fmt.Sprintf("Billed %s, received %s", l.Quantity, pl.ReceivedQty)})
								}
								if ok && !l.UnitPrice.Round(4).Equal(pl.UnitPrice.Round(4)) {
									issues = append(issues, InvoiceIssue{ItemID: l.ItemID, Code: "price_changed",
										Message: fmt.Sprintf("Billed %s per unit, ordered at %s", l.UnitPrice, pl.UnitPrice)})
								}
							}
							if len(lines) > 0 && !sum.Round(2).Equal(in.Total.Round(2)) {
								issues = append(issues, InvoiceIssue{Code: "total_mismatch", Message: fmt.Sprintf("Lines add up to %s, invoice total is %s", sum.Round(2), in.Total.Round(2))})
							}
						}
					}
					status := "matched"
					if len(issues) > 0 {
						status = "mismatch"
					}
					if lines == nil {
						lines = []line{}
					}
					linesJSON, _ := json.Marshal(lines)
					issuesJSON, _ := json.Marshal(issues)
					out, err = scanSIN(tx.QueryRow(c, `INSERT INTO supplier_invoices (id, supplier_id, po_id, invoice_number, invoice_date, total,
						currency, lines, status, issues, external_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,nullif($11,'')) RETURNING `+sinCols,
						ids.New(ids.SupplierInvoice), sup.ID, poID, in.InvoiceNumber, in.InvoiceDate, *in.Total, currency, linesJSON, status, issuesJSON, in.ExternalID))
					if db.IsUniqueViolation(err) {
						return httpx.Conflict("This supplier invoice number was already recorded.")
					}
					if err != nil {
						return err
					}
					event := ""
					if status == "mismatch" {
						event = "supplier_invoice.mismatch"
					}
					return c.Record(tx, httpx.Change{Action: "supplier_invoice.create", EventType: event, Feature: "purchasing.invoice_matching",
						EntityType: "supplier_invoice", EntityID: out.ID, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/supplier-invoices", Tag: tag, Feature: feature, Scope: "purchasing:read",
			Summary: "List supplier invoices", Response: sinList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT `+sinCols+` FROM supplier_invoices WHERE ($1 = '' OR supplier_id = $1)
					AND ($2 = '' OR status = $2) ORDER BY invoice_date DESC, id DESC LIMIT 200`, c.Query("supplierId"), c.Query("status"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (SupplierInvoice, error) { return scanSIN(r) })
				if list == nil {
					list = []SupplierInvoice{}
				}
				return sinList{Data: list}, err
			},
		},
		sinDecision("approve", "approved"),
		sinDecision("dispute", "disputed"),
	}
	return append(routes, suppliers.Routes()...)
}

func sinDecision(action, status string) httpx.Route {
	return httpx.Route{
		Method: "POST", Path: "/supplier-invoices/{id}:" + action, Tag: tag, Feature: "purchasing.invoice_matching", Scope: "purchasing:write",
		Summary:  map[string]string{"approve": "Approve a supplier invoice (accept any differences)", "dispute": "Dispute a supplier invoice"}[action],
		Response: SupplierInvoice{},
		Handler: func(c *httpx.Ctx) (any, error) {
			var out SupplierInvoice
			err := c.InTx(func(tx pgx.Tx) error {
				before, err := scanSIN(tx.QueryRow(c, `SELECT `+sinCols+` FROM supplier_invoices WHERE id=$1 FOR UPDATE`, c.Param("id")))
				if errors.Is(err, pgx.ErrNoRows) {
					return httpx.NotFound("Supplier invoice not found.")
				}
				if err != nil {
					return err
				}
				out, err = scanSIN(tx.QueryRow(c, `UPDATE supplier_invoices SET status=$2, updated_at=now() WHERE id=$1 RETURNING `+sinCols, before.ID, status))
				if err != nil {
					return err
				}
				return c.Record(tx, httpx.Change{Action: "supplier_invoice." + action, EntityType: "supplier_invoice", EntityID: out.ID, Before: before, After: out})
			})
			return out, err
		},
	}
}
