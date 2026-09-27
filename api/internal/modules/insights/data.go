// Package insights computes KPIs, reports and recommended actions from the
// data the other modules hold, and evaluates KPI alert rules.
//
// Everything here takes a context and a querier rather than a request, so the
// worker can use it to evaluate alert rules.
package insights

import (
	"context"
	"time"

	"github.com/selectdev/purros/api/internal/db"
	"github.com/shopspring/decimal"
)

// Enabled reports whether a feature is on.
type Enabled func(ctx context.Context, key string) (bool, error)

// day is one location's figures for one business day.
type day struct {
	LocationID   string
	Date         time.Time
	NetSales     decimal.Decimal
	Transactions int
	LaborHours   decimal.Decimal
	LaborCost    decimal.Decimal
	OverShort    decimal.Decimal
	WasteCost    decimal.Decimal
	COGS         decimal.Decimal
}

type dayKey struct {
	loc  string
	date string
}

// Net sales per location and business day. Transactions win over summaries
// for the same location and day, so feeds that send both aren't counted twice.
const salesSQL = `
WITH t AS (
	SELECT location_id, business_date AS day,
	       sum(CASE WHEN type = 'refund' THEN -abs(total - tax) ELSE total - tax END) AS net,
	       count(*) FILTER (WHERE type = 'sale') AS n
	FROM sales_transactions
	WHERE status = 'completed' AND business_date BETWEEN $1 AND $2 AND ($3 = '' OR location_id = $3)
	GROUP BY 1, 2
), s AS (
	SELECT location_id, business_date AS day, sum(net_sales) AS net, sum(coalesce(transactions, 0)) AS n
	FROM sales_summaries
	WHERE business_date BETWEEN $1 AND $2 AND ($3 = '' OR location_id = $3)
	GROUP BY 1, 2
)
SELECT location_id, day, net, n FROM t
UNION ALL
SELECT s.location_id, s.day, s.net, s.n FROM s
WHERE NOT EXISTS (SELECT 1 FROM t WHERE t.location_id = s.location_id AND t.day = s.day)`

// Worked hours and their cost per location and local day. Punches are paired
// in order per employee; a segment runs from in/break_end to out/break_start
// and segments over 16 hours (a missed punch) are ignored. Salaried rates are
// converted at 2,080 hours a year.
const laborSQL = `
WITH seg AS (
	SELECT p.employee_id, p.location_id, p.type, p.at,
	       lead(p.type) OVER w AS next_type, lead(p.at) OVER w AS next_at
	FROM punches p
	WHERE p.at >= $1::date - interval '2 days' AND p.at < $2::date + interval '2 days'
	WINDOW w AS (PARTITION BY p.employee_id ORDER BY p.at)
), work AS (
	SELECT s.employee_id, l.id AS location_id,
	       (s.at AT TIME ZONE l.timezone)::date AS day,
	       extract(epoch FROM s.next_at - s.at) / 3600.0 AS hours
	FROM seg s
	JOIN employees e ON e.id = s.employee_id
	JOIN locations l ON l.id = coalesce(s.location_id, e.home_location_id)
	WHERE s.type IN ('in', 'break_end') AND s.next_type IN ('out', 'break_start')
	  AND s.next_at - s.at <= interval '16 hours'
)
SELECT w.location_id, w.day, sum(w.hours)::numeric, sum(w.hours * coalesce(r.hourly, 0))::numeric
FROM work w
LEFT JOIN LATERAL (
	SELECT CASE WHEN pay_type = 'salary' THEN rate / 2080 ELSE rate END AS hourly
	FROM pay_rates WHERE employee_id = w.employee_id AND effective_from <= w.day
	ORDER BY effective_from DESC LIMIT 1
) r ON true
WHERE w.day BETWEEN $1 AND $2 AND ($3 = '' OR w.location_id = $3)
GROUP BY 1, 2`

// Closing-count over/short per location and business day.
const overShortSQL = `
SELECT location_id, business_date, sum(coalesce(over_short, 0))
FROM cash_counts
WHERE kind = 'close' AND business_date BETWEEN $1 AND $2 AND ($3 = '' OR location_id = $3)
GROUP BY 1, 2`

// Waste at cost per location and local day.
const wasteSQL = `
SELECT m.location_id, (m.occurred_at AT TIME ZONE l.timezone)::date, sum(-m.quantity * coalesce(m.unit_cost, 0))
FROM stock_movements m JOIN locations l ON l.id = m.location_id
WHERE m.type = 'waste' AND (m.occurred_at AT TIME ZONE l.timezone)::date BETWEEN $1 AND $2 AND ($3 = '' OR m.location_id = $3)
GROUP BY 1, 2`

// Cost of goods sold from usage recipes and stocked items, net of reversals,
// on the sale's business day.
const cogsSQL = `
SELECT t.location_id, t.business_date, sum(-m.quantity * coalesce(m.unit_cost, 0))
FROM stock_movements m JOIN sales_transactions t ON t.id = m.source_id
WHERE m.source_type = 'sales_transaction' AND t.business_date BETWEEN $1 AND $2 AND ($3 = '' OR t.location_id = $3)
GROUP BY 1, 2`

// source is one of the per-day queries and the feature it belongs to.
type source struct {
	feature string
	sql     string
	apply   func(d *day, v decimal.Decimal)
}

var sources = []source{
	{"sales", salesSQL, nil},
	{"time", laborSQL, nil},
	{"cash", overShortSQL, func(d *day, v decimal.Decimal) { d.OverShort = d.OverShort.Add(v) }},
	{"inventory.waste", wasteSQL, func(d *day, v decimal.Decimal) { d.WasteCost = d.WasteCost.Add(v) }},
	{"inventory", cogsSQL, func(d *day, v decimal.Decimal) { d.COGS = d.COGS.Add(v) }},
}

// dailyData loads per-location, per-day figures for enabled features. The
// returned set lists the features that were included.
func dailyData(ctx context.Context, q db.Querier, on Enabled, from, to time.Time, loc string) ([]*day, map[string]bool, error) {
	byKey := map[dayKey]*day{}
	var order []*day
	get := func(l string, d time.Time) *day {
		k := dayKey{l, d.Format(time.DateOnly)}
		if x, ok := byKey[k]; ok {
			return x
		}
		x := &day{LocationID: l, Date: d}
		byKey[k] = x
		order = append(order, x)
		return x
	}
	included := map[string]bool{}
	for _, s := range sources {
		ok, err := on(ctx, s.feature)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		included[s.feature] = true
		rows, err := q.Query(ctx, s.sql, from, to, loc)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var l string
			var d time.Time
			switch s.feature {
			case "sales":
				var net decimal.Decimal
				var n int
				if err := rows.Scan(&l, &d, &net, &n); err != nil {
					rows.Close()
					return nil, nil, err
				}
				x := get(l, d)
				x.NetSales, x.Transactions = x.NetSales.Add(net), x.Transactions+n
			case "time":
				var h, c decimal.Decimal
				if err := rows.Scan(&l, &d, &h, &c); err != nil {
					rows.Close()
					return nil, nil, err
				}
				x := get(l, d)
				x.LaborHours, x.LaborCost = x.LaborHours.Add(h), x.LaborCost.Add(c)
			default:
				var v decimal.Decimal
				if err := rows.Scan(&l, &d, &v); err != nil {
					rows.Close()
					return nil, nil, err
				}
				s.apply(get(l, d), v)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	return order, included, nil
}
