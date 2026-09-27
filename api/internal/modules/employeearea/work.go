package employeearea

import (
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/modules/people"
	"github.com/selectdev/purros/api/internal/modules/timeclock"
	"github.com/shopspring/decimal"
)

// pass copies the caller's query parameters (paging, date ranges) and pins
// employeeId to their own record.
func pass(c *httpx.Ctx, emp string, allowed ...string) url.Values {
	q := url.Values{"employeeId": {emp}}
	for _, k := range append(allowed, "limit", "cursor") {
		if v := c.Query(k); v != "" {
			q.Set(k, v)
		}
	}
	return q
}

// body decodes the caller's JSON body into a map (for delegation), so the
// employee can be pinned.
func body(c *httpx.Ctx) (map[string]any, error) {
	m := map[string]any{}
	if len(c.RawBody()) == 0 {
		return m, nil
	}
	var in any
	if err := c.Decode(&in); err != nil {
		return nil, err
	}
	obj, ok := in.(map[string]any)
	if !ok {
		return nil, httpx.BadRequest("The body must be a JSON object.")
	}
	return obj, nil
}

// owns checks that a record belongs to the employee.
func owns(c *httpx.Ctx, q db.Querier, table, id, emp, noun string) error {
	var ok bool
	if err := q.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1 AND employee_id = $2)`, id, emp).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return httpx.NotFound(noun + " not found.")
	}
	return nil
}

type PayPeriodEstimate struct {
	PayPeriodID     string           `json:"payPeriodId"`
	StartDate       httpx.Date       `json:"startDate"`
	EndDate         httpx.Date       `json:"endDate"`
	TimesheetStatus string           `json:"timesheetStatus"`
	RegularHours    decimal.Decimal  `json:"regularHours"`
	OvertimeHours   decimal.Decimal  `json:"overtimeHours"`
	HourlyRate      *decimal.Decimal `json:"hourlyRate"`
	EstimatedGross  *decimal.Decimal `json:"estimatedGross" doc:"Before taxes and deductions; a guide only"`
	Currency        string           `json:"currency"`
}

type RateEntry struct {
	PayType       string          `json:"payType"`
	Rate          decimal.Decimal `json:"rate"`
	Currency      string          `json:"currency"`
	EffectiveFrom httpx.Date      `json:"effectiveFrom"`
}

type MyPay struct {
	Rates     []RateEntry         `json:"rates" doc:"Newest first"`
	Estimates []PayPeriodEstimate `json:"estimates" doc:"Latest 12 pay periods with a timesheet"`
}

func payEstimates(c *httpx.Ctx, emp string) (MyPay, error) {
	out := MyPay{Rates: []RateEntry{}, Estimates: []PayPeriodEstimate{}}
	rows, err := c.App.Pool.Query(c, `SELECT pay_type, rate, currency, effective_from FROM pay_rates
		WHERE employee_id = $1 ORDER BY effective_from DESC`, emp)
	if err != nil {
		return out, err
	}
	rates, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RateEntry, error) {
		var x RateEntry
		return x, r.Scan(&x.PayType, &x.Rate, &x.Currency, &x.EffectiveFrom)
	})
	if err != nil {
		return out, err
	}
	if rates != nil {
		out.Rates = rates
	}
	rows, err = c.App.Pool.Query(c, `SELECT p.id, p.start_date, p.end_date, t.status, t.regular_minutes, t.overtime_minutes
		FROM timesheets t JOIN pay_periods p ON p.id = t.pay_period_id
		WHERE t.employee_id = $1 ORDER BY p.start_date DESC LIMIT 12`, emp)
	if err != nil {
		return out, err
	}
	type raw struct {
		e        PayPeriodEstimate
		reg, ovt int
	}
	raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (raw, error) {
		var x raw
		return x, r.Scan(&x.e.PayPeriodID, &x.e.StartDate, &x.e.EndDate, &x.e.TimesheetStatus, &x.reg, &x.ovt)
	})
	if err != nil {
		return out, err
	}
	rules, err := timeclock.RulesFor(c, c.App.Pool, emp)
	if err != nil {
		return out, err
	}
	sixty := decimal.NewFromInt(60)
	for _, x := range raws {
		e := x.e
		e.RegularHours = decimal.NewFromInt(int64(x.reg)).Div(sixty).Round(2)
		e.OvertimeHours = decimal.NewFromInt(int64(x.ovt)).Div(sixty).Round(2)
		rate, cur, err := people.CurrentRate(c, c.App.Pool, emp, e.EndDate.Time)
		if err != nil {
			return out, err
		}
		if rate != nil {
			r := rate.Round(4)
			gross := e.RegularHours.Mul(*rate).Add(e.OvertimeHours.Mul(*rate).Mul(rules.OvertimeMultiplier)).Round(2)
			e.HourlyRate, e.EstimatedGross, e.Currency = &r, &gross, cur
		}
		out.Estimates = append(out.Estimates, e)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Punch corrections
// ---------------------------------------------------------------------------

type Correction struct {
	ID             string     `json:"id"`
	EmployeeID     string     `json:"employeeId"`
	PunchID        *string    `json:"punchId" doc:"The punch being replaced; null for a missed punch"`
	Type           string     `json:"type"`
	At             time.Time  `json:"at"`
	LocationID     *string    `json:"locationId"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status" doc:"pending, approved, rejected or cancelled"`
	DecisionNote   *string    `json:"decisionNote"`
	DecidedAt      *time.Time `json:"decidedAt"`
	CreatedPunchID *string    `json:"createdPunchId"`
	CreatedAt      time.Time  `json:"createdAt"`
}

