package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/selectdev/purros/api/internal/crud"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
)

const tag = "Organization"

var orgUnits = &crud.Resource[OrgUnit, OrgUnitInput]{
	Path: "/org-units", Table: "org_units", Prefix: ids.OrgUnit, Noun: "org_unit", Tag: tag,
	Feature: "core", ReadScope: "organization:read", WriteScope: "organization:write", External: true, Archive: true,
	Filters:      []crud.Filter{{Query: "parentId", Column: "parent_id"}},
	CreatedEvent: "org_unit.created", UpdatedEvent: "org_unit.updated", ArchivedEvent: "org_unit.archived",
	Check: func(c *httpx.Ctx, q db.Querier, in *OrgUnitInput, before *OrgUnit) error {
		if in.ParentID == nil || *in.ParentID == "" {
			in.ParentID = nil
			return nil
		}
		self := ""
		if before != nil {
			self = before.ID
		}
		return checkParent(c, q, *in.ParentID, self)
	},
	BeforeArchive: func(c *httpx.Ctx, tx pgx.Tx, o *OrgUnit) error {
		var units, locs int
		if err := tx.QueryRow(c, `
			SELECT (SELECT count(*) FROM org_units WHERE parent_id = $1 AND archived_at IS NULL),
			       (SELECT count(*) FROM locations WHERE org_unit_id = $1 AND archived_at IS NULL)`, o.ID).
			Scan(&units, &locs); err != nil {
			return err
		}
		if units > 0 || locs > 0 {
			return httpx.Conflict(fmt.Sprintf("This org unit still contains %d org unit(s) and %d location(s). Move or archive them first.", units, locs))
		}
		return nil
	},
}

// checkParent makes sure a parent org unit exists, isn't archived, and isn't
// the unit itself or one of its descendants.
func checkParent(ctx context.Context, q db.Querier, parentID, self string) error {
	var archived *time.Time
	err := q.QueryRow(ctx, `SELECT archived_at FROM org_units WHERE id = $1`, parentID).Scan(&archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Validation(httpx.FieldError{Path: "parentId", Message: "Unknown org unit"})
	}
	if err != nil {
		return err
	}
	if archived != nil {
		return httpx.Validation(httpx.FieldError{Path: "parentId", Message: "This org unit is archived"})
	}
	if self == "" {
		return nil
	}
	var cycle bool
	err = q.QueryRow(ctx, `
		WITH RECURSIVE up AS (
			SELECT id, parent_id FROM org_units WHERE id = $1
			UNION ALL
			SELECT o.id, o.parent_id FROM org_units o JOIN up ON o.id = up.parent_id
		)
		SELECT EXISTS (SELECT 1 FROM up WHERE id = $2)`, parentID, self).Scan(&cycle)
	if err != nil {
		return err
	}
	if cycle {
		return httpx.Validation(httpx.FieldError{Path: "parentId", Message: "An org unit can't be placed under itself or one of its own units"})
	}
	return nil
}

var departments = &crud.Resource[Department, DepartmentInput]{
	Path: "/departments", Table: "departments", Prefix: ids.Department, Noun: "department", Tag: tag,
	Feature: "core", ReadScope: "organization:read", WriteScope: "organization:write", External: true, Archive: true,
	CreatedEvent: "department.created", UpdatedEvent: "department.updated", ArchivedEvent: "department.archived",
}

// normalize fills in defaults and checks what the validator can't.
func (in *LocationInput) normalize(ctx context.Context, q db.Querier) (cutoff time.Time, err error) {
	if in.Timezone == "" || in.Currency == "" {
		// Default to the company's, or UTC and USD before setup.
		tz, cur := "UTC", "USD"
		err := q.QueryRow(ctx, `SELECT timezone, currency FROM company LIMIT 1`).Scan(&tz, &cur)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return cutoff, err
		}
		if in.Timezone == "" {
			in.Timezone = tz
		}
		if in.Currency == "" {
			in.Currency = cur
		}
	}
	if in.BusinessDayCutoff == "" {
		in.BusinessDayCutoff = "00:00"
	}
	if in.Status == "" {
		in.Status = "open"
	}
	if in.OrgUnitID != nil && *in.OrgUnitID == "" {
		in.OrgUnitID = nil
	}
	if in.Code != nil && *in.Code == "" {
		in.Code = nil
	}
	if in.ExternalID != nil && *in.ExternalID == "" {
		in.ExternalID = nil
	}
	if len(in.Address) == 0 || string(in.Address) == "null" {
		in.Address = nil
	}
	var errs []httpx.FieldError
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "Local" {
		errs = append(errs, httpx.FieldError{Path: "timezone", Message: "Unknown time zone; use an IANA name such as America/Chicago"})
	}
	cutoff, err = time.Parse("15:04", in.BusinessDayCutoff)
	if err != nil {
		errs = append(errs, httpx.FieldError{Path: "businessDayCutoff", Message: "Must be HH:MM, e.g. 04:00"})
	}
	if len(errs) > 0 {
		return cutoff, httpx.Validation(errs...)
	}
	if in.OrgUnitID != nil {
		if err := checkParent(ctx, q, *in.OrgUnitID, ""); err != nil {
			if p, ok := errors.AsType[*httpx.Problem](err); ok && len(p.Errors) > 0 {
				p.Errors[0].Path = "orgUnitId"
			}
			return cutoff, err
		}
	}
	return cutoff, nil
}

func locationWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return httpx.Conflict("Another location already has this externalId.")
	}
	return err
}

// InsertLocation creates a location. The caller records the change.
func InsertLocation(ctx context.Context, q db.Querier, in LocationInput) (Location, error) {
	cutoff, err := in.normalize(ctx, q)
	if err != nil {
		return Location{}, err
	}
	id := ids.New(ids.Location)
	_, err = q.Exec(ctx, `
		INSERT INTO locations (id, org_unit_id, name, code, external_id, timezone, currency, business_day_cutoff, address, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, make_interval(hours => $8, mins => $9), $10, $11)`,
		id, in.OrgUnitID, in.Name, in.Code, in.ExternalID, in.Timezone, in.Currency,
		cutoff.Hour(), cutoff.Minute(), []byte(in.Address), in.Status)
	if err != nil {
		return Location{}, locationWriteError(err)
	}
	return GetLocation(ctx, q, id, "")
}

// UpdateLocation replaces a location's writable fields. The caller records the change.
func UpdateLocation(ctx context.Context, q db.Querier, id string, in LocationInput) (Location, error) {
	cutoff, err := in.normalize(ctx, q)
	if err != nil {
		return Location{}, err
	}
	_, err = q.Exec(ctx, `
		UPDATE locations SET org_unit_id = $2, name = $3, code = $4, external_id = $5, timezone = $6, currency = $7,
			business_day_cutoff = make_interval(hours => $8, mins => $9), address = $10, status = $11,
			version = version + 1, updated_at = now()
		WHERE id = $1`,
		id, in.OrgUnitID, in.Name, in.Code, in.ExternalID, in.Timezone, in.Currency,
		cutoff.Hour(), cutoff.Minute(), []byte(in.Address), in.Status)
	if err != nil {
		return Location{}, locationWriteError(err)
	}
	return GetLocation(ctx, q, id, "")
}

func (l Location) input() LocationInput {
	in := LocationInput{OrgUnitID: l.OrgUnitID, Name: l.Name, Code: l.Code, ExternalID: l.ExternalID,
		Timezone: l.Timezone, Currency: l.Currency, BusinessDayCutoff: l.BusinessDayCutoff, Status: l.Status}
	if string(l.Address) != "null" {
		in.Address = l.Address
	}
	return in
}

