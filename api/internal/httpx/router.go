package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Auth says who may call a route.
type Auth int

const (
	AuthKey         Auth = iota // any valid API key (default)
	AuthNone                    // public
	AuthIntegration             // integration keys only
	AuthUser                    // people only: sessions and personal API keys
	AuthSession                 // signed-in browser sessions only
)

// Special Route.Permission values.
const (
	PermSelf   = "self"   // any person; the handler only touches their own data
	PermAnyone = "anyone" // any signed-in person (shared reference data)
	PermOwner  = "owner"  // Owners only
	PermNobody = "-"      // not available to people (integration keys only)
)

// Route declares one API operation. The same declaration drives routing,
// authentication, feature and scope checks, and the OpenAPI document.
type Route struct {
	Method string
	// Path relative to /api/v1. Segments may be literals ("punches:batch"),
	// parameters ("{id}") or parameters with an action suffix ("{id}:terminate").
	Path string

	Feature string // feature key that must be enabled ("" or "core" = always on)
	Scope   string // scope an integration key needs ("" = none)
	// Permission a person needs (see PermSelf, PermOwner, PermNobody). Routes
	// with a Scope get theirs from the permission table at start-up.
	Permission string
	Auth       Auth
	// ReachOf finds the location or employee a request is about, for routes
	// whose target isn't a locationId or employeeId parameter, so people with
	// a limited reach can use them.
	ReachOf func(c *Ctx) (ReachTarget, error)
	// RecordReach means the handler checks reach itself once it has loaded
	// the record (CRUD resources with a location).
	RecordReach bool
	// AllowMFAPending lets sessions that still owe their second sign-in step
	// call the route; AllowMFAEnroll lets users who must set up 2FA call it.
	AllowMFAPending bool
	AllowMFAEnroll  bool
	// Filters are extra query parameter names the handler filters by,
	// besides those in Query.
	Filters []string
	Ingest  bool // counts against the higher ingestion rate limit

	Summary     string
	Description string
	Tag         string

	Query    any // prototype struct for query parameters (OpenAPI only)
	Body     any // prototype struct for the JSON body
	Response any // prototype for the success response body
	Status   int // success status, default 200

	Handler func(c *Ctx) (any, error)

	segs       []segment
	queryNames map[string]bool
	bodyNames  map[string]bool
}

type segment struct {
	literal string // for literal segments
	param   string // parameter name
	suffix  string // literal after the parameter, e.g. ":terminate"
}

func parsePath(path string) []segment {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	segs := make([]segment, len(parts))
	for i, p := range parts {
		if strings.HasPrefix(p, "{") {
			end := strings.IndexByte(p, '}')
			if end < 0 {
				panic("httpx: bad path " + path)
			}
			segs[i] = segment{param: p[1:end], suffix: p[end+1:]}
		} else {
			segs[i] = segment{literal: p}
		}
	}
	return segs
}

func (r *Route) match(parts []string) (map[string]string, bool) {
	if len(parts) != len(r.segs) {
		return nil, false
	}
	var params map[string]string
	for i, s := range r.segs {
		p := parts[i]
		if s.param == "" {
			if p != s.literal {
				return nil, false
			}
			continue
		}
		if !strings.HasSuffix(p, s.suffix) {
			return nil, false
		}
		v := strings.TrimSuffix(p, s.suffix)
		if v == "" || (s.suffix == "" && strings.Contains(v, ":")) {
			return nil, false
		}
		if params == nil {
			params = map[string]string{}
		}
		params[s.param] = v
	}
	return params, true
}

// specificity ranks literal segments above parameters so "/employees/external/{x}"
// wins over "/employees/{id}/{sub}"-style routes.
func (r *Route) specificity() int {
	n := 0
	for _, s := range r.segs {
		if s.param == "" {
			n += 2
		} else if s.suffix != "" {
			n++
		}
	}
	return n
}

// Router holds the registered routes.
type Router struct {
	routes []*Route
}

func (rt *Router) Add(routes ...Route) {
	for _, r := range routes {
		r := r
		if r.Status == 0 {
			r.Status = http.StatusOK
		}
		r.segs = parsePath(r.Path)
		r.queryNames = fieldNames(r.Query)
		for _, f := range r.Filters {
			r.queryNames[f] = true
		}
		r.bodyNames = fieldNames(r.Body)
		for _, existing := range rt.routes {
			if existing.Method == r.Method && existing.Path == r.Path {
				panic(fmt.Sprintf("httpx: duplicate route %s %s", r.Method, r.Path))
			}
		}
		rt.routes = append(rt.routes, &r)
	}
	sort.SliceStable(rt.routes, func(i, j int) bool {
		return rt.routes[i].specificity() > rt.routes[j].specificity()
	})
}

// SetPermissions assigns each route's Permission from lookup, for routes that
// don't declare one. It returns the routes with a Scope but no permission.
func (rt *Router) SetPermissions(lookup func(method, path string) (string, bool)) []string {
	var missing []string
	for _, r := range rt.routes {
		if r.Permission != "" {
			continue
		}
		if p, ok := lookup(r.Method, r.Path); ok {
			r.Permission = p
		} else if r.Scope != "" {
			missing = append(missing, r.Method+" "+r.Path)
		}
	}
	return missing
}

// Routes returns all routes (for OpenAPI generation).
func (rt *Router) Routes() []*Route { return rt.routes }

// find returns the matching route, or a 404/405 problem.
func (rt *Router) find(method, path string) (*Route, map[string]string, *Problem) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	methodMismatch := false
	for _, r := range rt.routes {
		params, ok := r.match(parts)
		if !ok {
			continue
		}
		if r.Method != method {
			methodMismatch = true
			continue
		}
		return r, params, nil
	}
	if methodMismatch {
		return nil, nil, MethodNotAllowed()
	}
	return nil, nil, NotFound("No such endpoint.")
}

// SetReach gives routes without a ReachOf one built from a query returning
// the record's location and employee for the {id} parameter.
func (rt *Router) SetReach(lookup func(method, path string) (string, bool)) {
	for _, r := range rt.routes {
		if r.ReachOf != nil {
			continue
		}
		sql, ok := lookup(r.Method, r.Path)
		if !ok {
			continue
		}
		r.ReachOf = func(c *Ctx) (ReachTarget, error) {
			var loc, emp *string
			err := c.App.Pool.QueryRow(c, sql, c.Params["id"]).Scan(&loc, &emp)
			if errors.Is(err, pgx.ErrNoRows) {
				return ReachTarget{}, NotFound("Not found.")
			}
			if err != nil {
				return ReachTarget{}, err
			}
			t := ReachTarget{}
			if loc != nil {
				t.LocationID = *loc
			}
			if emp != nil {
				t.EmployeeID = *emp
			}
			if t == (ReachTarget{}) {
				t.LocationID = unknownID // no location: only reach Everyone covers it
			}
			return t, nil
		}
	}
}
