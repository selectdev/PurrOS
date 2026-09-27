package inventory

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/refs"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type LevelConfigInput struct {
	refs.ItemRef
	refs.LocationRef
	ParLevel     *decimal.Decimal `json:"parLevel,omitempty" doc:"Stock wanted on hand after a delivery"`
	ReorderPoint *decimal.Decimal `json:"reorderPoint,omitempty" doc:"Alert when stock falls below this"`
}

type MovementOut struct {
	ID         string           `json:"id"`
	ItemID     string           `json:"itemId"`
	LocationID string           `json:"locationId"`
	Quantity   decimal.Decimal  `json:"quantity"`
	Type       string           `json:"type"`
	UnitCost   *decimal.Decimal `json:"unitCost"`
	Reason     *string          `json:"reason"`
	Note       *string          `json:"note"`
	SourceType *string          `json:"sourceType"`
	SourceID   *string          `json:"sourceId"`
	OccurredAt time.Time        `json:"occurredAt"`
	CreatedAt  time.Time        `json:"createdAt"`
}

type AdjustmentInput struct {
	refs.ItemRef
	refs.LocationRef
	Quantity *decimal.Decimal `json:"quantity" validate:"required" doc:"Positive adds stock, negative removes it"`
	UnitCost *decimal.Decimal `json:"unitCost,omitempty" doc:"Cost per unit for stock added"`
	Reason   string           `json:"reason" validate:"required,max=100"`
	Note     string           `json:"note,omitempty" validate:"max=500"`
}

type WasteInput struct {
	refs.ItemRef
	refs.LocationRef
	Quantity *decimal.Decimal `json:"quantity" validate:"required" doc:"Amount wasted (positive)"`
	Reason   string           `json:"reason" validate:"required,oneof=waste spoilage damage theft sample staff_meal expired other"`
	Note     string           `json:"note,omitempty" validate:"max=500"`
	PhotoURL string           `json:"photoUrl,omitempty" validate:"omitempty,url"`
}

type MoveResult struct {
	Movement string `json:"movementType"`
	Level    Level  `json:"level"`
}

type CountLineInput struct {
	refs.ItemRef
	Counted *decimal.Decimal `json:"counted" validate:"required"`
}

type CountInput struct {
	refs.LocationRef
	Note  string           `json:"note,omitempty" validate:"max=500"`
	Lines []CountLineInput `json:"lines" validate:"required,min=1,dive"`
}

type CountLine struct {
	ItemID   string           `json:"itemId"`
	Counted  decimal.Decimal  `json:"counted"`
	Expected *decimal.Decimal `json:"expected" doc:"Stock on hand when the count was entered"`
	Variance *decimal.Decimal `json:"variance"`
}

type StockCount struct {
	ID         string      `json:"id"`
	LocationID string      `json:"locationId"`
	Status     string      `json:"status"`
	CountedAt  time.Time   `json:"countedAt"`
	PostedAt   *time.Time  `json:"postedAt"`
	Note       *string     `json:"note"`
	Lines      []CountLine `json:"lines"`
}

type TransferLineInput struct {
	refs.ItemRef
	Quantity *decimal.Decimal `json:"quantity" validate:"required"`
}

type TransferInput struct {
	FromLocationID string              `json:"fromLocationId" validate:"required"`
	ToLocationID   string              `json:"toLocationId" validate:"required,nefield=FromLocationID"`
	Note           string              `json:"note,omitempty" validate:"max=500"`
	Lines          []TransferLineInput `json:"lines" validate:"required,min=1,dive"`
}

type ReceiveLine struct {
	ItemID      string           `json:"itemId" validate:"required"`
	ReceivedQty *decimal.Decimal `json:"receivedQty" validate:"required"`
}

type ReceiveTransferInput struct {
	Lines []ReceiveLine `json:"lines,omitempty" validate:"dive" doc:"Omit to receive everything as sent"`
}

type TransferLine struct {
	ItemID      string           `json:"itemId"`
	SentQty     decimal.Decimal  `json:"sentQty"`
	ReceivedQty *decimal.Decimal `json:"receivedQty"`
}