type CorrectionInput struct {
	PunchID    string    `json:"punchId,omitempty" doc:"Set to replace a wrong punch; leave out for a missed punch"`
	Type       string    `json:"type" validate:"required,oneof=in out break_start break_end"`
	At         time.Time `json:"at" validate:"required"`
	LocationID string    `json:"locationId,omitempty"`
	Reason     string    `json:"reason" validate:"required,max=500"`
}

type DecisionInput struct {
	Note string `json:"note,omitempty" validate:"max=500"`
}

const corrCols = `id, employee_id, punch_id, type, at, location_id, reason, status, decision_note, decided_at, created_punch_id, created_at`

func scanCorrection(r pgx.Row) (Correction, error) {
	var x Correction
	err := r.Scan(&x.ID, &x.EmployeeID, &x.PunchID, &x.Type, &x.At, &x.LocationID, &x.Reason, &x.Status, &x.DecisionNote,
		&x.DecidedAt, &x.CreatedPunchID, &x.CreatedAt)
	return x, err
}

func getCorrection(c *httpx.Ctx, q db.Querier, id string, lock bool) (Correction, error) {
	sql := `SELECT ` + corrCols + ` FROM punch_corrections WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	x, err := scanCorrection(q.QueryRow(c, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return x, httpx.NotFound("Punch correction not found.")
	}
	return x, err
}

func listCorrections(c *httpx.Ctx, emp, status string) (list[Correction], error) {
	rows, err := c.App.Pool.Query(c, `SELECT `+corrCols+` FROM punch_corrections
		WHERE ($1 = '' OR employee_id = $1) AND ($2 = '' OR status = $2) ORDER BY created_at DESC LIMIT 200`, emp, status)
	return collect(rows, err, func(r pgx.CollectableRow) (Correction, error) { return scanCorrection(r) })
}

// periodLocked reports whether a locked pay period covers t for the employee.
func periodLocked(c *httpx.Ctx, q db.Querier, t time.Time) (bool, error) {
	var locked bool
	err := q.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM pay_periods WHERE status = 'locked'
		AND $1::timestamptz >= start_date::timestamptz - interval '1 day' AND $1::timestamptz < end_date::timestamptz + interval '2 days')`, t).Scan(&locked)
	return locked, err
}

