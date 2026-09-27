package httpx

import (
	"encoding/json"
	"net/url"
)

// Lookup returns the route declared with this method and path pattern.
func (rt *Router) Lookup(method, path string) *Route {
	for _, r := range rt.routes {
		if r.Method == method && r.Path == path {
			return r
		}
	}
	return nil
}

// Delegate runs another route's handler for the caller, with the given path
// parameters, query and JSON body. The target's feature switch is checked,
// but not its permission: callers (such as the Employee Area) must pin the
// request to data the person may see, e.g. their own employee ID.
func (c *Ctx) Delegate(method, path string, params map[string]string, query url.Values, body any) (any, error) {
	target := c.App.Router.Lookup(method, path)
	if target == nil {
		return nil, Internal()
	}
	if target.Feature != "" {
		if err := c.RequireFeature(target.Feature); err != nil {
			return nil, err
		}
	}
	req := c.Req.Clone(c)
	req.URL.RawQuery = query.Encode()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	if params == nil {
		params = map[string]string{}
	}
	sub := &Ctx{Context: c.Context, App: c.App, Req: req, Route: target, Params: params,
		Principal: c.Principal, RequestID: c.RequestID, body: raw}
	return target.Handler(sub)
}

// RawBody returns the request body as sent.
func (c *Ctx) RawBody() []byte { return c.body }
