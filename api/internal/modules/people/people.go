// Package people serves employee records.
package people

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
)

const feature = "people"

type Employee struct {
	ID                string                     `json:"id"`
	ExternalID        *string                    `json:"externalId"`
	EmployeeNumber    *string                    `json:"employeeNumber"`
	FirstName         string                     `json:"firstName"`
	LastName          string                     `json:"lastName"`
	PreferredName     *string                    `json:"preferredName"`
	Email             *string                    `json:"email"`
	Phone             *string                    `json:"phone"`
	EmploymentType    string                     `json:"employmentType"`
	Status            string                     `json:"status"`
	StartDate         *httpx.Date                `json:"startDate"`
	EndDate           *httpx.Date                `json:"endDate"`
	TerminationReason *string                    `json:"terminationReason"`
	HomeLocationID    *string                    `json:"homeLocationId"`
	DepartmentID      *string                    `json:"departmentId"`
	Position          *string                    `json:"position"`
	ManagerID         *string                    `json:"managerId"`
	CustomFields      map[string]any             `json:"customFields"`
	IntegrationData   map[string]json.RawMessage `json:"integrationData"`
	Version           int                        `json:"version"`
	CreatedAt         time.Time                  `json:"createdAt"`
	UpdatedAt         time.Time                  `json:"updatedAt"`
	ArchivedAt        *time.Time                 `json:"archivedAt"`
}

// EmployeeInput is the writable part of an employee, used for create, upsert
// and (merged onto the current record) patch.
type EmployeeInput struct {
	ExternalID      *string                    `json:"externalId,omitempty" validate:"omitempty,extid"`
	EmployeeNumber  *string                    `json:"employeeNumber,omitempty" validate:"omitempty,max=50"`
	FirstName       string                     `json:"firstName" validate:"required,max=100"`
	LastName        string                     `json:"lastName" validate:"required,max=100"`
	PreferredName   *string                    `json:"preferredName,omitempty" validate:"omitempty,max=100"`
	Email           *string                    `json:"email,omitempty" validate:"omitempty,email"`
	Phone           *string                    `json:"phone,omitempty" validate:"omitempty,max=40"`
	EmploymentType  string                     `json:"employmentType,omitempty" validate:"omitempty,oneof=full_time part_time casual contractor"`
	Status          string                     `json:"status,omitempty" validate:"omitempty,oneof=active on_leave"`
	StartDate       *httpx.Date                `json:"startDate,omitempty"`
	HomeLocationID  *string                    `json:"homeLocationId,omitempty"`
	DepartmentID    *string                    `json:"departmentId,omitempty"`
	Position        *string                    `json:"position,omitempty" validate:"omitempty,max=100"`
	ManagerID       *string                    `json:"managerId,omitempty"`
	CustomFields    map[string]any             `json:"customFields,omitempty"`
	IntegrationData map[string]json.RawMessage `json:"integrationData,omitempty" doc:"Only your integration's own namespace can be written"`
}

type TerminateInput struct {
	EndDate httpx.Date `json:"endDate" validate:"required"`
	Reason  string     `json:"reason,omitempty" validate:"max=200"`
}

type ListQuery struct {
	httpx.ListParams
	Status     string `json:"status,omitempty" doc:"active, on_leave or terminated"`
	LocationID string `json:"locationId,omitempty" doc:"Home location"`
}

const cols = `id, external_id, employee_number, first_name, last_name, preferred_name, email, phone,
	employment_type, status, start_date, end_date, termination_reason, home_location_id, department_id,
	position, manager_id, custom_fields, integration_data, version, created_at, updated_at, archived_at`

func scan(row pgx.Row) (Employee, error) {
	var e Employee
	var start, end httpx.Date
	var startValid, endValid *time.Time
	err := row.Scan(&e.ID, &e.ExternalID, &e.EmployeeNumber, &e.FirstName, &e.LastName, &e.PreferredName,
		&e.Email, &e.Phone, &e.EmploymentType, &e.Status, &startValid, &endValid, &e.TerminationReason,
		&e.HomeLocationID, &e.DepartmentID, &e.Position, &e.ManagerID, &e.CustomFields, &e.IntegrationData,
		&e.Version, &e.CreatedAt, &e.UpdatedAt, &e.ArchivedAt)
	if startValid != nil {
		start = httpx.NewDate(*startValid)
		e.StartDate = &start
	}
	if endValid != nil {
		end = httpx.NewDate(*endValid)
		e.EndDate = &end
	}
	return e, err
}

