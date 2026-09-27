// Package scheduling serves demand drivers, forecasts, staffing rules, shifts,
// schedules, availability and shift swaps.
package scheduling

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/crud"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/ingest"
	"github.com/selectdev/purros/api/internal/modules/organization"
	"github.com/selectdev/purros/api/internal/modules/people"
	"github.com/selectdev/purros/api/internal/modules/timeclock"
	"github.com/selectdev/purros/api/internal/refs"
	"github.com/shopspring/decimal"
)

const (
	feature         = "scheduling"
	forecastFeature = "scheduling.forecasting"
	tag             = "Scheduling"
	historyWeeks    = 8
)

// ---------------------------------------------------------------------------
// Demand drivers
// ---------------------------------------------------------------------------

type DemandDriverInput struct {
	ExternalID string `json:"externalId" validate:"required,extid"`
	refs.LocationRef
	Driver      string           `json:"driver" validate:"required,max=50" doc:"e.g. appointments, foot_traffic, orders"`
	PeriodStart time.Time        `json:"periodStart" validate:"required"`
	PeriodEnd   time.Time        `json:"periodEnd" validate:"required,gtfield=PeriodStart"`
	Value       *decimal.Decimal `json:"value" validate:"required"`
}

type DemandDriverBatch struct {
	Source  string              `json:"source" validate:"required"`
	Drivers []DemandDriverInput `json:"drivers" validate:"required"`
}

// ---------------------------------------------------------------------------
// Forecasts
// ---------------------------------------------------------------------------

type ForecastPoint struct {
	PeriodStart time.Time        `json:"periodStart"`
	PeriodEnd   time.Time        `json:"periodEnd"`
	Value       decimal.Decimal  `json:"value"`
	Actual      *decimal.Decimal `json:"actual"`
}

type Forecast struct {
	LocationID string          `json:"locationId"`
	Driver     string          `json:"driver"`
	Interval   string          `json:"interval"`
	Method     string          `json:"method"`
	Points     []ForecastPoint `json:"points"`
}

type forecastQuery struct {
	LocationID string     `json:"locationId" doc:"Required"`
	Driver     string     `json:"driver,omitempty" doc:"sales (default), transactions, or any demand driver name"`
	From       httpx.Date `json:"from" doc:"First date (required)"`
	To         httpx.Date `json:"to" doc:"Last date (required, at most 62 days after from)"`
	Interval   string     `json:"interval,omitempty" doc:"hour or day (default)"`
}

type AdjustmentInput struct {
	refs.LocationRef
	Driver  string           `json:"driver" validate:"required,max=50"`
	Date    httpx.Date       `json:"date" validate:"required"`
	Percent *decimal.Decimal `json:"percent" validate:"required" doc:"e.g. 20 for +20%, -10 for -10%"`
	Note    string           `json:"note,omitempty" validate:"max=200"`
}

// actuals returns hourly actual values of a driver at a location between two instants.
func actuals(ctx context.Context, q db.Querier, locID, driver string, from, to time.Time) (map[time.Time]decimal.Decimal, error) {
	var sql string
	switch driver {
	case "sales":
		sql = `SELECT hour, sum(v) FROM (
				SELECT date_trunc('hour', occurred_at) AS hour, total AS v FROM sales_transactions
				WHERE location_id = $1 AND status = 'completed' AND occurred_at >= $2 AND occurred_at < $3
				UNION ALL
				SELECT date_trunc('hour', period_start), net_sales FROM sales_summaries
				WHERE location_id = $1 AND period_start >= $2 AND period_start < $3
			) x GROUP BY hour`
	case "transactions":
		sql = `SELECT hour, sum(v) FROM (
				SELECT date_trunc('hour', occurred_at) AS hour, 1::numeric AS v FROM sales_transactions
				WHERE location_id = $1 AND status = 'completed' AND type = 'sale' AND occurred_at >= $2 AND occurred_at < $3
				UNION ALL
				SELECT date_trunc('hour', period_start), coalesce(transactions, 0) FROM sales_summaries
				WHERE location_id = $1 AND period_start >= $2 AND period_start < $3
			) x GROUP BY hour`
	default:
		sql = `SELECT date_trunc('hour', period_start), sum(value) FROM demand_drivers
			WHERE location_id = $1 AND driver = $4 AND period_start >= $2 AND period_start < $3 GROUP BY 1`
	}
	args := []any{locID, from, to}
	if driver != "sales" && driver != "transactions" {
		args = append(args, driver)
	}
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[time.Time]decimal.Decimal{}
	for rows.Next() {
		var h time.Time
		var v decimal.Decimal
		if err := rows.Scan(&h, &v); err != nil {
			return nil, err
		}
		out[h.UTC()] = v
	}
	return out, rows.Err()
}

