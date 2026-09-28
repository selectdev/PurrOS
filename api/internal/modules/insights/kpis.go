package insights

import (
	"context"
	"time"

	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/shopspring/decimal"
)

// KPIs are headline figures for a date range. A figure whose feature is
// disabled is left out.
type KPIs struct {
	From       httpx.Date `json:"from"`
	To         httpx.Date `json:"to"`
	LocationID *string    `json:"locationId"`

	NetSales          *decimal.Decimal `json:"netSales,omitempty" doc:"Sales less tax and refunds (sales feeds)"`
	Transactions      *int             `json:"transactions,omitempty"`
	AverageTicket     *decimal.Decimal `json:"averageTicket,omitempty"`
	LaborHours        *decimal.Decimal `json:"laborHours,omitempty" doc:"Worked hours from punches, breaks excluded"`
	LaborCost         *decimal.Decimal `json:"laborCost,omitempty" doc:"Worked hours × each employee's pay rate"`
	LaborPercent      *decimal.Decimal `json:"laborPercent,omitempty" doc:"Labor cost as a percentage of net sales"`
	SalesPerLaborHour *decimal.Decimal `json:"salesPerLaborHour,omitempty"`
	OverShort         *decimal.Decimal `json:"overShort,omitempty" doc:"Closing cash counts against expected"`
	WasteCost         *decimal.Decimal `json:"wasteCost,omitempty"`
	COGS              *decimal.Decimal `json:"cogs,omitempty" doc:"Cost of goods sold from the stock ledger"`
	COGSPercent       *decimal.Decimal `json:"cogsPercent,omitempty"`

	FormsSubmitted        *int `json:"formsSubmitted,omitempty"`
	FormsFailed           *int `json:"formsFailed,omitempty" doc:"Submissions with at least one failed answer"`
	OpenCorrectiveActions *int `json:"openCorrectiveActions,omitempty" doc:"Now, not limited to the range"`
	OpenWorkOrders        *int `json:"openWorkOrders,omitempty" doc:"Now, not limited to the range"`
	LowStockItems         *int `json:"lowStockItems,omitempty" doc:"Now: stock at or below its reorder point"`
}

// KPIKeys are the KPIs alert rules can watch.
var KPIKeys = []string{"netSales", "transactions", "averageTicket", "laborHours", "laborCost", "laborPercent",
	"salesPerLaborHour", "overShort", "wasteCost", "cogs", "cogsPercent", "formsFailed", "openCorrectiveActions",
	"openWorkOrders", "lowStockItems"}

// Value returns a KPI by key, or nil when it isn't available.
func (k KPIs) Value(key string) *decimal.Decimal {
	i := func(p *int) *decimal.Decimal {
		if p == nil {
			return nil
		}
		d := decimal.NewFromInt(int64(*p))
		return &d
	}
	switch key {
	case "netSales":
		return k.NetSales
	case "transactions":
		return i(k.Transactions)
	case "averageTicket":
		return k.AverageTicket
	case "laborHours":
		return k.LaborHours
	case "laborCost":
		return k.LaborCost
	case "laborPercent":
		return k.LaborPercent
	case "salesPerLaborHour":
		return k.SalesPerLaborHour
	case "overShort":
		return k.OverShort
	case "wasteCost":
		return k.WasteCost
	case "cogs":
		return k.COGS
	case "cogsPercent":
		return k.COGSPercent
	case "formsFailed":
		return i(k.FormsFailed)
	case "openCorrectiveActions":
		return i(k.OpenCorrectiveActions)
	case "openWorkOrders":
		return i(k.OpenWorkOrders)
	case "lowStockItems":
		return i(k.LowStockItems)
	}
	return nil
}

var hundred = decimal.NewFromInt(100)

// ratio returns a/b rounded, or nil when b is zero.
func ratio(a, b decimal.Decimal, scale int32) *decimal.Decimal {
	if b.IsZero() {
		return nil
	}
	return new(a.Div(b).Round(scale))
}

// totals is the sum of a set of days.
type totals struct {
	day
}

