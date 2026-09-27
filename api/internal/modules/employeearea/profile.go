// Package employeearea serves the Employee Area: every employee's own data
// and self-service requests, under /me. Any person whose account is linked to
// an employee record can use it for their own data, whatever their role.
package employeearea

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
)

const (
	feature = "employee_area"
	tag     = "Employee Area"
)

// self returns the caller's employee ID.
func self(c *httpx.Ctx) (string, error) {
	u := c.Principal.User
	if u == nil || u.EmployeeID == "" {
		return "", httpx.Forbidden("Your account isn't linked to an employee record. Ask HR to link it.")
	}
	return u.EmployeeID, nil
}

// me declares an Employee Area route.
func me(method, path, summary string, handler func(c *httpx.Ctx, emp string) (any, error)) httpx.Route {
	return httpx.Route{
		Method: method, Path: "/me" + path, Tag: tag, Feature: feature, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
		Summary: summary,
		Handler: func(c *httpx.Ctx) (any, error) {
			emp, err := self(c)
			if err != nil {
				return nil, err
			}
			return handler(c, emp)
		},
	}
}

type Contact struct {
	Name         string `json:"name" validate:"required,max=200"`
	Relationship string `json:"relationship,omitempty" validate:"max=100"`
	Phone        string `json:"phone" validate:"required,max=50"`
}

type Profile struct {
	EmployeeID        string          `json:"employeeId"`
	EmployeeNumber    *string         `json:"employeeNumber"`
	FirstName         string          `json:"firstName"`
	LastName          string          `json:"lastName"`
	PreferredName     *string         `json:"preferredName"`
	Email             *string         `json:"email"`
	Phone             *string         `json:"phone"`
	Address           json.RawMessage `json:"address"`
	EmergencyContacts []Contact       `json:"emergencyContacts"`
	Position          *string         `json:"position"`
	EmploymentType    string          `json:"employmentType"`
	Status            string          `json:"status"`
	StartDate         *httpx.Date     `json:"startDate"`
	Department        *NamedRef       `json:"department"`
	Location          *NamedRef       `json:"location"`
	Manager           *NamedRef       `json:"manager"`
}

type NamedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Address struct {
	Line1      string `json:"line1,omitempty" validate:"max=200"`
	Line2      string `json:"line2,omitempty" validate:"max=200"`
	City       string `json:"city,omitempty" validate:"max=100"`
	Region     string `json:"region,omitempty" validate:"max=100"`
	PostalCode string `json:"postalCode,omitempty" validate:"max=20"`
	Country    string `json:"country,omitempty" validate:"max=2" doc:"ISO 3166-1 alpha-2"`
}

// ProfilePatch holds the fields employees keep up to date themselves.
type ProfilePatch struct {
	PreferredName     *string    `json:"preferredName,omitempty" validate:"omitempty,max=100"`
	Phone             *string    `json:"phone,omitempty" validate:"omitempty,max=50"`
	Address           *Address   `json:"address,omitempty"`
	EmergencyContacts *[]Contact `json:"emergencyContacts,omitempty" validate:"omitempty,max=5,dive"`
}

func loadProfile(c *httpx.Ctx, q db.Querier, emp string) (Profile, error) {
	var p Profile
	var dep, depName, loc, locName, mgr, mgrName *string
	var addr, contacts []byte
	err := q.QueryRow(c, `
		SELECT e.id, e.employee_number, e.first_name, e.last_name, e.preferred_name, e.email, e.phone,
		       coalesce(e.address, 'null'::jsonb), e.emergency_contacts, e.position, e.employment_type, e.status, e.start_date,
		       d.id, d.name, l.id, l.name, m.id, m.first_name || ' ' || m.last_name
		FROM employees e
		LEFT JOIN departments d ON d.id = e.department_id
		LEFT JOIN locations l ON l.id = e.home_location_id
		LEFT JOIN employees m ON m.id = e.manager_id
		WHERE e.id = $1`, emp).
		Scan(&p.EmployeeID, &p.EmployeeNumber, &p.FirstName, &p.LastName, &p.PreferredName, &p.Email, &p.Phone,
			&addr, &contacts, &p.Position, &p.EmploymentType, &p.Status, &p.StartDate,
			&dep, &depName, &loc, &locName, &mgr, &mgrName)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, httpx.NotFound("Employee record not found.")
	}
	if err != nil {
		return p, err
	}
	p.Address = addr
	if err := json.Unmarshal(contacts, &p.EmergencyContacts); err != nil || p.EmergencyContacts == nil {
		p.EmergencyContacts = []Contact{}
	}
	ref := func(id, name *string) *NamedRef {
		if id == nil {
			return nil
		}
		return &NamedRef{ID: *id, Name: *name}
	}
	p.Department, p.Location, p.Manager = ref(dep, depName), ref(loc, locName), ref(mgr, mgrName)
	return p, nil
}

