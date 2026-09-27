// Package timeclock serves punches (clock-in/out events).
package timeclock

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/modules/organization"
)

const feature = "time"

type Punch struct {
	ID         string     `json:"id"`
	EmployeeID string     `json:"employeeId"`
	Type       string     `json:"type"`
	At         time.Time  `json:"at"`
	LocationID *string    `json:"locationId"`
	DeviceID   string     `json:"deviceId"`
	Source     string     `json:"source"`
	ExternalID *string    `json:"externalId"`
	VoidedAt   *time.Time `json:"voidedAt" doc:"Replaced by an approved correction; kept for the record"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type PunchInput struct {
	EmployeeID         string    `json:"employeeId,omitempty" validate:"required_without=EmployeeExternalID"`
	EmployeeExternalID string    `json:"employeeExternalId,omitempty" validate:"required_without=EmployeeID"`
	Type               string    `json:"type" validate:"required,oneof=in out break_start break_end"`
	At                 time.Time `json:"at" validate:"required"`
	DeviceID           string    `json:"deviceId,omitempty" validate:"max=100"`
	LocationID         string    `json:"locationId,omitempty"`
	LocationExternalID string    `json:"locationExternalId,omitempty"`
	ExternalID         string    `json:"externalId,omitempty" validate:"omitempty,extid"`
}

type PunchBatch struct {
	Source  string       `json:"source" validate:"required" doc:"Where the punches came from, e.g. timeclock:lobby or pos:store-101"`
	Punches []PunchInput `json:"punches" validate:"required"`
}

type ListQuery struct {
	httpx.ListParams
	EmployeeID    string    `json:"employeeId,omitempty"`
	From          time.Time `json:"from,omitempty" doc:"Punches at or after this time"`
	To            time.Time `json:"to,omitempty" doc:"Punches before this time"`
	IncludeVoided bool      `json:"includeVoided,omitempty" doc:"Include punches replaced by corrections"`
}

const cols = `id, employee_id, type, at, location_id, device_id, source, external_id, voided_at, created_at`

func scan(r pgx.Row) (Punch, error) {
	var p Punch
	err := r.Scan(&p.ID, &p.EmployeeID, &p.Type, &p.At, &p.LocationID, &p.DeviceID, &p.Source, &p.ExternalID, &p.VoidedAt, &p.CreatedAt)
	return p, err
}

func Routes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "POST", Path: "/time/punches:batch", Tag: "Time & Attendance", Feature: feature, Scope: "time:write",
			Ingest: true, Status: 202,
			Summary: "Send punches in bulk",
			Description: "Punches are de-duplicated on employee, type, time and device, so re-sending is safe. " +
				"Each record gets its own result; rejected records are not stored.",
			Body: PunchBatch{}, Response: httpx.BatchResult{},
			Handler: ingestPunches,
		},
		{
			Method: "GET", Path: "/time/punches", Tag: "Time & Attendance", Feature: feature, Scope: "time:read",
			Summary: "List punches", Query: ListQuery{}, Response: httpx.Page[Punch]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				var from, to *time.Time
				for name, dst := range map[string]**time.Time{"from": &from, "to": &to} {
					if s := c.Query(name); s != "" {
						t, err := time.Parse(time.RFC3339, s)
						if err != nil {
							return nil, httpx.Validation(httpx.FieldError{Path: name, Message: "Must be an RFC 3339 timestamp"})
						}
						*dst = &t
					}
				}
				rows, err := c.App.Pool.Query(c, `SELECT `+cols+` FROM punches
					WHERE id > $1 AND ($2::timestamptz IS NULL OR created_at >= $2)
					  AND ($3 = '' OR employee_id = $3)
					  AND ($4::timestamptz IS NULL OR at >= $4) AND ($5::timestamptz IS NULL OR at < $5)
					  AND ($7 OR voided_at IS NULL)
					ORDER BY id LIMIT $6`, lp.AfterID, lp.UpdatedSince, c.Query("employeeId"), from, to, lp.Limit+1, c.Query("includeVoided") == "true")
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Punch, error) { return scan(r) })
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(p Punch) string { return p.ID }), nil
			},
		},
	}
}

func ingestPunches(c *httpx.Ctx) (any, error) {
	var batch PunchBatch
	if err := c.Decode(&batch); err != nil {
		return nil, err
	}
	if !httpx.ValidSource(batch.Source) {
		return nil, httpx.Validation(httpx.FieldError{Path: "source", Message: "Must be 1–100 characters without spaces or '/'"})
	}
	if err := c.CheckBatchSize("punches", len(batch.Punches)); err != nil {
		return nil, err
	}

	res := httpx.BatchResult{BatchID: ids.New(ids.Batch), Received: len(batch.Punches), Results: []httpx.RecordResult{}}
	err := c.InTx(func(tx pgx.Tx) error {
		locs := organization.NewLocationResolver(tx)
		empByExt := map[string]string{}

		for i, in := range batch.Punches {
			if errs := httpx.ValidateItem(in); errs != nil {
				res.Add(httpx.RecordResult{Index: i, ExternalID: in.ExternalID, Status: "rejected", Errors: errs})
				continue
			}
			empID := in.EmployeeID
			if empID == "" {
				if cached, ok := empByExt[in.EmployeeExternalID]; ok {
					empID = cached
				} else {
					err := tx.QueryRow(c, `SELECT id FROM employees WHERE external_id = $1`, in.EmployeeExternalID).Scan(&empID)
					if errors.Is(err, pgx.ErrNoRows) {
						res.Add(httpx.Reject(i, in.ExternalID, "employeeExternalId", "Unknown employee"))
						continue
					}
					if err != nil {
						return err
					}
					empByExt[in.EmployeeExternalID] = empID
				}
			}
			var locID *string
			if in.LocationID != "" || in.LocationExternalID != "" {
				l, err := locs.Resolve(c, in.LocationID, in.LocationExternalID)
				if err != nil {
					res.Add(httpx.Reject(i, in.ExternalID, "location", err.Error()))
					continue
				}
				locID = &l.ID
			}
			var ext *string
			if in.ExternalID != "" {
				ext = &in.ExternalID
			}

			var p Punch
			err := httpx.Savepoint(c, tx, func(sp pgx.Tx) error {
				var err error
				p, err = scan(sp.QueryRow(c, `
					INSERT INTO punches (id, employee_id, type, at, location_id, device_id, source, external_id)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
					ON CONFLICT (employee_id, type, at, device_id) DO NOTHING
					RETURNING `+cols,
					ids.New(ids.Punch), empID, in.Type, in.At.UTC(), locID, in.DeviceID, batch.Source, ext))
				if err != nil {
					return err
				}
				return c.Record(sp, httpx.Change{
					Action: "punch.create", EventType: "punch.received", Feature: feature,
					EntityType: "punch", EntityID: p.ID, LocationID: deref(p.LocationID), After: p,
				})
			})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				res.Add(httpx.RecordResult{Index: i, ExternalID: in.ExternalID, Status: "duplicate"})
			case err != nil && db.IsForeignKeyViolation(err):
				res.Add(httpx.Reject(i, in.ExternalID, "employeeId", "Unknown employee"))
			case err != nil:
				return err
			default:
				res.Add(httpx.RecordResult{Index: i, ExternalID: in.ExternalID, Status: "created", ID: p.ID})
			}
		}
		_, err := tx.Exec(c, `
			INSERT INTO ingest_batches (id, api_key_id, integration_id, kind, source, received, created, updated, rejected)
			VALUES ($1, $2, nullif($3, ''), 'punches', $4, $5, $6, $7, $8)`,
			res.BatchID, c.Principal.KeyID, c.Principal.IntegrationID, batch.Source, res.Received, res.Created, res.Updated, res.Rejected)
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