// ComputeForecast forecasts a driver hour by hour: the average of the same
// weekday and hour over the previous weeks that have data, plus adjustments.
func ComputeForecast(ctx context.Context, q db.Querier, loc organization.Location, driver string, from, to httpx.Date, interval string) (Forecast, error) {
	tz, err := time.LoadLocation(loc.Timezone)
	if err != nil {
		tz = time.UTC
	}
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, tz)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, tz).AddDate(0, 0, 1)
	histStart := start.AddDate(0, 0, -7*historyWeeks)
	hist, err := actuals(ctx, q, loc.ID, driver, histStart, end)
	if err != nil {
		return Forecast{}, err
	}
	adj := map[string]decimal.Decimal{}
	rows, err := q.Query(ctx, `SELECT date, percent FROM forecast_adjustments WHERE location_id=$1 AND driver=$2 AND date BETWEEN $3 AND $4`,
		loc.ID, driver, from, to)
	if err != nil {
		return Forecast{}, err
	}
	for rows.Next() {
		var d time.Time
		var p decimal.Decimal
		if err := rows.Scan(&d, &p); err != nil {
			rows.Close()
			return Forecast{}, err
		}
		adj[d.Format("2006-01-02")] = p
	}
	rows.Close()

	f := Forecast{LocationID: loc.ID, Driver: driver, Interval: interval, Method: fmt.Sprintf("same weekday and hour, average of up to %d prior weeks", historyWeeks), Points: []ForecastPoint{}}
	hundred := decimal.NewFromInt(100)
	var day *ForecastPoint
	for h := start; h.Before(end); h = h.Add(time.Hour) {
		sum, n := decimal.Zero, 0
		for w := 1; w <= historyWeeks; w++ {
			past := h.AddDate(0, 0, -7*w).UTC()
			if v, ok := hist[past]; ok {
				sum = sum.Add(v)
				n++
			} else if hasDataAround(hist, past) {
				n++ // a week with data but none this hour counts as zero
			}
		}
		val := decimal.Zero
		if n > 0 {
			val = sum.Div(decimal.NewFromInt(int64(n)))
		}
		if p, ok := adj[h.Format("2006-01-02")]; ok {
			val = val.Mul(hundred.Add(p)).Div(hundred)
		}
		var actual *decimal.Decimal
		if v, ok := hist[h.UTC()]; ok {
			a := v
			actual = &a
		}
		pt := ForecastPoint{PeriodStart: h.UTC(), PeriodEnd: h.Add(time.Hour).UTC(), Value: val.Round(2), Actual: actual}
		if interval == "hour" {
			f.Points = append(f.Points, pt)
			continue
		}
		if day == nil || h.In(tz).Hour() == 0 {
			if day != nil {
				f.Points = append(f.Points, *day)
			}
			dEnd := time.Date(h.In(tz).Year(), h.In(tz).Month(), h.In(tz).Day(), 0, 0, 0, 0, tz).AddDate(0, 0, 1)
			day = &ForecastPoint{PeriodStart: h.UTC(), PeriodEnd: dEnd.UTC(), Value: decimal.Zero}
		}
		day.Value = day.Value.Add(val).Round(2)
		if actual != nil {
			a := *actual
			if day.Actual != nil {
				a = a.Add(*day.Actual)
			}
			day.Actual = &a
		}
	}
	if day != nil {
		f.Points = append(f.Points, *day)
	}
	return f, nil
}

// hasDataAround reports whether there is any actual in the 24 hours around t,
// so days the location was open but quiet count as zero rather than unknown.
func hasDataAround(hist map[time.Time]decimal.Decimal, t time.Time) bool {
	for d := -12; d <= 12; d++ {
		if _, ok := hist[t.Add(time.Duration(d)*time.Hour)]; ok {
			return true
		}
	}
	return false
}

func parseRange(c *httpx.Ctx, maxDays int) (httpx.Date, httpx.Date, error) {
	from, err := httpx.ParseDate(c.Query("from"))
	if err != nil {
		return from, from, httpx.Validation(httpx.FieldError{Path: "from", Message: "Required date in YYYY-MM-DD format"})
	}
	to, err := httpx.ParseDate(c.Query("to"))
	if err != nil {
		return from, to, httpx.Validation(httpx.FieldError{Path: "to", Message: "Required date in YYYY-MM-DD format"})
	}
	if to.Before(from.Time) || to.Sub(from.Time) > time.Duration(maxDays)*24*time.Hour {
		return from, to, httpx.Validation(httpx.FieldError{Path: "to", Message: fmt.Sprintf("Must be on or after from, and at most %d days later", maxDays)})
	}
	return from, to, nil
}

// ---------------------------------------------------------------------------
// Staffing rules and needs
// ---------------------------------------------------------------------------

