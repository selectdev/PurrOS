package timeclock

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/crud"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/modules/people"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Labor rule sets
// ---------------------------------------------------------------------------

type LaborRuleSet struct {
	ID                        string          `json:"id" db:"id"`
	Name                      string          `json:"name" db:"name"`
	RoundingMinutes           int             `json:"roundingMinutes" db:"rounding_minutes"`
	DailyOvertimeMinutes      *int            `json:"dailyOvertimeMinutes" db:"daily_overtime_minutes"`
	WeeklyOvertimeMinutes     *int            `json:"weeklyOvertimeMinutes" db:"weekly_overtime_minutes"`
	OvertimeMultiplier        decimal.Decimal `json:"overtimeMultiplier" db:"overtime_multiplier"`
	BreakRequiredAfterMinutes *int            `json:"breakRequiredAfterMinutes" db:"break_required_after_minutes"`
	MinRestMinutes            *int            `json:"minRestMinutes" db:"min_rest_minutes"`
	EarlyClockInMinutes       int             `json:"earlyClockInMinutes" db:"early_clock_in_minutes"`
	CreatedAt                 time.Time       `json:"createdAt" db:"created_at"`
	UpdatedAt                 time.Time       `json:"updatedAt" db:"updated_at"`
}

type LaborRuleSetInput struct {
	Name                      string           `json:"name" db:"name" validate:"required,max=100"`
	RoundingMinutes           int              `json:"roundingMinutes,omitempty" db:"rounding_minutes" validate:"min=0,max=60"`
	DailyOvertimeMinutes      *int             `json:"dailyOvertimeMinutes,omitempty" db:"daily_overtime_minutes" validate:"omitempty,min=1"`
	WeeklyOvertimeMinutes     *int             `json:"weeklyOvertimeMinutes,omitempty" db:"weekly_overtime_minutes" validate:"omitempty,min=1"`
	OvertimeMultiplier        *decimal.Decimal `json:"overtimeMultiplier,omitempty" db:"overtime_multiplier"`
	BreakRequiredAfterMinutes *int             `json:"breakRequiredAfterMinutes,omitempty" db:"break_required_after_minutes" validate:"omitempty,min=1"`
	MinRestMinutes            *int             `json:"minRestMinutes,omitempty" db:"min_rest_minutes" validate:"omitempty,min=1"`
	EarlyClockInMinutes       int              `json:"earlyClockInMinutes,omitempty" db:"early_clock_in_minutes" validate:"min=0"`
}

var laborRules = &crud.Resource[LaborRuleSet, LaborRuleSetInput]{
	Path: "/labor-rule-sets", Table: "labor_rule_sets", Prefix: ids.LaborRuleSet, Noun: "labor_rule_set",
	Tag: "Time & Attendance", Feature: feature, ReadScope: "time:read", WriteScope: "time:write",
	Defaults: func(in *LaborRuleSetInput) {
		if in.OvertimeMultiplier == nil {
			in.OvertimeMultiplier = crud.Ptr(decimal.RequireFromString("1.5"))
		}
	},
}

// rules used when no rule set is assigned.
var defaultRules = LaborRuleSet{Name: "Default", WeeklyOvertimeMinutes: crud.Ptr(2400), OvertimeMultiplier: decimal.RequireFromString("1.5")}

// RulesFor returns the labor rules for an employee (their home location's
// rule set, else the first rule set, else built-in defaults).
func RulesFor(ctx context.Context, q db.Querier, employeeID string) (LaborRuleSet, error) {
	var id *string
	err := q.QueryRow(ctx, `
		SELECT coalesce(l.labor_rule_set_id, (SELECT id FROM labor_rule_sets ORDER BY created_at LIMIT 1))
		FROM employees e LEFT JOIN locations l ON l.id = e.home_location_id WHERE e.id = $1`, employeeID).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return defaultRules, err
	}
	if id == nil {
		id = new(string)
		if err := q.QueryRow(ctx, `SELECT id FROM labor_rule_sets ORDER BY created_at LIMIT 1`).Scan(id); err != nil {
			return defaultRules, nil
		}
	}
	return laborRules.Get(ctx, q, "id", *id, false)
}