func get(ctx context.Context, q db.Querier, where string, arg any, lock bool) (Employee, error) {
	sql := `SELECT ` + cols + ` FROM employees WHERE ` + where
	if lock {
		sql += ` FOR UPDATE`
	}
	e, err := scan(q.QueryRow(ctx, sql, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return e, httpx.NotFound("Employee not found.")
	}
	return e, err
}

// toInput returns the writable view of an employee (for patch merging).
func (e Employee) toInput() EmployeeInput {
	return EmployeeInput{
		ExternalID: e.ExternalID, EmployeeNumber: e.EmployeeNumber, FirstName: e.FirstName, LastName: e.LastName,
		PreferredName: e.PreferredName, Email: e.Email, Phone: e.Phone, EmploymentType: e.EmploymentType,
		Status: e.Status, StartDate: e.StartDate, HomeLocationID: e.HomeLocationID, DepartmentID: e.DepartmentID,
		Position: e.Position, ManagerID: e.ManagerID, CustomFields: e.CustomFields, IntegrationData: e.IntegrationData,
	}
}

// checkIntegrationData enforces that an integration writes only its own
// namespace, and merges it into the existing data.
func checkIntegrationData(c *httpx.Ctx, existing, incoming map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	maps.Copy(out, existing)
	for k, v := range incoming {
		if existingV, ok := existing[k]; ok && string(existingV) == string(v) {
			continue // unchanged (e.g. echoed back in a PATCH)
		}
		if c.Principal == nil || c.Principal.Kind != "integration" || k != c.Principal.IntegrationName {
			return nil, httpx.Validation(httpx.FieldError{
				Path:    "integrationData." + k,
				Message: "Only the integration named " + strconv.Quote(k) + " can write this namespace",
			})
		}
		if string(v) == "null" {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out, nil
}

// refError turns foreign-key violations into field errors.
func refError(err error) error {
	if db.IsForeignKeyViolation(err) {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "home_location_id"):
			return httpx.Validation(httpx.FieldError{Path: "homeLocationId", Message: "Unknown location"})
		case strings.Contains(msg, "department_id"):
			return httpx.Validation(httpx.FieldError{Path: "departmentId", Message: "Unknown department"})
		case strings.Contains(msg, "manager_id"):
			return httpx.Validation(httpx.FieldError{Path: "managerId", Message: "Unknown employee"})
		}
	}
	if db.IsUniqueViolation(err) {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "external_id"):
			return httpx.Conflict("Another employee already has this externalId.")
		case strings.Contains(msg, "employee_number"):
			return httpx.Conflict("Another employee already has this employeeNumber.")
		}
	}
	return err
}

