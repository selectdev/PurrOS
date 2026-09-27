package httpx

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reach values (docs/admin/users-and-roles.md#reach).
const (
	ReachEveryone    = "everyone"
	ReachLocations   = "assigned_locations"
	ReachDepartments = "assigned_departments"
	ReachOwnTeam     = "own_team"
)

// ReachTarget is what a request is about, for reach checks.
type ReachTarget struct {
	LocationID string
	EmployeeID string
}

type reachLimit struct {
	perm     string
	reach    string
	deferred bool // the handler checks the record's reach (RecordReach)
	checked  bool
}

var reachWords = map[string]string{
	ReachLocations:   "your assigned locations",
	ReachDepartments: "your assigned departments",
	ReachOwnTeam:     "your own team",
}

// authorize checks a person's permission for the route and, when their reach
// is limited, that the request stays inside it.
func (c *Ctx) authorize() error {
	u := c.Principal.User
	perm := c.Route.Permission
	switch {
	case perm == "" && c.Route.Scope == "":
		return nil // platform endpoints any caller may use
	case perm == PermSelf || perm == PermAnyone:
		return nil
	case perm == PermNobody:
		return Forbidden("This endpoint is only available to integration keys.")
	case u.Owner:
		return nil
	case perm == PermOwner || perm == "":
		return Forbidden("Only an Owner can do this.")
	}
	reach, ok := u.Perms[perm]
	if !ok {
		return Forbidden("Your role doesn't have the " + perm + " permission.")
	}
	if reach == ReachEveryone {
		return nil
	}
	c.limit = &reachLimit{perm: perm, reach: reach}

	var targets []ReachTarget
	if c.Route.ReachOf != nil {
		t, err := c.Route.ReachOf(c)
		if err != nil {
			return err
		}
		targets = append(targets, t)
	}
	targets = append(targets, c.requestTargets()...)
	if len(targets) == 0 {
		if c.Route.RecordReach {
			c.limit.deferred = true
			return nil
		}
		return OutOfReach("Your " + perm + " permission covers " + reachWords[reach] +
			" only. Narrow this request with locationId (or employeeId).")
	}
	for _, t := range targets {
		if err := c.checkTarget(t); err != nil {
			return err
		}
	}
	c.limit.checked = true
	return nil
}

// requestTargets finds locationId and employeeId values in the path, in
// query parameters the route filters by, and in the top level of the body.
func (c *Ctx) requestTargets() []ReachTarget {
	var out []ReachTarget
	add := func(kind, v string) {
		if v == "" {
			return
		}
		if kind == "location" {
			out = append(out, ReachTarget{LocationID: v})
		} else {
			out = append(out, ReachTarget{EmployeeID: v})
		}
	}
	r := c.Route
	add("location", c.Params["locationId"])
	add("employee", c.Params["employeeId"])
	if strings.HasPrefix(r.Path, "/locations/{id}") {
		add("location", c.Params["id"])
	}
	if strings.HasPrefix(r.Path, "/employees/{id}") {
		add("employee", c.Params["id"])
	}
	if r.queryNames["locationId"] {
		add("location", c.Query("locationId"))
	}
	if r.queryNames["employeeId"] {
		add("employee", c.Query("employeeId"))
	}
	if len(c.body) > 0 && (r.bodyNames["locationId"] || r.bodyNames["employeeId"] ||
		r.bodyNames["locationExternalId"] || r.bodyNames["employeeExternalId"]) {
		var top map[string]json.RawMessage
		if json.Unmarshal(c.body, &top) == nil {
			str := func(k string) string {
				var s string
				if r.bodyNames[k] {
					_ = json.Unmarshal(top[k], &s)
				}
				return s
			}
			add("location", str("locationId"))
			add("employee", str("employeeId"))
			// External IDs are resolved here; an unknown one can't be in reach.
			if ext := str("locationExternalId"); ext != "" {
				id := unknownID
				_ = c.App.Pool.QueryRow(c, `SELECT id FROM locations WHERE external_id = $1`, ext).Scan(&id)
				add("location", id)
			}
			if ext := str("employeeExternalId"); ext != "" {
				id := unknownID
				_ = c.App.Pool.QueryRow(c, `SELECT id FROM employees WHERE external_id = $1`, ext).Scan(&id)
				add("employee", id)
			}
		}
	}
	return out
}

// unknownID stands in for an external ID that matched nothing.
const unknownID = "unknown"

func (c *Ctx) checkTarget(t ReachTarget) error {
	if t.LocationID != "" {
		if err := c.CheckLocationReach(t.LocationID); err != nil {
			return err
		}
	}
	if t.EmployeeID != "" {
		if err := c.CheckEmployeeReach(t.EmployeeID); err != nil {
			return err
		}
	}
	return nil
}

// Limited reports whether the caller's reach for this route is narrower than
// everyone.
func (c *Ctx) Limited() bool { return c.limit != nil }

// CheckLocationReach returns out_of_reach unless the caller may act on the
// location. It is a no-op for callers without a limit.
func (c *Ctx) CheckLocationReach(locationID string) error {
	if c.limit == nil {
		return nil
	}
	c.limit.checked = true
	u := c.Principal.User
	if c.limit.reach == ReachLocations && locationID != "" && u.LocationIDs[locationID] {
		return nil
	}
	if locationID == "" {
		return OutOfReach("Your " + c.limit.perm + " permission covers " + reachWords[c.limit.reach] + " only, and this record has no location.")
	}
	return OutOfReach("This location is outside " + reachWords[c.limit.reach] + ".")
}

// CheckEmployeeReach returns out_of_reach unless the caller may act on the
// employee: at an assigned location, in an assigned department, or reporting
// to them (directly or indirectly).
func (c *Ctx) CheckEmployeeReach(employeeID string) error {
	if c.limit == nil {
		return nil
	}
	c.limit.checked = true
	u := c.Principal.User
	var loc, dep *string
	var inTeam bool
	err := c.App.Pool.QueryRow(c, `
		WITH RECURSIVE chain AS (
			SELECT manager_id AS id, 1 AS depth FROM employees WHERE id = $1
			UNION ALL SELECT e.manager_id, chain.depth + 1 FROM employees e JOIN chain ON e.id = chain.id
			WHERE chain.depth < 50
		)
		SELECT home_location_id, department_id,
		       $2 <> '' AND EXISTS (SELECT 1 FROM chain WHERE id = $2)
		FROM employees WHERE id = $1`, employeeID, u.EmployeeID).Scan(&loc, &dep, &inTeam)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutOfReach("This employee is outside " + reachWords[c.limit.reach] + ".")
	}
	if err != nil {
		return err
	}
	switch c.limit.reach {
	case ReachLocations:
		if loc != nil && u.LocationIDs[*loc] {
			return nil
		}
	case ReachDepartments:
		if dep != nil && u.DepartmentIDs[*dep] {
			return nil
		}
	case ReachOwnTeam:
		if inTeam {
			return nil
		}
	}
	return OutOfReach("This employee is outside " + reachWords[c.limit.reach] + ".")
}

