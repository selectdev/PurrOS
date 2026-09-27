package sales

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
	"github.com/selectdev/purros/api/internal/pdf"
	"github.com/selectdev/purros/api/internal/refs"
	"github.com/shopspring/decimal"
)

const (
	ordersFeature  = "sales.orders"
	invoiceFeature = "sales.invoicing"
)

// ---------------------------------------------------------------------------
// Customers
// ---------------------------------------------------------------------------

type Customer struct {
	ID               string          `json:"id" db:"id"`
	Name             string          `json:"name" db:"name"`
	Email            *string         `json:"email" db:"email"`
	Phone            *string         `json:"phone" db:"phone"`
	BillingAddress   json.RawMessage `json:"billingAddress" db:"billing_address"`
	ShippingAddress  json.RawMessage `json:"shippingAddress" db:"shipping_address"`
	PaymentTermsDays int             `json:"paymentTermsDays" db:"payment_terms_days"`
	ExternalID       *string         `json:"externalId" db:"external_id"`
	Version          int             `json:"version" db:"version"`
	CreatedAt        time.Time       `json:"createdAt" db:"created_at"`
	UpdatedAt        time.Time       `json:"updatedAt" db:"updated_at"`
	ArchivedAt       *time.Time      `json:"archivedAt" db:"archived_at"`
}

type CustomerInput struct {
	Name             string          `json:"name" db:"name" validate:"required,max=200"`
	Email            *string         `json:"email,omitempty" db:"email" validate:"omitempty,email"`
	Phone            *string         `json:"phone,omitempty" db:"phone" validate:"omitempty,max=40"`
	BillingAddress   json.RawMessage `json:"billingAddress,omitempty" db:"billing_address"`
	ShippingAddress  json.RawMessage `json:"shippingAddress,omitempty" db:"shipping_address"`
	PaymentTermsDays int             `json:"paymentTermsDays,omitempty" db:"payment_terms_days" validate:"min=0,max=365"`
	ExternalID       *string         `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
}

var customers = &crud.Resource[Customer, CustomerInput]{
	Path: "/customers", Table: "customers", Prefix: ids.Customer, Noun: "customer", Tag: "Sales",
	Feature: ordersFeature, ReadScope: "sales:read", WriteScope: "sales:write", External: true, Archive: true,
	Check: func(c *httpx.Ctx, q db.Querier, in *CustomerInput, _ *Customer) error {
		for _, a := range []*json.RawMessage{&in.BillingAddress, &in.ShippingAddress} {
			if len(*a) == 0 {
				*a = nil
			} else if !json.Valid(*a) {
				return httpx.Validation(httpx.FieldError{Path: "address", Message: "Must be a JSON object"})
			}
		}
		return nil
	},
}

// ---------------------------------------------------------------------------
// Sales orders
// ---------------------------------------------------------------------------

type OrderLine struct {
	LineNo    int             `json:"lineNo"`
	ItemID    string          `json:"itemId"`
	Quantity  decimal.Decimal `json:"quantity"`
	UnitPrice decimal.Decimal `json:"unitPrice"`
	Amount    decimal.Decimal `json:"amount"`
}

type SalesOrder struct {
	ID             string          `json:"id"`
	Number         string          `json:"number"`
	Source         string          `json:"source"`
	ExternalID     *string         `json:"externalId"`
	CustomerID     *string         `json:"customerId"`
	LocationID     string          `json:"locationId" doc:"Where it's fulfilled from"`
	Status         string          `json:"status" doc:"open, shipped or cancelled"`
	PaymentStatus  string          `json:"paymentStatus"`
	Shipping       json.RawMessage `json:"shipping"`
	TrackingNumber *string         `json:"trackingNumber"`
	Total          decimal.Decimal `json:"total"`
	Currency       string          `json:"currency"`
	ShippedAt      *time.Time      `json:"shippedAt"`
	Version        int             `json:"version"`
	Lines          []OrderLine     `json:"lines"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

type OrderLineInput struct {
	refs.ItemRef
	Quantity  *decimal.Decimal `json:"quantity" validate:"required"`
	UnitPrice *decimal.Decimal `json:"unitPrice" validate:"required"`
}

type OrderInput struct {
	Source             string `json:"source,omitempty" validate:"max=100" doc:"For orders from other systems, e.g. web-store"`
	CustomerID         string `json:"customerId,omitempty"`
	CustomerExternalID string `json:"customerExternalId,omitempty"`
	Customer           *struct {
		Name       string `json:"name" validate:"required"`
		Email      string `json:"email,omitempty" validate:"omitempty,email"`
		ExternalID string `json:"externalId,omitempty"`
	} `json:"customer,omitempty" doc:"Create or match the customer inline (by externalId)"`
	refs.LocationRef
	Lines         []OrderLineInput `json:"lines" validate:"required,min=1,dive"`
	Shipping      json.RawMessage  `json:"shipping,omitempty"`
	PaymentStatus string           `json:"paymentStatus,omitempty" validate:"omitempty,oneof=unpaid paid refunded"`
	Status        string           `json:"status,omitempty" validate:"omitempty,oneof=open cancelled" doc:"Send cancelled to cancel via upsert"`
	Currency      string           `json:"currency,omitempty" validate:"omitempty,currency"`
}

type ShipInput struct {
	TrackingNumber string `json:"trackingNumber,omitempty" validate:"max=100"`
}

const soCols = `id, number, source, external_id, customer_id, location_id, status, payment_status, coalesce(shipping, 'null'::jsonb),
	tracking_number, total, currency, shipped_at, version, created_at, updated_at`

func loadOrder(ctx context.Context, q db.Querier, where string, arg any, lock bool) (SalesOrder, error) {
	sql := `SELECT ` + soCols + ` FROM sales_orders WHERE ` + where
	if lock {
		sql += ` FOR UPDATE`
	}
	var o SalesOrder
	err := q.QueryRow(ctx, sql, arg).Scan(&o.ID, &o.Number, &o.Source, &o.ExternalID, &o.CustomerID, &o.LocationID, &o.Status,
		&o.PaymentStatus, &o.Shipping, &o.TrackingNumber, &o.Total, &o.Currency, &o.ShippedAt, &o.Version, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, httpx.NotFound("Sales order not found.")
	}
	if err != nil {
		return o, err
	}
	rows, err := q.Query(ctx, `SELECT line_no, item_id, quantity, unit_price FROM sales_order_lines WHERE order_id=$1 ORDER BY line_no`, o.ID)
	if err != nil {
		return o, err
	}
	o.Lines, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (OrderLine, error) {
		var l OrderLine
		err := r.Scan(&l.LineNo, &l.ItemID, &l.Quantity, &l.UnitPrice)
		l.Amount = l.Quantity.Mul(l.UnitPrice).Round(2)
		return l, err
	})
	return o, err
}

