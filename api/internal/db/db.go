// Package db owns the PostgreSQL connection pool, transactions and migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Querier is satisfied by *pgxpool.Pool and pgx.Tx, so store functions work in
// or outside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Connect opens a pool as the PurrOS server ("purros" in pg_stat_activity).
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return ConnectAs(ctx, url, "purros")
}

// ConnectAs opens a pool whose connections show appName in pg_stat_activity.
func ConnectAs(ctx context.Context, url, appName string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if _, set := cfg.ConnConfig.RuntimeParams["application_name"]; !set {
		cfg.ConnConfig.RuntimeParams["application_name"] = appName
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return pool, nil
}

// InTx runs fn in a transaction, committing on success and rolling back on
// error or panic.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Migrate applies all pending migrations. It holds a Postgres advisory lock so
// several instances starting at once don't race.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := newProvider(sqlDB)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

// PendingMigrations reports how many migrations have not been applied.
func PendingMigrations(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := newProvider(sqlDB)
	if err != nil {
		return 0, err
	}
	statuses, err := provider.Status(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range statuses {
		if s.State == goose.StatePending {
			n++
		}
	}
	return n, nil
}

func newProvider(sqlDB *sql.DB) (*goose.Provider, error) {
	locker, err := newLocker()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, sqlDB, mustSub(migrations, "migrations"),
		goose.WithSessionLocker(locker))
}

// IsUniqueViolation reports whether err is a unique-constraint violation.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsForeignKeyViolation reports whether err is a foreign-key violation.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// MigrationState is one migration and whether it has been applied.
type MigrationState struct {
	Version   int64
	Name      string
	Applied   bool
	AppliedAt *time.Time
}

// Migrations lists every embedded migration and its state in the database.
func Migrations(ctx context.Context, pool *pgxpool.Pool) ([]MigrationState, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := newProvider(sqlDB)
	if err != nil {
		return nil, err
	}
	statuses, err := provider.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MigrationState, 0, len(statuses))
	for _, s := range statuses {
		m := MigrationState{Version: s.Source.Version, Name: filepath.Base(s.Source.Path), Applied: s.State == goose.StateApplied}
		if m.Applied && !s.AppliedAt.IsZero() {
			t := s.AppliedAt
			m.AppliedAt = &t
		}
		out = append(out, m)
	}
	return out, nil
}

// SchemaVersion is the newest applied migration (0 for an empty database).
func SchemaVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	var v int64
	err := pool.QueryRow(ctx, `SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&v)
	return v, err
}

// LatestVersion is the newest migration this build contains.
func LatestVersion() int64 {
	entries, _ := migrations.ReadDir("migrations")
	var v int64
	for _, e := range entries {
		var n int64
		if _, err := fmt.Sscanf(e.Name(), "%d_", &n); err == nil && n > v {
			v = n
		}
	}
	return v
}

// MigrateTo applies pending migrations up to and including version.
func MigrateTo(ctx context.Context, pool *pgxpool.Pool, version int64) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := newProvider(sqlDB)
	if err != nil {
		return err
	}
	_, err = provider.UpTo(ctx, version)
	return err
}

// ServerConnections counts connections from running PurrOS servers and
// workers to this database.
func ServerConnections(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND application_name = 'purros' AND pid <> pg_backend_pid()`).Scan(&n)
	return n, err
}
