package insights

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/selectdev/purros/api/internal/db"
	"github.com/shopspring/decimal"
)

// LaborPercentTarget is the labor percentage above which yesterday's labor is
// flagged.
var LaborPercentTarget = decimal.NewFromInt(30)

// Recommendation is a suggested action.
type Recommendation struct {
	Kind       string  `json:"kind" doc:"reorder, overdue_corrective_action, stale_work_order, high_labor, cash_variance, approve_purchase_order"`
	Severity   string  `json:"severity" doc:"critical, warning or info"`
	Title      string  `json:"title"`
	Detail     string  `json:"detail"`
	LocationID *string `json:"locationId"`
	EntityType string  `json:"entityType,omitempty"`
	EntityID   string  `json:"entityId,omitempty"`
}

var severityRank = map[string]int{"critical": 0, "warning": 1, "info": 2}

type recommender struct {
	feature string
	run     func(ctx context.Context, q db.Querier, on Enabled, now time.Time, loc string) ([]Recommendation, error)
}

var recommenders = []recommender{
	{"inventory", recommendReorders},
	{"operations.corrective_actions", recommendOverdueActions},
	{"equipment.work_orders", recommendStaleWorkOrders},
	{"time", recommendLabor},
	{"cash", recommendCash},
	{"purchasing", recommendApprovals},
}

// Recommend returns suggested actions, most severe first.
func Recommend(ctx context.Context, q db.Querier, on Enabled, now time.Time, loc string) ([]Recommendation, error) {
	out := []Recommendation{}
	for _, r := range recommenders {
		ok, err := on(ctx, r.feature)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		list, err := r.run(ctx, q, on, now, loc)
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
	}
	sort.SliceStable(out, func(i, j int) bool { return severityRank[out[i].Severity] < severityRank[out[j].Severity] })
	return out, nil
}

func strp(s string) *string { return &s }

func recommendReorders(ctx context.Context, q db.Querier, _ Enabled, _ time.Time, loc string) ([]Recommendation, error) {
	rows, err := q.Query(ctx, `SELECT i.id, i.name, l.id, l.name, s.on_hand, s.reorder_point, s.par_level
		FROM stock_levels s JOIN items i ON i.id = s.item_id JOIN locations l ON l.id = s.location_id
		WHERE i.archived_at IS NULL AND s.reorder_point IS NOT NULL AND s.on_hand <= s.reorder_point AND ($1 = '' OR s.location_id = $1)
		ORDER BY s.on_hand - s.reorder_point, i.name LIMIT 50`, loc)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recommendation
	for rows.Next() {
		var itemID, item, locID, locName string
		var onHand, rp decimal.Decimal
		var par *decimal.Decimal
		if err := rows.Scan(&itemID, &item, &locID, &locName, &onHand, &rp, &par); err != nil {
			return nil, err
		}
		sev := "warning"
		if !onHand.IsPositive() {
			sev = "critical"
		}
		detail := fmt.Sprintf("%s on hand at %s; reorder point %s.", onHand.String(), locName, rp.String())
		if par != nil && par.GreaterThan(onHand) {
			detail += fmt.Sprintf(" Order %s to reach par.", par.Sub(onHand).String())
		}
		out = append(out, Recommendation{Kind: "reorder", Severity: sev, Title: "Reorder " + item, Detail: detail,
			LocationID: strp(locID), EntityType: "item", EntityID: itemID})
	}
	return out, rows.Err()
}

func recommendOverdueActions(ctx context.Context, q db.Querier, _ Enabled, now time.Time, loc string) ([]Recommendation, error) {
	rows, err := q.Query(ctx, `SELECT id, title, location_id, due_on::text FROM corrective_actions
		WHERE status = 'open' AND due_on < $1::date AND ($2 = '' OR location_id = $2) ORDER BY due_on, id LIMIT 50`, now, loc)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recommendation
	for rows.Next() {
		var id, title, due string
		var locID *string
		if err := rows.Scan(&id, &title, &locID, &due); err != nil {
			return nil, err
		}
		out = append(out, Recommendation{Kind: "overdue_corrective_action", Severity: "warning", Title: "Overdue: " + title,
			Detail: "This corrective action was due " + due + ".", LocationID: locID, EntityType: "corrective_action", EntityID: id})
	}
	return out, rows.Err()
}

