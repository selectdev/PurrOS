// Package backup makes and restores complete, consistent PurrOS database
// backups without external tools (the container image has no pg_dump).
//
// A backup is a gzip-compressed tar archive, optionally encrypted with a
// passphrase (see crypt.go):
//
//	data/<table>.tsv   the table in PostgreSQL COPY text format
//	manifest.json      versions, tables with row counts and SHA-256, sequences
//
// Every table is read in one REPEATABLE READ snapshot, so the backup is
// consistent while PurrOS keeps running.
package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/storage"
)

// FormatVersion is the archive layout version.
const FormatVersion = 1

// Extension is the file extension of backups.
const Extension = ".purros-backup"

// Tables that aren't backed up: migration bookkeeping (recreated by the
// migrations) and the backup log itself.
var skipTables = map[string]bool{"goose_db_version": true, "backup_runs": true}

type Manifest struct {
	Format            int                 `json:"format"`
	PurrOSVersion     string              `json:"purrosVersion"`
	SchemaVersion     int64               `json:"schemaVersion"`
	CreatedAt         time.Time           `json:"createdAt"`
	Company           string              `json:"company"`
	SecretFingerprint string              `json:"secretFingerprint"`
	Tables            []Table             `json:"tables"`
	Sequences         map[string]Sequence `json:"sequences"`
	// Files are uploaded attachments, when the backup includes them.
	Files        []FileEntry `json:"files,omitempty"`
	MissingFiles []string    `json:"missingFiles,omitempty" doc:"Attachments whose file wasn't in storage"`
	Encrypted    bool        `json:"-"`
}

// FileEntry is a stored file included in the backup.
type FileEntry struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// FileBytes is the total size of included files.
func (m Manifest) FileBytes() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

type Table struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    int64    `json:"rows"`
	SHA256  string   `json:"sha256"`
	Bytes   int64    `json:"bytes"`
}

type Sequence struct {
	Value    int64 `json:"value"`
	IsCalled bool  `json:"isCalled"`
}

// Rows is the total number of rows.
func (m Manifest) Rows() int64 {
	var n int64
	for _, t := range m.Tables {
		n += t.Rows
	}
	return n
}

// Options for Create.
type Options struct {
	Passphrase        string // encrypt when set
	PurrOSVersion     string
	SecretFingerprint string
	// Files, when set, adds the uploaded files it holds to the backup.
	Files storage.Driver
}

// Create writes a backup of the database to w.
func Create(ctx context.Context, pool *pgxpool.Pool, w io.Writer, opt Options) (Manifest, error) {
	m := Manifest{Format: FormatVersion, PurrOSVersion: opt.PurrOSVersion, CreatedAt: time.Now().UTC(),
		SecretFingerprint: opt.SecretFingerprint, Sequences: map[string]Sequence{}, Encrypted: opt.Passphrase != ""}

	out := w
	var enc *encWriter
	if opt.Passphrase != "" {
		var err error
		if enc, err = newEncWriter(w, opt.Passphrase); err != nil {
			return m, err
		}
		out = enc
	}
	gz, _ := gzip.NewWriterLevel(out, gzip.BestSpeed)
	tw := tar.NewWriter(gz)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return m, err
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return m, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&m.SchemaVersion); err != nil {
		return m, fmt.Errorf("read schema version (is the database migrated?): %w", err)
	}
	_ = tx.QueryRow(ctx, `SELECT name FROM company LIMIT 1`).Scan(&m.Company)

	tables, err := listTables(ctx, tx)
	if err != nil {
		return m, err
	}
	tmp, err := os.CreateTemp("", "purros-backup-*")
	if err != nil {
		return m, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	for _, name := range tables {
		cols, err := tableColumns(ctx, tx, name)
		if err != nil {
			return m, err
		}
		if err := tmp.Truncate(0); err != nil {
			return m, err
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return m, err
		}
		h := sha256.New()
		tag, err := tx.Conn().PgConn().CopyTo(ctx, io.MultiWriter(tmp, h),
			fmt.Sprintf(`COPY public.%s (%s) TO STDOUT`, quoteIdent(name), quoteIdents(cols)))
		if err != nil {
			return m, fmt.Errorf("copy %s: %w", name, err)
		}
		size, err := tmp.Seek(0, io.SeekCurrent)
		if err != nil {
			return m, err
		}
		if err := tw.WriteHeader(&tar.Header{Name: "data/" + name + ".tsv", Mode: 0o600, Size: size, ModTime: m.CreatedAt}); err != nil {
			return m, err
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return m, err
		}
		if _, err := io.CopyN(tw, tmp, size); err != nil {
			return m, err
		}
		m.Tables = append(m.Tables, Table{Name: name, Columns: cols, Rows: tag.RowsAffected(), SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: size})
	}

	rows, err := tx.Query(ctx, `SELECT sequencename FROM pg_sequences WHERE schemaname = 'public' ORDER BY 1`)
	if err != nil {
		return m, err
	}
	seqs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return m, err
	}
	for _, s := range seqs {
		var st Sequence
		if err := tx.QueryRow(ctx, `SELECT last_value, is_called FROM public.`+quoteIdent(s)).Scan(&st.Value, &st.IsCalled); err != nil {
			return m, err
		}
		m.Sequences[s] = st
	}

	if opt.Files != nil {
		if err := addFiles(ctx, tx, tw, opt.Files, &m); err != nil {
			return m, err
		}
	}

	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(mb)), ModTime: m.CreatedAt}); err != nil {
		return m, err
	}
	if _, err := tw.Write(mb); err != nil {
		return m, err
	}
	if err := tw.Close(); err != nil {
		return m, err
	}
	if err := gz.Close(); err != nil {
		return m, err
	}
	if enc != nil {
		if err := enc.Close(); err != nil {
			return m, err
		}
	}
	return m, nil
}

