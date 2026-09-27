package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/backup"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/mail"
	"github.com/selectdev/purros/api/internal/modules/insights"
	"github.com/selectdev/purros/api/internal/modules/platform"
	"github.com/selectdev/purros/api/internal/secure"
	"github.com/selectdev/purros/api/internal/server"
	"github.com/selectdev/purros/api/internal/storage"
	"github.com/selectdev/purros/api/internal/webhooks"
	"github.com/spf13/cobra"
)

func (a *app) serveCmd() *cobra.Command {
	var noMigrate bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the API (and, by default, the background worker)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, pool, err := connect(ctx, true)
			if err != nil {
				return err
			}
			defer pool.Close()
			log := logger()
			if !noMigrate {
				if err := db.Migrate(ctx, pool); err != nil {
					return fmt.Errorf("migrate: %w", err)
				}
			}
			app, err := server.NewApp(cfg, pool, log)
			if err != nil {
				return err
			}
			go app.Features.Listen(ctx)
			if cfg.RunWorker {
				w := webhooks.New(pool, app.Features, app.Box, log)
				w.Jobs = jobs(cfg, pool, app.Features, log)
				go func() {
					if err := w.Run(ctx); err != nil {
						log.Error("worker", "err", err)
					}
				}()
			}
			return server.Serve(ctx, cfg.ListenAddr, server.Handler(app), log)
		},
	}
	cmd.Flags().BoolVar(&noMigrate, "no-migrate", false, "don't apply migrations on start")
	return cmd
}

func (a *app) workerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run only the background worker (webhooks, email, alerts, scheduled backups)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, pool, err := connect(ctx, true)
			if err != nil {
				return err
			}
			defer pool.Close()
			box, err := secure.NewBox(cfg.Secret)
			if err != nil {
				return err
			}
			store := features.NewStore(pool)
			go store.Listen(ctx)
			log := logger()
			w := webhooks.New(pool, store, box, log)
			w.Jobs = jobs(cfg, pool, store, log)
			return w.Run(ctx)
		},
	}
}

// jobs are the worker's periodic jobs.
func jobs(cfg config.Config, pool *pgxpool.Pool, fs *features.Store, log *slog.Logger) []webhooks.Job {
	out := []webhooks.Job{insights.AlertJob(pool, fs)}
	if cfg.SMTP.Enabled() {
		out = append(out, mail.NewSender(pool, cfg.SMTP, log).Job())
	} else {
		log.Info("email is off: SMTP_HOST is not set")
	}
	if cfg.Backup.Scheduled() {
		s := backup.Schedule{Dir: cfg.Backup.Dir, HourUTC: cfg.Backup.HourUTC, Keep: cfg.Backup.Keep, RemoteKeep: cfg.Backup.S3Keep,
			Passphrase: cfg.Backup.Passphrase, Version: platform.Version, SecretFP: secure.Fingerprint(cfg.Secret)}
		var err error
		if cfg.Backup.S3Enabled {
			if s.Remote, err = storage.NewS3(cfg.Backup.S3); err != nil {
				log.Error("scheduled backups are off: backup bucket", "err", err)
				return out
			}
		}
		if cfg.IncludeFiles() {
			if s.Files, err = storage.New(cfg.Storage); err != nil {
				log.Error("scheduled backups are off: file storage", "err", err)
				return out
			}
		}
		out = append(out, backup.Job(pool, s, log))
	} else {
		log.Info("scheduled backups are off: set PURROS_BACKUP_DIR or PURROS_BACKUP_S3_ENABLED")
	}
	return out
}

func (a *app) migrateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations",
		Long:  "Apply pending database migrations. `serve` does this on start unless --no-migrate is given.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				ms, err := db.Migrations(cmd.Context(), pool)
				if err != nil {
					return err
				}
				var pending []string
				for _, m := range ms {
					if !m.Applied {
						pending = append(pending, m.Name)
					}
				}
				if len(pending) == 0 {
					a.printf("Migrations are up to date.\n")
					return nil
				}
				if dryRun {
					a.printf("%d pending migration(s):\n  %s\n", len(pending), strings.Join(pending, "\n  "))
					return nil
				}
				if err := db.Migrate(cmd.Context(), pool); err != nil {
					return err
				}
				a.printf("Applied %d migration(s):\n  %s\n", len(pending), strings.Join(pending, "\n  "))
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list pending migrations without applying them")
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "List migrations and whether they are applied",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				ms, err := db.Migrations(cmd.Context(), pool)
				if err != nil {
					return err
				}
				return a.emit(ms, func() {
					var rows [][]string
					for _, m := range ms {
						state, at := "pending", ""
						if m.Applied {
							state = "applied"
							if m.AppliedAt != nil {
								at = m.AppliedAt.Local().Format("2006-01-02 15:04")
							}
						}
						rows = append(rows, []string{strconv.FormatInt(m.Version, 10), m.Name, state, at})
					}
					a.table("VERSION\tMIGRATION\tSTATE\tAPPLIED", rows)
				})
			})
		},
	})
	return cmd
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.jsonOut {
				return a.emit(map[string]string{"version": platform.Version}, nil)
			}
			a.printf("purros %s\n", platform.Version)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

