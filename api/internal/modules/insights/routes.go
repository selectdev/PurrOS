package insights

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/webhooks"
)

const (
	feature = "insights"
	tag     = "Reports & Insights"
)

// MaxRangeDays is the longest date range a KPI or report request may cover.
const MaxRangeDays = 366

type rangeQuery struct {
	From       string `json:"from,omitempty" doc:"First business date (YYYY-MM-DD), default today (UTC)"`
	To         string `json:"to,omitempty" doc:"Last business date, default from"`
	LocationID string `json:"locationId,omitempty"`
}

type reportQuery struct {
	rangeQuery
	Format string `json:"format,omitempty" doc:"json (default) or csv"`
}

type recQuery struct {
	LocationID string `json:"locationId,omitempty"`
}

type reportList struct {
	Data []ReportDef `json:"data"`
}

type recList struct {
	Data []Recommendation `json:"data"`
}

// parseRange reads from/to, checks the location exists, and returns the range.
func parseRange(c *httpx.Ctx) (time.Time, time.Time, string, error) {
	var from, to time.Time
	today := httpx.NewDate(c.App.Now()).Time
	from, to = today, today
	for name, dst := range map[string]*time.Time{"from": &from, "to": &to} {
		if s := c.Query(name); s != "" {
			d, err := httpx.ParseDate(s)
			if err != nil {
				return from, to, "", httpx.Validation(httpx.FieldError{Path: name, Message: "Must be a date in YYYY-MM-DD format"})
			}
			*dst = d.Time
		}
	}
	if c.Query("from") != "" && c.Query("to") == "" {
		to = from
	}
	if to.Before(from) {
		return from, to, "", httpx.Validation(httpx.FieldError{Path: "to", Message: "Must be on or after from"})
	}
	if to.Sub(from) > MaxRangeDays*24*time.Hour {
		return from, to, "", httpx.Validation(httpx.FieldError{Path: "to", Message: "The range can cover at most 366 days"})
	}
	loc := c.Query("locationId")
	if loc != "" {
		var ok bool
		if err := c.App.Pool.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM locations WHERE id = $1)`, loc).Scan(&ok); err != nil {
			return from, to, "", err
		}
		if !ok {
			return from, to, "", httpx.Validation(httpx.FieldError{Path: "locationId", Message: "Unknown location"})
		}
	}
	return from, to, loc, nil
}

func enabled(c *httpx.Ctx) Enabled { return c.App.Features.IsEnabled }

// Routes returns the insights routes.
func Routes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "GET", Path: "/kpis", Tag: tag, Feature: feature, Scope: "reports:read",
			Summary:     "Headline KPIs for a date range",
			Description: "Figures for disabled features are left out. Open corrective actions, open work orders and low-stock items are current counts.",
			Query:       rangeQuery{}, Response: KPIs{},
			Handler: func(c *httpx.Ctx) (any, error) {
				from, to, loc, err := parseRange(c)
				if err != nil {
					return nil, err
				}
				return Compute(c, c.App.Pool, enabled(c), from, to, loc)
			},
		},
		{
			Method: "GET", Path: "/reports", Tag: tag, Feature: feature, Scope: "reports:read",
			Summary: "List the available reports", Response: reportList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				out := reportList{Data: []ReportDef{}}
				for _, r := range Reports {
					ok, err := c.App.Features.IsEnabled(c, r.Feature)
					if err != nil {
						return nil, err
					}
					if ok {
						out.Data = append(out.Data, r)
					}
				}
				return out, nil
			},
		},
		{
			Method: "GET", Path: "/reports/{key}", Tag: tag, Feature: feature, Scope: "reports:read",
			Summary: "Run a report", Description: "Add format=csv for a CSV download.",
			Query: reportQuery{}, Response: Report{},
			Handler: func(c *httpx.Ctx) (any, error) {
				def, ok := FindReport(c.Param("key"))
				if ok {
					on, err := c.App.Features.IsEnabled(c, def.Feature)
					if err != nil {
						return nil, err
					}
					ok = on
				}
				if !ok {
					return nil, httpx.NotFound("Report not found.")
				}
				format := c.Query("format")
				if format != "" && format != "json" && format != "csv" {
					return nil, httpx.Validation(httpx.FieldError{Path: "format", Message: "Must be json or csv"})
				}
				from, to, loc, err := parseRange(c)
				if err != nil {
					return nil, err
				}
				r, err := def.Run(c, c.App.Pool, enabled(c), from, to, loc)
				if err != nil {
					return nil, err
				}
				if format == "csv" {
					name := def.Key
					if !def.Snapshot {
						name += "_" + from.Format(time.DateOnly) + "_" + to.Format(time.DateOnly)
					}
					return httpx.Raw{ContentType: "text/csv; charset=utf-8", Filename: name + ".csv", Body: r.CSV()}, nil
				}
				return r, nil
			},
		},
		{
			Method: "GET", Path: "/recommendations", Tag: tag, Feature: "insights.recommendations", Scope: "reports:read",
			Summary:     "Recommended actions",
			Description: "Rule-based suggestions from current data: reorders, overdue corrective actions, stale urgent work orders, high labor and cash variances yesterday, and purchase orders awaiting approval.",
			Query:       recQuery{}, Response: recList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				loc := c.Query("locationId")
				list, err := Recommend(c, c.App.Pool, enabled(c), c.App.Now(), loc)
				return recList{Data: list}, err
			},
		},
	}
	return append(routes, alertRules.Routes()...)
}

// AlertJob evaluates alert rules from the worker.
func AlertJob(pool *pgxpool.Pool, fs *features.Store) webhooks.Job {
	return webhooks.Job{
		Name: "alert rules", Every: 5 * time.Minute,
		Run: func(ctx context.Context) error {
			_, err := EvaluateAlerts(ctx, pool, fs.IsEnabled, time.Now())
			return err
		},
	}
}