// fieldNames returns the JSON names of a struct prototype's fields, including
// embedded structs.
func fieldNames(proto any) map[string]bool {
	out := map[string]bool{}
	if proto == nil {
		return out
	}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return
		}
		for i := range t.NumField() {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			name, _, _ := strings.Cut(tag, ",")
			if f.Anonymous && name == "" {
				walk(f.Type)
				continue
			}
			if name == "-" || !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			out[name] = true
		}
	}
	walk(reflect.TypeOf(proto))
	return out
}

// Has reports whether the user holds a permission with any reach.
func (u *UserAccess) Has(perm string) bool {
	if u == nil {
		return false
	}
	_, ok := u.Perms[perm]
	return u.Owner || ok
}

// HasEveryone reports whether the user holds a permission company-wide.
func (u *UserAccess) HasEveryone(perm string) bool {
	return u != nil && (u.Owner || u.Perms[perm] == ReachEveryone)
}

// RequireEveryone checks a company-wide permission inside a handler, for
// actions that have no location or employee to measure reach against.
func (c *Ctx) RequireEveryone(perm string) error {
	u := c.Principal.User
	if u == nil {
		return Forbidden("This endpoint is only available to people.")
	}
	if u.HasEveryone(perm) {
		return nil
	}
	if u.Has(perm) {
		return OutOfReach("This needs the " + perm + " permission with reach Everyone.")
	}
	return Forbidden("Your role doesn't have the " + perm + " permission.")
}

// RateLimit applies an extra limit to a key (e.g. sign-in attempts per IP).
func (c *Ctx) RateLimit(key string, limit int, window time.Duration) error {
	if c.App.Limiter == nil {
		return nil
	}
	res, err := c.App.Limiter.Allow(c, key, limit, window)
	if err != nil {
		c.App.Log.Warn("rate limiter unavailable", "err", err)
		return nil
	}
	if !res.Allowed {
		return RateLimited(res.ResetSeconds)
	}
	return nil
}

// CheckPermissionReach checks, inside a handler, that a person holds perm
// with a reach covering a record's location or employee. A record with
// neither needs reach Everyone.
func (c *Ctx) CheckPermissionReach(perm, locationID, employeeID string) error {
	u := c.Principal.User
	if u == nil {
		return Forbidden("This endpoint is only available to people.")
	}
	if u.Owner {
		return nil
	}
	reach, ok := u.Perms[perm]
	if !ok {
		return Forbidden("Your role doesn't have the " + perm + " permission.")
	}
	if reach == ReachEveryone {
		return nil
	}
	saved := c.limit
	c.limit = &reachLimit{perm: perm, reach: reach}
	defer func() { c.limit = saved }()
	switch {
	case employeeID != "":
		if err := c.CheckEmployeeReach(employeeID); err == nil || locationID == "" {
			return err
		}
		return c.CheckLocationReach(locationID)
	case locationID != "":
		return c.CheckLocationReach(locationID)
	}
	return OutOfReach("Your " + perm + " permission covers " + reachWords[reach] + " only, and this record isn't tied to a location or employee.")
}