type statusReport struct {
	Version       string          `json:"version"`
	SchemaVersion int64           `json:"schemaVersion"`
	LatestSchema  int64           `json:"latestSchema"`
	Company       string          `json:"company"`
	Counts        map[string]int  `json:"counts"`
	Queues        map[string]int  `json:"queues"`
	LastBackup    *backupRunJSON  `json:"lastBackup"`
	Backups       map[string]any  `json:"backupSchedule"`
	Email         map[string]any  `json:"email"`
	Storage       map[string]any  `json:"storage"`
	Features      map[string]bool `json:"features"`
}

type backupRunJSON struct {
	Kind      string     `json:"kind"`
	Status    string     `json:"status"`
	File      *string    `json:"file"`
	Size      *int64     `json:"sizeBytes"`
	Error     *string    `json:"error"`
	StartedAt time.Time  `json:"startedAt"`
	Finished  *time.Time `json:"finishedAt"`
}

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show an overview of this install",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			return withPool(ctx, false, func(cfg config.Config, pool *pgxpool.Pool) error {
				r := statusReport{Version: platform.Version, LatestSchema: db.LatestVersion(), Counts: map[string]int{}, Queues: map[string]int{},
					Features: map[string]bool{}}
				var err error
				if r.SchemaVersion, err = db.SchemaVersion(ctx, pool); err != nil {
					return err
				}
				if r.SchemaVersion == 0 {
					a.printf("The database is empty. Run `purros setup` to get started.\n")
					return nil
				}
				_ = pool.QueryRow(ctx, `SELECT name FROM company LIMIT 1`).Scan(&r.Company)
				count := func(m map[string]int, key, sql string) {
					var n int
					if pool.QueryRow(ctx, sql).Scan(&n) == nil {
						m[key] = n
					}
				}
				count(r.Counts, "locations", `SELECT count(*) FROM locations WHERE archived_at IS NULL`)
				count(r.Counts, "employees", `SELECT count(*) FROM employees WHERE status <> 'terminated' AND archived_at IS NULL`)
				count(r.Counts, "usersActive", `SELECT count(*) FROM users WHERE status = 'active'`)
				count(r.Counts, "usersInvited", `SELECT count(*) FROM users WHERE status = 'invited'`)
				count(r.Counts, "owners", `SELECT count(*) FROM users u JOIN roles r ON r.id = u.role_id WHERE r.system_key = 'owner' AND u.status = 'active'`)
				count(r.Counts, "integrations", `SELECT count(*) FROM integrations`)
				count(r.Counts, "activeSessions", `SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND expires_at > now()`)
				count(r.Queues, "eventsWaiting", `SELECT count(*) FROM outbox_events WHERE dispatched_at IS NULL`)
				count(r.Queues, "webhooksDue", `SELECT count(*) FROM webhook_deliveries WHERE status = 'pending'`)
				count(r.Queues, "webhookEndpointsDisabled", `SELECT count(*) FROM webhook_endpoints WHERE status = 'disabled'`)
				count(r.Queues, "emailsQueued", `SELECT count(*) FROM emails WHERE status = 'queued'`)
				count(r.Queues, "emailsFailed", `SELECT count(*) FROM emails WHERE status = 'failed'`)
				if runs, err := backup.Last(ctx, pool, 1); err == nil && len(runs) > 0 {
					x := runs[0]
					r.LastBackup = &backupRunJSON{Kind: x.Kind, Status: x.Status, File: x.File, Size: x.Size, Error: x.Error, StartedAt: x.StartedAt, Finished: x.FinishedAt}
				}
				r.Backups = map[string]any{"enabled": cfg.Backup.Scheduled(), "dir": cfg.Backup.Dir, "hourUtc": cfg.Backup.HourUTC,
					"keep": cfg.Backup.Keep, "encrypted": cfg.Backup.Passphrase != "", "files": cfg.IncludeFiles(), "s3": ""}
				if cfg.Backup.S3Enabled {
					r.Backups["s3"] = "s3://" + cfg.Backup.S3.Bucket + "/" + cfg.Backup.S3.Prefix
					r.Backups["s3Keep"] = cfg.Backup.S3Keep
				}
				r.Storage = map[string]any{"driver": cfg.Storage.Driver}
				if cfg.Storage.Driver == "s3" {
					r.Storage["location"] = "s3://" + cfg.Storage.S3.Bucket + "/" + cfg.Storage.S3.Prefix
				} else {
					r.Storage["location"] = cfg.Storage.LocalPath
				}
				var files int
				var bytes int64
				_ = pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(size_bytes), 0) FROM attachments WHERE deleted_at IS NULL`).Scan(&files, &bytes)
				r.Storage["attachments"], r.Storage["bytes"] = files, bytes
				r.Email = map[string]any{"configured": cfg.SMTP.Enabled(), "host": cfg.SMTP.Host}
				store := features.NewStore(pool)
				for _, f := range features.Registry {
					if f.Parent() == "" {
						on, _ := store.IsEnabled(ctx, f.Key)
						r.Features[f.Key] = on
					}
				}
				return a.emit(r, func() { a.printStatus(r) })
			})
		},
	}
}

func (a *app) printStatus(r statusReport) {
	a.printf("PurrOS %s — %s\n\n", r.Version, r.Company)
	schema := fmt.Sprintf("%d", r.SchemaVersion)
	if r.SchemaVersion < r.LatestSchema {
		schema += fmt.Sprintf(" (%d pending: run `purros migrate`)", r.LatestSchema-r.SchemaVersion)
	}
	a.table("", [][]string{
		{"Database schema", schema},
		{"Locations", strconv.Itoa(r.Counts["locations"])},
		{"Employees", strconv.Itoa(r.Counts["employees"])},
		{"Accounts", fmt.Sprintf("%d active, %d invited, %d active Owner(s)", r.Counts["usersActive"], r.Counts["usersInvited"], r.Counts["owners"])},
		{"Signed-in sessions", strconv.Itoa(r.Counts["activeSessions"])},
		{"Integrations", strconv.Itoa(r.Counts["integrations"])},
		{"Queues", fmt.Sprintf("%d events, %d webhook deliveries, %d emails waiting", r.Queues["eventsWaiting"], r.Queues["webhooksDue"], r.Queues["emailsQueued"])},
		{"Email", map[bool]string{true: "configured (" + fmt.Sprint(r.Email["host"]) + ")", false: "not configured"}[r.Email["configured"] == true]},
		{"File storage", fmt.Sprintf("%v at %v: %v attachment(s), %s", r.Storage["driver"], r.Storage["location"], r.Storage["attachments"], humanBytes(toInt64(r.Storage["bytes"])))},
		{"Scheduled backups", backupScheduleText(r.Backups)},
		{"Last backup", lastBackupText(r.LastBackup)},
	})
	var on, off []string
	for k, v := range r.Features {
		if v {
			on = append(on, k)
		} else {
			off = append(off, k)
		}
	}
	a.printf("\nFeatures on: %d, off: %d  (details: purros features list)\n", len(on), len(off))
}

func backupScheduleText(b map[string]any) string {
	if b["enabled"] != true {
		return "off (set PURROS_BACKUP_DIR or PURROS_BACKUP_S3_ENABLED)"
	}
	var where []string
	if b["dir"] != "" {
		where = append(where, fmt.Sprintf("%v (keeping %v)", b["dir"], b["keep"]))
	}
	if b["s3"] != "" {
		where = append(where, fmt.Sprintf("%v (keeping %v)", b["s3"], b["s3Keep"]))
	}
	s := fmt.Sprintf("daily at %02d:00 UTC to %s", b["hourUtc"], strings.Join(where, " and "))
	if b["files"] == true {
		s += ", with uploaded files"
	}
	if b["encrypted"] == true {
		s += ", encrypted"
	}
	return s
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func lastBackupText(r *backupRunJSON) string {
	if r == nil {
		return "none yet"
	}
	s := fmt.Sprintf("%s, %s (%s)", r.StartedAt.Local().Format("2006-01-02 15:04"), r.Status, r.Kind)
	if r.Size != nil && *r.Size > 0 {
		s += ", " + humanBytes(*r.Size)
	}
	if r.Error != nil {
		s += ": " + *r.Error
	}
	return s
}

// ---------------------------------------------------------------------------
// doctor
// ---------------------------------------------------------------------------

type check struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok, warn, fail
	Message string `json:"message,omitempty"`
}

func (a *app) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the installation and report problems",
		Long: `Check configuration, database, migrations, the encryption secret, email,