func requestCorrection(c *httpx.Ctx, emp string) (any, error) {
	var in CorrectionInput
	if err := c.Decode(&in); err != nil {
		return nil, err
	}
	if in.At.After(c.App.Now().Add(time.Hour)) {
		return nil, httpx.Validation(httpx.FieldError{Path: "at", Message: "Can't be in the future"})
	}
	var out Correction
	err := c.InTx(func(tx pgx.Tx) error {
		var punchID, locID *string
		if in.PunchID != "" {
			var voided *time.Time
			err := tx.QueryRow(c, `SELECT voided_at FROM punches WHERE id = $1 AND employee_id = $2`, in.PunchID, emp).Scan(&voided)
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.Validation(httpx.FieldError{Path: "punchId", Message: "Unknown punch"})
			}
			if err != nil {
				return err
			}
			if voided != nil {
				return httpx.Validation(httpx.FieldError{Path: "punchId", Message: "This punch was already replaced"})
			}
			punchID = &in.PunchID
		}
		if in.LocationID != "" {
			var ok bool
			if err := tx.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM locations WHERE id = $1)`, in.LocationID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return httpx.Validation(httpx.FieldError{Path: "locationId", Message: "Unknown location"})
			}
			locID = &in.LocationID
		}
		if locked, err := periodLocked(c, tx, in.At); err != nil {
			return err
		} else if locked {
			return httpx.PeriodLocked("This pay period is locked. Ask your manager or payroll.")
		}
		var err error
		out, err = scanCorrection(tx.QueryRow(c, `INSERT INTO punch_corrections (id, employee_id, punch_id, type, at, location_id, reason)
			VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+corrCols, ids.New(ids.PunchCorrection), emp, punchID, in.Type, in.At, locID, in.Reason))
		if err != nil {
			return err
		}
		return c.Record(tx, httpx.Change{Action: "punch_correction.request", EntityType: "employee", EntityID: emp, After: out})
	})
	return out, err
}

// decideCorrection approves (adding the punch and voiding the one it
// replaces) or rejects a pending correction.
func decideCorrection(approve bool) func(c *httpx.Ctx) (any, error) {
	return func(c *httpx.Ctx) (any, error) {
		var in DecisionInput
		if err := c.DecodeOptional(&in); err != nil {
			return nil, err
		}
		var out Correction
		err := c.InTx(func(tx pgx.Tx) error {
			x, err := getCorrection(c, tx, c.Param("id"), true)
			if err != nil {
				return err
			}
			if err := c.CheckEmployeeReach(x.EmployeeID); err != nil {
				return err
			}
			if x.Status != "pending" {
				return httpx.Conflict("This correction was already " + x.Status + ".")
			}
			if c.Principal.User != nil && c.Principal.User.EmployeeID == x.EmployeeID {
				return httpx.Forbidden("You can't decide your own punch correction.")
			}
			status, note := "rejected", nilIfEmpty(in.Note)
			var created *string
			if approve {
				status = "approved"
				if locked, err := periodLocked(c, tx, x.At); err != nil {
					return err
				} else if locked {
					return httpx.PeriodLocked("This pay period is locked.")
				}
				id := ids.New(ids.Punch)
				if _, err := tx.Exec(c, `INSERT INTO punches (id, employee_id, type, at, location_id, device_id, source, external_id)
					VALUES ($1, $2, $3, $4, $5, 'correction:' || $6, 'correction', $6)`, id, x.EmployeeID, x.Type, x.At, x.LocationID, x.ID); err != nil {
					if db.IsUniqueViolation(err) {
						return httpx.Conflict("An identical punch already exists.")
					}
					return err
				}
				created = &id
				if x.PunchID != nil {
					if _, err := tx.Exec(c, `UPDATE punches SET voided_at = now(), voided_by = $2 WHERE id = $1`, *x.PunchID, x.ID); err != nil {
						return err
					}
				}
			}
			if _, err := tx.Exec(c, `UPDATE punch_corrections SET status = $2, decision_note = $3, decided_by = $4, decided_at = now(),
				created_punch_id = $5, updated_at = now() WHERE id = $1`, x.ID, status, note, c.Actor().ID, created); err != nil {
				return err
			}
			if out, err = getCorrection(c, tx, x.ID, false); err != nil {
				return err
			}
			ch := httpx.Change{Action: "punch_correction." + status, EntityType: "employee", EntityID: x.EmployeeID, Before: x, After: out}
			if approve {
				ch.EventType, ch.Feature = "punch.corrected", "time"
				if x.LocationID != nil {
					ch.LocationID = *x.LocationID
				}
			}
			return c.Record(tx, ch)
		})
		return out, err
	}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// correctionTarget tells the reach check whose correction a request is about.
func correctionTarget(c *httpx.Ctx) (httpx.ReachTarget, error) {
	var emp string
	err := c.App.Pool.QueryRow(c, `SELECT employee_id FROM punch_corrections WHERE id = $1`, c.Param("id")).Scan(&emp)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ReachTarget{}, httpx.NotFound("Punch correction not found.")
	}
	return httpx.ReachTarget{EmployeeID: emp}, err
}

