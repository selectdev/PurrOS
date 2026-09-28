package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/events"
	purrmail "github.com/selectdev/purros/api/internal/mail"
	"github.com/selectdev/purros/api/internal/secure"
	"github.com/spf13/cobra"
)

func randomSecret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// init
// ---------------------------------------------------------------------------

func (a *app) initCmd() *cobra.Command {
	var dir, publicURL string
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the configuration files with generated secrets",
		Long: `Create the configuration directory used by docker-compose.yml:

  purros.env     settings for PurrOS, with a new PURROS_SECRET
  postgres.env   settings for the database container, with a new password

An existing PURROS_SECRET and database password are always kept: replacing the
secret would make encrypted data unreadable.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			appPath := filepath.Join(dir, config.EnvFileName)
			dbPath := filepath.Join(dir, "postgres.env")
			existing := map[string]string{}
			for _, p := range []string{appPath, dbPath} {
				if _, err := os.Stat(p); err != nil {
					continue
				}
				if !force {
					return fmt.Errorf("%s already exists (use --force to rewrite it; its PURROS_SECRET and passwords are kept)", p)
				}
				vals, err := parseEnvFile(p)
				if err != nil {
					return err
				}
				maps.Copy(existing, vals)
			}
			if publicURL == "" {
				publicURL = existing["PURROS_URL"]
			}
			if publicURL == "" && a.canPrompt() {
				var err error
				if publicURL, err = a.ask("Public URL of this PurrOS (e.g. https://erp.example.com)", "http://localhost:8080", validURL); err != nil {
					return err
				}
			}
			if publicURL == "" {
				publicURL = "http://localhost:8080"
			}
			if err := validURL(publicURL); err != nil {
				return err
			}
			secret := existing["PURROS_SECRET"]
			if secret == "" {
				secret = randomSecret(36)
			}
			dbPass := existing["POSTGRES_PASSWORD"]
			if dbPass == "" {
				dbPass = randomSecret(24)
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
			today := time.Now().Format("2006-01-02")
			files := []struct{ path, content string }{
				{appPath, fmt.Sprintf(appEnvTemplate, today, strings.TrimRight(publicURL, "/"), secret, dbPass)},
				{dbPath, fmt.Sprintf(postgresEnvTemplate, today, dbPass)},
			}
			for _, f := range files {
				if err := os.WriteFile(f.path, []byte(f.content), 0o600); err != nil {
					return err
				}
			}
			a.printf("Wrote %s and %s (readable only by you).\n\n", appPath, dbPath)
			a.printf("Keep a copy of PURROS_SECRET somewhere safe: without it, encrypted data in the\ndatabase and in backups can't be read.\n\n")
			a.printf("Next:\n  docker compose up -d\n  docker compose exec api purros setup\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", config.ConfigDir(), "configuration directory to write (default $PURROS_CONFIG_DIR or ./config)")
	cmd.Flags().StringVar(&publicURL, "url", "", "public URL (PURROS_URL)")
	cmd.Flags().BoolVar(&force, "force", false, "rewrite existing files, keeping their secrets")
	return cmd
}

const appEnvTemplate = `# PurrOS configuration, created by "purros init" on %s.
# See docs/getting-started/configuration.md for every option.

# --- Required -------------------------------------------------------------
PURROS_URL=%s
# Encrypts secrets in the database and signs sessions. Keep a copy somewhere
# safe; never change it (use "purros secret rotate" to replace it).
PURROS_SECRET=%s
# The password must match POSTGRES_PASSWORD in postgres.env.
DATABASE_URL=postgres://purros:%s@db:5432/purros?sslmode=disable

# --- Optional -------------------------------------------------------------
PURROS_RUN_WORKER=true
PURROS_TRUST_PROXY=true
LOG_LEVEL=info

# Nightly backups into the state volume. Leave empty to turn them off.
PURROS_BACKUP_DIR=/var/lib/purros/backups
# PURROS_BACKUP_HOUR=2
# PURROS_BACKUP_KEEP=14
# PURROS_BACKUP_PASSPHRASE=

# Email
# SMTP_HOST=smtp.your-provider.example
# SMTP_PORT=587
# SMTP_USER=apikey
# SMTP_PASSWORD=
# SMTP_FROM="Acme Operations <ops@acme.example>"
`

const postgresEnvTemplate = `# Database container settings, created by "purros init" on %s.
POSTGRES_USER=purros
POSTGRES_PASSWORD=%s
POSTGRES_DB=purros
`

func validURL(s string) error {
	if !regexp.MustCompile(`^https?://[^/\s]+(/.*)?$`).MatchString(s) {
		return errors.New("enter a URL starting with http:// or https://")
	}
	return nil
}

