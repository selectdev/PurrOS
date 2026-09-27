package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/storage"
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
	RunID     string
	RemoteKey string // set when uploaded to S3
	Path      string
	Size      int64
	Manifest  Manifest
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
	res.RunID = runID
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

// RunOptions configure a backup made by Run.
type RunOptions struct {
	Kind string
	// Dir keeps the backup locally. When empty, the backup is only uploaded
	// (Remote is then required) and the local copy is deleted.
	Dir  string
	Keep int // local retention; 0 keeps everything
	// Path overrides the file name in Dir.
	Path       string
	Remote     storage.Driver // upload here when set
	RemoteKeep int            // remote retention; 0 keeps everything
	Options
}

// Run makes a backup, uploads it when a remote is set, and applies retention.
// Upload failures are recorded on the run; they fail Run only when there is
// no local copy.
func Run(ctx context.Context, pool *pgxpool.Pool, o RunOptions) (Result, error) {
	dir, temp := o.Dir, false
	if o.Path != "" {
		dir = filepath.Dir(o.Path)
	}
	if dir == "" {
		if o.Remote == nil {
			return Result{}, errors.New("set a backup directory or S3 bucket")
		}
		d, err := os.MkdirTemp("", "purros-backup-")
		if err != nil {
			return Result{}, err
		}
		defer os.RemoveAll(d)
		dir, temp = d, true
	}
	path := o.Path
	if path == "" {
		path = NewPath(dir, time.Now(), o.Kind)
	}
	res, err := ToFile(ctx, pool, path, o.Kind, o.Options)
	if err != nil {
		return res, err
	}
	if o.Remote != nil {
		key, uerr := Upload(ctx, o.Remote, path)
		if uerr != nil {
			_, _ = pool.Exec(ctx, `UPDATE backup_runs SET upload_error = $2 WHERE id = $1`, res.RunID, uerr.Error())
			if temp {
				_, _ = pool.Exec(ctx, `UPDATE backup_runs SET status = 'failed', error = $2 WHERE id = $1`, res.RunID, "upload failed: "+uerr.Error())
				return res, fmt.Errorf("upload to %s failed: %w", o.Remote.Describe(), uerr)
			}
			return res, &UploadError{Err: uerr}
		}
		res.RemoteKey = key
		_, _ = pool.Exec(ctx, `UPDATE backup_runs SET remote_key = $2, uploaded_at = now(), file = CASE WHEN $3 THEN NULL ELSE file END
			WHERE id = $1`, res.RunID, key, temp)
		if o.RemoteKeep > 0 {
			if _, err := RemotePrune(ctx, o.Remote, o.RemoteKeep); err != nil {
				return res, fmt.Errorf("prune old backups in %s: %w", o.Remote.Describe(), err)
			}
		}
	}
	if temp {
		res.Path = ""
	} else if o.Keep > 0 && o.Path == "" {
		if _, err := Prune(dir, o.Keep, 0, time.Now()); err != nil {
			return res, err
		}
	}
	return res, nil
}

// UploadError means the backup was made locally but couldn't be uploaded.
type UploadError struct{ Err error }

func (e *UploadError) Error() string {
	return "the backup was saved locally but the upload failed: " + e.Err.Error()
}
func (e *UploadError) Unwrap() error { return e.Err }

// Upload copies a backup file to the remote under its file name.
func Upload(ctx context.Context, remote storage.Driver, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	key := filepath.Base(path)
	return key, remote.Put(ctx, key, f, st.Size(), "application/octet-stream")
}

// RemoteList returns the backups in the remote, newest first.
func RemoteList(ctx context.Context, remote storage.Driver) ([]storage.Object, error) {
	all, err := remote.List(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []storage.Object
	for _, o := range all {
		if strings.HasSuffix(o.Key, Extension) && !strings.Contains(o.Key, "/") {
			out = append(out, o)
		}
	}
	// Names start with a UTC timestamp, so they sort by age.
	sort.Slice(out, func(i, j int) bool { return out[i].Key > out[j].Key })
	return out, nil
}

// RemotePrune deletes all but the newest keep backups in the remote.
func RemotePrune(ctx context.Context, remote storage.Driver, keep int) ([]string, error) {
	list, err := RemoteList(ctx, remote)
	if err != nil {
		return nil, err
	}
	var removed []string
	for i, o := range list {
		if i == 0 || i < keep {
			continue
		}
		if err := remote.Delete(ctx, o.Key); err != nil {
			return removed, err
		}
		removed = append(removed, o.Key)
	}
	return removed, nil
}

// Download copies a remote backup to dst (a file path).
func Download(ctx context.Context, remote storage.Driver, name, dst string) error {
	if err := storage.CheckKey(name); err != nil || strings.Contains(name, "/") {
		return fmt.Errorf("invalid backup name %q", name)
	}
	body, _, err := remote.Get(ctx, name)
	if errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("no backup %q in %s", name, remote.Describe())
	}
	if err != nil {
		return err
	}
	defer body.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, body); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// Schedule configures automatic backups (PURROS_BACKUP_*).
type Schedule struct {
	Dir        string         // local directory; may be empty when Remote is set
	HourUTC    int            // PURROS_BACKUP_HOUR (default 2)
	Keep       int            // PURROS_BACKUP_KEEP (default 14)
	Remote     storage.Driver // PURROS_BACKUP_S3_*
	RemoteKeep int
	Files      storage.Driver // include uploaded files from here
	Passphrase string         // PURROS_BACKUP_PASSPHRASE
	Version    string
	SecretFP   string
}

// RunScheduled makes today's backup if it's due and not done yet. Only one
// worker does it, even when several run.
func RunScheduled(ctx context.Context, pool *pgxpool.Pool, s Schedule, now time.Time, log *slog.Logger) (bool, error) {
	if (s.Dir == "" && s.Remote == nil) || now.UTC().Hour() < s.HourUTC {
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
	res, err := Run(ctx, pool, RunOptions{Kind: KindScheduled, Dir: s.Dir, Keep: s.Keep, Remote: s.Remote, RemoteKeep: s.RemoteKeep,
		Options: Options{Passphrase: s.Passphrase, PurrOSVersion: s.Version, SecretFingerprint: s.SecretFP, Files: s.Files}})
	var up *UploadError
	switch {
	case errors.As(err, &up):
		log.Error("backup upload failed", "file", res.Path, "err", up.Err)
	case err != nil:
		return false, fmt.Errorf("scheduled backup: %w", err)
	}
	log.Info("backup created", "file", res.Path, "remote", res.RemoteKey, "bytes", res.Size, "rows", res.Manifest.Rows(),
		"files", len(res.Manifest.Files))
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
	RemoteKey   *string
	UploadError *string
	Kind        string
	Status      string
	File        *string
	Size        *int64
	Error       *string
	StartedAt   time.Time
	FinishedAt  *time.Time
}

// Last returns the newest backup runs.
func Last(ctx context.Context, pool *pgxpool.Pool, n int) ([]LastRun, error) {
	rows, err := pool.Query(ctx, `SELECT kind, status, file, size_bytes, error, started_at, finished_at, remote_key, upload_error
		FROM backup_runs ORDER BY started_at DESC LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LastRun
	for rows.Next() {
		var r LastRun
		if err := rows.Scan(&r.Kind, &r.Status, &r.File, &r.Size, &r.Error, &r.StartedAt, &r.FinishedAt, &r.RemoteKey, &r.UploadError); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
