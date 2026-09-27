// Package testutil sets up a real PostgreSQL database and a running API for
// integration tests. Tests are skipped unless PURROS_TEST_DATABASE_URL points
// at a server where the user may create databases, e.g.
//
//	PURROS_TEST_DATABASE_URL=postgres://purros:purros@localhost:5432/postgres
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/cli"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/secure"
	"github.com/selectdev/purros/api/internal/server"
	"github.com/selectdev/purros/api/internal/webhooks"
)

const testSecret = "test-secret-test-secret-test-secret-000000"

// Env is a running API backed by a fresh database.
type Env struct {
	T          *testing.T
	Pool       *pgxpool.Pool
	App        *httpx.App
	Server     *httptest.Server
	Worker     *webhooks.Worker
	LocationID string
	Key        string // integration key with the scopes given to New
	Hooks      *Receiver
	HookSecret string

	DatabaseURL string // the test database, for CLI tests
	Secret      string // PURROS_SECRET of the app
}

// New creates a database, runs migrations, sets up a company with one
// location (external ID "101", America/Chicago, business day ends 04:00) and
// registers an integration with the given scopes and webhook events.
func New(t *testing.T, scopes []string, events []string) *Env {
	t.Helper()
	adminURL := os.Getenv("PURROS_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("set PURROS_TEST_DATABASE_URL to run integration tests")
	}
	ctx := context.Background()
	dbName := strings.ToLower(strings.ReplaceAll(ids.New("t"), "_", ""))
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin db: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, _ := url.Parse(adminURL)
	u.Path = "/" + dbName
	dbURL := u.String()
	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		admin.Close(context.Background())
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := cli.Setup(ctx, pool, cli.SetupInput{Company: "Test Co", Currency: "USD", Timezone: "America/Chicago", OwnerEmail: "owner@test"}); err != nil {
		t.Fatal(err)
	}
	locID, err := cli.CreateLocation(ctx, pool, cli.LocationInput{Name: "Store 101", ExternalID: "101", Timezone: "America/Chicago", Currency: "USD", Cutoff: "04:00"})
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{URL: "http://test", Secret: testSecret, RateLimitPerMin: 10_000, IngestRateLimitPerMin: 10_000, MaxBatchSize: 1000,
		SMTP:    config.SMTP{Host: "smtp.test", Port: 587, From: "PurrOS <noreply@test>"},
		Storage: config.Storage{Driver: "local", LocalPath: t.TempDir(), MaxUploadMB: 1, SignedURLTTL: 300}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := server.NewApp(cfg, pool, log)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.Handler(app))
	t.Cleanup(srv.Close)

	env := &Env{T: t, Pool: pool, App: app, Server: srv, LocationID: locID, Hooks: &Receiver{}, DatabaseURL: dbURL, Secret: testSecret}
	hookSrv := httptest.NewServer(env.Hooks)
	t.Cleanup(hookSrv.Close)

	m := cli.Manifest{Name: "test-integration", DisplayName: "Test Integration", Scopes: scopes}
	if len(events) > 0 {
		m.Webhooks = &struct {
			URL    string   `json:"url"`
			Events []string `json:"events"`
		}{URL: hookSrv.URL, Events: events}
	}
	if err := m.Validate(ctx, app.Features, true); err != nil {
		t.Fatal(err)
	}
	box, _ := secure.NewBox(testSecret)
	reg, err := cli.RegisterIntegration(ctx, pool, box, m, map[string]any{"token": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	env.Key, env.HookSecret = reg.APIKey, reg.WebhookSecret
	env.Hooks.secret = reg.WebhookSecret
	env.Worker = webhooks.New(pool, app.Features, box, log)
	return env
}

// Response is a decoded API response.
type Response struct {
	Status int
	Header http.Header
	Body   map[string]any
	Raw    []byte
}

// Do sends a request with the integration key. body may be a string or any
// JSON-serializable value.
func (e *Env) Do(method, path string, body any, headers ...string) Response {
	e.T.Helper()
	return e.DoWithKey(e.Key, method, path, body, headers...)
}

func (e *Env) DoWithKey(key, method, path string, body any, headers ...string) Response {
	e.T.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		r = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, e.Server.URL+path, r)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := Response{Status: resp.StatusCode, Header: resp.Header, Raw: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.Body)
	}
	return out
}

// Expect fails the test unless the status matches.
func (r Response) Expect(t *testing.T, status int) Response {
	t.Helper()
	if r.Status != status {
		t.Fatalf("expected HTTP %d, got %d: %s", status, r.Status, r.Raw)
	}
	return r
}

// Str returns a string field by dotted path, e.g. "data.0.id".
func (r Response) Str(path string) string {
	v := r.Get(path)
	s, _ := v.(string)
	return s
}

// Get returns a field by dotted path.
func (r Response) Get(path string) any {
	var cur any = r.Body
	for _, p := range strings.Split(path, ".") {
		switch c := cur.(type) {
		case map[string]any:
			cur = c[p]
		case []any:
			var i int
			fmt.Sscanf(p, "%d", &i)
			if i >= len(c) {
				return nil
			}
			cur = c[i]
		default:
			return nil
		}
	}
	return cur
}

// ProcessWebhooks dispatches pending events and delivers everything that is due.
func (e *Env) ProcessWebhooks() {
	e.T.Helper()
	ctx := context.Background()
	if _, err := e.Worker.Dispatch(ctx); err != nil {
		e.T.Fatal(err)
	}
	// Deliver until nothing is due (events about one record go one at a time).
	for range 50 {
		n, err := e.Worker.Deliver(ctx)
		if err != nil {
			e.T.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
}

// Receiver records webhook deliveries and verifies their signatures.
type Receiver struct {
	mu       sync.Mutex
	secret   string
	Events   []webhooks.Envelope
	BadSigs  int
	FailNext int // respond 500 to the next N deliveries
}

func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := secure.VerifyWebhook(h.secret, r.Header.Get("PurrOS-Signature"), body, time.Now(), 5*time.Minute); err != nil {
		h.BadSigs++
		w.WriteHeader(400)
		return
	}
	if h.FailNext > 0 {
		h.FailNext--
		w.WriteHeader(500)
		return
	}
	var ev webhooks.Envelope
	_ = json.Unmarshal(body, &ev)
	h.Events = append(h.Events, ev)
	w.WriteHeader(204)
}

// Types returns the received event types in order.
func (h *Receiver) Types() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.Events))
	for i, e := range h.Events {
		out[i] = e.Type
	}
	return out
}

// EmptyDatabase creates an empty database (no migrations) and returns its URL.
func EmptyDatabase(t *testing.T) string {
	t.Helper()
	adminURL := os.Getenv("PURROS_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("set PURROS_TEST_DATABASE_URL to run integration tests")
	}
	ctx := context.Background()
	dbName := strings.ToLower(strings.ReplaceAll(ids.New("t"), "_", ""))
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin db: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		admin.Close(context.Background())
	})
	u, _ := url.Parse(adminURL)
	u.Path = "/" + dbName
	return u.String()
}

// TestSecret is the PURROS_SECRET test apps use.
const TestSecret = testSecret