type Transfer struct {
	ID             string         `json:"id"`
	FromLocationID string         `json:"fromLocationId"`
	ToLocationID   string         `json:"toLocationId"`
	Status         string         `json:"status"`
	Note           *string        `json:"note"`
	SentAt         time.Time      `json:"sentAt"`
	ReceivedAt     *time.Time     `json:"receivedAt"`
	Lines          []TransferLine `json:"lines"`
}

type RecipeComponentInput struct {
	refs.ItemRef
	Quantity *decimal.Decimal `json:"quantity" validate:"required"`
}

type RecipeInput struct {
	Components []RecipeComponentInput `json:"components" validate:"dive" doc:"Empty removes the recipe"`
}

type RecipeComponent struct {
	ItemID   string          `json:"itemId"`
	SKU      string          `json:"sku"`
	Name     string          `json:"name"`
	Quantity decimal.Decimal `json:"quantity"`
}

type Recipe struct {
	ItemID     string            `json:"itemId"`
	Components []RecipeComponent `json:"components"`
}

type levelList struct {
	Data []Level `json:"data"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func loadCount(c *httpx.Ctx, q pgx.Tx, id string, lock bool) (StockCount, error) {
	var s StockCount
	sql := `SELECT id, location_id, status, counted_at, posted_at, note FROM stock_counts WHERE id=$1`
	if lock {
		sql += ` FOR UPDATE`
	}
	err := q.QueryRow(c, sql, id).Scan(&s.ID, &s.LocationID, &s.Status, &s.CountedAt, &s.PostedAt, &s.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, httpx.NotFound("Stock count not found.")
	}
	if err != nil {
		return s, err
	}
	rows, err := q.Query(c, `SELECT item_id, counted, expected FROM stock_count_lines WHERE count_id=$1 ORDER BY item_id`, id)
	if err != nil {
		return s, err
	}
	s.Lines, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (CountLine, error) {
		var l CountLine
		err := r.Scan(&l.ItemID, &l.Counted, &l.Expected)
		if l.Expected != nil {
			v := l.Counted.Sub(*l.Expected)
			l.Variance = &v
		}
		return l, err
	})
	return s, err
}

func loadTransfer(c *httpx.Ctx, q pgx.Tx, id string, lock bool) (Transfer, error) {
	var t Transfer
	sql := `SELECT id, from_location_id, to_location_id, status, note, sent_at, received_at FROM transfers WHERE id=$1`
	if lock {
		sql += ` FOR UPDATE`
	}
	err := q.QueryRow(c, sql, id).Scan(&t.ID, &t.FromLocationID, &t.ToLocationID, &t.Status, &t.Note, &t.SentAt, &t.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, httpx.NotFound("Transfer not found.")
	}
	if err != nil {
		return t, err
	}
	rows, err := q.Query(c, `SELECT item_id, sent_qty, received_qty FROM transfer_lines WHERE transfer_id=$1 ORDER BY item_id`, id)
	if err != nil {
		return t, err
	}
	t.Lines, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (TransferLine, error) {
		var l TransferLine
		return l, r.Scan(&l.ItemID, &l.SentQty, &l.ReceivedQty)
	})
	return t, err
}

func loadRecipe(c *httpx.Ctx, q pgx.Tx, itemID string) (Recipe, error) {
	out := Recipe{ItemID: itemID, Components: []RecipeComponent{}}
	rows, err := q.Query(c, `SELECT r.component_item_id, i.sku, i.name, r.quantity FROM usage_recipes r
		JOIN items i ON i.id = r.component_item_id WHERE r.item_id=$1 ORDER BY i.name`, itemID)
	if err != nil {
		return out, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RecipeComponent, error) {
		var rc RecipeComponent
		return rc, r.Scan(&rc.ItemID, &rc.SKU, &rc.Name, &rc.Quantity)
	})
	if list != nil {
		out.Components = list
	}
	return out, err
}

func inTxRead[T any](c *httpx.Ctx, fn func(tx pgx.Tx) (T, error)) (T, error) {
	var out T
	err := c.InTx(func(tx pgx.Tx) (err error) { out, err = fn(tx); return err })
	return out, err
}

type countList struct {
	Data []StockCount `json:"data"`
}

type transferList struct {
	Data []Transfer `json:"data"`
}

// StockRoutes are the stock ledger routes.
func StockRoutes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "GET", Path: "/stock-levels", Tag: "Inventory", Feature: feature, Scope: "inventory:read",
			Summary:     "Stock on hand, reserved and available",
			Description: "Filter with locationId and itemId; belowReorderPoint=true lists items to reorder.",
			Response:    levelList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT `+levelCols+` FROM stock_levels
					WHERE ($1 = '' OR location_id = $1) AND ($2 = '' OR item_id = $2)
					  AND (NOT $3 OR (reorder_point IS NOT NULL AND on_hand < reorder_point))
					  AND ($4::timestamptz IS NULL OR updated_at >= $4)
					ORDER BY location_id, item_id LIMIT 5000`,
					c.Query("locationId"), c.Query("itemId"), c.Query("belowReorderPoint") == "true", timeOrNil(c.Query("updatedSince")))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Level, error) { return scanLevel(r) })
				if list == nil {
					list = []Level{}
				}
				return levelList{Data: list}, err
			},
		},
		{
			Method: "POST", Path: "/stock-levels:configure", Tag: "Inventory", Feature: feature, Scope: "inventory:write",
			Summary: "Set par level and reorder point for an item at a location", Body: LevelConfigInput{}, Response: Level{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in LevelConfigInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				return inTxRead(c, func(tx pgx.Tx) (Level, error) {
					item, err := refs.Item(c, tx, in.ItemRef, "")
					if err != nil {
						return Level{}, err
					}
					loc, err := refs.Location(c, tx, in.LocationRef, "")
					if err != nil {
						return Level{}, err
					}
					if _, err := lockLevel(c, tx, item, loc); err != nil {
						return Level{}, err
					}
					l, err := scanLevel(tx.QueryRow(c, `UPDATE stock_levels SET par_level=$3, reorder_point=$4, updated_at=now()
						WHERE item_id=$1 AND location_id=$2 RETURNING `+levelCols, item, loc, in.ParLevel, in.ReorderPoint))
					if err != nil {
						return l, err
					}
					return l, c.Record(tx, httpx.Change{Action: "stock_level.configure", EntityType: "stock_level", EntityID: item + ":" + loc, LocationID: loc, After: l})
				})
			},
		},
		{
			Method: "GET", Path: "/stock-movements", Tag: "Inventory", Feature: feature, Scope: "inventory:read",
			Summary: "The stock ledger", Response: httpx.Page[MovementOut]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `SELECT id, item_id, location_id, quantity, type, unit_cost, reason, note,
					source_type, source_id, occurred_at, created_at FROM stock_movements
					WHERE id > $1 AND ($2 = '' OR item_id = $2) AND ($3 = '' OR location_id = $3) AND ($4 = '' OR type = $4)
					  AND ($5 = '' OR source_id = $5)
					ORDER BY id LIMIT $6`, lp.AfterID, c.Query("itemId"), c.Query("locationId"), c.Query("type"), c.Query("sourceId"), lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (MovementOut, error) {
					var m MovementOut
					err := r.Scan(&m.ID, &m.ItemID, &m.LocationID, &m.Quantity, &m.Type, &m.UnitCost, &m.Reason, &m.Note,
						&m.SourceType, &m.SourceID, &m.OccurredAt, &m.CreatedAt)
					return m, err
				})
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(m MovementOut) string { return m.ID }), nil
			},
		},
		{
			Method: "POST", Path: "/inventory/adjustments", Tag: "Inventory", Feature: feature, Scope: "inventory:write",
			Summary: "Adjust stock", Body: AdjustmentInput{}, Response: MoveResult{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in AdjustmentInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if in.Quantity.IsZero() {
					return nil, httpx.Validation(httpx.FieldError{Path: "quantity", Message: "Must not be zero"})
				}
				return inTxRead(c, func(tx pgx.Tx) (MoveResult, error) {
					item, err := refs.Item(c, tx, in.ItemRef, "")
					if err != nil {
						return MoveResult{}, err
					}
					loc, err := refs.Location(c, tx, in.LocationRef, "")
					if err != nil {
						return MoveResult{}, err
					}
					l, err := Move(c, tx, Movement{ItemID: item, LocationID: loc, Quantity: *in.Quantity, Type: "adjustment",
						UnitCost: in.UnitCost, Reason: in.Reason, Note: in.Note})
					return MoveResult{Movement: "adjustment", Level: l}, err
				})
			},
		},
		{
			Method: "POST", Path: "/inventory/waste", Tag: "Inventory", Feature: "inventory.waste", Scope: "inventory:write",
			Summary: "Record waste or shrink", Body: WasteInput{}, Response: MoveResult{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in WasteInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if !in.Quantity.IsPositive() {
					return nil, httpx.Validation(httpx.FieldError{Path: "quantity", Message: "Must be greater than zero"})
				}
				return inTxRead(c, func(tx pgx.Tx) (MoveResult, error) {
					item, err := refs.Item(c, tx, in.ItemRef, "")
					if err != nil {
						return MoveResult{}, err
					}
					loc, err := refs.Location(c, tx, in.LocationRef, "")
					if err != nil {
						return MoveResult{}, err
					}
					note := in.Note
					if in.PhotoURL != "" {
						note = (note + " " + in.PhotoURL)
					}
					l, err := Move(c, tx, Movement{ItemID: item, LocationID: loc, Quantity: in.Quantity.Neg(), Type: "waste",
						Reason: in.Reason, Note: note})
					if err != nil {
						return MoveResult{}, err
					}
					err = c.Record(tx, httpx.Change{Action: "waste.record", EventType: "waste.recorded", Feature: "inventory.waste",
						EntityType: "item", EntityID: item, LocationID: loc,
						After: map[string]any{"itemId": item, "locationId": loc, "quantity": in.Quantity, "reason": in.Reason, "note": in.Note, "photoUrl": in.PhotoURL}})
					return MoveResult{Movement: "waste", Level: l}, err
				})
			},
		},
		{
			Method: "POST", Path: "/stock-counts", Tag: "Inventory", Feature: feature, Scope: "inventory:write",
			Summary: "Enter a stock count", Description: "The count is open until posted; expected quantities are captured now.",
			Body: CountInput{}, Response: StockCount{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in CountInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				return inTxRead(c, func(tx pgx.Tx) (StockCount, error) {
					loc, err := refs.Location(c, tx, in.LocationRef, "")
					if err != nil {
						return StockCount{}, err
					}
					id := ids.New(ids.StockCount)
					if _, err := tx.Exec(c, `INSERT INTO stock_counts (id, location_id, note) VALUES ($1,$2,nullif($3,''))`, id, loc, in.Note); err != nil {
						return StockCount{}, err
					}
					for i, l := range in.Lines {
						if l.Counted.IsNegative() {
							return StockCount{}, httpx.Validation(httpx.FieldError{Path: "lines[" + itoa(i) + "].counted", Message: "Must be zero or more"})
						}
						item, err := refs.Item(c, tx, l.ItemRef, "lines["+itoa(i)+"]")
						if err != nil {
							return StockCount{}, err
						}
						lvl, err := GetLevel(c, tx, item, loc)
						if err != nil {
							return StockCount{}, err
						}
						if _, err := tx.Exec(c, `INSERT INTO stock_count_lines (count_id, item_id, counted, expected) VALUES ($1,$2,$3,$4)
							ON CONFLICT (count_id, item_id) DO UPDATE SET counted = EXCLUDED.counted`, id, item, *l.Counted, lvl.OnHand); err != nil {
							return StockCount{}, err
						}
					}
					return loadCount(c, tx, id, false)
				})
			},
		},
		{
			Method: "GET", Path: "/stock-counts", Tag: "Inventory", Feature: feature, Scope: "inventory:read",
			Summary: "List stock counts", Response: countList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return inTxRead(c, func(tx pgx.Tx) (countList, error) {
					rows, err := tx.Query(c, `SELECT id FROM stock_counts WHERE ($1 = '' OR location_id = $1) AND ($2 = '' OR status = $2)
						ORDER BY counted_at DESC LIMIT 100`, c.Query("locationId"), c.Query("status"))
					if err != nil {
						return countList{}, err
					}
					idsList, err := pgx.CollectRows(rows, pgx.RowTo[string])
					if err != nil {
						return countList{}, err
					}
					out := countList{Data: []StockCount{}}
					for _, id := range idsList {
						s, err := loadCount(c, tx, id, false)
						if err != nil {
							return out, err
						}
						out.Data = append(out.Data, s)
					}
					return out, nil
				})
			},
		},
		{
			Method: "GET", Path: "/stock-counts/{id}", Tag: "Inventory", Feature: feature, Scope: "inventory:read",
			Summary: "Get a stock count with variances", Response: StockCount{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return inTxRead(c, func(tx pgx.Tx) (StockCount, error) { return loadCount(c, tx, c.Param("id"), false) })
			},
		},
		{
			Method: "POST", Path: "/stock-counts/{id}:post", Tag: "Inventory", Feature: feature, Scope: "inventory:write",
			Summary:     "Post a count to the stock ledger",
			Description: "Each line becomes a count adjustment of (counted − on hand now), so sales made since the count was entered aren't lost.",
			Response:    StockCount{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return inTxRead(c, func(tx pgx.Tx) (StockCount, error) {
					s, err := loadCount(c, tx, c.Param("id"), true)
					if err != nil {
						return s, err
					}
					if s.Status != "open" {
						return s, httpx.Conflict("This count is already " + s.Status + ".")
					}
					for _, l := range s.Lines {
						lvl, err := lockLevel(c, tx, l.ItemID, s.LocationID)
						if err != nil {
							return s, err
						}
						if _, err := Move(c, tx, Movement{ItemID: l.ItemID, LocationID: s.LocationID, Quantity: l.Counted.Sub(lvl.OnHand),
							Type: "count", SourceType: "stock_count", SourceID: s.ID}); err != nil {
							return s, err
						}
					}
					if _, err := tx.Exec(c, `UPDATE stock_counts SET status='posted', posted_at=now(), updated_at=now() WHERE id=$1`, s.ID); err != nil {
						return s, err
					}
					out, err := loadCount(c, tx, s.ID, false)
					if err != nil {
						return out, err
					}
					return out, c.Record(tx, httpx.Change{Action: "stock_count.post", EventType: "stock_count.posted", Feature: feature,
						EntityType: "stock_count", EntityID: out.ID, LocationID: out.LocationID, After: out})
				})
			},
		},
		{
			Method: "POST", Path: "/stock-counts/{id}:cancel", Tag: "Inventory", Feature: feature, Scope: "inventory:write",
			Summary: "Cancel an open count", Response: StockCount{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return inTxRead(c, func(tx pgx.Tx) (StockCount, error) {
					s, err := loadCount(c, tx, c.Param("id"), true)
					if err != nil {
						return s, err
					}
					if s.Status != "open" {
						return s, httpx.Conflict("This count is already " + s.Status + ".")
					}
					if _, err := tx.Exec(c, `UPDATE stock_counts SET status='cancelled', updated_at=now() WHERE id=$1`, s.ID); err != nil {
						return s, err
					}
					return loadCount(c, tx, s.ID, false)
				})
			},
		},
		{
			Method: "POST", Path: "/transfers", Tag: "Inventory", Feature: "inventory.transfers", Scope: "inventory:write",
			Summary: "Send stock to another location", Body: TransferInput{}, Response: Transfer{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in TransferInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				return inTxRead(c, func(tx pgx.Tx) (Transfer, error) {
					from, err := refs.Location(c, tx, refs.LocationRef{LocationID: in.FromLocationID}, "from")
					if err != nil {
						return Transfer{}, err
					}
					to, err := refs.Location(c, tx, refs.LocationRef{LocationID: in.ToLocationID}, "to")
					if err != nil {
						return Transfer{}, err
					}
					id := ids.New(ids.Transfer)
					if _, err := tx.Exec(c, `INSERT INTO transfers (id, from_location_id, to_location_id, note) VALUES ($1,$2,$3,nullif($4,''))`,
						id, from, to, in.Note); err != nil {
						return Transfer{}, err
					}
					for i, l := range in.Lines {
						if !l.Quantity.IsPositive() {
							return Transfer{}, httpx.Validation(httpx.FieldError{Path: "lines[" + itoa(i) + "].quantity", Message: "Must be greater than zero"})
						}
						item, err := refs.Item(c, tx, l.ItemRef, "lines["+itoa(i)+"]")
						if err != nil {
							return Transfer{}, err
						}
						if _, err := tx.Exec(c, `INSERT INTO transfer_lines (transfer_id, item_id, sent_qty) VALUES ($1,$2,$3)
							ON CONFLICT (transfer_id, item_id) DO UPDATE SET sent_qty = transfer_lines.sent_qty + EXCLUDED.sent_qty`,
							id, item, *l.Quantity); err != nil {
							return Transfer{}, err
						}
						if _, err := Move(c, tx, Movement{ItemID: item, LocationID: from, Quantity: l.Quantity.Neg(), Type: "transfer_out",
							SourceType: "transfer", SourceID: id}); err != nil {
							return Transfer{}, err
						}
					}
					t, err := loadTransfer(c, tx, id, false)
					if err != nil {
						return t, err
					}
					return t, c.Record(tx, httpx.Change{Action: "transfer.send", EventType: "transfer.sent", Feature: "inventory.transfers",
						EntityType: "transfer", EntityID: id, LocationID: from, After: t})
				})
			},
		},
		{
			Method: "POST", Path: "/transfers/{id}:receive", Tag: "Inventory", Feature: "inventory.transfers", Scope: "inventory:write",
			Summary: "Confirm what arrived", Body: ReceiveTransferInput{}, Response: Transfer{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in ReceiveTransferInput
				if err := c.DecodeOptional(&in); err != nil {
					return nil, err
				}
				return inTxRead(c, func(tx pgx.Tx) (Transfer, error) {
					t, err := loadTransfer(c, tx, c.Param("id"), true)
					if err != nil {
						return t, err
					}
					if t.Status != "sent" {
						return t, httpx.Conflict("This transfer is already " + t.Status + ".")
					}
					received := map[string]decimal.Decimal{}
					for _, l := range t.Lines {
						received[l.ItemID] = l.SentQty
					}
					for i, l := range in.Lines {
						if _, ok := received[l.ItemID]; !ok {
							return t, httpx.Validation(httpx.FieldError{Path: "lines[" + itoa(i) + "].itemId", Message: "Not on this transfer"})
						}
						if l.ReceivedQty.IsNegative() {
							return t, httpx.Validation(httpx.FieldError{Path: "lines[" + itoa(i) + "].receivedQty", Message: "Must be zero or more"})
						}
						received[l.ItemID] = *l.ReceivedQty
					}
					discrepancy := false
					for _, l := range t.Lines {
						qty := received[l.ItemID]
						if !qty.Equal(l.SentQty) {
							discrepancy = true
						}
						var cost *decimal.Decimal
						_ = tx.QueryRow(c, `SELECT unit_cost FROM stock_movements WHERE source_type='transfer' AND source_id=$1
							AND item_id=$2 AND type='transfer_out' LIMIT 1`, t.ID, l.ItemID).Scan(&cost)
						if _, err := tx.Exec(c, `UPDATE transfer_lines SET received_qty=$3 WHERE transfer_id=$1 AND item_id=$2`, t.ID, l.ItemID, qty); err != nil {
							return t, err
						}
						if _, err := Move(c, tx, Movement{ItemID: l.ItemID, LocationID: t.ToLocationID, Quantity: qty, Type: "transfer_in",
							UnitCost: cost, SourceType: "transfer", SourceID: t.ID}); err != nil {
							return t, err
						}
					}
					if _, err := tx.Exec(c, `UPDATE transfers SET status='received', received_at=now(), updated_at=now() WHERE id=$1`, t.ID); err != nil {
						return t, err
					}
					out, err := loadTransfer(c, tx, t.ID, false)
					if err != nil {
						return out, err
					}
					if err := c.Record(tx, httpx.Change{Action: "transfer.receive", EventType: "transfer.received", Feature: "inventory.transfers",
						EntityType: "transfer", EntityID: out.ID, LocationID: out.ToLocationID, After: out}); err != nil {
						return out, err
					}
					if discrepancy {
						err = c.Record(tx, httpx.Change{Action: "transfer.discrepancy", EventType: "transfer.discrepancy", Feature: "inventory.transfers",
							EntityType: "transfer", EntityID: out.ID, LocationID: out.ToLocationID, After: out})
					}
					return out, err
				})
			},
		},
		{
			Method: "GET", Path: "/transfers", Tag: "Inventory", Feature: "inventory.transfers", Scope: "inventory:read",
			Summary: "List transfers", Response: transferList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return inTxRead(c, func(tx pgx.Tx) (transferList, error) {
					rows, err := tx.Query(c, `SELECT id FROM transfers WHERE ($1 = '' OR from_location_id = $1 OR to_location_id = $1)
						AND ($2 = '' OR status = $2) ORDER BY sent_at DESC LIMIT 100`, c.Query("locationId"), c.Query("status"))
					if err != nil {
						return transferList{}, err
					}
					idsList, err := pgx.CollectRows(rows, pgx.RowTo[string])
					if err != nil {
						return transferList{}, err
					}
					out := transferList{Data: []Transfer{}}
					for _, id := range idsList {
						t, err := loadTransfer(c, tx, id, false)
						if err != nil {
							return out, err
						}
						out.Data = append(out.Data, t)
					}
					return out, nil
				})
			},
		},
		{
			Method: "GET", Path: "/transfers/{id}", Tag: "Inventory", Feature: "inventory.transfers", Scope: "inventory:read",
			Summary: "Get a transfer", Response: Transfer{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return inTxRead(c, func(tx pgx.Tx) (Transfer, error) { return loadTransfer(c, tx, c.Param("id"), false) })
			},
		},
		{
			Method: "GET", Path: "/usage-recipes/{itemId}", Tag: "Inventory", Feature: "inventory.usage_recipes", Scope: "inventory:read",
			Summary: "What one unit of a sold item uses", Response: Recipe{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return inTxRead(c, func(tx pgx.Tx) (Recipe, error) {
					if _, err := get(c, tx, "id = $1", c.Param("itemId"), false); err != nil {
						return Recipe{}, err
					}
					return loadRecipe(c, tx, c.Param("itemId"))
				})
			},
		},
		{
			Method: "PUT", Path: "/usage-recipes/{itemId}", Tag: "Inventory", Feature: "inventory.usage_recipes", Scope: "inventory:write",
			Summary:     "Replace a usage recipe",
			Description: "When the item is sold, its components are deducted from stock instead of the item itself.",
			Body:        RecipeInput{}, Response: Recipe{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in RecipeInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				return inTxRead(c, func(tx pgx.Tx) (Recipe, error) {
					item := c.Param("itemId")
					if _, err := get(c, tx, "id = $1", item, true); err != nil {
						return Recipe{}, err
					}
					before, err := loadRecipe(c, tx, item)
					if err != nil {
						return before, err
					}
					if _, err := tx.Exec(c, `DELETE FROM usage_recipes WHERE item_id=$1`, item); err != nil {
						return before, err
					}
					for i, comp := range in.Components {
						if !comp.Quantity.IsPositive() {
							return before, httpx.Validation(httpx.FieldError{Path: "components[" + itoa(i) + "].quantity", Message: "Must be greater than zero"})
						}
						cid, err := refs.Item(c, tx, comp.ItemRef, "components["+itoa(i)+"]")
						if err != nil {
							return before, err
						}
						if cid == item {
							return before, httpx.Validation(httpx.FieldError{Path: "components[" + itoa(i) + "].itemId", Message: "An item can't use itself"})
						}
						if _, err := tx.Exec(c, `INSERT INTO usage_recipes (item_id, component_item_id, quantity) VALUES ($1,$2,$3)
							ON CONFLICT (item_id, component_item_id) DO UPDATE SET quantity = usage_recipes.quantity + EXCLUDED.quantity`,
							item, cid, *comp.Quantity); err != nil {
							return before, err
						}
					}
					out, err := loadRecipe(c, tx, item)
					if err != nil {
						return out, err
					}
					return out, c.Record(tx, httpx.Change{Action: "usage_recipe.set", EntityType: "item", EntityID: item, Before: before, After: out})
				})
			},
		},
	}
}

func itoa(i int) string {
	b := []byte{}
	if i == 0 {
		return "0"
	}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func timeOrNil(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