// addFiles copies every live attachment's file into the archive.
func addFiles(ctx context.Context, tx pgx.Tx, tw *tar.Writer, files storage.Driver, m *Manifest) error {
	rows, err := tx.Query(ctx, `SELECT storage_key FROM attachments WHERE deleted_at IS NULL ORDER BY storage_key`)
	if err != nil {
		return err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, key := range keys {
		body, obj, err := files.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			m.MissingFiles = append(m.MissingFiles, key)
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s from storage: %w", key, err)
		}
		h := sha256.New()
		err = tw.WriteHeader(&tar.Header{Name: "files/" + key, Mode: 0o600, Size: obj.Size, ModTime: m.CreatedAt})
		if err == nil {
			var n int64
			n, err = io.Copy(tw, io.TeeReader(io.LimitReader(body, obj.Size), h))
			if err == nil && n != obj.Size {
				err = fmt.Errorf("%s changed size while being backed up", key)
			}
		}
		body.Close()
		if err != nil {
			return err
		}
		m.Files = append(m.Files, FileEntry{Key: key, Size: obj.Size, SHA256: hex.EncodeToString(h.Sum(nil))})
	}
	return nil
}

func listTables(ctx context.Context, q db.Querier) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	all, err := pgx.CollectRows(rows, pgx.RowTo[string])
	var out []string
	for _, t := range all {
		if !skipTables[t] {
			out = append(out, t)
		}
	}
	return out, err
}

