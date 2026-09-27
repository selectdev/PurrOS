package insights

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/selectdev/purros/api/internal/db"
	"github.com/shopspring/decimal"
)

// Report is a named table of figures.
type Report struct {
	Key     string           `json:"key"`
	Title   string           `json:"title"`
	From    *string          `json:"from" doc:"null for point-in-time reports"`
	To      *string          `json:"to"`
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
}

// ReportDef describes a built-in report.
type ReportDef struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Feature  string `json:"feature" doc:"Feature the report needs"`
	Snapshot bool   `json:"snapshot" doc:"Point in time; from and to are ignored"`
	run      func(ctx context.Context, q db.Querier, on Enabled, from, to time.Time, loc string) ([]string, []map[string]any, error)
}

// Reports are the built-in reports.
var Reports = []ReportDef{
	{Key: "sales-by-day", Title: "Sales by day", Feature: "sales", run: salesByDay},
	{Key: "labor-by-day", Title: "Labor by day", Feature: "time", run: laborByDay},
	{Key: "location-ranking", Title: "Location ranking", Feature: "insights", run: locationRanking},
	{Key: "over-short", Title: "Cash over/short by day", Feature: "cash", run: overShortByDay},
	{Key: "waste-by-reason", Title: "Waste by reason", Feature: "inventory.waste", run: wasteByReason},
	{Key: "variance", Title: "Stock count variance", Feature: "inventory", run: countVariance},
	{Key: "stock-valuation", Title: "Stock valuation", Feature: "inventory", Snapshot: true, run: stockValuation},
}

// FindReport returns a report definition by key.
func FindReport(key string) (ReportDef, bool) {
	i := slices.IndexFunc(Reports, func(r ReportDef) bool { return r.Key == key })
	if i < 0 {
		return ReportDef{}, false
	}
	return Reports[i], true
}

// Run runs a report.
func (d ReportDef) Run(ctx context.Context, q db.Querier, on Enabled, from, to time.Time, loc string) (Report, error) {
	cols, rows, err := d.run(ctx, q, on, from, to, loc)
	if rows == nil {
		rows = []map[string]any{}
	}
	r := Report{Key: d.Key, Title: d.Title, Columns: cols, Rows: rows}
	if !d.Snapshot {
		f, t := from.Format(time.DateOnly), to.Format(time.DateOnly)
		r.From, r.To = &f, &t
	}
	return r, err
}

// CSV renders a report as CSV in column order.
func (r Report) CSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(r.Columns)
	for _, row := range r.Rows {
		rec := make([]string, len(r.Columns))
		for i, c := range r.Columns {
			switch v := row[c].(type) {
			case nil:
			case *decimal.Decimal:
				if v != nil {
					rec[i] = v.String()
				}
			case *int:
				if v != nil {
					rec[i] = fmt.Sprint(*v)
				}
			case fmt.Stringer:
				rec[i] = v.String()
			default:
				rec[i] = fmt.Sprint(v)
			}
		}
		_ = w.Write(rec)
	}
	w.Flush()
	return buf.Bytes()
}

func sortDays(days []*day) {
	sort.Slice(days, func(i, j int) bool {
		if !days[i].Date.Equal(days[j].Date) {
			return days[i].Date.Before(days[j].Date)
		}
		return days[i].LocationID < days[j].LocationID
	})
}

func only(on Enabled, key string) Enabled {
	return func(ctx context.Context, k string) (bool, error) {
		if k != key {
			return false, nil
		}
		return on(ctx, k)
	}
}

func salesByDay(ctx context.Context, q db.Querier, on Enabled, from, to time.Time, loc string) ([]string, []map[string]any, error) {
	days, _, err := dailyData(ctx, q, only(on, "sales"), from, to, loc)
	sortDays(days)
	var rows []map[string]any
	for _, d := range days {
		rows = append(rows, map[string]any{
			"date": d.Date.Format(time.DateOnly), "locationId": d.LocationID, "netSales": d.NetSales.Round(2),
			"transactions": d.Transactions, "averageTicket": ratio(d.NetSales, decimal.NewFromInt(int64(d.Transactions)), 2),
		})
	}
	return []string{"date", "locationId", "netSales", "transactions", "averageTicket"}, rows, err
}

func laborByDay(ctx context.Context, q db.Querier, on Enabled, from, to time.Time, loc string) ([]string, []map[string]any, error) {
	days, included, err := dailyData(ctx, q, func(ctx context.Context, k string) (bool, error) {
		if k != "time" && k != "sales" {
			return false, nil
		}
		return on(ctx, k)
	}, from, to, loc)
	sortDays(days)
	var rows []map[string]any
	for _, d := range days {
		row := map[string]any{"date": d.Date.Format(time.DateOnly), "locationId": d.LocationID,
			"laborHours": d.LaborHours.Round(2), "laborCost": d.LaborCost.Round(2), "netSales": nil, "laborPercent": nil}
		if included["sales"] {
			row["netSales"] = d.NetSales.Round(2)
			if p := ratio(d.LaborCost, d.NetSales, 6); p != nil {
				row["laborPercent"] = p.Mul(hundred).Round(2)
			}
		}
		rows = append(rows, row)
	}
	return []string{"date", "locationId", "laborHours", "laborCost", "netSales", "laborPercent"}, rows, err
}