queues, stock ledger integrity and backups. Exits with code 1 when a check fails.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := runDoctor(cmd.Context())
			failed := 0
			for _, c := range checks {
				if c.Status == "fail" {
					failed++
				}
			}
			err := a.emit(checks, func() {
				a.printf("PurrOS doctor (%s)\n", platform.Version)
				for _, c := range checks {
					mark := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[c.Status]
					if c.Message != "" {
						a.printf("  %s %-24s %s\n", mark, c.Name, c.Message)
					} else {
						a.printf("  %s %s\n", mark, c.Name)
					}
				}
				if failed == 0 {
					a.printf("All checks passed.\n")
				} else {
					a.printf("%d check(s) failed.\n", failed)
				}
			})
			if err != nil {
				return err
			}
			if failed > 0 {
				return exitError{code: 1}
			}
			return nil
		},
	}
}

func runDoctor(ctx context.Context) []check {
	var out []check
	add := func(name string, err error) {
		c := check{Name: name, Status: "ok"}
		var w warning
		switch {
		case errors.As(err, &w):
			c.Status, c.Message = "warn", w.msg
		case err != nil:
			c.Status, c.Message = "fail", err.Error()
		}
		out = append(out, c)
	}
	cfg, cfgErr := config.Load(true)
	add("configuration", cfgErr)
	if cfg.DatabaseURL == "" {
		return out
	}
	if u, err := url.Parse(cfg.URL); err == nil && u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		add("public URL", warn("PURROS_URL uses http://; sessions need https:// in production"))
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	add("database connection", err)
	if err != nil {
		return out
	}
	defer pool.Close()

	n, err := db.PendingMigrations(ctx, pool)
	if err == nil && n > 0 {
		err = fmt.Errorf("%d pending (run `purros migrate`)", n)
	}
	add("migrations", err)
	if err != nil {
		return out
	}
	var companies int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM company`).Scan(&companies)
	if err == nil && companies == 0 {
		err = errors.New("not set up yet (run `purros setup`)")
	}
	add("company setup", err)

	var owners, invitedOwners int
	err = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE u.status = 'active'), count(*) FILTER (WHERE u.status = 'invited')
		FROM users u JOIN roles r ON r.id = u.role_id WHERE r.system_key = 'owner'`).Scan(&owners, &invitedOwners)
	switch {
	case err != nil || companies == 0 || owners > 0:
	case invitedOwners > 0:
		err = warn("the Owner hasn't accepted the invitation yet (new link: `purros users sign-in-link <email>`)")
	default:
		err = errors.New("no Owner can sign in (use `purros recover owner <email>`)")
	}
	add("owner account", err)

	if cfg.Secret != "" {
		add("encryption secret", secretCheck(ctx, pool, cfg.Secret))
	}
	if cfg.SMTP.Enabled() {
		add("email (SMTP)", smtpCheck(cfg.SMTP))
	} else {
		add("email (SMTP)", warn("not configured: invitations and sign-in links must be shared by hand"))
	}

	var stuck int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE dispatched_at IS NULL AND created_at < now() - interval '5 minutes'`).Scan(&stuck)
	if err == nil && stuck > 0 {
		err = fmt.Errorf("%d events waiting more than 5 minutes (is the worker running?)", stuck)
	}
	add("event outbox", err)
	var failing int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM webhook_endpoints WHERE status = 'disabled'`).Scan(&failing)
	if err == nil && failing > 0 {
		err = warn(fmt.Sprintf("%d webhook endpoint(s) disabled after repeated failures", failing))
	}
	add("webhook endpoints", err)
	var failedEmails int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM emails WHERE status = 'failed' AND created_at > now() - interval '7 days'`).Scan(&failedEmails)
	if err == nil && failedEmails > 0 {
		err = warn(fmt.Sprintf("%d email(s) failed in the last 7 days", failedEmails))
	}
	add("email delivery", err)

	var drift int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM stock_levels l
		LEFT JOIN (SELECT item_id, location_id, sum(quantity) AS q FROM stock_movements GROUP BY 1, 2) m
		  ON m.item_id = l.item_id AND m.location_id = l.location_id
		WHERE l.on_hand <> coalesce(m.q, 0)`).Scan(&drift)
	if err == nil && drift > 0 {
		err = fmt.Errorf("%d stock level(s) don't match the ledger", drift)
	}
	add("stock ledger", err)

	var badTZ []string
	rows, err := pool.Query(ctx, `SELECT name, timezone FROM locations`)
	if err == nil {
		for rows.Next() {
			var name, tz string
			if rows.Scan(&name, &tz) == nil {
				if _, e := time.LoadLocation(tz); e != nil {
					badTZ = append(badTZ, name+" ("+tz+")")
				}
			}
		}
		rows.Close()
		if len(badTZ) > 0 {
			err = fmt.Errorf("unknown time zone for %s", strings.Join(badTZ, ", "))
		}
	}
	add("location time zones", err)

	if store, err := storage.New(cfg.Storage); err != nil {
		add("file storage", err)
	} else {
		add("file storage", storageCheck(ctx, store))
	}
	if cfg.Backup.S3Enabled {
		r, err := storage.NewS3(cfg.Backup.S3)
		if err == nil {
			_, err = r.List(ctx, "")
			if err != nil {
				err = fmt.Errorf("can't list %s: %w", r.Describe(), err)
			}
		}
		add("backup bucket (S3)", err)
	}

	runs, err := backup.Last(ctx, pool, 20)
	if err == nil {
		var lastOK *time.Time
		for _, r := range runs {
			if r.Status == "succeeded" {
				t := r.StartedAt
				lastOK = &t
				break
			}
		}
		switch {
		case !cfg.Backup.Scheduled() && lastOK == nil:
			err = warn("no backups yet: set PURROS_BACKUP_DIR or PURROS_BACKUP_S3_ENABLED, or run `purros backup create`")
		case cfg.Backup.Scheduled() && (lastOK == nil || time.Since(*lastOK) > 36*time.Hour):
			err = errors.New("scheduled backups are on but none succeeded in the last 36 hours (is the worker running?)")
		case len(runs) > 0 && runs[0].Status == "failed":
			err = warn("the latest backup failed: " + deref(runs[0].Error))
		case len(runs) > 0 && runs[0].UploadError != nil:
			err = warn("the latest backup wasn't uploaded to S3: " + *runs[0].UploadError)
		}
	}
	add("backups", err)
	return out
}