type StaffingRule struct {
	ID            string           `json:"id" db:"id"`
	LocationID    string           `json:"locationId" db:"location_id"`
	DepartmentID  *string          `json:"departmentId" db:"department_id"`
	Driver        *string          `json:"driver" db:"driver"`
	UnitsPerStaff *decimal.Decimal `json:"unitsPerStaff" db:"units_per_staff"`
	MinStaff      int              `json:"minStaff" db:"min_staff"`
	CreatedAt     time.Time        `json:"createdAt" db:"created_at"`
	UpdatedAt     time.Time        `json:"updatedAt" db:"updated_at"`
	ArchivedAt    *time.Time       `json:"archivedAt" db:"archived_at"`
}

type StaffingRuleInput struct {
	LocationID    string           `json:"locationId" db:"location_id" validate:"required"`
	DepartmentID  *string          `json:"departmentId,omitempty" db:"department_id"`
	Driver        *string          `json:"driver,omitempty" db:"driver" validate:"omitempty,max=50" doc:"Leave empty for a fixed minimum"`
	UnitsPerStaff *decimal.Decimal `json:"unitsPerStaff,omitempty" db:"units_per_staff" doc:"e.g. 40 transactions per staff-hour"`
	MinStaff      int              `json:"minStaff,omitempty" db:"min_staff" validate:"min=0"`
}

var staffingRules = &crud.Resource[StaffingRule, StaffingRuleInput]{
	Path: "/staffing-rules", Table: "staffing_rules", Prefix: ids.StaffingRule, Noun: "staffing_rule", Tag: tag,
	Feature: feature, ReadScope: "scheduling:read", WriteScope: "scheduling:write", Archive: true,
	Filters: []crud.Filter{{Query: "locationId", Column: "location_id"}},
	Check: func(c *httpx.Ctx, q db.Querier, in *StaffingRuleInput, _ *StaffingRule) error {
		if (in.Driver == nil) != (in.UnitsPerStaff == nil) {
			return httpx.Validation(httpx.FieldError{Path: "unitsPerStaff", Message: "Set driver and unitsPerStaff together"})
		}
		if in.UnitsPerStaff != nil && !in.UnitsPerStaff.IsPositive() {
			return httpx.Validation(httpx.FieldError{Path: "unitsPerStaff", Message: "Must be greater than zero"})
		}
		return nil
	},
}

type NeedHour struct {
	Start     time.Time `json:"start"`
	Needed    int       `json:"needed"`
	Scheduled int       `json:"scheduled"`
	Gap       int       `json:"gap" doc:"needed − scheduled; negative means overstaffed"`
}

type StaffingNeeds struct {
	LocationID string     `json:"locationId"`
	Date       httpx.Date `json:"date"`
	Hours      []NeedHour `json:"hours"`
}

// ---------------------------------------------------------------------------
// Shifts
// ---------------------------------------------------------------------------

type Shift struct {
	ID           string     `json:"id" db:"id"`
	LocationID   string     `json:"locationId" db:"location_id"`
	DepartmentID *string    `json:"departmentId" db:"department_id"`
	EmployeeID   *string    `json:"employeeId" db:"employee_id" doc:"null for an open shift"`
	Position     *string    `json:"position" db:"position"`
	StartsAt     time.Time  `json:"startsAt" db:"starts_at"`
	EndsAt       time.Time  `json:"endsAt" db:"ends_at"`
	BreakMinutes int        `json:"breakMinutes" db:"break_minutes"`
	Status       string     `json:"status" db:"status"`
	Notes        *string    `json:"notes" db:"notes"`
	ExternalID   *string    `json:"externalId" db:"external_id"`
	PublishedAt  *time.Time `json:"publishedAt" db:"published_at"`
	Version      int        `json:"version" db:"version"`
	CreatedAt    time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time  `json:"updatedAt" db:"updated_at"`
}