func lockLocation(c *httpx.Ctx, tx pgx.Tx, id string) (Location, error) {
	l, err := scanLocation(tx.QueryRow(c, `SELECT `+locationCols+` FROM locations WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return l, httpx.NotFound("Location not found.")
	}
	return l, err
}

func recordLocation(c *httpx.Ctx, tx pgx.Tx, action, event string, before *Location, after Location) error {
	ch := httpx.Change{Action: "location." + action, EventType: event, Feature: "core",
		EntityType: "location", EntityID: after.ID, LocationID: after.ID, After: after}
	if before != nil {
		ch.Before = *before
	}
	return c.Record(tx, ch)
}

type locationListQuery struct {
	httpx.ListParams
	OrgUnitID       string `json:"orgUnitId,omitempty" doc:"Only locations in this org unit (not its sub-units)"`
	Status          string `json:"status,omitempty" doc:"open, temporarily_closed or closed"`
	IncludeArchived bool   `json:"includeArchived,omitempty" doc:"Include archived locations"`
}

func locationRoutes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "GET", Path: "/locations", Tag: tag, Scope: "organization:read",
			Summary: "List locations", Query: locationListQuery{}, Response: httpx.Page[Location]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `SELECT `+locationCols+` FROM locations
					WHERE id > $1 AND ($2::timestamptz IS NULL OR updated_at >= $2)
					  AND ($3 = '' OR org_unit_id = $3) AND ($4 = '' OR status = $4)
					  AND ($5 OR archived_at IS NULL)
					ORDER BY id LIMIT $6`,
					lp.AfterID, lp.UpdatedSince, c.Query("orgUnitId"), c.Query("status"),
					c.Query("includeArchived") == "true", lp.Limit+1)
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
			Method: "GET", Path: "/locations/{id}", Tag: tag, Scope: "organization:read",
			Summary: "Get a location", Response: Location{},
			Handler: func(c *httpx.Ctx) (any, error) { return GetLocation(c, c.App.Pool, c.Param("id"), "") },
		},
		{
			Method: "GET", Path: "/locations/external/{externalId}", Tag: tag, Scope: "organization:read",
			Summary: "Get a location by external ID", Response: Location{},
			Handler: func(c *httpx.Ctx) (any, error) { return GetLocation(c, c.App.Pool, "", c.Param("externalId")) },
		},
		{
			Method: "POST", Path: "/locations", Tag: tag, Scope: "organization:write",
			Summary: "Create a location", Body: LocationInput{}, Response: Location{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in LocationInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Location
				err := c.InTx(func(tx pgx.Tx) (err error) {
					if out, err = InsertLocation(c, tx, in); err != nil {
						return err
					}
					return recordLocation(c, tx, "create", "location.created", nil, out)
				})
				return out, err
			},
		},
		{
			Method: "PATCH", Path: "/locations/{id}", Tag: tag, Scope: "organization:write",
			Summary:     "Update a location (partial)",
			Description: "Send `If-Match: <version>` to update only if nobody changed it in the meantime.",
			Body:        LocationInput{}, Response: Location{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out Location
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := lockLocation(c, tx, c.Param("id"))
					if err != nil {
						return err
					}
					if h := strings.Trim(c.Req.Header.Get("If-Match"), `"`); h != "" && h != fmt.Sprint(before.Version) {
						return httpx.VersionMismatch()
					}
					in, err := httpx.MergePatch(c, before.input())
					if err != nil {
						return err
					}
					if out, err = UpdateLocation(c, tx, before.ID, in); err != nil {
						return err
					}
					return recordLocation(c, tx, "update", "location.updated", &before, out)
				})
				return out, err
			},
		},
		{
			Method: "PUT", Path: "/locations/external/{externalId}", Tag: tag, Scope: "organization:write",
			Summary:     "Create or replace a location by external ID",
			Description: "Returns 201 when created and 200 when updated.",
			Body:        LocationInput{}, Response: Location{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in LocationInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				ext := c.Param("externalId")
				in.ExternalID = &ext
				var out Location
				created := false
				err := c.InTx(func(tx pgx.Tx) error {
					var id string
					err := tx.QueryRow(c, `SELECT id FROM locations WHERE external_id = $1 FOR UPDATE`, ext).Scan(&id)
					if errors.Is(err, pgx.ErrNoRows) {
						created = true
						if out, err = InsertLocation(c, tx, in); err != nil {
							return err
						}
						return recordLocation(c, tx, "create", "location.created", nil, out)
					}
					if err != nil {
						return err
					}
					before, err := GetLocation(c, tx, id, "")
					if err != nil {
						return err
					}
					if out, err = UpdateLocation(c, tx, id, in); err != nil {
						return err
					}
					return recordLocation(c, tx, "update", "location.updated", &before, out)
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
			Method: "DELETE", Path: "/locations/{id}", Tag: tag, Scope: "organization:write",
			Summary:     "Archive a location",
			Description: "Soft delete: the location is marked closed and archived, and all its history is kept.",
			Response:    Location{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out Location
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := lockLocation(c, tx, c.Param("id"))
					if err != nil {
						return err
					}
					if _, err := tx.Exec(c, `UPDATE locations SET status = 'closed', archived_at = coalesce(archived_at, now()),
						version = version + 1, updated_at = now() WHERE id = $1`, before.ID); err != nil {
						return err
					}
					if out, err = GetLocation(c, tx, before.ID, ""); err != nil {
						return err
					}
					return recordLocation(c, tx, "archive", "location.archived", &before, out)
				})
				return out, err
			},
		},
	}
}
