// Package cli implements the purros command: the API server, the background
// worker, and the admin tool used to set up, recover, update, back up and
// restore an install.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// app holds global options and I/O for every command.
type app struct {
	in          *bufio.Reader
	inFile      *os.File // set when stdin is a terminal-capable file
	out, errOut io.Writer

	envFile     string
	jsonOut     bool
	yes         bool
	noInput     bool
	interactive bool // --interactive forces prompts (tests, piping answers)

	backupPass string // passphrase of the backup being read
}

// Run executes the command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunWith(args, os.Stdin, stdout, stderr)
}

// RunWith is Run with an explicit stdin (for tests).
func RunWith(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &app{in: bufio.NewReader(stdin), out: stdout, errOut: stderr}
	if f, ok := stdin.(*os.File); ok {
		a.inFile = f
	}
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		if ee, ok := errors.AsType[exitError](err); ok {
			return ee.code
		}
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

// exitError ends the command with a code, having already printed its message.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "purros",
		Short: "PurrOS API server and admin tool",
		Long: `PurrOS API server, background worker and admin tool.

Configuration comes from environment variables (see docs/getting-started/configuration.md),
loaded from $PURROS_CONFIG_DIR/purros.env (default ./config/purros.env) when it
exists, or from the file given with --env-file.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			path := a.envFile
			if path == "" {
				path = os.Getenv("PURROS_ENV_FILE")
			}
			if path == "" {
				// The default file is optional: in containers the
				// environment comes from docker compose instead.
				if _, err := os.Stat(config.EnvFile()); err != nil {
					return nil
				}
				path = config.EnvFile()
			}
			return loadEnvFile(path)
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&a.envFile, "env-file", "", "load environment variables from this file (default $PURROS_ENV_FILE, then $PURROS_CONFIG_DIR/purros.env)")
	pf.BoolVar(&a.jsonOut, "json", false, "print machine-readable JSON")
	pf.BoolVarP(&a.yes, "yes", "y", false, "answer yes to confirmations")
	pf.BoolVar(&a.noInput, "no-input", false, "never prompt; fail when input is missing")
	pf.BoolVar(&a.interactive, "interactive", false, "prompt even when stdin isn't a terminal")
	_ = pf.MarkHidden("interactive")

	root.AddGroup(
		&cobra.Group{ID: "server", Title: "Server:"},
		&cobra.Group{ID: "setup", Title: "Setup & configuration:"},
		&cobra.Group{ID: "people", Title: "Accounts & recovery:"},
		&cobra.Group{ID: "data", Title: "Backups:"},
		&cobra.Group{ID: "manage", Title: "Management:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add("server", a.serveCmd(), a.workerCmd(), a.migrateCmd(), a.statusCmd(), a.doctorCmd())
	add("setup", a.initCmd(), a.setupCmd(), a.configCmd(), a.secretCmd(), a.emailCmd(), a.storageCmd())
	add("people", a.usersCmd(), a.rolesCmd(), a.recoverCmd())
	add("data", a.backupCmd())
	add("manage", a.locationsCmd(), a.integrationsCmd(), a.webhooksCmd(), a.apiKeysCmd(), a.featuresCmd())
	root.AddCommand(a.versionCmd())
	return root
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

func (a *app) printf(format string, args ...any) { fmt.Fprintf(a.out, format, args...) }

// emit prints v as JSON with --json, otherwise calls human.
func (a *app) emit(v any, human func()) error {
	if a.jsonOut {
		enc := json.NewEncoder(a.out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	human()
	return nil
}

func (a *app) table(header string, rows [][]string) {
	tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
	if header != "" {
		fmt.Fprintln(tw, header)
	}
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// canPrompt reports whether questions can be asked.
func (a *app) canPrompt() bool {
	if a.noInput || a.jsonOut {
		return false
	}
	if a.interactive {
		return true
	}
	return a.inFile != nil && term.IsTerminal(int(a.inFile.Fd()))
}

// ask prompts for a value, with a default shown in brackets.
func (a *app) ask(label, def string, check func(string) error) (string, error) {
	for {
		if def != "" {
			fmt.Fprintf(a.out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(a.out, "%s: ", label)
		}
		line, err := a.in.ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no answer (input ended)")
		}
		v := strings.TrimSpace(line)
		if v == "" {
			v = def
		}
		if check != nil {
			if err := check(v); err != nil {
				fmt.Fprintf(a.out, "  %v\n", err)
				continue
			}
		}
		return v, nil
	}
}

// askSecret reads a value without echoing it when on a terminal.
func (a *app) askSecret(label string) (string, error) {
	fmt.Fprintf(a.out, "%s: ", label)
	if a.inFile != nil && term.IsTerminal(int(a.inFile.Fd())) {
		b, err := term.ReadPassword(int(a.inFile.Fd()))
		fmt.Fprintln(a.out)
		return strings.TrimSpace(string(b)), err
	}
	line, err := a.in.ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no answer (input ended)")
	}
	return strings.TrimSpace(line), nil
}

// confirm asks a yes/no question; --yes answers it.
func (a *app) confirm(question string) error {
	if a.yes {
		return nil
	}
	if !a.canPrompt() {
		return fmt.Errorf("%s Re-run with --yes to confirm", question)
	}
	ans, err := a.ask(question+" (y/N)", "", nil)
	if err != nil {
		return err
	}
	if a := strings.ToLower(ans); a != "y" && a != "yes" {
		return errors.New("cancelled")
	}
	return nil
}

// confirmTyped asks the person to type a word (e.g. the company name) before
// a destructive action; --yes skips it.
func (a *app) confirmTyped(what, word string) error {
	if a.yes {
		return nil
	}
	if !a.canPrompt() {
		return fmt.Errorf("this %s. Re-run with --yes to confirm", what)
	}
	ans, err := a.ask(fmt.Sprintf("This %s. Type %q to continue", what, word), "", nil)
	if err != nil {
		return err
	}
	if ans != word {
		return errors.New("cancelled")
	}
	return nil
}

// parseEnvFile reads a KEY=VALUE file. Blank lines, # comments and an
// "export " prefix are allowed; values may be quoted.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("env file: %w", err)
	}
	defer f.Close()
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		vals[k] = v
	}
	return vals, sc.Err()
}

// loadEnvFile sets variables from a KEY=VALUE file. Variables already set in
// the environment win.
func loadEnvFile(path string) error {
	vals, err := parseEnvFile(path)
	if err != nil {
		return err
	}
	for k, v := range vals {
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
	return nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// withPool runs an admin command with a database pool ("purros-cli").
func withPool(ctx context.Context, requireSecret bool, fn func(cfg config.Config, pool *pgxpool.Pool) error) error {
	cfg, err := config.Load(requireSecret)
	if err != nil {
		return err
	}
	pool, err := db.ConnectAs(ctx, cfg.DatabaseURL, "purros-cli")
	if err != nil {
		return err
	}
	defer pool.Close()
	return fn(cfg, pool)
}
