// Package cli implements the purros command.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/mail"
	"github.com/selectdev/purros/api/internal/modules/insights"
	"github.com/selectdev/purros/api/internal/modules/platform"
	"github.com/selectdev/purros/api/internal/secure"
	"github.com/selectdev/purros/api/internal/server"
	"github.com/selectdev/purros/api/internal/webhooks"
)

const usage = `PurrOS API server and admin tool.

Usage: purros <command> [options]

Server
  serve                   Run the API (and, by default, the background worker)
  worker                  Run only the background worker
  migrate                 Apply database migrations
  doctor                  Check the installation

Setup
  setup                   Create the company and first Owner
  locations create        Add a location
  locations list          List locations
  users sign-in-link      Print a one-time sign-in link for an account (--email, --reset-mfa)

Email
  email test              Send a test email (--to)

Integrations
  integrations register   Register an integration from its manifest; prints its API key once
  integrations list       List integrations
  api-keys revoke <id>    Revoke an API key

Features
  features list           Show every feature and whether it is on
  features enable <key>   Switch a feature on (with what it needs)
  features disable <key>  Switch a feature off (with what depends on it)

  version                 Print the version

Configuration comes from environment variables; see docs/getting-started/configuration.md.
`

// Run executes the command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, rest := args[0], args[1:]
	if len(rest) > 0 && (cmd == "locations" || cmd == "integrations" || cmd == "features" || cmd == "api-keys" || cmd == "users" || cmd == "email") {
		cmd, rest = cmd+" "+rest[0], rest[1:]
	}
	var err error
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, "purros", platform.Version)
	case "serve":
		err = serve(ctx, rest, stdout)
	case "worker":
		err = worker(ctx)
	case "migrate":
		err = migrate(ctx, stdout)
	case "doctor":
		err = doctor(ctx, stdout)
	case "setup":
		err = setup(ctx, rest, stdout)
	case "locations create":
		err = locationsCreate(ctx, rest, stdout)
	case "locations list":
		err = locationsList(ctx, stdout)
	case "users sign-in-link":
		err = usersSignInLink(ctx, rest, stdout)
	case "email test":
		err = emailTest(ctx, rest, stdout)
	case "integrations register":
		err = integrationsRegister(ctx, rest, stdout)
	case "integrations list":
		err = integrationsList(ctx, stdout)
	case "api-keys revoke":
		err = apiKeysRevoke(ctx, rest, stdout)
	case "features list":
		err = featuresList(ctx, stdout)
	case "features enable", "features disable":
		err = featuresSet(ctx, strings.HasSuffix(cmd, "enable"), rest, stdout)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", strings.Join(args, " "), usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func logger() *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func connect(ctx context.Context, requireSecret bool) (config.Config, *pgxpool.Pool, error) {
	cfg, err := config.Load(requireSecret)
	if err != nil {
		return cfg, nil, err
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	return cfg, pool, err
}