type ShiftInput struct {
	LocationID   string    `json:"locationId" db:"location_id" validate:"required"`
	DepartmentID *string   `json:"departmentId,omitempty" db:"department_id"`
	EmployeeID   *string   `json:"employeeId,omitempty" db:"employee_id"`
	Position     *string   `json:"position,omitempty" db:"position" validate:"omitempty,max=100"`
	StartsAt     time.Time `json:"startsAt" db:"starts_at" validate:"required"`
	EndsAt       time.Time `json:"endsAt" db:"ends_at" validate:"required,gtfield=StartsAt"`
	BreakMinutes int       `json:"breakMinutes,omitempty" db:"break_minutes" validate:"min=0"`
	Status       string    `json:"status,omitempty" db:"status" validate:"omitempty,oneof=draft published cancelled"`
	Notes        *string   `json:"notes,omitempty" db:"notes" validate:"omitempty,max=500"`
	ExternalID   *string   `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
}

// checkShift enforces overlap and minimum-rest rules for an employee's shift.
func checkShift(c *httpx.Ctx, q db.Querier, in *ShiftInput, before *Shift) error {
	if in.Status == "" {
		in.Status = "draft"
		if before != nil {
			in.Status = before.Status
		}
	}
	if in.EndsAt.Sub(in.StartsAt) > 16*time.Hour {
		return httpx.Validation(httpx.FieldError{Path: "endsAt", Message: "A shift can be at most 16 hours"})
	}
	if in.EmployeeID == nil || in.Status == "cancelled" {
		return nil
	}
	selfID := ""
	if before != nil {
		selfID = before.ID
	}
	var conflict string
	err := q.QueryRow(c, `SELECT id FROM shifts WHERE employee_id=$1 AND status <> 'cancelled' AND id <> $2
		AND starts_at < $4 AND ends_at > $3 LIMIT 1`, *in.EmployeeID, selfID, in.StartsAt, in.EndsAt).Scan(&conflict)
	if err == nil {
		return httpx.Conflict("This employee already has an overlapping shift (" + conflict + ").")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	rules, err := timeclock.RulesFor(c, q, *in.EmployeeID)
	if err != nil {
		return err
	}
	if rules.MinRestMinutes != nil {
		rest := time.Duration(*rules.MinRestMinutes) * time.Minute
		err := q.QueryRow(c, `SELECT id FROM shifts WHERE employee_id=$1 AND status <> 'cancelled' AND id <> $2
			AND ((ends_at > $3 - $5::interval AND ends_at <= $3) OR (starts_at >= $4 AND starts_at < $4 + $5::interval)) LIMIT 1`,
			*in.EmployeeID, selfID, in.StartsAt, in.EndsAt, fmt.Sprintf("%d minutes", *rules.MinRestMinutes)).Scan(&conflict)
		if err == nil {
			return httpx.Validation(httpx.FieldError{Path: "startsAt",
				Message: fmt.Sprintf("Less than %s rest between this shift and shift %s", rest, conflict)})
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	return nil
}

var shifts = &crud.Resource[Shift, ShiftInput]{
	Path: "/shifts", Table: "shifts", Prefix: ids.Shift, Noun: "shift", Tag: tag,
	Feature: feature, ReadScope: "scheduling:read", WriteScope: "scheduling:write", External: true, NoList: true,
	UpdatedEvent: "shift.changed",
	Check:        checkShift,
	LocationOf:   func(s *Shift) string { return s.LocationID },
}

type shiftQuery struct {
	httpx.ListParams
	LocationID string    `json:"locationId,omitempty"`
	EmployeeID string    `json:"employeeId,omitempty"`
	From       time.Time `json:"from,omitempty" doc:"Shifts ending after this time"`
	To         time.Time `json:"to,omitempty" doc:"Shifts starting before this time"`
	Status     string    `json:"status,omitempty"`
	Open       bool      `json:"open,omitempty" doc:"Only open (unassigned) shifts"`
}

func listShifts(c *httpx.Ctx, q db.Querier, locID, empID, status string, open bool, from, to *time.Time, afterID string, limit int) ([]Shift, error) {
	rows, err := q.Query(c, `SELECT id, location_id, department_id, employee_id, position, starts_at, ends_at, break_minutes,
		status, notes, external_id, published_at, version, created_at, updated_at FROM shifts
		WHERE id > $1 AND ($2 = '' OR location_id = $2) AND ($3 = '' OR employee_id = $3) AND ($4 = '' OR status = $4)
		  AND (NOT $5 OR employee_id IS NULL) AND ($6::timestamptz IS NULL OR ends_at > $6) AND ($7::timestamptz IS NULL OR starts_at < $7)
		ORDER BY id LIMIT $8`, afterID, locID, empID, status, open, from, to, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[Shift])
}

func timeParam(c *httpx.Ctx, name string) (*time.Time, error) {
	s := c.Query(name)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, httpx.Validation(httpx.FieldError{Path: name, Message: "Must be an RFC 3339 timestamp"})
	}
	return &t, nil
}

type PublishInput struct {
	refs.LocationRef
	From time.Time `json:"from" validate:"required"`
	To   time.Time `json:"to" validate:"required,gtfield=From"`
}

type PublishResult struct {
	LocationID string `json:"locationId"`
	Published  int    `json:"published"`
	OpenShifts int    `json:"openShifts"`
}

type Schedule struct {
	LocationID    string           `json:"locationId"`
	From          time.Time        `json:"from"`
	To            time.Time        `json:"to"`
	Shifts        []Shift          `json:"shifts"`
	TotalHours    decimal.Decimal  `json:"totalHours"`
	EstimatedCost *decimal.Decimal `json:"estimatedCost" doc:"Based on current pay rates; null if no shift has a rate"`
	OpenShifts    int              `json:"openShifts"`
}

// ---------------------------------------------------------------------------
// Availability and swaps
// ---------------------------------------------------------------------------

type Availability struct {
	ID         string          `json:"id" db:"id"`
	EmployeeID string          `json:"employeeId" db:"employee_id"`
	Weekday    int             `json:"weekday" db:"weekday" doc:"0 = Sunday"`
	StartTime  httpx.TimeOfDay `json:"startTime" db:"start_time"`
	EndTime    httpx.TimeOfDay `json:"endTime" db:"end_time"`
	Kind       string          `json:"kind" db:"kind"`
	CreatedAt  time.Time       `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time       `json:"updatedAt" db:"updated_at"`
	ArchivedAt *time.Time      `json:"archivedAt" db:"archived_at"`
}