type correctionQuery struct {
	EmployeeID string `json:"employeeId,omitempty"`
	Status     string `json:"status,omitempty" doc:"pending, approved, rejected or cancelled"`
}

// CorrectionRoutes are the manager side of punch corrections.
func CorrectionRoutes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "GET", Path: "/punch-corrections", Tag: "Time & Attendance", Feature: "time", Scope: "time:read", Permission: "punches.correct",
			Summary: "List punch corrections", Query: correctionQuery{}, Response: list[Correction]{},
			Handler: func(c *httpx.Ctx) (any, error) { return listCorrections(c, c.Query("employeeId"), c.Query("status")) },
		},
		{
			Method: "POST", Path: "/punch-corrections/{id}:approve", Tag: "Time & Attendance", Feature: "time", Scope: "time:write",
			Permission: "punches.correct", ReachOf: correctionTarget,
			Summary:     "Approve a punch correction",
			Description: "Adds the corrected punch and keeps the one it replaces, marked void. Rebuild the timesheet to update totals.",
			Body:        DecisionInput{}, Response: Correction{},
			Handler: decideCorrection(true),
		},
		{
			Method: "POST", Path: "/punch-corrections/{id}:reject", Tag: "Time & Attendance", Feature: "time", Scope: "time:write",
			Permission: "punches.correct", ReachOf: correctionTarget,
			Summary: "Reject a punch correction", Body: DecisionInput{}, Response: Correction{},
			Handler: decideCorrection(false),
		},
	}
}

// ---------------------------------------------------------------------------
// Employee Area routes for time, schedule and pay
// ---------------------------------------------------------------------------

type SwapOffer struct {
	ShiftID      string `json:"shiftId"`
	ToEmployeeID string `json:"toEmployeeId"`
}