// ---------------------------------------------------------------------------
// Pay periods
// ---------------------------------------------------------------------------

type PayPeriod struct {
	ID        string     `json:"id"`
	StartDate httpx.Date `json:"startDate"`
	EndDate   httpx.Date `json:"endDate"`
	Status    string     `json:"status"`
	LockedAt  *time.Time `json:"lockedAt"`
	CreatedAt time.Time  `json:"createdAt"`
}

type PayPeriodInput struct {
	StartDate httpx.Date  `json:"startDate" validate:"required"`
	EndDate   *httpx.Date `json:"endDate,omitempty" doc:"Defaults to the company's pay period length"`
}

type LockInput struct {
	Force bool `json:"force,omitempty" doc:"Lock even if some timesheets aren't approved"`
}

func scanPeriod(r pgx.Row) (PayPeriod, error) {
	var p PayPeriod
	var s, e time.Time
	err := r.Scan(&p.ID, &s, &e, &p.Status, &p.LockedAt, &p.CreatedAt)
	p.StartDate, p.EndDate = httpx.NewDate(s), httpx.NewDate(e)
	return p, err
}

const periodCols = `id, start_date, end_date, status, locked_at, created_at`

func getPeriod(ctx context.Context, q db.Querier, id string, lock bool) (PayPeriod, error) {
	sql := `SELECT ` + periodCols + ` FROM pay_periods WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	p, err := scanPeriod(q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, httpx.NotFound("Pay period not found.")
	}
	return p, err
}

// ---------------------------------------------------------------------------
// Timesheets
// ---------------------------------------------------------------------------

type ShiftWorked struct {
	In           time.Time  `json:"in"`
	Out          *time.Time `json:"out"`
	BreakMinutes int        `json:"breakMinutes"`
	Minutes      int        `json:"minutes"`
}

type Day struct {
	Date            string        `json:"date"`
	WorkedMinutes   int           `json:"workedMinutes"`
	BreakMinutes    int           `json:"breakMinutes"`
	OvertimeMinutes int           `json:"overtimeMinutes"`
	Shifts          []ShiftWorked `json:"shifts"`
}

type Exception struct {
	Date    string `json:"date"`
	Code    string `json:"code" doc:"missing_clock_out, missing_clock_in, missing_break, overtime"`
	Message string `json:"message"`
}

type Timesheet struct {
	ID              string      `json:"id"`
	EmployeeID      string      `json:"employeeId"`
	PayPeriodID     string      `json:"payPeriodId"`
	Status          string      `json:"status" doc:"open, approved, rejected or locked"`
	WorkedMinutes   int         `json:"workedMinutes"`
	RegularMinutes  int         `json:"regularMinutes"`
	OvertimeMinutes int         `json:"overtimeMinutes"`
	BreakMinutes    int         `json:"breakMinutes"`
	Days            []Day       `json:"days"`
	Exceptions      []Exception `json:"exceptions"`
	Note            *string     `json:"note"`
	ApprovedAt      *time.Time  `json:"approvedAt"`
	Version         int         `json:"version"`
	CreatedAt       time.Time   `json:"createdAt"`
	UpdatedAt       time.Time   `json:"updatedAt"`
}

const tsCols = `id, employee_id, pay_period_id, status, worked_minutes, regular_minutes, overtime_minutes, break_minutes,
	days, exceptions, note, approved_at, version, created_at, updated_at`

func scanTimesheet(r pgx.Row) (Timesheet, error) {
	var t Timesheet
	err := r.Scan(&t.ID, &t.EmployeeID, &t.PayPeriodID, &t.Status, &t.WorkedMinutes, &t.RegularMinutes, &t.OvertimeMinutes,
		&t.BreakMinutes, &t.Days, &t.Exceptions, &t.Note, &t.ApprovedAt, &t.Version, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

type BuildInput struct {
	PayPeriodID string `json:"payPeriodId" validate:"required"`
}

type BuildResult struct {
	PayPeriodID string `json:"payPeriodId"`
	Built       int    `json:"built"`
	Skipped     int    `json:"skipped" doc:"Timesheets already approved or locked"`
}

type DecisionInput struct {
	Note string `json:"note,omitempty" validate:"max=500"`
}

type punchRow struct {
	id, typ string
	at      time.Time
}

func companyTZ(ctx context.Context, q db.Querier) *time.Location {
	var tz string
	if err := q.QueryRow(ctx, `SELECT timezone FROM company LIMIT 1`).Scan(&tz); err != nil {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

func roundTo(t time.Time, minutes int) time.Time {
	if minutes <= 1 {
		return t.Truncate(time.Minute)
	}
	return t.Round(time.Duration(minutes) * time.Minute)
}

// compute builds the days, exceptions and totals for one employee's punches.
func compute(punches []punchRow, rules LaborRuleSet, tz *time.Location, periodStart time.Time) (days []Day, exc []Exception, worked, regular, overtime, breaks int) {
	byDate := map[string]*Day{}
	day := func(t time.Time) *Day {
		k := t.In(tz).Format("2006-01-02")
		if byDate[k] == nil {
			byDate[k] = &Day{Date: k, Shifts: []ShiftWorked{}}
		}
		return byDate[k]
	}
	var open *ShiftWorked
	var breakStart *time.Time
	closeShift := func(out time.Time) {
		out = roundTo(out, rules.RoundingMinutes)
		open.Out = &out
		mins := int(out.Sub(open.In).Minutes()) - open.BreakMinutes
		if mins < 0 {
			mins = 0
		}
		open.Minutes = mins
		d := day(open.In)
		d.Shifts = append(d.Shifts, *open)
		d.WorkedMinutes += mins
		d.BreakMinutes += open.BreakMinutes
		if rules.BreakRequiredAfterMinutes != nil && open.BreakMinutes == 0 && mins > *rules.BreakRequiredAfterMinutes {
			exc = append(exc, Exception{Date: d.Date, Code: "missing_break",
				Message: fmt.Sprintf("Worked %d minutes without a recorded break", mins)})
		}
		open, breakStart = nil, nil
	}
	for _, p := range punches {
		switch p.typ {
		case "in":
			if open != nil {
				exc = append(exc, Exception{Date: day(open.In).Date, Code: "missing_clock_out", Message: "Clocked in again without clocking out"})
				open = nil
			}
			in := roundTo(p.at, rules.RoundingMinutes)
			open = &ShiftWorked{In: in}
		case "break_start":
			if open != nil {
				t := p.at
				breakStart = &t
			}
		case "break_end":
			if open != nil && breakStart != nil {
				open.BreakMinutes += int(p.at.Sub(*breakStart).Minutes())
				breakStart = nil
			}
		case "out":
			if open == nil {
				exc = append(exc, Exception{Date: p.at.In(tz).Format("2006-01-02"), Code: "missing_clock_in", Message: "Clocked out without clocking in"})
				continue
			}
			if breakStart != nil {
				open.BreakMinutes += int(p.at.Sub(*breakStart).Minutes())
			}
			closeShift(p.at)
		}
	}
	if open != nil {
		exc = append(exc, Exception{Date: day(open.In).Date, Code: "missing_clock_out", Message: "No clock-out recorded"})
	}

	keys := make([]string, 0, len(byDate))
	for k := range byDate {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	weekly := map[int]int{}
	for _, k := range keys {
		d := byDate[k]
		dailyOT := 0
		if rules.DailyOvertimeMinutes != nil && d.WorkedMinutes > *rules.DailyOvertimeMinutes {
			dailyOT = d.WorkedMinutes - *rules.DailyOvertimeMinutes
		}
		date, _ := time.ParseInLocation("2006-01-02", k, tz)
		week := int(date.Sub(periodStart).Hours() / (24 * 7))
		reg := d.WorkedMinutes - dailyOT
		weeklyOT := 0
		if rules.WeeklyOvertimeMinutes != nil && weekly[week]+reg > *rules.WeeklyOvertimeMinutes {
			weeklyOT = min(reg, weekly[week]+reg-*rules.WeeklyOvertimeMinutes)
		}
		weekly[week] += reg - weeklyOT
		d.OvertimeMinutes = dailyOT + weeklyOT
		if d.OvertimeMinutes > 0 {
			exc = append(exc, Exception{Date: k, Code: "overtime", Message: fmt.Sprintf("%d overtime minutes", d.OvertimeMinutes)})
		}
		worked += d.WorkedMinutes
		overtime += d.OvertimeMinutes
		breaks += d.BreakMinutes
		days = append(days, *d)
	}
	if days == nil {
		days = []Day{}
	}
	if exc == nil {
		exc = []Exception{}
	}
	sort.SliceStable(exc, func(i, j int) bool { return exc[i].Date < exc[j].Date })
	return days, exc, worked, worked - overtime, overtime, breaks
}

func buildTimesheets(c *httpx.Ctx, tx pgx.Tx, period PayPeriod) (BuildResult, error) {
	res := BuildResult{PayPeriodID: period.ID}
	tz := companyTZ(c, tx)
	start := time.Date(period.StartDate.Year(), period.StartDate.Month(), period.StartDate.Day(), 0, 0, 0, 0, tz)
	end := time.Date(period.EndDate.Year(), period.EndDate.Month(), period.EndDate.Day(), 0, 0, 0, 0, tz).AddDate(0, 0, 1)
	rows, err := tx.Query(c, `SELECT employee_id, id, type, at FROM punches WHERE at >= $1 AND at < $2 ORDER BY employee_id, at, id`, start, end)
	if err != nil {
		return res, err
	}
	byEmp := map[string][]punchRow{}
	var order []string
	for rows.Next() {
		var emp string
		var p punchRow
		if err := rows.Scan(&emp, &p.id, &p.typ, &p.at); err != nil {
			rows.Close()
			return res, err
		}
		if byEmp[emp] == nil {
			order = append(order, emp)
		}
		byEmp[emp] = append(byEmp[emp], p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	for _, emp := range order {
		var status string
		err := tx.QueryRow(c, `SELECT status FROM timesheets WHERE employee_id=$1 AND pay_period_id=$2`, emp, period.ID).Scan(&status)
		if err == nil && (status == "approved" || status == "locked") {
			res.Skipped++
			continue
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return res, err
		}
		rules, err := RulesFor(c, tx, emp)
		if err != nil {
			return res, err
		}
		days, exc, worked, regular, ot, breaks := compute(byEmp[emp], rules, tz, start)
		if _, err := tx.Exec(c, `
			INSERT INTO timesheets (id, employee_id, pay_period_id, worked_minutes, regular_minutes, overtime_minutes, break_minutes, days, exceptions)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (employee_id, pay_period_id) DO UPDATE SET status='open', worked_minutes=EXCLUDED.worked_minutes,
				regular_minutes=EXCLUDED.regular_minutes, overtime_minutes=EXCLUDED.overtime_minutes,
				break_minutes=EXCLUDED.break_minutes, days=EXCLUDED.days, exceptions=EXCLUDED.exceptions,
				version=timesheets.version+1, updated_at=now()`,
			ids.New(ids.Timesheet), emp, period.ID, worked, regular, ot, breaks, days, exc); err != nil {
			return res, err
		}
		res.Built++
	}
	return res, c.Record(tx, httpx.Change{Action: "timesheets.build", EntityType: "pay_period", EntityID: period.ID, After: res})
}

func getTimesheet(ctx context.Context, q db.Querier, id string, lock bool) (Timesheet, error) {
	sql := `SELECT ` + tsCols + ` FROM timesheets WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	t, err := scanTimesheet(q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, httpx.NotFound("Timesheet not found.")
	}
	return t, err
}

