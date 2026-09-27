// Package organization serves the hierarchy, locations and departments.
package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
)

type OrgUnit struct {
	ID         string     `json:"id"`
	ParentID   *string    `json:"parentId"`
	LevelName  string     `json:"levelName" doc:"e.g. Region, District"`
	Name       string     `json:"name"`
	ExternalID *string    `json:"externalId"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ArchivedAt *time.Time `json:"archivedAt"`
}

type Location struct {
	ID                string          `json:"id"`
	OrgUnitID         *string         `json:"orgUnitId"`
	Name              string          `json:"name"`
	Code              *string         `json:"code"`
	ExternalID        *string         `json:"externalId"`
	Timezone          string          `json:"timezone"`
	Currency          string          `json:"currency"`
	BusinessDayCutoff string          `json:"businessDayCutoff" doc:"Time of day the business day ends, e.g. 04:00"`
	Address           json.RawMessage `json:"address"`
	Status            string          `json:"status" validate:"oneof=open temporarily_closed closed"`
	Version           int             `json:"version"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
	ArchivedAt        *time.Time      `json:"archivedAt"`
	cutoffSeconds     int
}

type Department struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ExternalID *string    `json:"externalId"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ArchivedAt *time.Time `json:"archivedAt"`
}

// BusinessDate returns the location's business day for an instant, applying
// its time zone and business-day cut-off (a sale at 01:30 in a bar whose day
// ends at 04:00 belongs to the previous day).
func (l Location) BusinessDate(at time.Time) httpx.Date {
	tz, err := time.LoadLocation(l.Timezone)
	if err != nil {
		tz = time.UTC
	}
	local := at.In(tz).Add(-time.Duration(l.cutoffSeconds) * time.Second)
	return httpx.NewDate(local)
}

const locationCols = `id, org_unit_id, name, code, external_id, timezone, currency,
	to_char(business_day_cutoff, 'HH24:MI'), extract(epoch from business_day_cutoff)::int,
	coalesce(address, 'null'::jsonb), status, version, created_at, updated_at, archived_at`

func scanLocation(row pgx.Row) (Location, error) {
	var l Location
	var addr []byte
	err := row.Scan(&l.ID, &l.OrgUnitID, &l.Name, &l.Code, &l.ExternalID, &l.Timezone, &l.Currency,
		&l.BusinessDayCutoff, &l.cutoffSeconds, &addr, &l.Status, &l.Version, &l.CreatedAt, &l.UpdatedAt, &l.ArchivedAt)
	l.Address = addr
	return l, err
}

// GetLocation loads a location by ID or external ID.
func GetLocation(ctx context.Context, q db.Querier, id, externalID string) (Location, error) {
	var row pgx.Row
	switch {
	case id != "":
		row = q.QueryRow(ctx, `SELECT `+locationCols+` FROM locations WHERE id = $1`, id)
	case externalID != "":
		row = q.QueryRow(ctx, `SELECT `+locationCols+` FROM locations WHERE external_id = $1`, externalID)
	default:
		return Location{}, errors.New("location id or external id required")
	}
	l, err := scanLocation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, httpx.NotFound("Location not found.")
	}
	return l, err
}

// LocationResolver caches location lookups during a batch.
type LocationResolver struct {
	q     db.Querier
	byID  map[string]Location
	byExt map[string]Location
}

func NewLocationResolver(q db.Querier) *LocationResolver {
	return &LocationResolver{q: q, byID: map[string]Location{}, byExt: map[string]Location{}}
}

// Resolve returns the location for an ID or external ID, or a descriptive error.
func (r *LocationResolver) Resolve(ctx context.Context, id, externalID string) (Location, error) {
	if l, ok := r.byID[id]; ok && id != "" {
		return l, nil
	}
	if l, ok := r.byExt[externalID]; ok && externalID != "" {
		return l, nil
	}
	if id == "" && externalID == "" {
		return Location{}, fmt.Errorf("locationId or locationExternalId is required")
	}
	l, err := GetLocation(ctx, r.q, id, externalID)
	if err != nil {
		var p *httpx.Problem
		if errors.As(err, &p) {
			if id != "" {
				return l, fmt.Errorf("unknown locationId %q", id)
			}
			return l, fmt.Errorf("unknown locationExternalId %q", externalID)
		}
		return l, err
	}
	r.byID[l.ID] = l
	if l.ExternalID != nil {
		r.byExt[*l.ExternalID] = l
	}
	return l, nil
}

type listQuery struct {
	httpx.ListParams
}

func Routes() []httpx.Route {
	return append([]httpx.Route{
		{
			Method: "GET", Path: "/org-units", Tag: "Organization", Scope: "organization:read",
			Summary: "List org units (regions, districts…)", Query: listQuery{}, Response: httpx.Page[OrgUnit]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `
					SELECT id, parent_id, level_name, name, external_id, created_at, updated_at, archived_at
					FROM org_units WHERE id > $1 AND ($2::timestamptz IS NULL OR updated_at >= $2)
					ORDER BY id LIMIT $3`, lp.AfterID, lp.UpdatedSince, lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (OrgUnit, error) {
					var o OrgUnit
					err := r.Scan(&o.ID, &o.ParentID, &o.LevelName, &o.Name, &o.ExternalID, &o.CreatedAt, &o.UpdatedAt, &o.ArchivedAt)
					return o, err
				})
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(o OrgUnit) string { return o.ID }), nil
			},
		},
		{
			Method: "GET", Path: "/locations", Tag: "Organization", Scope: "organization:read",
			Summary: "List locations", Query: listQuery{}, Response: httpx.Page[Location]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `SELECT `+locationCols+` FROM locations
					WHERE id > $1 AND ($2::timestamptz IS NULL OR updated_at >= $2)
					ORDER BY id LIMIT $3`, lp.AfterID, lp.UpdatedSince, lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Location, error) { return scanLocation(r) })
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(l Location) string { return l.ID }), nil
			},
		},
		{
			Method: "GET", Path: "/locations/{id}", Tag: "Organization", Scope: "organization:read",
			Summary: "Get a location", Response: Location{},
			Handler: func(c *httpx.Ctx) (any, error) { return GetLocation(c, c.App.Pool, c.Param("id"), "") },
		},
		{
			Method: "GET", Path: "/locations/external/{externalId}", Tag: "Organization", Scope: "organization:read",
			Summary: "Get a location by external ID", Response: Location{},
			Handler: func(c *httpx.Ctx) (any, error) { return GetLocation(c, c.App.Pool, "", c.Param("externalId")) },
		},
		{
			Method: "GET", Path: "/departments", Tag: "Organization", Scope: "organization:read",
			Summary: "List departments", Query: listQuery{}, Response: httpx.Page[Department]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `
					SELECT id, name, external_id, created_at, updated_at, archived_at FROM departments
					WHERE id > $1 AND ($2::timestamptz IS NULL OR updated_at >= $2)
					ORDER BY id LIMIT $3`, lp.AfterID, lp.UpdatedSince, lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Department, error) {
					var d Department
					err := r.Scan(&d.ID, &d.Name, &d.ExternalID, &d.CreatedAt, &d.UpdatedAt, &d.ArchivedAt)
					return d, err
				})
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(d Department) string { return d.ID }), nil
			},
		},
	}, accessRoutes()...)
}