func sum(days []*day) totals {
	var t totals
	for _, d := range days {
		t.NetSales = t.NetSales.Add(d.NetSales)
		t.Transactions += d.Transactions
		t.LaborHours = t.LaborHours.Add(d.LaborHours)
		t.LaborCost = t.LaborCost.Add(d.LaborCost)
		t.OverShort = t.OverShort.Add(d.OverShort)
		t.WasteCost = t.WasteCost.Add(d.WasteCost)
		t.COGS = t.COGS.Add(d.COGS)
	}
	return t
}

// fill sets the day-based KPIs from totals.
func (k *KPIs) fill(t totals, included map[string]bool) {
	if included["sales"] {
		k.NetSales = new(t.NetSales.Round(2))
		k.Transactions = new(t.Transactions)
		k.AverageTicket = ratio(t.NetSales, decimal.NewFromInt(int64(t.Transactions)), 2)
	}
	if included["time"] {
		k.LaborHours = new(t.LaborHours.Round(2))
		k.LaborCost = new(t.LaborCost.Round(2))
		if included["sales"] {
			if p := ratio(t.LaborCost, t.NetSales, 6); p != nil {
				k.LaborPercent = new(p.Mul(hundred).Round(2))
			}
			k.SalesPerLaborHour = ratio(t.NetSales, t.LaborHours, 2)
		}
	}
	if included["cash"] {
		k.OverShort = new(t.OverShort.Round(2))
	}
	if included["inventory.waste"] {
		k.WasteCost = new(t.WasteCost.Round(2))
	}
	if included["inventory"] {
		k.COGS = new(t.COGS.Round(2))
		if included["sales"] {
			if p := ratio(t.COGS, t.NetSales, 6); p != nil {
				k.COGSPercent = new(p.Mul(hundred).Round(2))
			}
		}
	}
}

// count runs a count query when a feature is enabled.
func count(ctx context.Context, q db.Querier, on Enabled, feature, sql string, args ...any) (*int, error) {
	ok, err := on(ctx, feature)
	if err != nil || !ok {
		return nil, err
	}
	var n int
	if err := q.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return nil, err
	}
	return &n, nil
}

// Compute returns KPIs for business dates from–to (inclusive), for one
// location or, when loc is "", every location.
func Compute(ctx context.Context, q db.Querier, on Enabled, from, to time.Time, loc string) (KPIs, error) {
	k := KPIs{From: httpx.NewDate(from), To: httpx.NewDate(to)}
	if loc != "" {
		k.LocationID = &loc
	}
	days, included, err := dailyData(ctx, q, on, from, to, loc)
	if err != nil {
		return k, err
	}
	k.fill(sum(days), included)

	if ok, err := on(ctx, "operations"); err != nil {
		return k, err
	} else if ok {
		var submitted, failed int
		if err := q.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE s.failed_count > 0)
			FROM form_submissions s LEFT JOIN locations l ON l.id = s.location_id
			WHERE (s.submitted_at AT TIME ZONE coalesce(l.timezone, 'UTC'))::date BETWEEN $1 AND $2
			  AND ($3 = '' OR s.location_id = $3)`, from, to, loc).Scan(&submitted, &failed); err != nil {
			return k, err
		}
		k.FormsSubmitted, k.FormsFailed = &submitted, &failed
	}
	if k.OpenCorrectiveActions, err = count(ctx, q, on, "operations.corrective_actions",
		`SELECT count(*) FROM corrective_actions WHERE status = 'open' AND ($1 = '' OR location_id = $1)`, loc); err != nil {
		return k, err
	}
	if k.OpenWorkOrders, err = count(ctx, q, on, "equipment.work_orders",
		`SELECT count(*) FROM work_orders WHERE status NOT IN ('resolved', 'closed') AND ($1 = '' OR location_id = $1)`, loc); err != nil {
		return k, err
	}
	if k.LowStockItems, err = count(ctx, q, on, "inventory",
		`SELECT count(*) FROM stock_levels s JOIN items i ON i.id = s.item_id
		 WHERE i.archived_at IS NULL AND s.reorder_point IS NOT NULL AND s.on_hand <= s.reorder_point
		   AND ($1 = '' OR s.location_id = $1)`, loc); err != nil {
		return k, err
	}
	return k, nil
}