// ---------------------------------------------------------------------------
// setup
// ---------------------------------------------------------------------------

func validEmail(s string) error {
	if _, err := mail.ParseAddress(s); err != nil || !strings.Contains(s, "@") {
		return errors.New("enter a valid email address")
	}
	return nil
}

func validTZ(s string) error {
	if _, err := time.LoadLocation(s); err != nil || s == "" {
		return errors.New("enter a time zone like America/Chicago or Europe/London")
	}
	return nil
}

func validCurrency(s string) error {
	if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(s) {
		return errors.New("enter a 3-letter currency code like USD or EUR")
	}
	return nil
}

func validCutoff(s string) error {
	if _, err := time.Parse("15:04", s); err != nil {
		return errors.New("enter a time like 00:00 or 04:00")
	}
	return nil
}

func notEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	return nil
}

// localTZ guesses the server's time zone.
func localTZ() string {
	if tz := os.Getenv("TZ"); tz != "" && validTZ(tz) == nil {
		return tz
	}
	if name := time.Local.String(); name != "Local" && validTZ(name) == nil {
		return name
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, after, ok := strings.Cut(target, "zoneinfo/"); ok {
			if tz := after; validTZ(tz) == nil {
				return tz
			}
		}
	}
	return "UTC"
}

type setupResult struct {
	OwnerID    string `json:"ownerId"`
	OwnerEmail string `json:"ownerEmail"`
	SignInLink string `json:"signInLink"`
	LocationID string `json:"locationId,omitempty"`
}