type warning struct{ msg string }

func (w warning) Error() string { return w.msg }

func warn(msg string) error { return warning{msg} }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// secretCheck decrypts a sample of sealed values with PURROS_SECRET.
func secretCheck(ctx context.Context, pool *pgxpool.Pool, secret string) error {
	box, err := secure.NewBox(secret)
	if err != nil {
		return err
	}
	bad, total := 0, 0
	for _, col := range secure.SealedColumns {
		rows, err := pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT 20`, col.Column, col.Table, col.Column))
		if err != nil {
			return err
		}
		for rows.Next() {
			var sealed []byte
			if rows.Scan(&sealed) == nil {
				total++
				if _, err := box.Open(sealed); err != nil {
					bad++
				}
			}
		}
		rows.Close()
	}
	if bad > 0 {
		return fmt.Errorf("%d of %d encrypted values can't be read: PURROS_SECRET isn't the one this data was encrypted with", bad, total)
	}
	return nil
}

// smtpCheck connects to the mail server without sending anything.
func smtpCheck(s config.SMTP) error {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("can't reach %s: %w", addr, err)
	}
	defer conn.Close()
	if s.Secure {
		return nil // implicit TLS: reaching the port is as far as we go without sending
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("%s isn't answering as an SMTP server: %w", addr, err)
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); !ok && s.RequireTLS {
		return errors.New("the server doesn't offer STARTTLS (required by SMTP_REQUIRE_TLS)")
	}
	return c.Quit()
}

// storageCheck writes, reads and deletes a small test file.
func storageCheck(ctx context.Context, d storage.Driver) error {
	key := "purros-check/" + time.Now().UTC().Format("20060102T150405.000000000")
	payload := "purros storage check"
	if err := d.Put(ctx, key, strings.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		return fmt.Errorf("can't write to %s: %w", d.Describe(), err)
	}
	defer d.Delete(context.WithoutCancel(ctx), key) //nolint:errcheck
	body, _, err := d.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("can't read from %s: %w", d.Describe(), err)
	}
	defer body.Close()
	got := make([]byte, len(payload)+1)
	n, _ := io.ReadFull(body, got)
	if string(got[:n]) != payload {
		return fmt.Errorf("%s returned different content than was written", d.Describe())
	}
	return d.Delete(ctx, key)
}