func decide(status, event string) func(c *httpx.Ctx) (any, error) {
	return func(c *httpx.Ctx) (any, error) {
		var in DecisionInput
		if err := c.DecodeOptional(&in); err != nil {
			return nil, err
		}
		var out Timesheet
		err := c.InTx(func(tx pgx.Tx) error {
			before, err := getTimesheet(c, tx, c.Param("id"), true)
			if err != nil {
				return err
			}
			if before.Status == "locked" {
				return httpx.PeriodLocked("This timesheet's pay period is locked.")
			}
			approvedAt := "NULL"
			if status == "approved" {
				approvedAt = "now()"
			}
			out, err = scanTimesheet(tx.QueryRow(c, `UPDATE timesheets SET status=$2, note=nullif($3,''), approved_at=`+approvedAt+`,
				version=version+1, updated_at=now() WHERE id=$1 RETURNING `+tsCols, before.ID, status, in.Note))
			if err != nil {
				return err
			}
			return c.Record(tx, httpx.Change{Action: "timesheet." + status, EventType: event, Feature: feature,
				EntityType: "timesheet", EntityID: out.ID, Before: before, After: out})
		})
		return out, err
	}
}

// ExportRow is one employee's line in a payroll export.
type ExportRow struct {
	EmployeeID     string           `json:"employeeId"`
	ExternalID     *string          `json:"externalId"`
	EmployeeNumber *string          `json:"employeeNumber"`
	Name           string           `json:"name"`
	TimesheetID    string           `json:"timesheetId"`
	Status         string           `json:"status"`
	RegularHours   decimal.Decimal  `json:"regularHours"`
	OvertimeHours  decimal.Decimal  `json:"overtimeHours"`
	HourlyRate     *decimal.Decimal `json:"hourlyRate"`
	Currency       string           `json:"currency"`
	EstimatedGross *decimal.Decimal `json:"estimatedGross" doc:"Before taxes and deductions"`
}