func recommendStaleWorkOrders(ctx context.Context, q db.Querier, _ Enabled, now time.Time, loc string) ([]Recommendation, error) {
	rows, err := q.Query(ctx, `SELECT id, title, location_id, priority, created_at FROM work_orders
		WHERE priority IN ('high', 'urgent') AND status NOT IN ('resolved', 'closed') AND created_at < $1
		  AND ($2 = '' OR location_id = $2) ORDER BY created_at, id LIMIT 50`, now.Add(-24*time.Hour), loc)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recommendation
	for rows.Next() {
		var id, title, locID, priority string
		var created time.Time
		if err := rows.Scan(&id, &title, &locID, &priority, &created); err != nil {
			return nil, err
		}
		sev := "warning"
		if priority == "urgent" {
			sev = "critical"
		}
		days := int(now.Sub(created).Hours() / 24)
		out = append(out, Recommendation{Kind: "stale_work_order", Severity: sev, Title: "Follow up: " + title,
			Detail:     fmt.Sprintf("This %s-priority work order has been open for %d day(s).", priority, days),
			LocationID: strp(locID), EntityType: "work_order", EntityID: id})
	}
	return out, rows.Err()
}

func yesterday(now time.Time) time.Time {
	y := now.UTC().AddDate(0, 0, -1)
	return time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, time.UTC)
}

func recommendLabor(ctx context.Context, q db.Querier, on Enabled, now time.Time, loc string) ([]Recommendation, error) {
	if ok, err := on(ctx, "sales"); err != nil || !ok {
		return nil, err
	}
	y := yesterday(now)
	days, _, err := dailyData(ctx, q, func(ctx context.Context, k string) (bool, error) { return k == "time" || k == "sales", nil }, y, y, loc)
	if err != nil {
		return nil, err
	}
	sortDays(days)
	var out []Recommendation
	for _, d := range days {
		p := ratio(d.LaborCost, d.NetSales, 6)
		if p == nil {
			continue
		}
		pct := p.Mul(hundred).Round(1)
		if pct.GreaterThan(LaborPercentTarget) {
			out = append(out, Recommendation{Kind: "high_labor", Severity: "warning", Title: "Labor ran high on " + y.Format(time.DateOnly),
				Detail:     fmt.Sprintf("Labor was %s%% of sales against a %s%% target. Review the schedule against the forecast.", pct.String(), LaborPercentTarget.String()),
				LocationID: strp(d.LocationID)})
		}
	}
	return out, nil
}

func recommendCash(ctx context.Context, q db.Querier, _ Enabled, now time.Time, loc string) ([]Recommendation, error) {
	y := yesterday(now)
	rows, err := q.Query(ctx, `SELECT c.location_id, sum(c.over_short), max(co.over_short_tolerance)
		FROM cash_counts c CROSS JOIN (SELECT over_short_tolerance FROM company LIMIT 1) co
		WHERE c.kind = 'close' AND c.business_date = $1 AND c.over_short IS NOT NULL AND ($2 = '' OR c.location_id = $2)
		GROUP BY 1 HAVING abs(sum(c.over_short)) > max(co.over_short_tolerance) ORDER BY 1`, y, loc)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recommendation
	for rows.Next() {
		var locID string
		var os, tol decimal.Decimal
		if err := rows.Scan(&locID, &os, &tol); err != nil {
			return nil, err
		}
		out = append(out, Recommendation{Kind: "cash_variance", Severity: "warning", Title: "Cash over/short on " + y.Format(time.DateOnly),
			Detail:     fmt.Sprintf("Closing counts were %s against expected, beyond the %s tolerance.", os.StringFixed(2), tol.StringFixed(2)),
			LocationID: strp(locID)})
	}
	return out, rows.Err()
}

func recommendApprovals(ctx context.Context, q db.Querier, _ Enabled, _ time.Time, loc string) ([]Recommendation, error) {
	rows, err := q.Query(ctx, `SELECT id, number, location_id FROM purchase_orders
		WHERE status = 'awaiting_approval' AND ($1 = '' OR location_id = $1) ORDER BY created_at, id LIMIT 50`, loc)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recommendation
	for rows.Next() {
		var id, number, locID string
		if err := rows.Scan(&id, &number, &locID); err != nil {
			return nil, err
		}
		out = append(out, Recommendation{Kind: "approve_purchase_order", Severity: "info", Title: "Approve " + number,
			Detail: "This purchase order is over the approval limit and waiting for approval.", LocationID: strp(locID),
			EntityType: "purchase_order", EntityID: id})
	}
	return out, rows.Err()
}