func insert(c *httpx.Ctx, tx pgx.Tx, in EmployeeInput) (Employee, error) {
	idata, err := checkIntegrationData(c, nil, in.IntegrationData)
	if err != nil {
		return Employee{}, err
	}
	if in.EmploymentType == "" {
		in.EmploymentType = "full_time"
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.CustomFields == nil {
		in.CustomFields = map[string]any{}
	}
	e, err := scan(tx.QueryRow(c, `
		INSERT INTO employees (id, external_id, employee_number, first_name, last_name, preferred_name, email, phone,
			employment_type, status, start_date, home_location_id, department_id, position, manager_id,
			custom_fields, integration_data)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING `+cols,
		ids.New(ids.Employee), in.ExternalID, in.EmployeeNumber, in.FirstName, in.LastName, in.PreferredName,
		in.Email, in.Phone, in.EmploymentType, in.Status, in.StartDate, in.HomeLocationID, in.DepartmentID,
		in.Position, in.ManagerID, in.CustomFields, idata))
	if err != nil {
		return e, refError(err)
	}
	return e, c.Record(tx, httpx.Change{
		Action: "employee.create", EventType: "employee.created", Feature: feature,
		EntityType: "employee", EntityID: e.ID, LocationID: deref(e.HomeLocationID), After: e,
	})
}

func update(c *httpx.Ctx, tx pgx.Tx, before Employee, in EmployeeInput) (Employee, error) {
	idata, err := checkIntegrationData(c, before.IntegrationData, in.IntegrationData)
	if err != nil {
		return Employee{}, err
	}
	if in.CustomFields == nil {
		in.CustomFields = map[string]any{}
	}
	if in.ManagerID != nil && *in.ManagerID == before.ID {
		return Employee{}, httpx.Validation(httpx.FieldError{Path: "managerId", Message: "An employee can't manage themselves"})
	}
	status := in.Status
	if before.Status == "terminated" {
		status = "terminated" // use :rehire (future) to reactivate
	} else if status == "" {
		status = before.Status
	}
	after, err := scan(tx.QueryRow(c, `
		UPDATE employees SET external_id=$2, employee_number=$3, first_name=$4, last_name=$5, preferred_name=$6,
			email=$7, phone=$8, employment_type=coalesce(nullif($9, ''), employment_type), status=$10, start_date=$11,
			home_location_id=$12, department_id=$13, position=$14, manager_id=$15, custom_fields=$16,
			integration_data=$17, version=version+1, updated_at=now()
		WHERE id=$1 RETURNING `+cols,
		before.ID, in.ExternalID, in.EmployeeNumber, in.FirstName, in.LastName, in.PreferredName, in.Email,
		in.Phone, in.EmploymentType, status, in.StartDate, in.HomeLocationID, in.DepartmentID, in.Position,
		in.ManagerID, in.CustomFields, idata))
	if err != nil {
		return after, refError(err)
	}
	return after, c.Record(tx, httpx.Change{
		Action: "employee.update", EventType: "employee.updated", Feature: feature,
		EntityType: "employee", EntityID: after.ID, LocationID: deref(after.HomeLocationID), Before: before, After: after,
	})
}

func checkIfMatch(c *httpx.Ctx, e Employee) error {
	h := c.Req.Header.Get("If-Match")
	if h == "" {
		return nil
	}
	v, err := strconv.Atoi(strings.Trim(h, `"`))
	if err != nil {
		return httpx.BadRequest("If-Match must be the record's version number.")
	}
	if v != e.Version {
		return httpx.VersionMismatch()
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func Routes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "GET", Path: "/employees", Tag: "People", Feature: feature, Scope: "people:read",
			Summary: "List employees", Query: ListQuery{}, Response: httpx.Page[Employee]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				status := c.Query("status")
				if status != "" && status != "active" && status != "on_leave" && status != "terminated" {
					return nil, httpx.Validation(httpx.FieldError{Path: "status", Message: "Must be one of: active, on_leave, terminated"})
				}
				rows, err := c.App.Pool.Query(c, `SELECT `+cols+` FROM employees
					WHERE id > $1 AND ($2::timestamptz IS NULL OR updated_at >= $2)
					  AND ($3 = '' OR status = $3) AND ($4 = '' OR home_location_id = $4)
					ORDER BY id LIMIT $5`, lp.AfterID, lp.UpdatedSince, status, c.Query("locationId"), lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Employee, error) { return scan(r) })
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(e Employee) string { return e.ID }), nil
			},
		},
		{
			Method: "POST", Path: "/employees", Tag: "People", Feature: feature, Scope: "people:write",
			Summary: "Create an employee", Body: EmployeeInput{}, Response: Employee{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in EmployeeInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Employee
				err := c.InTx(func(tx pgx.Tx) (err error) { out, err = insert(c, tx, in); return err })
				return out, err
			},
		},
		{
			Method: "GET", Path: "/employees/{id}", Tag: "People", Feature: feature, Scope: "people:read",
			Summary: "Get an employee", Response: Employee{},
			Handler: func(c *httpx.Ctx) (any, error) { return get(c, c.App.Pool, "id = $1", c.Param("id"), false) },
		},
		{
			Method: "GET", Path: "/employees/external/{externalId}", Tag: "People", Feature: feature, Scope: "people:read",
			Summary: "Get an employee by external ID", Response: Employee{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return get(c, c.App.Pool, "external_id = $1", c.Param("externalId"), false)
			},
		},
		{
			Method: "PATCH", Path: "/employees/{id}", Tag: "People", Feature: feature, Scope: "people:write",
			Summary:     "Update an employee (partial)",
			Description: "Send only the fields to change. Send If-Match with the version to avoid overwriting someone else's change.",
			Body:        EmployeeInput{}, Response: Employee{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out Employee
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := get(c, tx, "id = $1", c.Param("id"), true)
					if err != nil {
						return err
					}
					if err := checkIfMatch(c, before); err != nil {
						return err
					}
					in, err := httpx.MergePatch(c, before.toInput())
					if err != nil {
						return err
					}
					out, err = update(c, tx, before, in)
					return err
				})
				return out, err
			},
		},
		{
			Method: "PUT", Path: "/employees/external/{externalId}", Tag: "People", Feature: feature, Scope: "people:write",
			Summary:     "Create or replace an employee by external ID",
			Description: "Returns 201 when created and 200 when updated. The externalId in the path wins over the body.",
			Body:        EmployeeInput{}, Response: Employee{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in EmployeeInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				ext := c.Param("externalId")
				in.ExternalID = &ext
				var out Employee
				created := false
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := get(c, tx, "external_id = $1", ext, true)
					var p *httpx.Problem
					if errors.As(err, &p) && p.Code == "not_found" {
						created = true
						out, err = insert(c, tx, in)
						return err
					}
					if err != nil {
						return err
					}
					if err := checkIfMatch(c, before); err != nil {
						return err
					}
					out, err = update(c, tx, before, in)
					return err
				})
				if err != nil {
					return nil, err
				}
				if created {
					return httpx.Result{Status: 201, Body: out}, nil
				}
				return out, nil
			},
		},
		{
			Method: "POST", Path: "/employees/{id}:terminate", Tag: "People", Feature: feature, Scope: "people:write",
			Summary: "Terminate an employee", Body: TerminateInput{}, Response: Employee{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in TerminateInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Employee
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := get(c, tx, "id = $1", c.Param("id"), true)
					if err != nil {
						return err
					}
					if before.Status == "terminated" {
						return httpx.Conflict("This employee is already terminated.")
					}
					if before.StartDate != nil && in.EndDate.Before(before.StartDate.Time) {
						return httpx.Validation(httpx.FieldError{Path: "endDate", Message: "Must not be before the start date"})
					}
					out, err = scan(tx.QueryRow(c, `
						UPDATE employees SET status='terminated', end_date=$2, termination_reason=nullif($3, ''),
							version=version+1, updated_at=now()
						WHERE id=$1 RETURNING `+cols, before.ID, in.EndDate, in.Reason))
					if err != nil {
						return err
					}
					// Deactivate any account linked to the employee.
					if _, err := tx.Exec(c, `UPDATE users SET status='deactivated', updated_at=now() WHERE employee_id=$1`, before.ID); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{
						Action: "employee.terminate", EventType: "employee.terminated", Feature: feature,
						EntityType: "employee", EntityID: out.ID, LocationID: deref(out.HomeLocationID), Before: before, After: out,
					})
				})
				return out, err
			},
		},
		{
			Method: "DELETE", Path: "/employees/{id}", Tag: "People", Feature: feature, Scope: "people:write",
			Summary: "Archive an employee", Description: "Soft delete. History is kept.", Response: Employee{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out Employee
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := get(c, tx, "id = $1", c.Param("id"), true)
					if err != nil {
						return err
					}
					if before.ArchivedAt != nil {
						out = before
						return nil
					}
					out, err = scan(tx.QueryRow(c, `UPDATE employees SET archived_at=now(), version=version+1, updated_at=now()
						WHERE id=$1 RETURNING `+cols, before.ID))
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{
						Action: "employee.archive", EventType: "employee.archived", Feature: feature,
						EntityType: "employee", EntityID: out.ID, LocationID: deref(out.HomeLocationID), Before: before, After: out,
					})
				})
				return out, err
			},
		},
	}
}