type AvailabilityInput struct {
	EmployeeID string          `json:"employeeId" db:"employee_id" validate:"required"`
	Weekday    int             `json:"weekday" db:"weekday" validate:"min=0,max=6"`
	StartTime  httpx.TimeOfDay `json:"startTime" db:"start_time"`
	EndTime    httpx.TimeOfDay `json:"endTime" db:"end_time"`
	Kind       string          `json:"kind,omitempty" db:"kind" validate:"omitempty,oneof=available unavailable preferred"`
}

var availability = &crud.Resource[Availability, AvailabilityInput]{
	Path: "/availability", Table: "availability", Prefix: ids.Availability, Noun: "availability", Tag: tag,
	Feature: feature, ReadScope: "scheduling:read", WriteScope: "scheduling:write", Archive: true,
	Filters: []crud.Filter{{Query: "employeeId", Column: "employee_id"}},
	Defaults: func(in *AvailabilityInput) {
		if in.Kind == "" {
			in.Kind = "available"
		}
	},
	Check: func(c *httpx.Ctx, q db.Querier, in *AvailabilityInput, _ *Availability) error {
		if in.EndTime.Minutes <= in.StartTime.Minutes {
			return httpx.Validation(httpx.FieldError{Path: "endTime", Message: "Must be after startTime"})
		}
		return nil
	},
}

type SwapRequest struct {
	ID             string     `json:"id"`
	ShiftID        string     `json:"shiftId"`
	FromEmployeeID *string    `json:"fromEmployeeId"`
	ToEmployeeID   string     `json:"toEmployeeId"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"createdAt"`
	DecidedAt      *time.Time `json:"decidedAt"`
}

type SwapInput struct {
	ShiftID      string `json:"shiftId" validate:"required"`
	ToEmployeeID string `json:"toEmployeeId" validate:"required"`
}

type ClaimInput struct {
	EmployeeID string `json:"employeeId" validate:"required"`
}

const swapCols = `id, shift_id, from_employee_id, to_employee_id, status, created_at, decided_at`

func scanSwap(r pgx.Row) (SwapRequest, error) {
	var s SwapRequest
	err := r.Scan(&s.ID, &s.ShiftID, &s.FromEmployeeID, &s.ToEmployeeID, &s.Status, &s.CreatedAt, &s.DecidedAt)
	return s, err
}

type swapList struct {
	Data []SwapRequest `json:"data"`
}

// assign sets a shift's employee, checking conflicts, and records the change.
func assign(c *httpx.Ctx, tx pgx.Tx, shiftID, empID string) (Shift, error) {
	before, err := shifts.Get(c, tx, "id", shiftID, true)
	if err != nil {
		return before, err
	}
	in := ShiftInput{LocationID: before.LocationID, DepartmentID: before.DepartmentID, EmployeeID: &empID, Position: before.Position,
		StartsAt: before.StartsAt, EndsAt: before.EndsAt, BreakMinutes: before.BreakMinutes, Status: before.Status,
		Notes: before.Notes, ExternalID: before.ExternalID}
	return shifts.Update(c, tx, before, in)
}

