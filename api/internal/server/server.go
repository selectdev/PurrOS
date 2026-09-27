// Package server assembles the HTTP server from the modules.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/modules/cash"
	"github.com/selectdev/purros/api/internal/modules/inventory"
	"github.com/selectdev/purros/api/internal/modules/organization"
	"github.com/selectdev/purros/api/internal/modules/people"
	"github.com/selectdev/purros/api/internal/modules/platform"
	"github.com/selectdev/purros/api/internal/modules/purchasing"
	"github.com/selectdev/purros/api/internal/modules/sales"
	"github.com/selectdev/purros/api/internal/modules/scheduling"
	"github.com/selectdev/purros/api/internal/modules/timeclock"
	"github.com/selectdev/purros/api/internal/secure"
)

// NewApp wires dependencies and registers every module's routes.
func NewApp(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (*httpx.App, error) {
	box, err := secure.NewBox(cfg.Secret)
	if err != nil {
		return nil, err
	}
	var limiter httpx.Limiter = httpx.NewMemoryLimiter()
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return nil, err
		}
		limiter = httpx.NewRedisLimiter(redis.NewClient(opts))
	}
	router := &httpx.Router{}
	router.Add(platform.Routes()...)
	router.Add(organization.Routes()...)
	router.Add(people.Routes()...)
	router.Add(people.ExtraRoutes()...)
	router.Add(timeclock.Routes()...)
	router.Add(timeclock.TimesheetRoutes()...)
	router.Add(timeclock.TimeOffRoutes()...)
	router.Add(scheduling.Routes()...)
	router.Add(purchasing.Routes()...)
	router.Add(inventory.Routes()...)
	router.Add(inventory.StockRoutes()...)
	router.Add(sales.Routes()...)
	router.Add(sales.OrderRoutes()...)
	router.Add(cash.Routes()...)

	return &httpx.App{
		Config:   cfg,
		Pool:     pool,
		Features: features.NewStore(pool),
		Box:      box,
		Limiter:  limiter,
		Log:      log,
		Router:   router,
		Now:      time.Now,
	}, nil
}

// Handler returns the root HTTP handler.
func Handler(app *httpx.App) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(httpx.APIPrefix+"/", app)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"status": "ok", "version": platform.Version})
	})
	mux.HandleFunc("GET /api/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		checks := map[string]string{"database": "ok", "migrations": "ok"}
		status := 200
		if err := app.Pool.Ping(ctx); err != nil {
			checks["database"], status = "unreachable", 503
		} else if n, err := db.PendingMigrations(ctx, app.Pool); err != nil || n > 0 {
			checks["migrations"], status = "pending", 503
		}
		httpx.WriteJSON(w, status, map[string]any{"status": map[bool]string{true: "ready", false: "not_ready"}[status == 200], "checks": checks})
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{
			"name": "PurrOS API", "version": platform.Version,
			"openapi": app.Config.URL + httpx.APIPrefix + "/openapi.json",
		})
	})
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

// Serve runs the HTTP server until ctx is cancelled, then shuts down gracefully.
func Serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		log.Info("shutting down")
		return srv.Shutdown(shutdownCtx)
	}
}
