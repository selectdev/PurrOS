package inventory

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/shopspring/decimal"
)

// Movement is one stock ledger entry. Quantity is positive for stock coming
// in and negative for stock going out.
type Movement struct {
	ItemID     string
	LocationID string
	Quantity   decimal.Decimal
	Type       string // receipt, sale, shipment, transfer_out, transfer_in, waste, count, adjustment, reversal
	UnitCost   *decimal.Decimal
	Reason     string
	Note       string
	SourceType string
	SourceID   string
	OccurredAt time.Time
}

// Level is the projection of the ledger for one item at one location.
type Level struct {
	ItemID       string           `json:"itemId"`
	LocationID   string           `json:"locationId"`
	OnHand       decimal.Decimal  `json:"onHand"`
	Reserved     decimal.Decimal  `json:"reserved"`
	Available    decimal.Decimal  `json:"available"`
	AvgCost      *decimal.Decimal `json:"avgCost"`
	Value        *decimal.Decimal `json:"value" doc:"onHand × avgCost"`
	ParLevel     *decimal.Decimal `json:"parLevel"`
	ReorderPoint *decimal.Decimal `json:"reorderPoint"`
	UpdatedAt    time.Time        `json:"updatedAt"`
}

func (l *Level) derive() {
	l.Available = l.OnHand.Sub(l.Reserved)
	if l.AvgCost != nil {
		v := l.OnHand.Mul(*l.AvgCost).Round(2)
		l.Value = &v
	}
}

const levelCols = `item_id, location_id, on_hand, reserved, avg_cost, par_level, reorder_point, updated_at`

func scanLevel(r pgx.Row) (Level, error) {
	var l Level
	err := r.Scan(&l.ItemID, &l.LocationID, &l.OnHand, &l.Reserved, &l.AvgCost, &l.ParLevel, &l.ReorderPoint, &l.UpdatedAt)
	l.derive()
	return l, err
}

