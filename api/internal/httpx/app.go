package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/secure"
	"github.com/selectdev/purros/api/internal/storage"
)

const (
	APIPrefix    = "/api/v1"
	maxBodyBytes = 10 << 20 // 10 MiB
)

// App holds the dependencies shared by every handler.
type App struct {
	Config   config.Config
	Pool     *pgxpool.Pool
	Features *features.Store
	Box      *secure.Box
	Limiter  Limiter
	Log      *slog.Logger
	Router   *Router
	Now      func() time.Time
	Storage  storage.Driver
}

// Principal is the authenticated caller.
type Principal struct {
	KeyID                  string
	Kind                   string // "integration" | "personal" (a user's API key) | "session"
	IntegrationID          string
	IntegrationName        string // manifest name, also its integrationData namespace
	IntegrationDisplayName string
	UserID                 string
	Scopes                 []string // integration keys only

	SessionID  string
	MFAPending bool        // signed in with a password, second step still due
	User       *UserAccess // personal keys and sessions
}

func (p *Principal) HasScope(s string) bool { return p != nil && slices.Contains(p.Scopes, s) }

// Ctx is passed to handlers.
type Ctx struct {
	context.Context
	App       *App
	Req       *http.Request
	Route     *Route
	Params    map[string]string
	Principal *Principal
	RequestID string
	body      []byte

	// limit is set when the caller holds the route's permission with a
	// reach narrower than everyone.
	limit *reachLimit
}

func (c *Ctx) Param(name string) string { return c.Params[name] }

func (c *Ctx) Query(name string) string { return c.Req.URL.Query().Get(name) }

func (c *Ctx) ClientIP() string {
	if c.App.Config.TrustProxy {
		if xff := c.Req.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(c.Req.RemoteAddr)
	if err != nil {
		return c.Req.RemoteAddr
	}
	return host
}

// Decode parses the JSON body into v and validates it.
func (c *Ctx) Decode(v any) error {
	if len(c.body) == 0 {
		return BadRequest("A JSON request body is required.")
	}
	if err := json.Unmarshal(c.body, v); err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			return Validation(FieldError{Path: ute.Field, Message: "Must be of type " + ute.Type.String()})
		}
		return BadRequest("Malformed JSON: " + err.Error())
	}
	return Validate(v)
}

// DecodeOptional decodes the body into v if one was sent; an empty body is fine.
func (c *Ctx) DecodeOptional(v any) error {
	if len(c.body) == 0 {
		return nil
	}
	return c.Decode(v)
}

// InTx runs fn in a database transaction.
func (c *Ctx) InTx(fn func(tx pgx.Tx) error) error {
	tx, err := c.App.Pool.Begin(c)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(c) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(c)
}

// RequireFeature returns a feature_disabled problem if key is off. Use it for
// sub-features checked inside a handler.
func (c *Ctx) RequireFeature(key string) error {
	ok, err := c.App.Features.IsEnabled(c, key)
	if err != nil {
		return err
	}
	if !ok {
		return FeatureDisabled(key)
	}
	return nil
}

// Raw is a non-JSON response body, such as a CSV export or a PDF.
type Raw struct {
	ContentType string
	Filename    string
	Body        []byte
}

// Stream is a streamed response body, such as a downloaded file.
type Stream struct {
	ContentType string
	Filename    string
	Inline      bool // show in the browser rather than download
	Size        int64
	Body        io.ReadCloser
}

// Redirect sends the client to another URL (e.g. a signed download URL).
type Redirect struct{ URL string }

// Result lets a handler choose the status code or add headers.
type Result struct {
	Status  int
	Body    any
	Headers map[string]string
}

// ServeHTTP handles /api/v1/*.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if reqID == "" || len(reqID) > 100 {
		reqID = ids.New(ids.Request)
	}
	w.Header().Set("X-Request-Id", reqID)
	start := time.Now()
	status := 0

	defer func() {
		if rec := recover(); rec != nil {
			a.Log.Error("panic", "requestId", reqID, "panic", rec, "stack", string(debug.Stack()))
			status = writeProblem(w, Internal(), reqID)
		}
		a.Log.Info("request", "requestId", reqID, "method", r.Method, "path", r.URL.Path,
			"status", status, "durationMs", time.Since(start).Milliseconds())
	}()

	status = a.serve(w, r, reqID)
}