// Routes returns the scheduling routes.
func Routes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "POST", Path: "/demand-drivers", Tag: tag, Feature: feature, Scope: "scheduling:write",
			Ingest: true, Status: 202, Summary: "Send demand drivers (appointments, foot traffic, orders…)",
			Description: "Hourly or daily numbers that drive staffing. Unique on (source, externalId).",
			Body:        DemandDriverBatch{}, Response: httpx.BatchResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return ingest.Run(c, "demand_drivers", "drivers", func(e *ingest.Env, _ int, in DemandDriverInput) (ingest.Outcome, error) {
					loc, err := e.Locs.Resolve(c, in.LocationID, in.LocationExternalID)
					if err != nil {
						return ingest.Outcome{}, ingest.Rejectf("location", "%s", err.Error())
					}
					if in.Driver == "sales" || in.Driver == "transactions" {
						return ingest.Outcome{}, ingest.Rejectf("driver", "sales and transactions come from sales feeds; use another name")
					}
					var id string
					var inserted bool
					err = e.Tx.QueryRow(c, `
						INSERT INTO demand_drivers (id, source, external_id, location_id, driver, period_start, period_end, value)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
						ON CONFLICT (source, external_id) DO UPDATE SET location_id=EXCLUDED.location_id, driver=EXCLUDED.driver,
							period_start=EXCLUDED.period_start, period_end=EXCLUDED.period_end, value=EXCLUDED.value, updated_at=now()
						RETURNING id, (xmax = 0)`,
						ids.New(ids.DemandDriver), e.Source, in.ExternalID, loc.ID, in.Driver, in.PeriodStart.UTC(), in.PeriodEnd.UTC(), *in.Value).
						Scan(&id, &inserted)
					return ingest.Outcome{Status: ingest.Status(inserted), ID: id}, err
				})
			},
		},
		{
			Method: "GET", Path: "/forecasts", Tag: tag, Feature: forecastFeature, Scope: "scheduling:read",
			Summary: "Forecast a demand driver", Query: forecastQuery{}, Response: Forecast{},
			Handler: func(c *httpx.Ctx) (any, error) {
				from, to, err := parseRange(c, 62)
				if err != nil {
					return nil, err
				}
				loc, err := organization.GetLocation(c, c.App.Pool, c.Query("locationId"), c.Query("locationExternalId"))
				if err != nil {
					return nil, err
				}
				driver := c.Query("driver")
				if driver == "" {
					driver = "sales"
				}
				interval := c.Query("interval")
				if interval == "" {
					interval = "day"
				}
				if interval != "day" && interval != "hour" {
					return nil, httpx.Validation(httpx.FieldError{Path: "interval", Message: "Must be one of: hour, day"})
				}
				return ComputeForecast(c, c.App.Pool, loc, driver, from, to, interval)
			},
		},
		{
			Method: "POST", Path: "/forecasts/adjustments", Tag: tag, Feature: forecastFeature, Scope: "scheduling:write",
			Summary: "Adjust a day's forecast by a percentage", Body: AdjustmentInput{}, Response: AdjustmentInput{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in AdjustmentInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				err := c.InTx(func(tx pgx.Tx) error {
					locID, err := refs.Location(c, tx, in.LocationRef, "")
					if err != nil {
						return err
					}
					in.LocationID = locID
					if _, err := tx.Exec(c, `INSERT INTO forecast_adjustments (id, location_id, driver, date, percent, note)
						VALUES ($1,$2,$3,$4,$5,nullif($6,''))
						ON CONFLICT (location_id, driver, date) DO UPDATE SET percent=EXCLUDED.percent, note=EXCLUDED.note`,
						ids.New(ids.ForecastAdj), locID, in.Driver, in.Date, *in.Percent, in.Note); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "forecast.adjust", EventType: "forecast.updated", Feature: forecastFeature,
						EntityType: "forecast", EntityID: locID + ":" + in.Driver, LocationID: locID, After: in})
				})
				return in, err
			},
		},
		{
			Method: "GET", Path: "/staffing-needs", Tag: tag, Feature: feature, Scope: "scheduling:read",
			Summary:     "Staff needed vs scheduled, hour by hour",
			Description: "Needs come from staffing rules applied to the forecast; scheduled counts non-cancelled shifts.",
			Response:    StaffingNeeds{},
			Handler: func(c *httpx.Ctx) (any, error) {
				date, err := httpx.ParseDate(c.Query("date"))
				if err != nil {
					return nil, httpx.Validation(httpx.FieldError{Path: "date", Message: "Required date in YYYY-MM-DD format"})
				}
				loc, err := organization.GetLocation(c, c.App.Pool, c.Query("locationId"), c.Query("locationExternalId"))
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `SELECT driver, units_per_staff, min_staff FROM staffing_rules
					WHERE location_id = $1 AND archived_at IS NULL`, loc.ID)
				if err != nil {
					return nil, err
				}
				type rule struct {
					driver *string
					units  *decimal.Decimal
					min    int
				}
				var rules []rule
				for rows.Next() {
					var r rule
					if err := rows.Scan(&r.driver, &r.units, &r.min); err != nil {
						rows.Close()
						return nil, err
					}
					rules = append(rules, r)
				}
				rows.Close()
				forecasts := map[string]Forecast{}
				out := StaffingNeeds{LocationID: loc.ID, Date: date, Hours: []NeedHour{}}
				tz, _ := time.LoadLocation(loc.Timezone)
				if tz == nil {
					tz = time.UTC
				}
				dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, tz)
				scheduled, err := listShifts(c, c.App.Pool, loc.ID, "", "", false, crud.Ptr(dayStart), crud.Ptr(dayStart.AddDate(0, 0, 1)), "", 10000)
				if err != nil {
					return nil, err
				}
				for h := 0; h < 24; h++ {
					start := dayStart.Add(time.Duration(h) * time.Hour)
					need := 0
					for _, r := range rules {
						n := r.min
						if r.driver != nil && r.units != nil {
							f, ok := forecasts[*r.driver]
							if !ok {
								f, err = ComputeForecast(c, c.App.Pool, loc, *r.driver, date, date, "hour")
								if err != nil {
									return nil, err
								}
								forecasts[*r.driver] = f
							}
							if h < len(f.Points) {
								v, _ := f.Points[h].Value.Div(*r.units).Float64()
								n = max(n, int(math.Ceil(v)))
							}
						}
						need += n
					}
					sched := 0
					for _, s := range scheduled {
						if s.Status != "cancelled" && s.StartsAt.Before(start.Add(time.Hour)) && s.EndsAt.After(start) {
							sched++
						}
					}
					out.Hours = append(out.Hours, NeedHour{Start: start.UTC(), Needed: need, Scheduled: sched, Gap: need - sched})
				}
				return out, nil
			},
		},
		{
			Method: "GET", Path: "/shifts", Tag: tag, Feature: feature, Scope: "scheduling:read",
			Summary: "List shifts", Query: shiftQuery{}, Response: httpx.Page[Shift]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				from, err := timeParam(c, "from")
				if err != nil {
					return nil, err
				}
				to, err := timeParam(c, "to")
				if err != nil {
					return nil, err
				}
				list, err := listShifts(c, c.App.Pool, c.Query("locationId"), c.Query("employeeId"), c.Query("status"),
					c.Query("open") == "true", from, to, lp.AfterID, lp.Limit+1)
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(s Shift) string { return s.ID }), nil
			},
		},
		{
			Method: "POST", Path: "/schedules:publish", Tag: tag, Feature: feature, Scope: "scheduling:write",
			Summary: "Publish draft shifts for a location and period", Body: PublishInput{}, Response: PublishResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in PublishInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out PublishResult
				err := c.InTx(func(tx pgx.Tx) error {
					locID, err := refs.Location(c, tx, in.LocationRef, "")
					if err != nil {
						return err
					}
					out.LocationID = locID
					rows, err := tx.Query(c, `UPDATE shifts SET status='published', published_at=now(), version=version+1, updated_at=now()
						WHERE location_id=$1 AND status='draft' AND starts_at >= $2 AND starts_at < $3
						RETURNING id, location_id, department_id, employee_id, position, starts_at, ends_at, break_minutes,
						status, notes, external_id, published_at, version, created_at, updated_at`, locID, in.From, in.To)
					if err != nil {
						return err
					}
					published, err := pgx.CollectRows(rows, pgx.RowToStructByName[Shift])
					if err != nil {
						return err
					}
					out.Published = len(published)
					for _, s := range published {
						if s.EmployeeID == nil {
							out.OpenShifts++
							if err := c.Record(tx, httpx.Change{Action: "shift.post_open", EventType: "open_shift.posted",
								Feature: "scheduling.open_shifts", EntityType: "shift", EntityID: s.ID, LocationID: locID, After: s}); err != nil {
								return err
							}
						}
					}
					return c.Record(tx, httpx.Change{Action: "schedule.publish", EventType: "schedule.published", Feature: feature,
						EntityType: "schedule", EntityID: locID, LocationID: locID,
						After: map[string]any{"locationId": locID, "from": in.From, "to": in.To, "published": out.Published}})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/schedules", Tag: tag, Feature: feature, Scope: "scheduling:read",
			Summary: "A location's schedule with hours and estimated cost", Response: Schedule{},
			Handler: func(c *httpx.Ctx) (any, error) {
				from, err := timeParam(c, "from")
				if err != nil {
					return nil, err
				}
				to, err := timeParam(c, "to")
				if err != nil {
					return nil, err
				}
				if from == nil || to == nil || !to.After(*from) || to.Sub(*from) > 62*24*time.Hour {
					return nil, httpx.Validation(httpx.FieldError{Path: "to", Message: "from and to are required, to after from, at most 62 days"})
				}
				locID, err := refs.Location(c, c.App.Pool, refs.LocationRef{LocationID: c.Query("locationId"), LocationExternalID: c.Query("locationExternalId")}, "")
				if err != nil {
					return nil, err
				}
				list, err := listShifts(c, c.App.Pool, locID, "", "", false, from, to, "", 10000)
				if err != nil {
					return nil, err
				}
				sort.Slice(list, func(i, j int) bool { return list[i].StartsAt.Before(list[j].StartsAt) })
				out := Schedule{LocationID: locID, From: *from, To: *to, Shifts: list, TotalHours: decimal.Zero}
				if out.Shifts == nil {
					out.Shifts = []Shift{}
				}
				cost := decimal.Zero
				costed := false
				for _, s := range list {
					if s.Status == "cancelled" {
						continue
					}
					hours := decimal.NewFromFloat(s.EndsAt.Sub(s.StartsAt).Minutes() - float64(s.BreakMinutes)).Div(decimal.NewFromInt(60))
					out.TotalHours = out.TotalHours.Add(hours)
					if s.EmployeeID == nil {
						out.OpenShifts++
						continue
					}
					rate, _, err := people.CurrentRate(c, c.App.Pool, *s.EmployeeID, s.StartsAt)
					if err != nil {
						return nil, err
					}
					if rate != nil {
						cost = cost.Add(hours.Mul(*rate))
						costed = true
					}
				}
				out.TotalHours = out.TotalHours.Round(2)
				if costed {
					cc := cost.Round(2)
					out.EstimatedCost = &cc
				}
				return out, nil
			},
		},
		{
			Method: "POST", Path: "/shifts/{id}:claim", Tag: tag, Feature: "scheduling.open_shifts", Scope: "scheduling:write",
			Summary: "Assign an open shift to an employee", Body: ClaimInput{}, Response: Shift{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in ClaimInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Shift
				err := c.InTx(func(tx pgx.Tx) error {
					s, err := shifts.Get(c, tx, "id", c.Param("id"), true)
					if err != nil {
						return err
					}
					if s.EmployeeID != nil {
						return httpx.Conflict("This shift is already assigned.")
					}
					out, err = assign(c, tx, s.ID, in.EmployeeID)
					return err
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/shift-swaps", Tag: tag, Feature: "scheduling.shift_swaps", Scope: "scheduling:write",
			Summary: "Request a shift swap", Body: SwapInput{}, Response: SwapRequest{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in SwapInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out SwapRequest
				err := c.InTx(func(tx pgx.Tx) error {
					s, err := shifts.Get(c, tx, "id", in.ShiftID, false)
					if err != nil {
						return err
					}
					if s.EmployeeID != nil && *s.EmployeeID == in.ToEmployeeID {
						return httpx.Validation(httpx.FieldError{Path: "toEmployeeId", Message: "Already assigned to this employee"})
					}
					out, err = scanSwap(tx.QueryRow(c, `INSERT INTO shift_swap_requests (id, shift_id, from_employee_id, to_employee_id)
						VALUES ($1,$2,$3,$4) RETURNING `+swapCols, ids.New(ids.ShiftSwap), s.ID, s.EmployeeID, in.ToEmployeeID))
					if db.IsForeignKeyViolation(err) {
						return httpx.Validation(httpx.FieldError{Path: "toEmployeeId", Message: "Unknown employee"})
					}
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "shift.swap_request", EventType: "shift.swap_requested",
						Feature: "scheduling.shift_swaps", EntityType: "shift", EntityID: s.ID, LocationID: s.LocationID, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/shift-swaps", Tag: tag, Feature: "scheduling.shift_swaps", Scope: "scheduling:read",
			Summary: "List shift swap requests", Response: swapList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT `+swapCols+` FROM shift_swap_requests WHERE $1 = '' OR status = $1
					ORDER BY created_at DESC LIMIT 200`, c.Query("status"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (SwapRequest, error) { return scanSwap(r) })
				if list == nil {
					list = []SwapRequest{}
				}
				return swapList{Data: list}, err
			},
		},
		swapDecision("approved"),
		swapDecision("rejected"),
	}
	routes = append(routes, shifts.Routes()...)
	routes = append(routes, staffingRules.Routes()...)
	routes = append(routes, availability.Routes()...)
	return routes
}

func swapDecision(status string) httpx.Route {
	action := map[string]string{"approved": "approve", "rejected": "reject"}[status]
	return httpx.Route{
		Method: "POST", Path: "/shift-swaps/{id}:" + action, Tag: tag, Feature: "scheduling.shift_swaps", Scope: "scheduling:write",
		Summary: map[string]string{"approved": "Approve a swap (reassigns the shift)", "rejected": "Reject a swap"}[status], Response: SwapRequest{},
		Handler: func(c *httpx.Ctx) (any, error) {
			var out SwapRequest
			err := c.InTx(func(tx pgx.Tx) error {
				before, err := scanSwap(tx.QueryRow(c, `SELECT `+swapCols+` FROM shift_swap_requests WHERE id=$1 FOR UPDATE`, c.Param("id")))
				if errors.Is(err, pgx.ErrNoRows) {
					return httpx.NotFound("Swap request not found.")
				}
				if err != nil {
					return err
				}
				if before.Status != "requested" {
					return httpx.Conflict("This swap was already " + before.Status + ".")
				}
				if status == "approved" {
					if _, err := assign(c, tx, before.ShiftID, before.ToEmployeeID); err != nil {
						return err
					}
				}
				out, err = scanSwap(tx.QueryRow(c, `UPDATE shift_swap_requests SET status=$2, decided_at=now() WHERE id=$1 RETURNING `+swapCols, before.ID, status))
				if err != nil {
					return err
				}
				event := ""
				if status == "approved" {
					event = "shift.swap_approved"
				}
				return c.Record(tx, httpx.Change{Action: "shift.swap_" + action, EventType: event, Feature: "scheduling.shift_swaps",
					EntityType: "shift", EntityID: before.ShiftID, Before: before, After: out})
			})
			return out, err
		},
	}
}