// lockLevel returns the level row for update, creating it if needed.
func lockLevel(ctx context.Context, q db.Querier, itemID, locID string) (Level, error) {
	if _, err := q.Exec(ctx, `INSERT INTO stock_levels (item_id, location_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, itemID, locID); err != nil {
		return Level{}, err
	}
	return scanLevel(q.QueryRow(ctx, `SELECT `+levelCols+` FROM stock_levels WHERE item_id=$1 AND location_id=$2 FOR UPDATE`, itemID, locID))
}

// GetLevel returns the current level (zero if none).
func GetLevel(ctx context.Context, q db.Querier, itemID, locID string) (Level, error) {
	l, err := scanLevel(q.QueryRow(ctx, `SELECT `+levelCols+` FROM stock_levels WHERE item_id=$1 AND location_id=$2`, itemID, locID))
	if errors.Is(err, pgx.ErrNoRows) {
		l = Level{ItemID: itemID, LocationID: locID}
		l.derive()
		return l, nil
	}
	return l, err
}

// Move writes a ledger entry and updates the level in the same transaction.
// Inbound movements with a unit cost update the weighted-average cost;
// outbound movements are costed at the current average.
func Move(c *httpx.Ctx, tx pgx.Tx, m Movement) (Level, error) {
	if m.Quantity.IsZero() {
		return GetLevel(c, tx, m.ItemID, m.LocationID)
	}
	before, err := lockLevel(c, tx, m.ItemID, m.LocationID)
	if err != nil {
		if db.IsForeignKeyViolation(err) {
			return before, httpx.Validation(httpx.FieldError{Path: "itemId", Message: "Unknown item or location"})
		}
		return before, err
	}
	cost := m.UnitCost
	avg := before.AvgCost
	if m.Quantity.IsPositive() && cost != nil {
		base := decimal.Max(before.OnHand, decimal.Zero)
		if avg == nil || base.IsZero() {
			a := *cost
			avg = &a
		} else {
			a := base.Mul(*avg).Add(m.Quantity.Mul(*cost)).Div(base.Add(m.Quantity)).Round(4)
			avg = &a
		}
	}
	if cost == nil {
		if avg != nil {
			cost = avg
		} else {
			var def *decimal.Decimal
			if err := tx.QueryRow(c, `SELECT default_cost FROM items WHERE id=$1`, m.ItemID).Scan(&def); err != nil {
				return before, err
			}
			cost = def
		}
	}
	at := m.OccurredAt
	if at.IsZero() {
		at = time.Now()
	}
	if _, err := tx.Exec(c, `
		INSERT INTO stock_movements (id, item_id, location_id, quantity, type, unit_cost, reason, note, source_type, source_id, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,nullif($7,''),nullif($8,''),nullif($9,''),nullif($10,''),$11)`,
		ids.New(ids.StockMovement), m.ItemID, m.LocationID, m.Quantity, m.Type, cost, m.Reason, m.Note, m.SourceType, m.SourceID, at); err != nil {
		return before, err
	}
	after, err := scanLevel(tx.QueryRow(c, `UPDATE stock_levels SET on_hand = on_hand + $3, avg_cost = $4, updated_at = now()
		WHERE item_id=$1 AND location_id=$2 RETURNING `+levelCols, m.ItemID, m.LocationID, m.Quantity, avg))
	if err != nil {
		return after, err
	}
	if err := c.Record(tx, httpx.Change{Action: "stock.move", EventType: "stock.level_changed", Feature: feature,
		EntityType: "stock_level", EntityID: m.ItemID + ":" + m.LocationID, LocationID: m.LocationID,
		After: map[string]any{"level": after, "change": m.Quantity, "type": m.Type, "sourceType": m.SourceType, "sourceId": m.SourceID}}); err != nil {
		return after, err
	}
	if after.ReorderPoint != nil && after.OnHand.LessThan(*after.ReorderPoint) && !before.OnHand.LessThan(*after.ReorderPoint) {
		if err := c.Record(tx, httpx.Change{Action: "stock.below_reorder_point", EventType: "stock.below_reorder_point", Feature: feature,
			EntityType: "stock_level", EntityID: m.ItemID + ":" + m.LocationID, LocationID: m.LocationID, After: after}); err != nil {
			return after, err
		}
	}
	return after, nil
}

// Reserve changes the reserved quantity (for sales orders).
func Reserve(c *httpx.Ctx, tx pgx.Tx, itemID, locID string, qty decimal.Decimal) error {
	if _, err := lockLevel(c, tx, itemID, locID); err != nil {
		return err
	}
	_, err := tx.Exec(c, `UPDATE stock_levels SET reserved = greatest(reserved + $3, 0), updated_at = now()
		WHERE item_id=$1 AND location_id=$2`, itemID, locID, qty)
	return err
}

// ReverseSource undoes the net stock effect of a source document (e.g. a
// sales transaction being re-sent or voided) with reversal movements.
func ReverseSource(c *httpx.Ctx, tx pgx.Tx, sourceType, sourceID string) error {
	rows, err := tx.Query(c, `SELECT item_id, location_id, sum(quantity) FROM stock_movements
		WHERE source_type=$1 AND source_id=$2 GROUP BY item_id, location_id HAVING sum(quantity) <> 0`, sourceType, sourceID)
	if err != nil {
		return err
	}
	type net struct {
		item, loc string
		qty       decimal.Decimal
	}
	var nets []net
	for rows.Next() {
		var n net
		if err := rows.Scan(&n.item, &n.loc, &n.qty); err != nil {
			rows.Close()
			return err
		}
		nets = append(nets, n)
	}
	rows.Close()
	for _, n := range nets {
		if _, err := Move(c, tx, Movement{ItemID: n.item, LocationID: n.loc, Quantity: n.qty.Neg(), Type: "reversal",
			SourceType: sourceType, SourceID: sourceID, Reason: "source changed"}); err != nil {
			return err
		}
	}
	return nil
}

// SyncSaleStock makes a sales transaction's stock effect match its current
// lines: any earlier effect is reversed, then each line deducts either the
// components of its usage recipe or the item itself.
func SyncSaleStock(c *httpx.Ctx, tx pgx.Tx, txnID string) error {
	on, err := c.App.Features.IsEnabled(c, feature)
	if err != nil || !on {
		return err
	}
	recipesOn, err := c.App.Features.IsEnabled(c, "inventory.usage_recipes")
	if err != nil {
		return err
	}
	if err := ReverseSource(c, tx, "sales_transaction", txnID); err != nil {
		return err
	}
	var locID, status string
	var at time.Time
	if err := tx.QueryRow(c, `SELECT location_id, status, occurred_at FROM sales_transactions WHERE id=$1`, txnID).Scan(&locID, &status, &at); err != nil {
		return err
	}
	if status == "voided" {
		return nil
	}
	rows, err := tx.Query(c, `
		SELECT coalesce(r.component_item_id, l.item_id), sum(l.quantity * coalesce(r.quantity, 1))
		FROM sales_transaction_lines l
		JOIN items i ON i.id = l.item_id
		LEFT JOIN usage_recipes r ON $2 AND r.item_id = l.item_id
		WHERE l.transaction_id = $1 AND l.item_id IS NOT NULL AND (r.item_id IS NOT NULL OR i.track_stock)
		GROUP BY 1`, txnID, recipesOn)
	if err != nil {
		return err
	}
	type use struct {
		item string
		qty  decimal.Decimal
	}
	var uses []use
	for rows.Next() {
		var u use
		if err := rows.Scan(&u.item, &u.qty); err != nil {
			rows.Close()
			return err
		}
		uses = append(uses, u)
	}
	rows.Close()
	for _, u := range uses {
		if _, err := Move(c, tx, Movement{ItemID: u.item, LocationID: locID, Quantity: u.qty.Neg(), Type: "sale",
			SourceType: "sales_transaction", SourceID: txnID, OccurredAt: at}); err != nil {
			return err
		}
	}
	return nil
}