type exportResult struct {
	PayPeriod PayPeriod   `json:"payPeriod"`
	Rows      []ExportRow `json:"rows"`
}

func exportPeriod(c *httpx.Ctx) (any, error) {
	period, err := getPeriod(c, c.App.Pool, c.Param("id"), false)
	if err != nil {
		return nil, err
	}
	rows, err := c.App.Pool.Query(c, `
		SELECT e.id, e.external_id, e.employee_number, e.first_name || ' ' || e.last_name, t.id, t.status,
		       t.regular_minutes, t.overtime_minutes
		FROM timesheets t JOIN employees e ON e.id = t.employee_id
		WHERE t.pay_period_id = $1 ORDER BY e.last_name, e.first_name`, period.ID)
	if err != nil {
		return nil, err
	}
	type raw struct {
		row      ExportRow
		reg, ovt int
	}
	var raws []raw
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.row.EmployeeID, &r.row.ExternalID, &r.row.EmployeeNumber, &r.row.Name, &r.row.TimesheetID,
			&r.row.Status, &r.reg, &r.ovt); err != nil {
			rows.Close()
			return nil, err
		}
		raws = append(raws, r)
	}
	rows.Close()
	out := exportResult{PayPeriod: period, Rows: []ExportRow{}}
	sixty := decimal.NewFromInt(60)
	for _, r := range raws {
		row := r.row
		row.RegularHours = decimal.NewFromInt(int64(r.reg)).Div(sixty).Round(2)
		row.OvertimeHours = decimal.NewFromInt(int64(r.ovt)).Div(sixty).Round(2)
		rate, cur, err := people.CurrentRate(c, c.App.Pool, row.EmployeeID, period.EndDate.Time)
		if err != nil {
			return nil, err
		}
		if rate != nil {
			rules, err := RulesFor(c, c.App.Pool, row.EmployeeID)
			if err != nil {
				return nil, err
			}
			rr := rate.Round(4)
			row.HourlyRate, row.Currency = &rr, cur
			gross := row.RegularHours.Mul(*rate).Add(row.OvertimeHours.Mul(*rate).Mul(rules.OvertimeMultiplier)).Round(2)
			row.EstimatedGross = &gross
		}
		out.Rows = append(out.Rows, row)
	}
	if c.Query("format") == "csv" {
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)
		_ = w.Write([]string{"employee_id", "external_id", "employee_number", "name", "status", "regular_hours", "overtime_hours", "hourly_rate", "currency", "estimated_gross"})
		for _, r := range out.Rows {
			rate, gross := "", ""
			if r.HourlyRate != nil {
				rate, gross = r.HourlyRate.String(), r.EstimatedGross.StringFixed(2)
			}
			_ = w.Write([]string{r.EmployeeID, deref(r.ExternalID), deref(r.EmployeeNumber), r.Name, r.Status,
				r.RegularHours.StringFixed(2), r.OvertimeHours.StringFixed(2), rate, r.Currency, gross})
		}
		w.Flush()
		return httpx.Raw{ContentType: "text/csv; charset=utf-8", Filename: "payroll-" + period.StartDate.String() + ".csv", Body: buf.Bytes()}, nil
	}
	return out, nil
}

