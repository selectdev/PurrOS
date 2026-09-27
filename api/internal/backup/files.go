package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/webhooks"
)

// Kinds of backup runs.
const (
	KindManual     = "manual"
	KindScheduled  = "scheduled"
	KindPreRestore = "pre_restore"
)

// Result describes a finished backup file.
type Result struct {
	Path     string
	Size     int64
	Manifest Manifest
}

// FileName returns the name for a new backup.
func FileName(t time.Time, kind string) string {
	name := "purros-" + t.UTC().Format("20060102-150405")
	if kind != KindManual && kind != "" {
		name += "-" + strings.ReplaceAll(kind, "_", "-")
	}
	return name + Extension
}

// NewPath returns an unused path in dir for a new backup.
func NewPath(dir string, t time.Time, kind string) string {
	name := FileName(t, kind)
	path := filepath.Join(dir, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path
		}
		path = filepath.Join(dir, strings.TrimSuffix(name, Extension)+fmt.Sprintf("-%d", i)+Extension)
	}
}

// ToFile writes a backup to path (via a temporary file, so an interrupted
// backup never leaves a partial file behind) and logs the run.
func ToFile(ctx context.Context, pool *pgxpool.Pool, path, kind string, opt Options) (Result, error) {
	res := Result{Path: path}
	if _, err := os.Stat(path); err == nil {
		return res, fmt.Errorf("%s already exists", path)
	}
	runID := ids.New("bkp")
	_, _ = pool.Exec(ctx, `INSERT INTO backup_runs (id, kind, status, file, encrypted) VALUES ($1, $2, 'running', $3, $4)`,
		runID, kind, path, opt.Passphrase != "")
	finish := func(err error) error {
		status, msg := "succeeded", ""
		if err != nil {
			status, msg = "failed", err.Error()
		}
		_, _ = pool.Exec(context.WithoutCancel(ctx), `UPDATE backup_runs SET status = $2, error = nullif($3, ''), size_bytes = $4, finished_at = now()
			WHERE id = $1`, runID, status, msg, res.Size)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return res, finish(err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".purros-backup-*.tmp")
	if err != nil {
		return res, finish(err)
	}
	defer os.Remove(tmp.Name())
	m, err := Create(ctx, pool, tmp, opt)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return res, finish(err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return res, finish(err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return res, finish(err)
	}
	if st, err := os.Stat(path); err == nil {
		res.Size = st.Size()
	}
	res.Manifest = m
	return res, finish(nil)
}

// File is a backup found in a directory.
type File struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// List returns the backups in dir, newest first.
func List(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Extension) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, File{Path: filepath.Join(dir, e.Name()), Size: info.Size(), ModTime: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// Prune deletes all but the newest keep backups in dir, and any older than
// maxAge (when set). It never deletes the newest backup.
func Prune(dir string, keep int, maxAge time.Duration, now time.Time) ([]string, error) {
	files, err := List(dir)
	if err != nil {
		return nil, err
	}
	var removed []string
	for i, f := range files {
		if i == 0 {
			continue
		}
		tooMany := keep > 0 && i >= keep
		tooOld := maxAge > 0 && now.Sub(f.ModTime) > maxAge
		if tooMany || tooOld {
			if err := os.Remove(f.Path); err != nil {
				return removed, err
			}
			removed = append(removed, f.Path)
		}
	}
	return removed, nil
}

// Schedule configures automatic backups (PURROS_BACKUP_*).
type Schedule struct {
	Dir        string // PURROS_BACKUP_DIR; backups are off when empty
	HourUTC    int    // PURROS_BACKUP_HOUR (default 2)
	Keep       int    // PURROS_BACKUP_KEEP (default 14)
	Passphrase string // PURROS_BACKUP_PASSPHRASE
	Version    string
	SecretFP   string
}

// RunScheduled makes today's backup if it's due and not done yet. Only one
// worker does it, even when several run.
func RunScheduled(ctx context.Context, pool *pgxpool.Pool, s Schedule, now time.Time, log *slog.Logger) (bool, error) {
	if s.Dir == "" || now.UTC().Hour() < s.HourUTC {
		return false, nil
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext('purros_backup'))`).Scan(&locked); err != nil || !locked {
		return false, err
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('purros_backup'))`) //nolint:errcheck
	var done bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM backup_runs WHERE kind = 'scheduled' AND status = 'succeeded'
		AND started_at >= date_trunc('day', $1::timestamptz AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')`, now).Scan(&done); err != nil {
		return false, err
	}
	if done {
		return false, nil
	}
	path := NewPath(s.Dir, now, KindScheduled)
	res, err := ToFile(ctx, pool, path, KindScheduled, Options{Passphrase: s.Passphrase, PurrOSVersion: s.Version, SecretFingerprint: s.SecretFP})
	if err != nil {
		return false, fmt.Errorf("scheduled backup: %w", err)
	}
	log.Info("backup created", "file", res.Path, "bytes", res.Size, "rows", res.Manifest.Rows())
	if removed, err := Prune(s.Dir, s.Keep, 0, now); err != nil {
		log.Warn("prune backups", "err", err)
	} else if len(removed) > 0 {
		log.Info("old backups removed", "count", len(removed))
	}
	return true, nil
}

// Job is the worker job for scheduled backups.
func Job(pool *pgxpool.Pool, s Schedule, log *slog.Logger) webhooks.Job {
	return webhooks.Job{Name: "backup", Every: 10 * time.Minute, Run: func(ctx context.Context) error {
		_, err := RunScheduled(ctx, pool, s, time.Now(), log)
		return err
	}}
}

// LastRun is the newest backup run.
type LastRun struct {
	Kind       string
	Status     string
	File       *string
	Size       *int64
	Error      *string
	StartedAt  time.Time
	FinishedAt *time.Time
}

// Last returns the newest backup runs.
func Last(ctx context.Context, pool *pgxpool.Pool, n int) ([]LastRun, error) {
	rows, err := pool.Query(ctx, `SELECT kind, status, file, size_bytes, error, started_at, finished_at
		FROM backup_runs ORDER BY started_at DESC LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LastRun
	for rows.Next() {
		var r LastRun
		if err := rows.Scan(&r.Kind, &r.Status, &r.File, &r.Size, &r.Error, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