type timeQuery struct {
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

func workRoutes() []httpx.Route {
	r := func(route httpx.Route, query, bodyProto, resp any) httpx.Route {
		route.Query, route.Body, route.Response = query, bodyProto, resp
		return route
	}
	return []httpx.Route{
		// Time
		r(me("GET", "/punches", "Your punches (from/to are RFC 3339 times)", func(c *httpx.Ctx, emp string) (any, error) {
			return c.Delegate("GET", "/time/punches", nil, pass(c, emp, "from", "to", "includeVoided"), nil)
		}), timeQuery{}, nil, httpx.Page[timeclock.Punch]{}),
		r(me("GET", "/timesheets", "Your timesheets", func(c *httpx.Ctx, emp string) (any, error) {
			return c.Delegate("GET", "/timesheets", nil, pass(c, emp, "payPeriodId", "status"), nil)
		}), timeQuery{}, nil, nil),
		r(me("GET", "/punch-corrections", "Your punch correction requests", func(c *httpx.Ctx, emp string) (any, error) {
			return listCorrections(c, emp, c.Query("status"))
		}), nil, nil, list[Correction]{}),
		withStatus(r(me("POST", "/punch-corrections", "Ask your manager to fix a missed or wrong punch", requestCorrection),
			nil, CorrectionInput{}, Correction{}), 201),
		r(me("POST", "/punch-corrections/{id}:cancel", "Cancel a pending correction request", func(c *httpx.Ctx, emp string) (any, error) {
			var out Correction
			err := c.InTx(func(tx pgx.Tx) error {
				x, err := getCorrection(c, tx, c.Param("id"), true)
				if err != nil || x.EmployeeID != emp {
					return httpx.NotFound("Punch correction not found.")
				}
				if x.Status != "pending" {
					return httpx.Conflict("This correction was already " + x.Status + ".")
				}
				if _, err := tx.Exec(c, `UPDATE punch_corrections SET status = 'cancelled', updated_at = now() WHERE id = $1`, x.ID); err != nil {
					return err
				}
				out, err = getCorrection(c, tx, x.ID, false)
				return err
			})
			return out, err
		}), nil, nil, Correction{}),

		// Time off
		r(me("GET", "/time-off/balances", "Your time-off balances", func(c *httpx.Ctx, emp string) (any, error) {
			return c.Delegate("GET", "/time-off/balances", nil, pass(c, emp), nil)
		}), nil, nil, nil),
		r(me("GET", "/time-off/requests", "Your time-off requests", func(c *httpx.Ctx, emp string) (any, error) {
			return c.Delegate("GET", "/time-off/requests", nil, pass(c, emp, "status"), nil)
		}), nil, nil, nil),
		withStatus(r(me("POST", "/time-off/requests", "Request time off", func(c *httpx.Ctx, emp string) (any, error) {
			b, err := body(c)
			if err != nil {
				return nil, err
			}
			delete(b, "employeeExternalId")
			b["employeeId"] = emp
			return c.Delegate("POST", "/time-off/requests", nil, nil, b)
		}), nil, timeclock.TimeOffRequestInput{}, timeclock.TimeOffRequest{}), 201),
		r(me("POST", "/time-off/requests/{id}:cancel", "Cancel one of your time-off requests", func(c *httpx.Ctx, emp string) (any, error) {
			if err := owns(c, c.App.Pool, "time_off_requests", c.Param("id"), emp, "Time-off request"); err != nil {
				return nil, err
			}
			return c.Delegate("POST", "/time-off/requests/{id}:cancel", map[string]string{"id": c.Param("id")}, nil, nil)
		}), nil, nil, timeclock.TimeOffRequest{}),

		// Schedule
		r(me("GET", "/shifts", "Your published shifts (from/to are RFC 3339 times)", func(c *httpx.Ctx, emp string) (any, error) {
			q := pass(c, emp, "from", "to")
			q.Set("status", "published")
			return c.Delegate("GET", "/shifts", nil, q, nil)
		}), timeQuery{}, nil, nil),
		r(me("GET", "/open-shifts", "Published open shifts you can pick up", func(c *httpx.Ctx, emp string) (any, error) {
			if err := c.RequireFeature("scheduling.open_shifts"); err != nil {
				return nil, err
			}
			q := url.Values{"open": {"true"}, "status": {"published"}}
			for _, k := range []string{"from", "to", "limit", "cursor"} {
				if v := c.Query(k); v != "" {
					q.Set(k, v)
				}
			}
			var loc *string
			if err := c.App.Pool.QueryRow(c, `SELECT home_location_id FROM employees WHERE id = $1`, emp).Scan(&loc); err != nil {
				return nil, err
			}
			if loc == nil {
				return map[string]any{"data": []any{}, "nextCursor": nil}, nil
			}
			q.Set("locationId", *loc)
			return c.Delegate("GET", "/shifts", nil, q, nil)
		}), timeQuery{}, nil, nil),
		r(me("POST", "/shifts/{id}:claim", "Pick up an open shift at your location", func(c *httpx.Ctx, emp string) (any, error) {
			var ok bool
			if err := c.App.Pool.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM shifts s JOIN employees e ON e.id = $2
				WHERE s.id = $1 AND s.status = 'published' AND s.employee_id IS NULL AND s.location_id = e.home_location_id)`,
				c.Param("id"), emp).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpx.NotFound("Open shift not found.")
			}
			return c.Delegate("POST", "/shifts/{id}:claim", map[string]string{"id": c.Param("id")}, nil, map[string]any{"employeeId": emp})
		}), nil, nil, nil),
		r(me("GET", "/shift-swaps", "Swap requests you made or were offered", func(c *httpx.Ctx, emp string) (any, error) {
			if err := c.RequireFeature("scheduling.shift_swaps"); err != nil {
				return nil, err
			}
			rows, err := c.App.Pool.Query(c, `SELECT id, shift_id, from_employee_id, to_employee_id, status, created_at, decided_at
				FROM shift_swap_requests WHERE from_employee_id = $1 OR to_employee_id = $1 ORDER BY created_at DESC LIMIT 100`, emp)
			type swap struct {
				ID             string     `json:"id"`
				ShiftID        string     `json:"shiftId"`
				FromEmployeeID *string    `json:"fromEmployeeId"`
				ToEmployeeID   string     `json:"toEmployeeId"`
				Status         string     `json:"status"`
				CreatedAt      time.Time  `json:"createdAt"`
				DecidedAt      *time.Time `json:"decidedAt"`
			}
			return collect(rows, err, func(r pgx.CollectableRow) (swap, error) {
				var s swap
				return s, r.Scan(&s.ID, &s.ShiftID, &s.FromEmployeeID, &s.ToEmployeeID, &s.Status, &s.CreatedAt, &s.DecidedAt)
			})
		}), nil, nil, nil),
		withStatus(r(me("POST", "/shift-swaps", "Offer one of your shifts to a co-worker", func(c *httpx.Ctx, emp string) (any, error) {
			b, err := body(c)
			if err != nil {
				return nil, err
			}
			shiftID, _ := b["shiftId"].(string)
			if err := owns(c, c.App.Pool, "shifts", shiftID, emp, "Shift"); err != nil {
				return nil, httpx.Validation(httpx.FieldError{Path: "shiftId", Message: "Not one of your shifts"})
			}
			return c.Delegate("POST", "/shift-swaps", nil, nil, map[string]any{"shiftId": shiftID, "toEmployeeId": b["toEmployeeId"]})
		}), nil, SwapOffer{}, nil), 201),
		r(me("GET", "/availability", "Your availability", func(c *httpx.Ctx, emp string) (any, error) {
			return c.Delegate("GET", "/availability", nil, pass(c, emp), nil)
		}), nil, nil, nil),
		withStatus(r(me("POST", "/availability", "Add an availability window", func(c *httpx.Ctx, emp string) (any, error) {
			b, err := body(c)
			if err != nil {
				return nil, err
			}
			b["employeeId"] = emp
			return c.Delegate("POST", "/availability", nil, nil, b)
		}), nil, nil, nil), 201),
		r(me("DELETE", "/availability/{id}", "Remove an availability window", func(c *httpx.Ctx, emp string) (any, error) {
			if err := owns(c, c.App.Pool, "availability", c.Param("id"), emp, "Availability"); err != nil {
				return nil, err
			}
			return c.Delegate("DELETE", "/availability/{id}", map[string]string{"id": c.Param("id")}, nil, nil)
		}), nil, nil, nil),

		// Pay
		r(me("GET", "/pay", "Your pay rate history and estimated gross pay per pay period", func(c *httpx.Ctx, emp string) (any, error) {
			if err := c.RequireFeature("employee_area.estimated_pay"); err != nil {
				return nil, err
			}
			return payEstimates(c, emp)
		}), nil, nil, MyPay{}),
		r(me("GET", "/payslips", "Your payslips", func(c *httpx.Ctx, emp string) (any, error) {
			if err := c.RequireFeature("employee_area.payslips"); err != nil {
				return nil, err
			}
			return c.Delegate("GET", "/payslips", nil, pass(c, emp), nil)
		}), nil, nil, nil),
	}
}

func withStatus(r httpx.Route, status int) httpx.Route {
	r.Status = status
	return r
}