func tableColumns(ctx context.Context, q db.Querier, table string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT attname FROM pg_attribute
		WHERE attrelid = ('public.' || quote_ident($1))::regclass AND attnum > 0 AND NOT attisdropped AND attgenerated = ''
		ORDER BY attnum`, table)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func quoteIdent(s string) string { return pgx.Identifier{s}.Sanitize() }

func quoteIdents(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = quoteIdent(c)
	}
	return strings.Join(q, ", ")
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// NeedsPassphrase reports whether the backup at path is encrypted.
func NeedsPassphrase(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	return isEncrypted(bufio.NewReader(f)), nil
}

// open returns a tar reader over the backup, decrypting it if needed.
func open(path, pass string) (*tar.Reader, bool, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, nil, err
	}
	br := bufio.NewReaderSize(f, 1<<16)
	var r io.Reader = br
	encrypted := isEncrypted(br)
	if encrypted {
		if pass == "" {
			f.Close()
			return nil, true, nil, errors.New("this backup is encrypted: give the passphrase (PURROS_BACKUP_PASSPHRASE or --passphrase-file)")
		}
		d, err := newDecReader(br, pass)
		if err != nil {
			f.Close()
			return nil, true, nil, err
		}
		r = d
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		f.Close()
		if encrypted {
			return nil, true, nil, ErrPassphrase
		}
		return nil, false, nil, errors.New("not a PurrOS backup (or the file is damaged)")
	}
	return tar.NewReader(gz), encrypted, func() { gz.Close(); f.Close() }, nil
}

// Verify reads the whole backup and checks every table against the
// manifest. It returns the manifest.
func Verify(path, pass string) (Manifest, error) {
	var m Manifest
	tr, encrypted, closeFn, err := open(path, pass)
	if err != nil {
		return m, err
	}
	defer closeFn()
	sums := map[string]string{}
	fileSums := map[string]string{}
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, damaged(err)
		}
		switch {
		case hdr.Name == "manifest.json":
			if err := json.NewDecoder(tr).Decode(&m); err != nil {
				return m, damaged(err)
			}
			found = true
		case strings.HasPrefix(hdr.Name, "data/") && strings.HasSuffix(hdr.Name, ".tsv"):
			h := sha256.New()
			if _, err := io.Copy(h, tr); err != nil {
				return m, damaged(err)
			}
			sums[strings.TrimSuffix(strings.TrimPrefix(hdr.Name, "data/"), ".tsv")] = hex.EncodeToString(h.Sum(nil))
		case strings.HasPrefix(hdr.Name, "files/"):
			h := sha256.New()
			if _, err := io.Copy(h, tr); err != nil {
				return m, damaged(err)
			}
			fileSums[strings.TrimPrefix(hdr.Name, "files/")] = hex.EncodeToString(h.Sum(nil))
		}
	}
	if !found {
		return m, errors.New("the backup has no manifest: it is incomplete or not a PurrOS backup")
	}
	m.Encrypted = encrypted
	if m.Format != FormatVersion {
		return m, fmt.Errorf("unsupported backup format %d (this PurrOS reads format %d)", m.Format, FormatVersion)
	}
	for _, t := range m.Tables {
		got, ok := sums[t.Name]
		if !ok {
			return m, fmt.Errorf("table %s is missing from the backup", t.Name)
		}
		if got != t.SHA256 {
			return m, fmt.Errorf("table %s doesn't match its checksum: the backup is damaged", t.Name)
		}
	}
	for _, f := range m.Files {
		if err := storage.CheckKey(f.Key); err != nil {
			return m, err
		}
		if fileSums[f.Key] != f.SHA256 {
			return m, fmt.Errorf("file %s is missing or doesn't match its checksum: the backup is damaged", f.Key)
		}
	}
	return m, nil
}

func damaged(err error) error {
	if errors.Is(err, ErrPassphrase) || errors.Is(err, errTruncated) {
		return err
	}
	return fmt.Errorf("the backup is damaged: %w", err)
}

// ---------------------------------------------------------------------------
// Restoring
// ---------------------------------------------------------------------------

// ErrNotEmpty is returned when restoring over a database that has data.
var ErrNotEmpty = errors.New("the database already holds a PurrOS company")

type RestoreOptions struct {
	Passphrase string
	// Replace allows restoring over a database that already has data. Its
	// current contents are dropped.
	Replace bool
	// Progress, if set, is told about each step.
	Progress func(step string)
	// Files receives the uploaded files in the backup. Without it they are
	// skipped.
	Files storage.Driver
}

// HasData reports whether the database holds a set-up company.
func HasData(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.company') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return false, err
	}
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM company`).Scan(&n)
	return n > 0, err
}

// Restore replaces the database with the backup, then migrates it to this
// build's schema. It verifies the whole backup before touching anything.
func Restore(ctx context.Context, pool *pgxpool.Pool, path string, opt RestoreOptions) (Manifest, error) {
	step := func(s string) {
		if opt.Progress != nil {
			opt.Progress(s)
		}
	}
	step("Verifying the backup")
	m, err := Verify(path, opt.Passphrase)
	if err != nil {
		return m, err
	}
	if latest := db.LatestVersion(); m.SchemaVersion > latest {
		return m, fmt.Errorf("the backup is from a newer PurrOS (schema %d, this build knows %d): update PurrOS first", m.SchemaVersion, latest)
	}
	full, err := HasData(ctx, pool)
	if err != nil {
		return m, err
	}
	if full && !opt.Replace {
		return m, ErrNotEmpty
	}

	step("Clearing the database")
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		return m, fmt.Errorf("clear the database (does DATABASE_URL's user own it?): %w", err)
	}
	pool.Reset() // drop cached statement plans for the old tables
	step(fmt.Sprintf("Creating the schema at version %d", m.SchemaVersion))
	if err := db.MigrateTo(ctx, pool, m.SchemaVersion); err != nil {
		return m, err
	}

	step(fmt.Sprintf("Loading %d rows into %d tables", m.Rows(), len(m.Tables)))
	if len(m.Files) > 0 && opt.Files != nil {
		step(fmt.Sprintf("Restoring %d uploaded files to %s", len(m.Files), opt.Files.Describe()))
	}
	if err := load(ctx, pool, path, opt.Passphrase, m, opt.Files); err != nil {
		return m, err
	}

	if m.SchemaVersion < db.LatestVersion() {
		step(fmt.Sprintf("Upgrading the schema from %d to %d", m.SchemaVersion, db.LatestVersion()))
	}
	if err := db.Migrate(ctx, pool); err != nil {
		return m, err
	}
	// Running servers reload feature switches.
	_, _ = pool.Exec(ctx, `SELECT pg_notify('purros_features', 'restore')`)
	return m, nil
}