func (a *App) serve(w http.ResponseWriter, r *http.Request, reqID string) int {
	path := strings.TrimPrefix(r.URL.Path, APIPrefix)
	route, params, prob := a.Router.find(r.Method, path)
	if prob != nil {
		return writeProblem(w, prob, reqID)
	}

	c := &Ctx{Context: r.Context(), App: a, Req: r, Route: route, Params: params, RequestID: reqID}

	// 1. Authenticate.
	if route.Auth != AuthNone {
		p, prob := a.authenticate(c)
		if prob != nil {
			return writeProblem(w, prob, reqID)
		}
		switch {
		case route.Auth == AuthIntegration && p.Kind != "integration":
			return writeProblem(w, Forbidden("This endpoint is only available to integration keys."), reqID)
		case route.Auth == AuthUser && p.User == nil:
			return writeProblem(w, Forbidden("This endpoint is only available to people (a session or personal API key)."), reqID)
		case route.Auth == AuthSession && p.Kind != "session":
			return writeProblem(w, Forbidden("This endpoint needs a signed-in session."), reqID)
		case p.MFAPending && !route.AllowMFAPending:
			return writeProblem(w, MFARequired(), reqID)
		case p.User != nil && p.User.MFAEnrollRequired && !route.AllowMFAPending && !route.AllowMFAEnroll:
			return writeProblem(w, MFAEnrollmentRequired(), reqID)
		}
		c.Principal = p
	}

	// 2. Feature switch.
	if route.Feature != "" {
		ok, err := a.Features.IsEnabled(c, route.Feature)
		if err != nil {
			return a.fail(w, c, err)
		}
		if !ok {
			return writeProblem(w, FeatureDisabled(route.Feature), reqID)
		}
	}

	// 5. Body (read before access checks, which may look at it). Streaming
	// routes (uploads) read it themselves.
	if r.Body != nil && r.Method != http.MethodGet && !route.Stream {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			return writeProblem(w, BadRequest("Request body too large or unreadable."), reqID)
		}
		c.body = body
	}

	// 3. Scope (integrations) or permission and reach (people).
	if c.Principal != nil {
		if c.Principal.User == nil {
			if route.Scope != "" && !c.Principal.HasScope(route.Scope) {
				return writeProblem(w, Forbidden("This API key lacks the "+route.Scope+" scope."), reqID)
			}
		} else if err := c.authorize(); err != nil {
			return a.fail(w, c, err)
		}
	}

	// 4. Rate limit.
	if c.Principal != nil && a.Limiter != nil {
		limit, class := a.Config.RateLimitPerMin, "default"
		if route.Ingest {
			limit, class = a.Config.IngestRateLimitPerMin, "ingest"
		}
		res, err := a.Limiter.Allow(c, c.Principal.KeyID+":"+class, limit, time.Minute)
		if err != nil {
			a.Log.Warn("rate limiter unavailable", "err", err)
		} else {
			w.Header().Set("RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("RateLimit-Remaining", strconv.Itoa(res.Remaining))
			w.Header().Set("RateLimit-Reset", strconv.Itoa(res.ResetSeconds))
			if !res.Allowed {
				w.Header().Set("Retry-After", strconv.Itoa(res.ResetSeconds))
				return writeProblem(w, RateLimited(res.ResetSeconds), reqID)
			}
		}
	}

	// 6. Idempotency (POST only).
	idemKey := r.Header.Get("Idempotency-Key")
	if route.Stream {
		idemKey = "" // the body isn't buffered, so replays can't be matched
	}
	if r.Method == http.MethodPost && idemKey != "" && c.Principal != nil {
		if len(idemKey) > 255 {
			return writeProblem(w, BadRequest("Idempotency-Key must be at most 255 characters."), reqID)
		}
		replay, prob, err := a.idempotencyLookup(c, idemKey)
		if err != nil {
			return a.fail(w, c, err)
		}
		if prob != nil {
			return writeProblem(w, prob, reqID)
		}
		if replay != nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(replay.status)
			_, _ = w.Write(replay.body)
			return replay.status
		}
	}

	// 7. Handle.
	out, err := route.Handler(c)
	if err != nil {
		return a.fail(w, c, err)
	}
	if c.limit != nil && c.limit.deferred && !c.limit.checked {
		a.Log.Error("reach not checked", "path", route.Path, "requestId", reqID)
		return writeProblem(w, OutOfReach("This request can't be limited to your reach."), reqID)
	}
	if st, ok := out.(Stream); ok {
		defer st.Body.Close()
		w.Header().Set("Content-Type", st.ContentType)
		w.Header().Set("Content-Disposition", ContentDisposition(st.Filename, st.Inline))
		if st.Size >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(st.Size, 10))
		}
		w.WriteHeader(route.Status)
		_, _ = io.Copy(w, st.Body)
		return route.Status
	}
	if rd, ok := out.(Redirect); ok {
		w.Header().Set("Location", rd.URL)
		w.WriteHeader(http.StatusFound)
		return http.StatusFound
	}
	if raw, ok := out.(Raw); ok {
		w.Header().Set("Content-Type", raw.ContentType)
		if raw.Filename != "" {
			w.Header().Set("Content-Disposition", `attachment; filename="`+raw.Filename+`"`)
		}
		w.WriteHeader(route.Status)
		_, _ = w.Write(raw.Body)
		return route.Status
	}
	status := route.Status
	body := out
	if res, ok := out.(Result); ok {
		if res.Status != 0 {
			status = res.Status
		}
		body = res.Body
		for k, v := range res.Headers {
			w.Header().Set(k, v)
		}
	}
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return a.fail(w, c, err)
		}
	}
	if idemKey != "" && r.Method == http.MethodPost && c.Principal != nil && status < 500 {
		if err := a.idempotencyStore(c, idemKey, status, payload); err != nil {
			a.Log.Warn("store idempotency record", "err", err, "requestId", reqID)
		}
	}
	if payload == nil {
		w.WriteHeader(status)
		return status
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
	return status
}

func (a *App) fail(w http.ResponseWriter, c *Ctx, err error) int {
	p, known := AsProblem(err)
	if !known {
		a.Log.Error("handler error", "requestId", c.RequestID, "path", c.Req.URL.Path, "err", err)
	}
	return writeProblem(w, p, c.RequestID)
}

func writeProblem(w http.ResponseWriter, p *Problem, reqID string) int {
	cp := *p
	cp.RequestID = reqID
	if cp.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(cp.retryAfter))
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(cp.Status)
	_ = json.NewEncoder(w).Encode(cp)
	return cp.Status
}

// WriteJSON writes a plain JSON response (for non-API endpoints such as /api/health).
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ContentDisposition builds a Content-Disposition header with a safe ASCII
// filename and the UTF-8 original.
func ContentDisposition(filename string, inline bool) string {
	disp := "attachment"
	if inline {
		disp = "inline"
	}
	if filename == "" {
		return disp
	}
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, filename)
	return disp + `; filename="` + ascii + `"; filename*=UTF-8''` + url.PathEscape(filename)
}