func (a *app) setupCmd() *cobra.Command {
	in := SetupInput{}
	loc := LocationInput{}
	var noLocation bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Create the company, the first Owner and (optionally) the first location",
		Long: `Create the company, the system roles, the first Owner and optionally the first
location, then print a one-time link for the Owner to set a password.

Run it without flags in a terminal for a guided setup, or pass every value as
flags for scripted installs. It only works once.`,
		Example: `  purros setup
  purros setup --company "Acme Coffee" --owner-email owner@example.com --timezone America/Chicago \
    --location-name "Store 101" --location-external-id 101 --location-cutoff 04:00`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			flags := cmd.Flags()
			prompting := a.canPrompt()
			if !prompting {
				var missing []string
				if in.Company == "" {
					missing = append(missing, "--company")
				}
				if in.OwnerEmail == "" {
					missing = append(missing, "--owner-email")
				}
				if len(missing) > 0 {
					return fmt.Errorf("missing %s (or run in a terminal for guided setup)", strings.Join(missing, ", "))
				}
			}
			return withPool(ctx, true, func(cfg config.Config, pool *pgxpool.Pool) error {
				if err := db.Migrate(ctx, pool); err != nil {
					return err
				}
				var exists bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM company)`).Scan(&exists); err != nil {
					return err
				}
				if exists {
					return errors.New("setup has already been run. To get back in, use `purros recover owner <email>`")
				}
				if prompting {
					a.printf("Let's set up PurrOS. Press Enter to accept the suggestion in [brackets].\n\n")
					var err error
					ask := func(dst *string, label, def string, check func(string) error, flag string) {
						if err != nil || (flags.Changed(flag) && *dst != "") {
							return
						}
						if *dst != "" {
							def = *dst
						}
						*dst, err = a.ask(label, def, check)
					}
					ask(&in.Company, "Company name", "", notEmpty, "company")
					ask(&in.Timezone, "Company time zone", localTZ(), validTZ, "timezone")
					ask(&in.Currency, "Currency", "USD", validCurrency, "currency")
					ask(&in.OwnerEmail, "Owner's email", "", validEmail, "owner-email")
					ask(&in.OwnerName, "Owner's name", "", notEmpty, "owner-name")
					if err != nil {
						return err
					}
					if !noLocation && loc.Name == "" {
						ans, err := a.ask("Add your first location now? (Y/n)", "y", nil)
						if err != nil {
							return err
						}
						noLocation = strings.HasPrefix(strings.ToLower(ans), "n")
					}
					if !noLocation {
						if loc.Timezone == "" {
							loc.Timezone = in.Timezone
						}
						if loc.Currency == "" {
							loc.Currency = in.Currency
						}
						ask(&loc.Name, "Location name", "", notEmpty, "location-name")
						ask(&loc.ExternalID, "Its ID in your POS or other systems (optional)", "", nil, "location-external-id")
						ask(&loc.Timezone, "Location time zone", loc.Timezone, validTZ, "location-timezone")
						ask(&loc.Cutoff, "Time the business day ends (e.g. 04:00 for late-night venues)", "00:00", validCutoff, "location-cutoff")
						if err != nil {
							return err
						}
					}
					a.printf("\n")
				}
				if err := validEmail(in.OwnerEmail); err != nil {
					return fmt.Errorf("--owner-email: %w", err)
				}
				if err := validCurrency(in.Currency); err != nil {
					return fmt.Errorf("--currency: %w", err)
				}
				ownerID, err := Setup(ctx, pool, in)
				if err != nil {
					return err
				}
				res := setupResult{OwnerID: ownerID, OwnerEmail: in.OwnerEmail}
				if loc.Name != "" && !noLocation {
					if loc.Timezone == "" {
						loc.Timezone = in.Timezone
					}
					if loc.Currency == "" {
						loc.Currency = in.Currency
					}
					if loc.Cutoff == "" {
						loc.Cutoff = "00:00"
					}
					if res.LocationID, err = CreateLocation(ctx, pool, loc); err != nil {
						return fmt.Errorf("company created, but the location failed: %w (add it with `purros locations create`)", err)
					}
				}
				if res.SignInLink, err = SignInLink(ctx, pool, cfg.URL, in.OwnerEmail, false); err != nil {
					return err
				}
				return a.emit(res, func() {
					a.printf("✓ Company %q created\n✓ Owner account: %s\n", in.Company, in.OwnerEmail)
					if res.LocationID != "" {
						a.printf("✓ Location %q created (%s)\n", loc.Name, res.LocationID)
					}
					a.printf("\nOpen this link to set the Owner's password (valid 7 days, works once):\n  %s\n\n", res.SignInLink)
					a.printf("Next steps:\n")
					if res.LocationID == "" {
						a.printf("  purros locations create --name \"Store 1\"     add a location\n")
					}
					a.printf("  purros integrations register --manifest …     connect your POS, timeclock or store\n")
					a.printf("  purros doctor                                 check the installation\n")
					if cfg.Backup.Dir == "" {
						a.printf("  set PURROS_BACKUP_DIR                         turn on nightly backups\n")
					}
				})
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.Company, "company", "", "company name")
	f.StringVar(&in.Currency, "currency", "USD", "default currency (ISO 4217)")
	f.StringVar(&in.Timezone, "timezone", "", "company time zone, e.g. America/Chicago (default: this server's)")
	f.StringVar(&in.OwnerEmail, "owner-email", "", "email of the first Owner")
	f.StringVar(&in.OwnerName, "owner-name", "", "name of the first Owner")
	f.StringVar(&loc.Name, "location-name", "", "create a first location with this name")
	f.StringVar(&loc.ExternalID, "location-external-id", "", "the location's ID in your POS or other systems")
	f.StringVar(&loc.Timezone, "location-timezone", "", "the location's time zone (default: the company's)")
	f.StringVar(&loc.Currency, "location-currency", "", "the location's currency (default: the company's)")
	f.StringVar(&loc.Cutoff, "location-cutoff", "", "time the location's business day ends, e.g. 04:00")
	f.BoolVar(&noLocation, "no-location", false, "don't ask for a first location")
	cmd.PreRun = func(*cobra.Command, []string) {
		if in.Timezone == "" && !a.canPrompt() {
			in.Timezone = localTZ()
		}
	}
	return cmd
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

func mask(s string) string {
	switch {
	case s == "":
		return ""
	case len(s) <= 8:
		return "********"
	}
	return s[:3] + "…" + s[len(s)-2:]
}

func maskURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	return u.Redacted()
}

func (a *app) configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Show and check configuration"}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration, with secrets masked",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(false)
			if err != nil && cfg.DatabaseURL == "" && cfg.URL == "" {
				return err
			}
			vals := map[string]string{
				"PURROS_URL": cfg.URL, "PURROS_SECRET": mask(cfg.Secret), "DATABASE_URL": maskURL(cfg.DatabaseURL),
				"REDIS_URL": maskURL(cfg.RedisURL), "PURROS_LISTEN": cfg.ListenAddr, "LOG_LEVEL": cfg.LogLevel,
				"PURROS_STATE_DIR":   cfg.StateDir,
				"PURROS_TRUST_PROXY": fmt.Sprint(cfg.TrustProxy), "PURROS_RUN_WORKER": fmt.Sprint(cfg.RunWorker),
				"API_RATE_LIMIT_PER_MIN": fmt.Sprint(cfg.RateLimitPerMin), "API_INGEST_RATE_LIMIT_PER_MIN": fmt.Sprint(cfg.IngestRateLimitPerMin),
				"API_MAX_BATCH_SIZE": fmt.Sprint(cfg.MaxBatchSize),
				"SMTP_HOST":          cfg.SMTP.Host, "SMTP_PORT": fmt.Sprint(cfg.SMTP.Port), "SMTP_USER": cfg.SMTP.User,
				"SMTP_PASSWORD": mask(cfg.SMTP.Password), "SMTP_FROM": cfg.SMTP.From,
				"PURROS_BACKUP_DIR": cfg.Backup.Dir, "PURROS_BACKUP_HOUR": fmt.Sprint(cfg.Backup.HourUTC),
				"PURROS_BACKUP_KEEP": fmt.Sprint(cfg.Backup.Keep), "PURROS_BACKUP_PASSPHRASE": mask(cfg.Backup.Passphrase),
				"PURROS_BACKUP_FILES": cfg.Backup.Files, "PURROS_BACKUP_S3_ENABLED": fmt.Sprint(cfg.Backup.S3Enabled),
				"PURROS_BACKUP_S3_BUCKET": cfg.Backup.S3.Bucket, "PURROS_BACKUP_S3_PREFIX": cfg.Backup.S3.Prefix,
				"PURROS_BACKUP_S3_ENDPOINT": cfg.Backup.S3.Endpoint, "PURROS_BACKUP_S3_SECRET_ACCESS_KEY": mask(cfg.Backup.S3.SecretAccessKey),
				"PURROS_BACKUP_S3_KEEP": fmt.Sprint(cfg.Backup.S3Keep),
				"STORAGE_DRIVER":        cfg.Storage.Driver, "STORAGE_LOCAL_PATH": cfg.Storage.LocalPath, "STORAGE_S3_BUCKET": cfg.Storage.S3.Bucket,
				"STORAGE_S3_ENDPOINT": cfg.Storage.S3.Endpoint, "STORAGE_S3_SECRET_ACCESS_KEY": mask(cfg.Storage.S3.SecretAccessKey),
				"STORAGE_MAX_UPLOAD_MB": fmt.Sprint(cfg.Storage.MaxUploadMB),
			}
			return a.emit(vals, func() {
				keys := make([]string, 0, len(vals))
				for k := range vals {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				var rows [][]string
				for _, k := range keys {
					v := vals[k]
					if v == "" {
						v = "-"
					}
					rows = append(rows, []string{k, v})
				}
				a.table("VARIABLE\tVALUE", rows)
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Validate the configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := config.Load(true); err != nil {
				return err
			}
			a.printf("Configuration is valid.\n")
			return nil
		},
	})
	return cmd
}

// ---------------------------------------------------------------------------
// secret
// ---------------------------------------------------------------------------

func (a *app) secretCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "secret", Short: "Generate, check and rotate PURROS_SECRET"}
	cmd.AddCommand(&cobra.Command{
		Use:   "generate",
		Short: "Print a new random secret",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			a.printf("%s\n", randomSecret(36))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Check that PURROS_SECRET can read the encrypted data",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), true, func(cfg config.Config, pool *pgxpool.Pool) error {
				if err := secretCheck(cmd.Context(), pool, cfg.Secret); err != nil {
					return err
				}
				a.printf("PURROS_SECRET reads the encrypted data (fingerprint %s).\n", secure.Fingerprint(cfg.Secret))
				return nil
			})
		},
	})
	var newFile string
	rotate := &cobra.Command{
		Use:   "rotate",
		Short: "Re-encrypt everything with a new secret",
		Long: `Re-encrypt every encrypted value (integration settings, webhook secrets,
authenticator secrets) with a new secret, in one transaction.

Stop PurrOS first. Afterwards, put the new secret in PURROS_SECRET everywhere
PurrOS runs, and start it again. Backups made before the rotation still need
the old secret.`,
		Example: `  purros secret generate > new-secret && chmod 600 new-secret
  purros secret rotate --new-secret-file new-secret`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if newFile == "" {
				return errors.New("--new-secret-file is required")
			}
			b, err := os.ReadFile(newFile)
			if err != nil {
				return err
			}
			newSecret := strings.TrimSpace(string(b))
			if len(newSecret) < 32 {
				return errors.New("the new secret must be at least 32 characters")
			}
			return withPool(cmd.Context(), true, func(cfg config.Config, pool *pgxpool.Pool) error {
				if newSecret == cfg.Secret {
					return errors.New("the new secret is the same as PURROS_SECRET")
				}
				if err := a.confirm("Re-encrypt all secrets with the new key?"); err != nil {
					return err
				}
				n, err := RotateSecret(cmd.Context(), pool, cfg.Secret, newSecret)
				if err != nil {
					return err
				}
				a.printf("Re-encrypted %d value(s). New fingerprint: %s\n\n", n, secure.Fingerprint(newSecret))
				a.printf("Now set PURROS_SECRET to the new secret everywhere PurrOS runs, restart it, and\nkeep the old secret as long as you keep backups made before today.\n")
				return nil
			})
		},
	}
	rotate.Flags().StringVar(&newFile, "new-secret-file", "", "file holding the new secret")
	cmd.AddCommand(rotate)
	return cmd
}

// RotateSecret re-encrypts every sealed value from oldSecret to newSecret.
func RotateSecret(ctx context.Context, pool *pgxpool.Pool, oldSecret, newSecret string) (int, error) {
	oldBox, err := secure.NewBox(oldSecret)
	if err != nil {
		return 0, err
	}
	newBox, err := secure.NewBox(newSecret)
	if err != nil {
		return 0, err
	}
	n := 0
	err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		for _, col := range secure.SealedColumns {
			rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s, %s FROM %s WHERE %s IS NOT NULL FOR UPDATE`, col.Key, col.Column, col.Table, col.Column))
			if err != nil {
				return err
			}
			type row struct {
				key    string
				sealed []byte
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
				var x row
				return x, r.Scan(&x.key, &x.sealed)
			})
			if err != nil {
				return err
			}
			for _, x := range list {
				plain, err := oldBox.Open(x.sealed)
				if err != nil {
					return fmt.Errorf("%s.%s of %s can't be decrypted with the current PURROS_SECRET; nothing was changed", col.Table, col.Column, x.key)
				}
				if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s = $2 WHERE %s = $1`, col.Table, col.Column, col.Key), x.key, newBox.Seal(plain)); err != nil {
					return err
				}
				n++
			}
		}
		return events.Audit(ctx, tx, events.AuditEntry{Actor: events.Actor{Type: "system", Name: "cli"}, Action: "secret.rotate",
			After: map[string]any{"values": n, "fingerprint": secure.Fingerprint(newSecret)}})
	})
	return n, err
}

// ---------------------------------------------------------------------------
// email
// ---------------------------------------------------------------------------

func (a *app) emailCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "email", Short: "Test and inspect outgoing email"}
	var to string
	test := &cobra.Command{
		Use:   "test",
		Short: "Send a test email through the configured SMTP server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if to == "" {
				return errors.New("--to is required")
			}
			cfg, _ := config.Load(false)
			if !cfg.SMTP.Enabled() {
				return errors.New("SMTP_HOST is not set")
			}
			s := purrmail.NewSender(nil, cfg.SMTP, logger())
			if err := s.Send(cmd.Context(), purrmail.Message{To: to, Subject: "PurrOS test email", Kind: "test",
				Body: "This is a test email from PurrOS. Email is working."}); err != nil {
				return fmt.Errorf("sending through %s:%d failed: %w", cfg.SMTP.Host, cfg.SMTP.Port, err)
			}
			a.printf("Sent a test email to %s through %s:%d.\n", to, cfg.SMTP.Host, cfg.SMTP.Port)
			return nil
		},
	}
	test.Flags().StringVar(&to, "to", "", "recipient")
	cmd.AddCommand(test)
	var limit int
	log := &cobra.Command{
		Use:   "log",
		Short: "Recent emails: recipient, kind and status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				rows, err := pool.Query(cmd.Context(), `SELECT created_at, to_address, kind, status, attempts, coalesce(last_error, '')
					FROM emails ORDER BY created_at DESC LIMIT $1`, limit)
				if err != nil {
					return err
				}
				type entry struct {
					At       time.Time `json:"createdAt"`
					To       string    `json:"to"`
					Kind     string    `json:"kind"`
					Status   string    `json:"status"`
					Attempts int       `json:"attempts"`
					Error    string    `json:"error,omitempty"`
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (entry, error) {
					var e entry
					return e, r.Scan(&e.At, &e.To, &e.Kind, &e.Status, &e.Attempts, &e.Error)
				})
				if err != nil {
					return err
				}
				return a.emit(list, func() {
					var out [][]string
					for _, e := range list {
						out = append(out, []string{e.At.Local().Format("2006-01-02 15:04"), e.To, e.Kind, e.Status, fmt.Sprint(e.Attempts), e.Error})
					}
					a.table("WHEN\tTO\tKIND\tSTATUS\tATTEMPTS\tERROR", out)
				})
			})
		},
	}
	log.Flags().IntVar(&limit, "limit", 30, "how many")
	cmd.AddCommand(log)
	return cmd
}