func reserveLines(c *httpx.Ctx, tx pgx.Tx, o SalesOrder, sign int64) error {
	for _, l := range o.Lines {
		if err := inventory.Reserve(c, tx, l.ItemID, o.LocationID, l.Quantity.Mul(decimal.NewFromInt(sign))); err != nil {
			return err
		}
	}
	return nil
}

// saveOrder creates or replaces an order's contents. existing is nil for new orders.
func saveOrder(c *httpx.Ctx, tx pgx.Tx, in OrderInput, externalID string, existing *SalesOrder) (SalesOrder, error) {
	loc, err := refs.Location(c, tx, in.LocationRef, "")
	if err != nil {
		return SalesOrder{}, err
	}
	var customerID *string
	switch {
	case in.CustomerID != "":
		customerID = &in.CustomerID
	case in.CustomerExternalID != "":
		cu, err := customers.Get(c, tx, "external_id", in.CustomerExternalID, false)
		if err != nil {
			return SalesOrder{}, httpx.Validation(httpx.FieldError{Path: "customerExternalId", Message: "Unknown customer"})
		}
		customerID = &cu.ID
	case in.Customer != nil:
		if in.Customer.ExternalID != "" {
			if cu, err := customers.Get(c, tx, "external_id", in.Customer.ExternalID, false); err == nil {
				customerID = &cu.ID
			}
		}
		if customerID == nil {
			ci := CustomerInput{Name: in.Customer.Name}
			if in.Customer.Email != "" {
				ci.Email = &in.Customer.Email
			}
			if in.Customer.ExternalID != "" {
				ci.ExternalID = &in.Customer.ExternalID
			}
			cu, err := customers.Insert(c, tx, ci)
			if err != nil {
				return SalesOrder{}, err
			}
			customerID = &cu.ID
		}
	}
	currency := in.Currency
	if currency == "" {
		if err := tx.QueryRow(c, `SELECT currency FROM locations WHERE id=$1`, loc).Scan(&currency); err != nil {
			return SalesOrder{}, err
		}
	}
	source := in.Source
	if source == "" {
		source = "purros"
	}
	payment := in.PaymentStatus
	if payment == "" {
		payment = "unpaid"
	}
	var shipping []byte
	if len(in.Shipping) > 0 && string(in.Shipping) != "null" {
		if !json.Valid(in.Shipping) {
			return SalesOrder{}, httpx.Validation(httpx.FieldError{Path: "shipping", Message: "Must be a JSON object"})
		}
		shipping = in.Shipping
	}
	var id string
	var ext *string
	if externalID != "" {
		ext = &externalID
	}
	if existing == nil {
		id = ids.New(ids.SalesOrder)
		if _, err := tx.Exec(c, `INSERT INTO sales_orders (id, source, external_id, customer_id, location_id, payment_status, shipping, currency)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, id, source, ext, customerID, loc, payment, shipping, currency); err != nil {
			if db.IsForeignKeyViolation(err) {
				return SalesOrder{}, httpx.Validation(httpx.FieldError{Path: "customerId", Message: "Unknown customer"})
			}
			return SalesOrder{}, err
		}
	} else {
		id = existing.ID
		if err := reserveLines(c, tx, *existing, -1); err != nil {
			return SalesOrder{}, err
		}
		if _, err := tx.Exec(c, `DELETE FROM sales_order_lines WHERE order_id=$1`, id); err != nil {
			return SalesOrder{}, err
		}
		if _, err := tx.Exec(c, `UPDATE sales_orders SET customer_id=$2, location_id=$3, payment_status=$4, shipping=$5, currency=$6,
			version=version+1, updated_at=now() WHERE id=$1`, id, customerID, loc, payment, shipping, currency); err != nil {
			return SalesOrder{}, err
		}
	}
	total := decimal.Zero
	for i, l := range in.Lines {
		path := fmt.Sprintf("lines[%d]", i)
		if !l.Quantity.IsPositive() || l.UnitPrice.IsNegative() {
			return SalesOrder{}, httpx.Validation(httpx.FieldError{Path: path, Message: "quantity must be > 0 and unitPrice ≥ 0"})
		}
		item, err := refs.Item(c, tx, l.ItemRef, path)
		if err != nil {
			return SalesOrder{}, err
		}
		if _, err := tx.Exec(c, `INSERT INTO sales_order_lines (order_id, line_no, item_id, quantity, unit_price) VALUES ($1,$2,$3,$4,$5)`,
			id, i+1, item, *l.Quantity, *l.UnitPrice); err != nil {
			return SalesOrder{}, err
		}
		total = total.Add(l.Quantity.Mul(*l.UnitPrice))
	}
	if _, err := tx.Exec(c, `UPDATE sales_orders SET total=$2 WHERE id=$1`, id, total.Round(2)); err != nil {
		return SalesOrder{}, err
	}
	o, err := loadOrder(c, tx, "id = $1", id, false)
	if err != nil {
		return o, err
	}
	return o, reserveLines(c, tx, o, 1)
}

func cancelOrder(c *httpx.Ctx, tx pgx.Tx, o SalesOrder) (SalesOrder, error) {
	if o.Status == "cancelled" {
		return o, nil
	}
	if o.Status == "shipped" {
		return o, httpx.Conflict("A shipped order can't be cancelled.")
	}
	if err := reserveLines(c, tx, o, -1); err != nil {
		return o, err
	}
	if _, err := tx.Exec(c, `UPDATE sales_orders SET status='cancelled', version=version+1, updated_at=now() WHERE id=$1`, o.ID); err != nil {
		return o, err
	}
	out, err := loadOrder(c, tx, "id = $1", o.ID, false)
	if err != nil {
		return out, err
	}
	return out, c.Record(tx, httpx.Change{Action: "sales_order.cancel", EventType: "sales_order.cancelled", Feature: ordersFeature,
		EntityType: "sales_order", EntityID: o.ID, LocationID: o.LocationID, Before: o, After: out})
}

// ---------------------------------------------------------------------------
// Invoices
// ---------------------------------------------------------------------------

type Invoice struct {
	ID           string          `json:"id"`
	Number       string          `json:"number"`
	SalesOrderID *string         `json:"salesOrderId"`
	CustomerID   *string         `json:"customerId"`
	IssuedOn     httpx.Date      `json:"issuedOn"`
	DueOn        httpx.Date      `json:"dueOn"`
	Lines        []OrderLine     `json:"lines"`
	Total        decimal.Decimal `json:"total"`
	Currency     string          `json:"currency"`
	Status       string          `json:"status" doc:"issued, paid or void"`
	PaidAt       *time.Time      `json:"paidAt"`
	CreatedAt    time.Time       `json:"createdAt"`
}

const invCols = `id, number, sales_order_id, customer_id, issued_on, due_on, lines, total, currency, status, paid_at, created_at`

func scanInvoice(r pgx.Row) (Invoice, error) {
	var i Invoice
	var issued, due time.Time
	err := r.Scan(&i.ID, &i.Number, &i.SalesOrderID, &i.CustomerID, &issued, &due, &i.Lines, &i.Total, &i.Currency, &i.Status, &i.PaidAt, &i.CreatedAt)
	i.IssuedOn, i.DueOn = httpx.NewDate(issued), httpx.NewDate(due)
	return i, err
}

func getInvoice(ctx context.Context, q db.Querier, id string, lock bool) (Invoice, error) {
	sql := `SELECT ` + invCols + ` FROM invoices WHERE id=$1`
	if lock {
		sql += ` FOR UPDATE`
	}
	i, err := scanInvoice(q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return i, httpx.NotFound("Invoice not found.")
	}
	return i, err
}

func invoicePDF(c *httpx.Ctx, inv Invoice) ([]byte, error) {
	var company, custName string
	if err := c.App.Pool.QueryRow(c, `SELECT name FROM company LIMIT 1`).Scan(&company); err != nil {
		return nil, err
	}
	if inv.CustomerID != nil {
		_ = c.App.Pool.QueryRow(c, `SELECT name FROM customers WHERE id=$1`, *inv.CustomerID).Scan(&custName)
	}
	itemNames := map[string]string{}
	for _, l := range inv.Lines {
		var n string
		if err := c.App.Pool.QueryRow(c, `SELECT sku || '  ' || name FROM items WHERE id=$1`, l.ItemID).Scan(&n); err == nil {
			itemNames[l.ItemID] = n
		}
	}
	lines := []pdf.Line{
		{X: 50, Y: 790, Size: 18, Bold: true, Text: company},
		{X: 400, Y: 790, Size: 18, Bold: true, Text: "INVOICE"},
		{X: 400, Y: 770, Text: "Number: " + inv.Number},
		{X: 400, Y: 756, Text: "Issued: " + inv.IssuedOn.String()},
		{X: 400, Y: 742, Text: "Due: " + inv.DueOn.String()},
		{X: 50, Y: 742, Text: "Bill to: " + custName},
		{X: 50, Y: 700, Bold: true, Text: "Item"},
		{X: 330, Y: 700, Bold: true, Text: "Qty"},
		{X: 400, Y: 700, Bold: true, Text: "Unit price"},
		{X: 490, Y: 700, Bold: true, Text: "Amount"},
	}
	y := 684.0
	for _, l := range inv.Lines {
		lines = append(lines,
			pdf.Line{X: 50, Y: y, Text: itemNames[l.ItemID]},
			pdf.Line{X: 330, Y: y, Text: l.Quantity.String()},
			pdf.Line{X: 400, Y: y, Text: l.UnitPrice.StringFixed(2)},
			pdf.Line{X: 490, Y: y, Text: l.Amount.StringFixed(2)})
		y -= 16
		if y < 80 {
			break
		}
	}
	lines = append(lines,
		pdf.Line{X: 400, Y: y - 12, Bold: true, Text: "Total " + inv.Currency},
		pdf.Line{X: 490, Y: y - 12, Bold: true, Text: inv.Total.StringFixed(2)},
		pdf.Line{X: 50, Y: 50, Size: 8, Text: "Status: " + inv.Status})
	return pdf.Document{Title: "Invoice " + inv.Number, Pages: [][]pdf.Line{lines}}.Bytes(), nil
}

type orderQuery struct {
	httpx.ListParams
	Status     string `json:"status,omitempty"`
	CustomerID string `json:"customerId,omitempty"`
	Source     string `json:"source,omitempty"`
}

type invoiceList struct {
	Data []Invoice `json:"data"`
}

// OrderRoutes returns the customer, sales order and invoice routes.
func OrderRoutes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "POST", Path: "/sales-orders", Tag: "Sales", Feature: ordersFeature, Scope: "sales:write",
			Summary: "Create a sales order (reserves stock)", Body: OrderInput{}, Response: SalesOrder{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in OrderInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out SalesOrder
				err := c.InTx(func(tx pgx.Tx) error {
					var err error
					out, err = saveOrder(c, tx, in, "", nil)
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "sales_order.create", EventType: "sales_order.created", Feature: ordersFeature,
						EntityType: "sales_order", EntityID: out.ID, LocationID: out.LocationID, After: out})
				})
				return out, err
			},
		},
		{
			Method: "PUT", Path: "/sales-orders/external/{externalId}", Tag: "Sales", Feature: ordersFeature, Scope: "sales:write",
			Summary:     "Create or update an order from another system (e.g. an online store)",
			Description: "Unique on (source, externalId). Send status: cancelled to cancel. Shipped orders can't change.",
			Body:        OrderInput{}, Response: SalesOrder{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in OrderInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if in.Source == "" || !httpx.ValidSource(in.Source) {
					return nil, httpx.Validation(httpx.FieldError{Path: "source", Message: "Required for external orders, e.g. web-store"})
				}
				ext := c.Param("externalId")
				var out SalesOrder
				created := false
				err := c.InTx(func(tx pgx.Tx) error {
					var existing SalesOrder
					err := tx.QueryRow(c, `SELECT id FROM sales_orders WHERE source=$1 AND external_id=$2 FOR UPDATE`, in.Source, ext).Scan(&existing.ID)
					switch {
					case errors.Is(err, pgx.ErrNoRows):
						created = true
						out, err = saveOrder(c, tx, in, ext, nil)
						if err != nil {
							return err
						}
						if err := c.Record(tx, httpx.Change{Action: "sales_order.create", EventType: "sales_order.created", Feature: ordersFeature,
							EntityType: "sales_order", EntityID: out.ID, LocationID: out.LocationID, After: out}); err != nil {
							return err
						}
					case err != nil:
						return err
					default:
						existing, err = loadOrder(c, tx, "id = $1", existing.ID, true)
						if err != nil {
							return err
						}
						if existing.Status == "shipped" {
							return httpx.Conflict("This order has shipped and can't be changed.")
						}
						if existing.Status == "cancelled" {
							out = existing
							return nil
						}
						out, err = saveOrder(c, tx, in, ext, &existing)
						if err != nil {
							return err
						}
						if err := c.Record(tx, httpx.Change{Action: "sales_order.update", EntityType: "sales_order", EntityID: out.ID,
							LocationID: out.LocationID, Before: existing, After: out}); err != nil {
							return err
						}
					}
					if in.Status == "cancelled" {
						out, err = cancelOrder(c, tx, out)
					}
					return err
				})
				if err != nil {
					return nil, err
				}
				if created {
					return httpx.Result{Status: 201, Body: out}, nil
				}
				return out, nil
			},
		},
		{
			Method: "GET", Path: "/sales-orders", Tag: "Sales", Feature: ordersFeature, Scope: "sales:read",
			Summary: "List sales orders", Query: orderQuery{}, Response: httpx.Page[SalesOrder]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				var out httpx.Page[SalesOrder]
				err = c.InTx(func(tx pgx.Tx) error {
					rows, err := tx.Query(c, `SELECT id FROM sales_orders WHERE id > $1 AND ($2 = '' OR status = $2)
						AND ($3 = '' OR customer_id = $3) AND ($4 = '' OR source = $4) ORDER BY id LIMIT $5`,
						lp.AfterID, c.Query("status"), c.Query("customerId"), c.Query("source"), lp.Limit+1)
					if err != nil {
						return err
					}
					idList, err := pgx.CollectRows(rows, pgx.RowTo[string])
					if err != nil {
						return err
					}
					var list []SalesOrder
					for _, id := range idList {
						o, err := loadOrder(c, tx, "id = $1", id, false)
						if err != nil {
							return err
						}
						list = append(list, o)
					}
					out = httpx.NewPage(list, lp.Limit, func(o SalesOrder) string { return o.ID })
					return nil
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/sales-orders/{id}", Tag: "Sales", Feature: ordersFeature, Scope: "sales:read",
			Summary: "Get a sales order", Response: SalesOrder{},
			Handler: func(c *httpx.Ctx) (any, error) { return loadOrder(c, c.App.Pool, "id = $1", c.Param("id"), false) },
		},
		{
			Method: "POST", Path: "/sales-orders/{id}:ship", Tag: "Sales", Feature: ordersFeature, Scope: "sales:write",
			Summary: "Ship an order (deducts stock)", Body: ShipInput{}, Response: SalesOrder{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in ShipInput
				if err := c.DecodeOptional(&in); err != nil {
					return nil, err
				}
				var out SalesOrder
				err := c.InTx(func(tx pgx.Tx) error {
					o, err := loadOrder(c, tx, "id = $1", c.Param("id"), true)
					if err != nil {
						return err
					}
					if o.Status != "open" {
						return httpx.Conflict("A " + o.Status + " order can't be shipped.")
					}
					var short []httpx.FieldError
					for i, l := range o.Lines {
						lvl, err := inventory.GetLevel(c, tx, l.ItemID, o.LocationID)
						if err != nil {
							return err
						}
						var track bool
						if err := tx.QueryRow(c, `SELECT track_stock FROM items WHERE id=$1`, l.ItemID).Scan(&track); err != nil {
							return err
						}
						if track && lvl.OnHand.LessThan(l.Quantity) {
							short = append(short, httpx.FieldError{Path: fmt.Sprintf("lines[%d]", i),
								Message: fmt.Sprintf("Only %s on hand, %s needed", lvl.OnHand, l.Quantity)})
						}
					}
					if len(short) > 0 {
						p := httpx.Validation(short...)
						p.Code, p.Title, p.Type = "insufficient_stock", "Insufficient stock", "https://purros.dev/errors/insufficient_stock"
						return p
					}
					if err := reserveLines(c, tx, o, -1); err != nil {
						return err
					}
					for _, l := range o.Lines {
						if _, err := inventory.Move(c, tx, inventory.Movement{ItemID: l.ItemID, LocationID: o.LocationID, Quantity: l.Quantity.Neg(),
							Type: "shipment", SourceType: "sales_order", SourceID: o.ID}); err != nil {
							return err
						}
					}
					if _, err := tx.Exec(c, `UPDATE sales_orders SET status='shipped', shipped_at=now(), tracking_number=nullif($2,''),
						version=version+1, updated_at=now() WHERE id=$1`, o.ID, in.TrackingNumber); err != nil {
						return err
					}
					out, err = loadOrder(c, tx, "id = $1", o.ID, false)
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "sales_order.ship", EventType: "sales_order.shipped", Feature: ordersFeature,
						EntityType: "sales_order", EntityID: o.ID, LocationID: o.LocationID, Before: o, After: out})
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/sales-orders/{id}:cancel", Tag: "Sales", Feature: ordersFeature, Scope: "sales:write",
			Summary: "Cancel an order (releases reserved stock)", Response: SalesOrder{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out SalesOrder
				err := c.InTx(func(tx pgx.Tx) error {
					o, err := loadOrder(c, tx, "id = $1", c.Param("id"), true)
					if err != nil {
						return err
					}
					out, err = cancelOrder(c, tx, o)
					return err
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/sales-orders/{id}:invoice", Tag: "Sales", Feature: invoiceFeature, Scope: "sales:write",
			Summary: "Issue an invoice for an order", Response: Invoice{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var out Invoice
				err := c.InTx(func(tx pgx.Tx) error {
					o, err := loadOrder(c, tx, "id = $1", c.Param("id"), true)
					if err != nil {
						return err
					}
					if o.Status == "cancelled" {
						return httpx.Conflict("A cancelled order can't be invoiced.")
					}
					var exists bool
					if err := tx.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM invoices WHERE sales_order_id=$1 AND status <> 'void')`, o.ID).Scan(&exists); err != nil {
						return err
					}
					if exists {
						return httpx.Conflict("This order already has an invoice.")
					}
					terms := 0
					if o.CustomerID != nil {
						_ = tx.QueryRow(c, `SELECT payment_terms_days FROM customers WHERE id=$1`, *o.CustomerID).Scan(&terms)
					}
					today := httpx.NewDate(time.Now())
					due := httpx.NewDate(today.AddDate(0, 0, terms))
					linesJSON, _ := json.Marshal(o.Lines)
					status := "issued"
					if o.PaymentStatus == "paid" {
						status = "paid"
					}
					out, err = scanInvoice(tx.QueryRow(c, `INSERT INTO invoices (id, sales_order_id, customer_id, issued_on, due_on, lines, total, currency, status, paid_at)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, CASE WHEN $9 = 'paid' THEN now() END) RETURNING `+invCols,
						ids.New(ids.Invoice), o.ID, o.CustomerID, today, due, linesJSON, o.Total, o.Currency, status))
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "invoice.issue", EventType: "invoice.issued", Feature: invoiceFeature,
						EntityType: "invoice", EntityID: out.ID, LocationID: o.LocationID, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/invoices", Tag: "Sales", Feature: invoiceFeature, Scope: "sales:read",
			Summary: "List invoices", Response: invoiceList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT `+invCols+` FROM invoices WHERE ($1 = '' OR status = $1) AND ($2 = '' OR customer_id = $2)
					ORDER BY issued_on DESC, id DESC LIMIT 200`, c.Query("status"), c.Query("customerId"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Invoice, error) { return scanInvoice(r) })
				if list == nil {
					list = []Invoice{}
				}
				return invoiceList{Data: list}, err
			},
		},
		{
			Method: "GET", Path: "/invoices/{id}", Tag: "Sales", Feature: invoiceFeature, Scope: "sales:read",
			Summary: "Get an invoice", Response: Invoice{},
			Handler: func(c *httpx.Ctx) (any, error) { return getInvoice(c, c.App.Pool, c.Param("id"), false) },
		},
		{
			Method: "GET", Path: "/invoices/{id}/pdf", Tag: "Sales", Feature: invoiceFeature, Scope: "sales:read",
			Summary: "Download an invoice as PDF",
			Handler: func(c *httpx.Ctx) (any, error) {
				inv, err := getInvoice(c, c.App.Pool, c.Param("id"), false)
				if err != nil {
					return nil, err
				}
				b, err := invoicePDF(c, inv)
				if err != nil {
					return nil, err
				}
				return httpx.Raw{ContentType: "application/pdf", Filename: inv.Number + ".pdf", Body: b}, nil
			},
		},
		invoiceStatus("pay", "paid", []string{"issued"}, "invoice.paid"),
		invoiceStatus("void", "void", []string{"issued"}, ""),
	}
	return append(routes, customers.Routes()...)
}

func invoiceStatus(action, status string, from []string, event string) httpx.Route {
	return httpx.Route{
		Method: "POST", Path: "/invoices/{id}:" + action, Tag: "Sales", Feature: invoiceFeature, Scope: "sales:write",
		Summary: map[string]string{"pay": "Record payment of an invoice", "void": "Void an invoice"}[action], Response: Invoice{},
		Handler: func(c *httpx.Ctx) (any, error) {
			var out Invoice
			err := c.InTx(func(tx pgx.Tx) error {
				before, err := getInvoice(c, tx, c.Param("id"), true)
				if err != nil {
					return err
				}
				if !slices.Contains(from, before.Status) {
					return httpx.Conflict("A " + before.Status + " invoice can't be " + status + ".")
				}
				out, err = scanInvoice(tx.QueryRow(c, `UPDATE invoices SET status=$2, paid_at = CASE WHEN $2 = 'paid' THEN now() ELSE paid_at END,
					updated_at=now() WHERE id=$1 RETURNING `+invCols, before.ID, status))
				if err != nil {
					return err
				}
				if status == "paid" && before.SalesOrderID != nil {
					if _, err := tx.Exec(c, `UPDATE sales_orders SET payment_status='paid', updated_at=now() WHERE id=$1`, *before.SalesOrderID); err != nil {
						return err
					}
				}
				return c.Record(tx, httpx.Change{Action: "invoice." + action, EventType: event, Feature: invoiceFeature,
					EntityType: "invoice", EntityID: out.ID, Before: before, After: out})
			})
			return out, err
		},
	}
}