func load(ctx context.Context, pool *pgxpool.Pool, path, pass string, m Manifest, files storage.Driver) error {
	fileSums := map[string]string{}
	for _, f := range m.Files {
		fileSums[f.Key] = f.SHA256
	}
	byName := map[string]Table{}
	for _, t := range m.Tables {
		byName[t.Name] = t
	}
	return db.InTx(ctx, pool, func(tx pgx.Tx) error {
		// Migrations may seed rows; the backup replaces them.
		existing, err := listTables(ctx, tx)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			q := make([]string, len(existing))
			for i, t := range existing {
				q[i] = "public." + quoteIdent(t)
			}
			if _, err := tx.Exec(ctx, `TRUNCATE `+strings.Join(q, ", ")+` CASCADE`); err != nil {
				return err
			}
		}
		for name := range byName {
			if !contains(existing, name) {
				return fmt.Errorf("table %s from the backup doesn't exist at schema version %d", name, m.SchemaVersion)
			}
		}
		// Foreign keys are dropped while loading (tables refer to each other in
		// cycles) and re-created afterwards, which checks every reference.
		rows, err := tx.Query(ctx, `SELECT conrelid::regclass::text, quote_ident(conname), pg_get_constraintdef(oid)
			FROM pg_constraint WHERE contype = 'f' AND connamespace = 'public'::regnamespace`)
		if err != nil {
			return err
		}
		type fk struct{ table, name, def string }
		fks, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (fk, error) {
			var f fk
			return f, r.Scan(&f.table, &f.name, &f.def)
		})
		if err != nil {
			return err
		}
		for _, f := range fks {
			if _, err := tx.Exec(ctx, `ALTER TABLE `+f.table+` DROP CONSTRAINT `+f.name); err != nil {
				return err
			}
		}

		tr, _, closeFn, err := open(path, pass)
		if err != nil {
			return err
		}
		defer closeFn()
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return damaged(err)
			}
			if key, ok := strings.CutPrefix(hdr.Name, "files/"); ok {
				want, listed := fileSums[key]
				if files == nil || !listed {
					continue
				}
				h := sha256.New()
				if err := files.Put(ctx, key, io.TeeReader(tr, h), hdr.Size, ""); err != nil {
					return fmt.Errorf("restore file %s: %w", key, err)
				}
				if hex.EncodeToString(h.Sum(nil)) != want {
					return fmt.Errorf("file %s changed while restoring: the backup file is being modified", key)
				}
				continue
			}
			if !strings.HasPrefix(hdr.Name, "data/") {
				continue
			}
			t, ok := byName[strings.TrimSuffix(strings.TrimPrefix(hdr.Name, "data/"), ".tsv")]
			if !ok {
				continue
			}
			h := sha256.New()
			tag, err := tx.Conn().PgConn().CopyFrom(ctx, io.TeeReader(tr, h),
				fmt.Sprintf(`COPY public.%s (%s) FROM STDIN`, quoteIdent(t.Name), quoteIdents(t.Columns)))
			if err != nil {
				return fmt.Errorf("load %s: %w", t.Name, err)
			}
			if hex.EncodeToString(h.Sum(nil)) != t.SHA256 || tag.RowsAffected() != t.Rows {
				return fmt.Errorf("table %s changed while restoring: the backup file is being modified", t.Name)
			}
		}
		for name, s := range m.Sequences {
			if _, err := tx.Exec(ctx, `SELECT setval(('public.' || quote_ident($1))::regclass, $2, $3)`, name, s.Value, s.IsCalled); err != nil {
				return fmt.Errorf("sequence %s: %w", name, err)
			}
		}
		for _, f := range fks {
			if _, err := tx.Exec(ctx, `ALTER TABLE `+f.table+` ADD CONSTRAINT `+f.name+` `+f.def); err != nil {
				return fmt.Errorf("the backup's data breaks a reference (%s): %w", f.name, err)
			}
		}
		return nil
	})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
