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
	ID         string     `json:"id" db:"id"`
	ParentID   *string    `json:"parentId" db:"parent_id"`
	LevelName  string     `json:"levelName" db:"level_name" doc:"e.g. Region, District"`
	Name       string     `json:"name" db:"name"`
	ExternalID *string    `json:"externalId" db:"external_id"`
	CreatedAt  time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time  `json:"updatedAt" db:"updated_at"`
	ArchivedAt *time.Time `json:"archivedAt" db:"archived_at"`
}

type OrgUnitInput struct {
	ParentID   *string `json:"parentId,omitempty" db:"parent_id" doc:"Org unit above this one; empty for a top-level unit"`
	LevelName  string  `json:"levelName" db:"level_name" validate:"required,max=50" doc:"e.g. Region, District"`
	Name       string  `json:"name" db:"name" validate:"required,max=200"`
	ExternalID *string `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
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

type LocationInput struct {
	OrgUnitID         *string         `json:"orgUnitId,omitempty" doc:"Org unit the location belongs to"`
	Name              string          `json:"name" validate:"required,max=200"`
	Code              *string         `json:"code,omitempty" validate:"omitempty,max=20" doc:"Short code, e.g. a store number"`
	ExternalID        *string         `json:"externalId,omitempty" validate:"omitempty,extid" doc:"The location's ID in your POS or other systems"`
	Timezone          string          `json:"timezone,omitempty" validate:"max=64" doc:"IANA time zone, e.g. America/Chicago (default: the company's)"`
	Currency          string          `json:"currency,omitempty" validate:"omitempty,currency" doc:"ISO 4217 code (default: the company's)"`
	BusinessDayCutoff string          `json:"businessDayCutoff,omitempty" doc:"HH:MM when the business day ends (default 00:00)"`
	Address           json.RawMessage `json:"address,omitempty" doc:"Free-form address object"`
	Status            string          `json:"status,omitempty" validate:"omitempty,oneof=open temporarily_closed closed" doc:"open (default), temporarily_closed or closed"`
}

type Department struct {
	ID         string     `json:"id" db:"id"`
	Name       string     `json:"name" db:"name"`
	ExternalID *string    `json:"externalId" db:"external_id"`
	CreatedAt  time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time  `json:"updatedAt" db:"updated_at"`
	ArchivedAt *time.Time `json:"archivedAt" db:"archived_at"`
}

type DepartmentInput struct {
	Name       string  `json:"name" db:"name" validate:"required,max=200"`
	ExternalID *string `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
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
		if _, ok := errors.AsType[*httpx.Problem](err); ok {
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

func Routes() []httpx.Route {
	routes := append(orgUnits.Routes(), departments.Routes()...)
	routes = append(routes, locationRoutes()...)
	return append(routes, accessRoutes()...)
}

type listQuery struct {
	httpx.ListParams
}
