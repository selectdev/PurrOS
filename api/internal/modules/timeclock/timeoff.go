package timeclock

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/crud"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/refs"
	"github.com/shopspring/decimal"
)

const timeOffFeature = "time.time_off"

type TimeOffType struct {
	ID         string     `json:"id" db:"id"`
	Name       string     `json:"name" db:"name"`
	Paid       bool       `json:"paid" db:"paid"`
	ExternalID *string    `json:"externalId" db:"external_id"`
	CreatedAt  time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time  `json:"updatedAt" db:"updated_at"`
	ArchivedAt *time.Time `json:"archivedAt" db:"archived_at"`
}

type TimeOffTypeInput struct {
	Name       string  `json:"name" db:"name" validate:"required,max=100"`
	Paid       bool    `json:"paid" db:"paid"`
	ExternalID *string `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
}

var timeOffTypes = &crud.Resource[TimeOffType, TimeOffTypeInput]{
	Path: "/time-off/types", Table: "time_off_types", Prefix: ids.TimeOffType, Noun: "time_off_type",
	Tag: "Time & Attendance", Feature: timeOffFeature, ReadScope: "time:read", WriteScope: "time:write",
	External: true, Archive: true,
}

type TimeOffRequest struct {
	ID           string          `json:"id"`
	EmployeeID   string          `json:"employeeId"`
	TypeID       string          `json:"typeId"`
	StartDate    httpx.Date      `json:"startDate"`
	EndDate      httpx.Date      `json:"endDate"`
	Hours        decimal.Decimal `json:"hours"`
	Status       string          `json:"status" doc:"requested, approved, rejected or cancelled"`
	Note         *string         `json:"note"`
	DecisionNote *string         `json:"decisionNote"`
	DecidedAt    *time.Time      `json:"decidedAt"`
	Version      int             `json:"version"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

type TimeOffRequestInput struct {
	refs.EmployeeRef
	TypeID    string           `json:"typeId" validate:"required"`
	StartDate httpx.Date       `json:"startDate" validate:"required"`
	EndDate   httpx.Date       `json:"endDate" validate:"required"`
	Hours     *decimal.Decimal `json:"hours" validate:"required"`
	Note      string           `json:"note,omitempty" validate:"max=500"`
}

type AdjustmentInput struct {
	refs.EmployeeRef
	TypeID string           `json:"typeId" validate:"required"`
	Hours  *decimal.Decimal `json:"hours" validate:"required" doc:"Positive to add (accrual), negative to deduct"`
	Reason string           `json:"reason" validate:"required,max=200"`
}

type Balance struct {
	EmployeeID string          `json:"employeeId"`
	TypeID     string          `json:"typeId"`
	TypeName   string          `json:"typeName"`
	Hours      decimal.Decimal `json:"hours"`
	Pending    decimal.Decimal `json:"pending" doc:"Hours in requests awaiting a decision"`
}

type balanceList struct {
	Data []Balance `json:"data"`
}

const torCols = `id, employee_id, type_id, start_date, end_date, hours, status, note, decision_note, decided_at, version, created_at, updated_at`

func scanTOR(r pgx.Row) (TimeOffRequest, error) {
	var t TimeOffRequest
	var s, e time.Time
	err := r.Scan(&t.ID, &t.EmployeeID, &t.TypeID, &s, &e, &t.Hours, &t.Status, &t.Note, &t.DecisionNote, &t.DecidedAt,
		&t.Version, &t.CreatedAt, &t.UpdatedAt)
	t.StartDate, t.EndDate = httpx.NewDate(s), httpx.NewDate(e)
	return t, err
}

func getTOR(c *httpx.Ctx, q db.Querier, id string) (TimeOffRequest, error) {
	t, err := scanTOR(q.QueryRow(c, `SELECT `+torCols+` FROM time_off_requests WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, httpx.NotFound("Time-off request not found.")
	}
	return t, err
}

func torDecision(to string, from []string, event string) func(c *httpx.Ctx) (any, error) {
	return func(c *httpx.Ctx) (any, error) {
		var in DecisionInput
		if err := c.DecodeOptional(&in); err != nil {
			return nil, err
		}
		var out TimeOffRequest
		err := c.InTx(func(tx pgx.Tx) error {
			before, err := getTOR(c, tx, c.Param("id"))
			if err != nil {
				return err
			}
			allowed := false
			for _, f := range from {
				allowed = allowed || before.Status == f
			}
			if !allowed {
				return httpx.Conflict("A " + before.Status + " request can't be " + to + ".")
			}
			out, err = scanTOR(tx.QueryRow(c, `UPDATE time_off_requests SET status=$2, decision_note=nullif($3,''), decided_at=now(),
				version=version+1, updated_at=now() WHERE id=$1 RETURNING `+torCols, before.ID, to, in.Note))
			if err != nil {
				return err
			}
			switch {
			case to == "approved":
				_, err = tx.Exec(c, `INSERT INTO time_off_ledger (id, employee_id, type_id, hours, reason, request_id) VALUES ($1,$2,$3,$4,'request approved',$5)`,
					ids.New(ids.TimeOffEntry), out.EmployeeID, out.TypeID, out.Hours.Neg(), out.ID)
			case to == "cancelled" && before.Status == "approved":
				_, err = tx.Exec(c, `INSERT INTO time_off_ledger (id, employee_id, type_id, hours, reason, request_id) VALUES ($1,$2,$3,$4,'approved request cancelled',$5)`,
					ids.New(ids.TimeOffEntry), out.EmployeeID, out.TypeID, out.Hours, out.ID)
			}
			if err != nil {
				return err
			}
			return c.Record(tx, httpx.Change{Action: "time_off." + to, EventType: event, Feature: timeOffFeature,
				EntityType: "time_off_request", EntityID: out.ID, Before: before, After: out})
		})
		return out, err
	}
}

type torQuery struct {
	httpx.ListParams
	EmployeeID string `json:"employeeId,omitempty"`
	Status     string `json:"status,omitempty"`
}

// TimeOffRoutes are the time-off routes.
func TimeOffRoutes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "GET", Path: "/time-off/requests", Tag: "Time & Attendance", Feature: timeOffFeature, Scope: "time:read",
			Summary: "List time-off requests", Query: torQuery{}, Response: httpx.Page[TimeOffRequest]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `SELECT `+torCols+` FROM time_off_requests
					WHERE id > $1 AND ($2 = '' OR employee_id = $2) AND ($3 = '' OR status = $3) ORDER BY id LIMIT $4`,
					lp.AfterID, c.Query("employeeId"), c.Query("status"), lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TimeOffRequest, error) { return scanTOR(r) })
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(t TimeOffRequest) string { return t.ID }), nil
			},
		},
		{
			Method: "POST", Path: "/time-off/requests", Tag: "Time & Attendance", Feature: timeOffFeature, Scope: "time:write",
			Summary: "Request time off", Body: TimeOffRequestInput{}, Response: TimeOffRequest{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in TimeOffRequestInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if in.EndDate.Before(in.StartDate.Time) {
					return nil, httpx.Validation(httpx.FieldError{Path: "endDate", Message: "Must not be before startDate"})
				}
				if !in.Hours.IsPositive() {
					return nil, httpx.Validation(httpx.FieldError{Path: "hours", Message: "Must be greater than zero"})
				}
				var out TimeOffRequest
				err := c.InTx(func(tx pgx.Tx) error {
					emp, err := refs.Employee(c, tx, in.EmployeeRef, "", false)
					if err != nil {
						return err
					}
					if _, err := timeOffTypes.Get(c, tx, "id", in.TypeID, false); err != nil {
						return httpx.Validation(httpx.FieldError{Path: "typeId", Message: "Unknown time-off type"})
					}
					out, err = scanTOR(tx.QueryRow(c, `INSERT INTO time_off_requests (id, employee_id, type_id, start_date, end_date, hours, note)
						VALUES ($1,$2,$3,$4,$5,$6,nullif($7,'')) RETURNING `+torCols,
						ids.New(ids.TimeOffRequest), emp, in.TypeID, in.StartDate, in.EndDate, *in.Hours, in.Note))
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "time_off.request", EventType: "time_off.requested", Feature: timeOffFeature,
						EntityType: "time_off_request", EntityID: out.ID, After: out})
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/time-off/requests/{id}:approve", Tag: "Time & Attendance", Feature: timeOffFeature, Scope: "time:write",
			Summary: "Approve a time-off request (deducts the balance)", Body: DecisionInput{}, Response: TimeOffRequest{},
			Handler: torDecision("approved", []string{"requested"}, "time_off.approved"),
		},
		{
			Method: "POST", Path: "/time-off/requests/{id}:reject", Tag: "Time & Attendance", Feature: timeOffFeature, Scope: "time:write",
			Summary: "Reject a time-off request", Body: DecisionInput{}, Response: TimeOffRequest{},
			Handler: torDecision("rejected", []string{"requested"}, "time_off.rejected"),
		},
		{
			Method: "POST", Path: "/time-off/requests/{id}:cancel", Tag: "Time & Attendance", Feature: timeOffFeature, Scope: "time:write",
			Summary: "Cancel a time-off request (restores the balance if it was approved)", Body: DecisionInput{}, Response: TimeOffRequest{},
			Handler: torDecision("cancelled", []string{"requested", "approved"}, ""),
		},
		{
			Method: "GET", Path: "/time-off/balances", Tag: "Time & Attendance", Feature: timeOffFeature, Scope: "time:read",
			Summary: "Time-off balances per employee and type", Response: balanceList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `
					WITH keys AS (
						SELECT employee_id, type_id FROM time_off_ledger
						UNION SELECT employee_id, type_id FROM time_off_requests WHERE status = 'requested'
					)
					SELECT k.employee_id, k.type_id, t.name,
					       coalesce((SELECT sum(hours) FROM time_off_ledger l WHERE l.employee_id = k.employee_id AND l.type_id = k.type_id), 0),
					       coalesce((SELECT sum(hours) FROM time_off_requests r WHERE r.employee_id = k.employee_id AND r.type_id = k.type_id AND r.status = 'requested'), 0)
					FROM keys k JOIN time_off_types t ON t.id = k.type_id
					WHERE $1 = '' OR k.employee_id = $1
					ORDER BY k.employee_id, t.name`, c.Query("employeeId"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Balance, error) {
					var b Balance
					err := r.Scan(&b.EmployeeID, &b.TypeID, &b.TypeName, &b.Hours, &b.Pending)
					return b, err
				})
				if list == nil {
					list = []Balance{}
				}
				return balanceList{Data: list}, err
			},
		},
		{
			Method: "POST", Path: "/time-off/adjustments", Tag: "Time & Attendance", Feature: timeOffFeature, Scope: "time:write",
			Summary: "Add or deduct time-off hours (accruals, corrections)", Body: AdjustmentInput{}, Response: balanceList{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in AdjustmentInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out balanceList
				err := c.InTx(func(tx pgx.Tx) error {
					emp, err := refs.Employee(c, tx, in.EmployeeRef, "", false)
					if err != nil {
						return err
					}
					if _, err := timeOffTypes.Get(c, tx, "id", in.TypeID, false); err != nil {
						return httpx.Validation(httpx.FieldError{Path: "typeId", Message: "Unknown time-off type"})
					}
					if _, err := tx.Exec(c, `INSERT INTO time_off_ledger (id, employee_id, type_id, hours, reason) VALUES ($1,$2,$3,$4,$5)`,
						ids.New(ids.TimeOffEntry), emp, in.TypeID, *in.Hours, in.Reason); err != nil {
						return err
					}
					var b Balance
					if err := tx.QueryRow(c, `SELECT $1::text, $2::text, (SELECT name FROM time_off_types WHERE id=$2),
						coalesce(sum(hours),0), 0::numeric FROM time_off_ledger WHERE employee_id=$1 AND type_id=$2`, emp, in.TypeID).
						Scan(&b.EmployeeID, &b.TypeID, &b.TypeName, &b.Hours, &b.Pending); err != nil {
						return err
					}
					out.Data = []Balance{b}
					return c.Record(tx, httpx.Change{Action: "time_off.adjust", EntityType: "employee", EntityID: emp,
						After: map[string]any{"typeId": in.TypeID, "hours": in.Hours, "reason": in.Reason}})
				})
				return out, err
			},
		},
	}
	return append(routes, timeOffTypes.Routes()...)
}