type Document struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Name      string      `json:"name"`
	URL       *string     `json:"url"`
	ExpiresOn *httpx.Date `json:"expiresOn"`
	CreatedAt time.Time   `json:"createdAt"`
}

type Announcement struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	RequireAck   bool       `json:"requireAck"`
	PublishAt    time.Time  `json:"publishAt"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	Acknowledged *time.Time `json:"acknowledgedAt"`
}

type Activity struct {
	At      time.Time       `json:"at"`
	Actor   string          `json:"actor" doc:"Who made the change"`
	Action  string          `json:"action"`
	Changes json.RawMessage `json:"changes" doc:"Changed fields and their new values"`
}

type list[T any] struct {
	Data []T `json:"data"`
}

func collect[T any](rows pgx.Rows, err error, scan func(pgx.CollectableRow) (T, error)) (list[T], error) {
	if err != nil {
		return list[T]{}, err
	}
	out, err := pgx.CollectRows(rows, scan)
	if out == nil {
		out = []T{}
	}
	return list[T]{Data: out}, err
}

func documents(c *httpx.Ctx, q db.Querier, emp string) (list[Document], error) {
	rows, err := q.Query(c, `SELECT id, type, name, url, expires_on, created_at FROM employee_documents
		WHERE employee_id = $1 AND shared_with_employee AND archived_at IS NULL ORDER BY created_at DESC`, emp)
	return collect(rows, err, func(r pgx.CollectableRow) (Document, error) {
		var d Document
		return d, r.Scan(&d.ID, &d.Type, &d.Name, &d.URL, &d.ExpiresOn, &d.CreatedAt)
	})
}

// announcements visible to the employee: company-wide or for their location.
func announcements(c *httpx.Ctx, q db.Querier, emp string) (list[Announcement], error) {
	rows, err := q.Query(c, `
		SELECT a.id, a.title, a.body, a.require_ack, a.publish_at, a.expires_at, k.acked_at
		FROM announcements a
		JOIN employees e ON e.id = $1
		LEFT JOIN announcement_acks k ON k.announcement_id = a.id AND k.employee_id = e.id
		WHERE a.archived_at IS NULL AND a.publish_at <= now() AND (a.expires_at IS NULL OR a.expires_at > now())
		  AND (cardinality(a.location_ids) = 0 OR e.home_location_id = ANY (a.location_ids))
		ORDER BY a.publish_at DESC LIMIT 100`, emp)
	return collect(rows, err, func(r pgx.CollectableRow) (Announcement, error) {
		var a Announcement
		return a, r.Scan(&a.ID, &a.Title, &a.Body, &a.RequireAck, &a.PublishAt, &a.ExpiresAt, &a.Acknowledged)
	})
}

func activity(c *httpx.Ctx, q db.Querier, emp string) (list[Activity], error) {
	rows, err := q.Query(c, `
		SELECT created_at, coalesce(nullif(actor_name, ''), actor_type), action, coalesce(after, 'null'::jsonb)
		FROM audit_log WHERE entity_type = 'employee' AND entity_id = $1
		ORDER BY created_at DESC LIMIT 200`, emp)
	return collect(rows, err, func(r pgx.CollectableRow) (Activity, error) {
		var a Activity
		return a, r.Scan(&a.At, &a.Actor, &a.Action, &a.Changes)
	})
}

func profileRoutes() []httpx.Route {
	patch := me("PATCH", "/profile", "Update your contact details and emergency contacts", func(c *httpx.Ctx, emp string) (any, error) {
		if err := c.RequireFeature("employee_area.profile_edit"); err != nil {
			return nil, err
		}
		var in ProfilePatch
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		var out Profile
		err := c.InTx(func(tx pgx.Tx) error {
			before, err := loadProfile(c, tx, emp)
			if err != nil {
				return err
			}
			var addr, contacts []byte
			if in.Address != nil {
				addr, _ = json.Marshal(in.Address)
			}
			if in.EmergencyContacts != nil {
				contacts, _ = json.Marshal(*in.EmergencyContacts)
			}
			if _, err := tx.Exec(c, `UPDATE employees SET
					preferred_name = CASE WHEN $2 THEN nullif($3, '') ELSE preferred_name END,
					phone = CASE WHEN $4 THEN nullif($5, '') ELSE phone END,
					address = coalesce($6::jsonb, address),
					emergency_contacts = coalesce($7::jsonb, emergency_contacts),
					version = version + 1, updated_at = now()
				WHERE id = $1`, emp, in.PreferredName != nil, deref(in.PreferredName), in.Phone != nil, deref(in.Phone),
				nullJSON(addr), nullJSON(contacts)); err != nil {
				return err
			}
			if out, err = loadProfile(c, tx, emp); err != nil {
				return err
			}
			loc := ""
			if out.Location != nil {
				loc = out.Location.ID
			}
			return c.Record(tx, httpx.Change{Action: "employee.self_update", EventType: "employee.updated", Feature: "people",
				EntityType: "employee", EntityID: emp, LocationID: loc, Before: before, After: out})
		})
		return out, err
	})
	patch.Body, patch.Response = ProfilePatch{}, Profile{}

	routes := []httpx.Route{
		withResponse(me("GET", "/profile", "Your profile", func(c *httpx.Ctx, emp string) (any, error) {
			return loadProfile(c, c.App.Pool, emp)
		}), Profile{}),
		patch,
		withResponse(me("GET", "/documents", "Documents shared with you", func(c *httpx.Ctx, emp string) (any, error) {
			if err := c.RequireFeature("people.documents"); err != nil {
				return nil, err
			}
			return documents(c, c.App.Pool, emp)
		}), list[Document]{}),
		withResponse(me("GET", "/announcements", "Announcements for you, newest first", func(c *httpx.Ctx, emp string) (any, error) {
			if err := c.RequireFeature("communication.announcements"); err != nil {
				return nil, err
			}
			return announcements(c, c.App.Pool, emp)
		}), list[Announcement]{}),
		me("POST", "/announcements/{id}:acknowledge", "Acknowledge an announcement", func(c *httpx.Ctx, emp string) (any, error) {
			if err := c.RequireFeature("communication.announcements"); err != nil {
				return nil, err
			}
			visible, err := announcements(c, c.App.Pool, emp)
			if err != nil {
				return nil, err
			}
			for _, a := range visible.Data {
				if a.ID == c.Param("id") {
					if _, err := c.App.Pool.Exec(c, `INSERT INTO announcement_acks (announcement_id, employee_id) VALUES ($1, $2)
						ON CONFLICT DO NOTHING`, a.ID, emp); err != nil {
						return nil, err
					}
					return httpx.Result{Status: 204}, nil
				}
			}
			return nil, httpx.NotFound("Announcement not found.")
		}),
		withResponse(me("GET", "/activity", "Changes made to your record: who, what and when", func(c *httpx.Ctx, emp string) (any, error) {
			return activity(c, c.App.Pool, emp)
		}), list[Activity]{}),
	}
	return routes
}

func withResponse(r httpx.Route, resp any) httpx.Route {
	r.Response = resp
	return r
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullJSON(b []byte) any {
	if b == nil {
		return nil
	}
	return string(b)
}