func serve(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	noMigrate := fs.Bool("no-migrate", false, "don't apply migrations on start")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, pool, err := connect(ctx, true)
	if err != nil {
		return err
	}
	defer pool.Close()
	log := logger()
	if !*noMigrate {
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
}

func worker(ctx context.Context) error {
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
}

func migrate(ctx context.Context, stdout io.Writer) error {
	_, pool, err := connect(ctx, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Migrations are up to date.")
	return nil
}

func doctor(ctx context.Context, stdout io.Writer) error {
	ok := true
	check := func(name string, err error) {
		if err != nil {
			ok = false
			fmt.Fprintf(stdout, "  ✗ %-22s %v\n", name, err)
		} else {
			fmt.Fprintf(stdout, "  ✓ %s\n", name)
		}
	}
	fmt.Fprintln(stdout, "PurrOS doctor")
	cfg, cfgErr := config.Load(true)
	check("configuration", cfgErr)
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is not set")
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	check("database connection", err)
	if err != nil {
		return errors.New("checks failed")
	}
	defer pool.Close()
	n, err := db.PendingMigrations(ctx, pool)
	if err == nil && n > 0 {
		err = fmt.Errorf("%d pending (run `purros migrate`)", n)
	}
	check("migrations", err)
	var companies int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM company`).Scan(&companies)
	if err == nil && companies == 0 {
		err = errors.New("not set up yet (run `purros setup`)")
	}
	check("company setup", err)
	var stuck int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE dispatched_at IS NULL AND created_at < now() - interval '5 minutes'`).Scan(&stuck)
	if err == nil && stuck > 0 {
		err = fmt.Errorf("%d events waiting more than 5 minutes (is the worker running?)", stuck)
	}
	check("event outbox", err)
	var failing int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM webhook_endpoints WHERE status = 'disabled'`).Scan(&failing)
	if err == nil && failing > 0 {
		err = fmt.Errorf("%d webhook endpoint(s) disabled after repeated failures", failing)
	}
	check("webhook endpoints", err)
	if !ok {
		return errors.New("some checks failed")
	}
	fmt.Fprintln(stdout, "All checks passed.")
	return nil
}

func setup(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	in := SetupInput{}
	fs.StringVar(&in.Company, "company", "", "company name")
	fs.StringVar(&in.Currency, "currency", "USD", "default currency (ISO 4217)")
	fs.StringVar(&in.Timezone, "timezone", "UTC", "company time zone, e.g. America/Chicago")
	fs.StringVar(&in.OwnerEmail, "owner-email", "", "email of the first Owner")
	fs.StringVar(&in.OwnerName, "owner-name", "", "name of the first Owner")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, pool, err := connect(ctx, true)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	id, err := Setup(ctx, pool, in)
	if err != nil {
		return err
	}
	cfg, _ := config.Load(false)
	link, err := SignInLink(ctx, pool, cfg.URL, in.OwnerEmail, false)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Company %q created. Owner account: %s (%s)\n", in.Company, in.OwnerEmail, id)
	fmt.Fprintf(stdout, "Set the Owner's password with this link (valid 7 days, works once):\n  %s\n", link)
	fmt.Fprintln(stdout, "Next: add a location with `purros locations create`, then register integrations.")
	return nil
}

// jobs are the worker's periodic jobs.
func jobs(cfg config.Config, pool *pgxpool.Pool, fs *features.Store, log *slog.Logger) []webhooks.Job {
	out := []webhooks.Job{insights.AlertJob(pool, fs)}
	if cfg.SMTP.Enabled() {
		out = append(out, mail.NewSender(pool, cfg.SMTP, log).Job())
	} else {
		log.Info("email is off: SMTP_HOST is not set")
	}
	return out
}

func usersSignInLink(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("users sign-in-link", flag.ContinueOnError)
	email := fs.String("email", "", "the account's email")
	resetMFA := fs.Bool("reset-mfa", false, "also remove their authenticator app and recovery codes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email is required")
	}
	cfg, pool, err := connect(ctx, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	link, err := SignInLink(ctx, pool, cfg.URL, *email, *resetMFA)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "One-time link for %s (share it privately):\n  %s\n", *email, link)
	return nil
}

func emailTest(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("email test", flag.ContinueOnError)
	to := fs.String("to", "", "recipient")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return errors.New("--to is required")
	}
	cfg, err := config.Load(false)
	if err != nil && cfg.DatabaseURL != "" {
		return err
	}
	if !cfg.SMTP.Enabled() {
		return errors.New("SMTP_HOST is not set")
	}
	s := mail.NewSender(nil, cfg.SMTP, logger())
	if err := s.Send(ctx, mail.Message{To: *to, Subject: "PurrOS test email", Kind: "test",
		Body: "This is a test email from PurrOS. Email is working."}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Sent a test email to %s through %s:%d.\n", *to, cfg.SMTP.Host, cfg.SMTP.Port)
	return nil
}

func locationsCreate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("locations create", flag.ContinueOnError)
	in := LocationInput{}
	fs.StringVar(&in.Name, "name", "", "location name")
	fs.StringVar(&in.Code, "code", "", "short code, e.g. 101")
	fs.StringVar(&in.ExternalID, "external-id", "", "ID of this location in your POS or other systems")
	fs.StringVar(&in.Timezone, "timezone", "UTC", "time zone, e.g. Europe/London")
	fs.StringVar(&in.Currency, "currency", "USD", "currency (ISO 4217)")
	fs.StringVar(&in.Cutoff, "cutoff", "00:00", "time the business day ends, e.g. 04:00")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, pool, err := connect(ctx, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	id, err := CreateLocation(ctx, pool, in)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Location %q created: %s\n", in.Name, id)
	return nil
}

func locationsList(ctx context.Context, stdout io.Writer) error {
	_, pool, err := connect(ctx, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `SELECT id, name, coalesce(external_id, ''), timezone, currency FROM locations ORDER BY name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tEXTERNAL ID\tTIMEZONE\tCURRENCY")
	for rows.Next() {
		var id, name, ext, tz, cur string
		if err := rows.Scan(&id, &name, &ext, &tz, &cur); err != nil {
			return err
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", id, name, ext, tz, cur)
	}
	tw.Flush()
	return rows.Err()
}

type kvFlags map[string]any

func (k kvFlags) String() string { return "" }
func (k kvFlags) Set(v string) error {
	key, val, ok := strings.Cut(v, "=")
	if !ok || key == "" {
		return errors.New("use key=value")
	}
	k[key] = val
	return nil
}

func integrationsRegister(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("integrations register", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "purros-integration.json", "path to the manifest")
	approveSensitive := fs.Bool("approve-sensitive", false, "approve the people:sensitive scope (Owner decision)")
	cfgVals := kvFlags{}
	fs.Var(cfgVals, "config", "config value as key=value (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, pool, err := connect(ctx, true)
	if err != nil {
		return err
	}
	defer pool.Close()
	m, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	if err := m.Validate(ctx, features.NewStore(pool), *approveSensitive); err != nil {
		return err
	}
	box, err := secure.NewBox(cfg.Secret)
	if err != nil {
		return err
	}
	reg, err := RegisterIntegration(ctx, pool, box, m, cfgVals)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Integration %q registered (%s).\n\n", m.DisplayName, reg.IntegrationID)
	fmt.Fprintf(stdout, "  PURROS_INTEGRATION_KEY=%s\n", reg.APIKey)
	if reg.WebhookSecret != "" {
		fmt.Fprintf(stdout, "  PURROS_WEBHOOK_SECRET=%s\n", reg.WebhookSecret)
	}
	fmt.Fprintln(stdout, "\nStore these now: they are shown only once.")
	return nil
}

func integrationsList(ctx context.Context, stdout io.Writer) error {
	_, pool, err := connect(ctx, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `
		SELECT i.name, i.status, array_to_string(i.scopes, ' '), coalesce(i.health_status, '-'),
		       i.last_heartbeat_at, coalesce(string_agg(k.id || ' ' || k.display_prefix || '…', ', '), '')
		FROM integrations i LEFT JOIN api_keys k ON k.integration_id = i.id AND k.revoked_at IS NULL
		GROUP BY i.id ORDER BY i.name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tHEALTH\tLAST HEARTBEAT\tKEYS\tSCOPES")
	for rows.Next() {
		var name, status, scopes, health, keys string
		var hb *time.Time
		if err := rows.Scan(&name, &status, &scopes, &health, &hb, &keys); err != nil {
			return err
		}
		last := "-"
		if hb != nil {
			last = hb.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", name, status, health, last, keys, scopes)
	}
	tw.Flush()
	return rows.Err()
}

func apiKeysRevoke(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: purros api-keys revoke <keyId>")
	}
	_, pool, err := connect(ctx, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	tag, err := pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, args[0])
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no active key %q", args[0])
	}
	fmt.Fprintln(stdout, "Key revoked.")
	return nil
}

func featuresList(ctx context.Context, stdout io.Writer) error {
	_, pool, err := connect(ctx, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := features.NewStore(pool)
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "FEATURE\tSTATUS\tNAME")
	for _, f := range features.Registry {
		on, err := store.IsEnabled(ctx, f.Key)
		if err != nil {
			return err
		}
		state := "off"
		if on {
			state = "on"
		}
		key := f.Key
		if f.Parent() != "" {
			key = "  " + key
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", key, state, f.Name)
	}
	tw.Flush()
	return nil
}

func featuresSet(ctx context.Context, enable bool, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: purros features enable|disable <key>")
	}
	_, pool, err := connect(ctx, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := features.NewStore(pool)
	var changed []string
	if enable {
		changed, err = store.Enable(ctx, args[0], "cli")
	} else {
		changed, err = store.Disable(ctx, args[0], "cli")
	}
	if err != nil {
		return err
	}
	sort.Strings(changed)
	verb := "Switched off"
	if enable {
		verb = "Switched on"
	}
	fmt.Fprintf(stdout, "%s: %s\n", verb, strings.Join(changed, ", "))
	action := "feature.disable"
	if enable {
		action = "feature.enable"
	}
	err = events.Audit(ctx, pool, events.AuditEntry{
		Actor: events.Actor{Type: "system", Name: "cli"}, Action: action,
		EntityType: "feature", EntityID: args[0], After: map[string]any{"changed": changed},
	})
	return err
}