type periodList struct {
	Data []PayPeriod `json:"data"`
}

type timesheetList struct {
	httpx.ListParams
	PayPeriodID string `json:"payPeriodId,omitempty"`
	EmployeeID  string `json:"employeeId,omitempty"`
	Status      string `json:"status,omitempty"`
}

// TimesheetRoutes are the timesheet, pay period and labor rule routes.
func TimesheetRoutes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "POST", Path: "/pay-periods", Tag: "Time & Attendance", Feature: feature, Scope: "payroll:write",
			Summary: "Create a pay period", Body: PayPeriodInput{}, Response: PayPeriod{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in PayPeriodInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out PayPeriod
				err := c.InTx(func(tx pgx.Tx) error {
					end := in.EndDate
					if end == nil {
						var days int
						if err := tx.QueryRow(c, `SELECT pay_period_length_days FROM company LIMIT 1`).Scan(&days); err != nil {
							return err
						}
						d := httpx.NewDate(in.StartDate.AddDate(0, 0, days-1))
						end = &d
					}
					if end.Before(in.StartDate.Time) {
						return httpx.Validation(httpx.FieldError{Path: "endDate", Message: "Must not be before startDate"})
					}
					var overlap bool
					if err := tx.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM pay_periods WHERE start_date <= $2 AND end_date >= $1)`,
						in.StartDate, *end).Scan(&overlap); err != nil {
						return err
					}
					if overlap {
						return httpx.Conflict("This pay period overlaps an existing one.")
					}
					var err error
					out, err = scanPeriod(tx.QueryRow(c, `INSERT INTO pay_periods (id, start_date, end_date) VALUES ($1,$2,$3) RETURNING `+periodCols,
						ids.New(ids.PayPeriod), in.StartDate, *end))
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "pay_period.create", EntityType: "pay_period", EntityID: out.ID, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/pay-periods", Tag: "Time & Attendance", Feature: feature, Scope: "payroll:read",
			Summary: "List pay periods (newest first)", Response: periodList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT `+periodCols+` FROM pay_periods ORDER BY start_date DESC LIMIT 200`)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PayPeriod, error) { return scanPeriod(r) })
				if list == nil {
					list = []PayPeriod{}
				}
				return periodList{Data: list}, err
			},
		},
		{
			Method: "GET", Path: "/pay-periods/{id}", Tag: "Time & Attendance", Feature: feature, Scope: "payroll:read",
			Summary: "Get a pay period", Response: PayPeriod{},
			Handler: func(c *httpx.Ctx) (any, error) { return getPeriod(c, c.App.Pool, c.Param("id"), false) },
		},
		{
			Method: "POST", Path: "/pay-periods/{id}:lock", Tag: "Time & Attendance", Feature: feature, Scope: "payroll:write",
			Summary:     "Lock a pay period (final payroll approval)",
			Description: "All timesheets must be approved unless force is set. Locked timesheets can't change.",
			Body:        LockInput{}, Response: PayPeriod{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in LockInput
				if err := c.DecodeOptional(&in); err != nil {
					return nil, err
				}
				var out PayPeriod
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := getPeriod(c, tx, c.Param("id"), true)
					if err != nil {
						return err
					}
					if before.Status == "locked" {
						return httpx.PeriodLocked("This pay period is already locked.")
					}
					var pending int
					if err := tx.QueryRow(c, `SELECT count(*) FROM timesheets WHERE pay_period_id=$1 AND status <> 'approved'`, before.ID).Scan(&pending); err != nil {
						return err
					}
					if pending > 0 && !in.Force {
						return httpx.Conflict(fmt.Sprintf("%d timesheet(s) aren't approved yet. Approve them or lock with force.", pending))
					}
					if _, err := tx.Exec(c, `UPDATE timesheets SET status='locked', updated_at=now() WHERE pay_period_id=$1`, before.ID); err != nil {
						return err
					}
					out, err = scanPeriod(tx.QueryRow(c, `UPDATE pay_periods SET status='locked', locked_at=now(), updated_at=now()
						WHERE id=$1 RETURNING `+periodCols, before.ID))
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "pay_period.lock", EventType: "pay_period.locked", Feature: feature,
						EntityType: "pay_period", EntityID: out.ID, Before: before, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/pay-periods/{id}/export", Tag: "Time & Attendance", Feature: "time.payroll_export", Scope: "payroll:read",
			Summary:     "Export hours for payroll",
			Description: "JSON by default; add ?format=csv for a CSV file. Estimated gross uses each employee's pay rate and the overtime multiplier.",
			Response:    exportResult{}, Handler: exportPeriod,
		},
		{
			Method: "POST", Path: "/timesheets:build", Tag: "Time & Attendance", Feature: feature, Scope: "time:write",
			Summary:     "Build or refresh timesheets from punches",
			Description: "Creates a timesheet for every employee with punches in the pay period. Approved and locked timesheets are left alone.",
			Body:        BuildInput{}, Response: BuildResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in BuildInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out BuildResult
				err := c.InTx(func(tx pgx.Tx) error {
					period, err := getPeriod(c, tx, in.PayPeriodID, true)
					if err != nil {
						return err
					}
					if period.Status == "locked" {
						return httpx.PeriodLocked("This pay period is locked.")
					}
					out, err = buildTimesheets(c, tx, period)
					return err
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/timesheets", Tag: "Time & Attendance", Feature: feature, Scope: "time:read",
			Summary: "List timesheets", Query: timesheetList{}, Response: httpx.Page[Timesheet]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `SELECT `+tsCols+` FROM timesheets
					WHERE id > $1 AND ($2 = '' OR pay_period_id = $2) AND ($3 = '' OR employee_id = $3) AND ($4 = '' OR status = $4)
					ORDER BY id LIMIT $5`, lp.AfterID, c.Query("payPeriodId"), c.Query("employeeId"), c.Query("status"), lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Timesheet, error) { return scanTimesheet(r) })
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(t Timesheet) string { return t.ID }), nil
			},
		},
		{
			Method: "GET", Path: "/timesheets/{id}", Tag: "Time & Attendance", Feature: feature, Scope: "time:read",
			Summary: "Get a timesheet with daily detail and exceptions", Response: Timesheet{},
			Handler: func(c *httpx.Ctx) (any, error) { return getTimesheet(c, c.App.Pool, c.Param("id"), false) },
		},
		{
			Method: "POST", Path: "/timesheets/{id}:approve", Tag: "Time & Attendance", Feature: feature, Scope: "time:write",
			Summary: "Approve a timesheet (manager step)", Body: DecisionInput{}, Response: Timesheet{},
			Handler: decide("approved", "timesheet.approved"),
		},
		{
			Method: "POST", Path: "/timesheets/{id}:reject", Tag: "Time & Attendance", Feature: feature, Scope: "time:write",
			Summary: "Reject a timesheet", Body: DecisionInput{}, Response: Timesheet{},
			Handler: decide("rejected", "timesheet.rejected"),
		},
	}
	return append(routes, laborRules.Routes()...)
}