func overShortByDay(ctx context.Context, q db.Querier, on Enabled, from, to time.Time, loc string) ([]string, []map[string]any, error) {
	days, _, err := dailyData(ctx, q, only(on, "cash"), from, to, loc)
	sortDays(days)
	var rows []map[string]any
	for _, d := range days {
		rows = append(rows, map[string]any{"date": d.Date.Format(time.DateOnly), "locationId": d.LocationID, "overShort": d.OverShort.Round(2)})
	}
	return []string{"date", "locationId", "overShort"}, rows, err
}

func locationRanking(ctx context.Context, q db.Querier, on Enabled, from, to time.Time, loc string) ([]string, []map[string]any, error) {
	cols := []string{"rank", "locationId", "name", "netSales", "transactions", "laborPercent", "salesPerLaborHour", "cogsPercent", "wasteCost", "overShort"}
	days, included, err := dailyData(ctx, q, on, from, to, loc)
	if err != nil {
		return cols, nil, err
	}
	byLoc := map[string][]*day{}
	for _, d := range days {
		byLoc[d.LocationID] = append(byLoc[d.LocationID], d)
	}
	names := map[string]string{}
	rs, err := q.Query(ctx, `SELECT id, name FROM locations WHERE archived_at IS NULL AND ($1 = '' OR id = $1)`, loc)
	if err != nil {
		return cols, nil, err
	}
	for rs.Next() {
		var id, name string
		if err := rs.Scan(&id, &name); err != nil {
			rs.Close()
			return cols, nil, err
		}
		names[id] = name
	}
	if err := rs.Err(); err != nil {
		return cols, nil, err
	}
	type ranked struct {
		id string
		k  KPIs
	}
	var list []ranked
	for id := range names {
		var k KPIs
		k.fill(sum(byLoc[id]), included)
		list = append(list, ranked{id, k})
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i].k.NetSales, list[j].k.NetSales
		if a != nil && b != nil && !a.Equal(*b) {
			return a.GreaterThan(*b)
		}
		return list[i].id < list[j].id
	})
	var rows []map[string]any
	for i, r := range list {
		rows = append(rows, map[string]any{
			"rank": i + 1, "locationId": r.id, "name": names[r.id], "netSales": r.k.NetSales, "transactions": r.k.Transactions,
			"laborPercent": r.k.LaborPercent, "salesPerLaborHour": r.k.SalesPerLaborHour, "cogsPercent": r.k.COGSPercent,
			"wasteCost": r.k.WasteCost, "overShort": r.k.OverShort,
		})
	}
	return cols, rows, nil
}

// collect runs a query and maps each row to the named columns.
func collect(ctx context.Context, q db.Querier, cols []string, sql string, args ...any) ([]string, []map[string]any, error) {
	rs, err := q.Query(ctx, sql, args...)
	if err != nil {
		return cols, nil, err
	}
	defer rs.Close()
	var rows []map[string]any
	for rs.Next() {
		vals, err := rs.Values()
		if err != nil {
			return cols, nil, err
		}
		row := map[string]any{}
		for i, c := range cols {
			row[c] = vals[i]
		}
		rows = append(rows, row)
	}
	return cols, rows, rs.Err()
}

func wasteByReason(ctx context.Context, q db.Querier, _ Enabled, from, to time.Time, loc string) ([]string, []map[string]any, error) {
	return collect(ctx, q, []string{"reason", "entries", "cost"}, `
		SELECT coalesce(m.reason, 'other'), count(*), round(sum(-m.quantity * coalesce(m.unit_cost, 0)), 2)::text
		FROM stock_movements m JOIN locations l ON l.id = m.location_id
		WHERE m.type = 'waste' AND (m.occurred_at AT TIME ZONE l.timezone)::date BETWEEN $1 AND $2 AND ($3 = '' OR m.location_id = $3)
		GROUP BY 1 ORDER BY sum(-m.quantity * coalesce(m.unit_cost, 0)) DESC, 1`, from, to, loc)
}

func countVariance(ctx context.Context, q db.Querier, _ Enabled, from, to time.Time, loc string) ([]string, []map[string]any, error) {
	return collect(ctx, q, []string{"itemId", "sku", "name", "locationId", "quantity", "cost"}, `
		SELECT i.id, i.sku, i.name, m.location_id, sum(m.quantity)::text, round(sum(m.quantity * coalesce(m.unit_cost, 0)), 2)::text
		FROM stock_movements m JOIN items i ON i.id = m.item_id JOIN locations l ON l.id = m.location_id
		WHERE m.type = 'count' AND (m.occurred_at AT TIME ZONE l.timezone)::date BETWEEN $1 AND $2 AND ($3 = '' OR m.location_id = $3)
		GROUP BY 1, 2, 3, 4 ORDER BY abs(sum(m.quantity * coalesce(m.unit_cost, 0))) DESC, 1, 4`, from, to, loc)
}

func stockValuation(ctx context.Context, q db.Querier, _ Enabled, _, _ time.Time, loc string) ([]string, []map[string]any, error) {
	return collect(ctx, q, []string{"itemId", "sku", "name", "locationId", "onHand", "avgCost", "value"}, `
		SELECT i.id, i.sku, i.name, s.location_id, s.on_hand::text, s.avg_cost::text,
		       round(greatest(s.on_hand, 0) * coalesce(s.avg_cost, i.default_cost, 0), 2)::text
		FROM stock_levels s JOIN items i ON i.id = s.item_id
		WHERE i.archived_at IS NULL AND s.on_hand <> 0 AND ($1 = '' OR s.location_id = $1)
		ORDER BY i.sku, s.location_id`, loc)
}
