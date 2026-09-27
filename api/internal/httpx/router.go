package httpx

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Auth says who may call a route.
type Auth int

const (
	AuthKey         Auth = iota // any valid API key (default)
	AuthNone                    // public
	AuthIntegration             // integration keys only
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
	Auth    Auth
	Ingest  bool // counts against the higher ingestion rate limit

	Summary     string
	Description string
	Tag         string

	Query    any // prototype struct for query parameters (OpenAPI only)
	Body     any // prototype struct for the JSON body
	Response any // prototype for the success response body
	Status   int // success status, default 200

	Handler func(c *Ctx) (any, error)

	segs []segment
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
