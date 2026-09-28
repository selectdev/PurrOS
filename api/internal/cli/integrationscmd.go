package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/integration"
	"github.com/selectdev/purros/api/internal/secure"
	"github.com/spf13/cobra"
)

var cliBy = integration.By{Actor: cliActor}

type kvFlags map[string]any

func (k kvFlags) String() string { return "" }
func (k kvFlags) Type() string   { return "key=value" }
func (k kvFlags) Set(v string) error {
	key, val, ok := strings.Cut(v, "=")
	if !ok || key == "" {
		return errors.New("use key=value")
	}
	k[key] = val
	return nil
}

// LoadManifest reads purros-integration.json.
func LoadManifest(path string) (integration.Manifest, error) {
	var m integration.Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// inTx runs fn in a transaction with the configuration and a secret box.
func inTx(ctx context.Context, fn func(cfg config.Config, pool *pgxpool.Pool, tx pgx.Tx, box *secure.Box) error) error {
	return withPool(ctx, true, func(cfg config.Config, pool *pgxpool.Pool) error {
		box, err := secure.NewBox(cfg.Secret)
		if err != nil {
			return err
		}
		return problemText(pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return fn(cfg, pool, tx, box) }))
	})
}

func integrationID(ctx context.Context, q pgx.Tx, name string) (string, error) {
	i, err := integration.GetByName(ctx, q, name)
	if err != nil {
		return "", fmt.Errorf("no integration %q", name)
	}
	return i.ID, nil
}

func (a *app) integrationsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "integrations", Short: "Register, list, pause, rotate and remove integrations",
		Long: `Manage integrations. The same actions are available in the API under /api/v1/integrations
(permission integrations.manage); see docs/integrations/README.md.`}

	var manifestPath string
	var approveSensitive bool
	cfgVals := kvFlags{}
	register := &cobra.Command{
		Use:   "register",
		Short: "Register an integration from its manifest; prints its API key once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			m, err := LoadManifest(manifestPath)
			if err != nil {
				return err
			}
			var reg integration.Registered
			err = inTx(ctx, func(_ config.Config, pool *pgxpool.Pool, tx pgx.Tx, box *secure.Box) error {
				if err := m.Validate(ctx, features.NewStore(pool), approveSensitive); err != nil {
					return err
				}
				reg, err = integration.Register(ctx, tx, box, m, cfgVals, cliBy)
				return err
			})
			if err != nil {
				return err
			}
			return a.emit(reg, func() {
				a.printf("Integration %q registered (%s).\n\n", reg.Integration.DisplayName, reg.Integration.ID)
				a.printf("  PURROS_INTEGRATION_KEY=%s\n", reg.APIKey)
				if reg.WebhookSecret != "" {
					a.printf("  PURROS_WEBHOOK_SECRET=%s\n", reg.WebhookSecret)
				}
				a.printf("\nStore these now: they are shown only once.\n")
			})
		},
	}
	register.Flags().StringVar(&manifestPath, "manifest", "purros-integration.json", "path to the manifest")
	register.Flags().BoolVar(&approveSensitive, "approve-sensitive", false, "approve the people:sensitive scope (Owner decision)")
	register.Flags().Var(cfgVals, "config", "config value as key=value (repeatable)")
	cmd.AddCommand(register)

	var updManifest string
	var updApprove bool
	updCfg := kvFlags{}
	update := &cobra.Command{
		Use:   "update <name>",
		Short: "Replace an integration's manifest (e.g. for a new version)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			m, err := LoadManifest(updManifest)
			if err != nil {
				return err
			}
			var out integration.Updated
			err = inTx(ctx, func(_ config.Config, pool *pgxpool.Pool, tx pgx.Tx, box *secure.Box) error {
				id, err := integrationID(ctx, tx, args[0])
				if err != nil {
					return err
				}
				if err := m.Validate(ctx, features.NewStore(pool), updApprove); err != nil {
					return err
				}
				out, err = integration.UpdateManifest(ctx, tx, box, id, m, updCfg, cliBy)
				return err
			})
			if err != nil {
				return err
			}
			return a.emit(out, func() {
				a.printf("Integration %s updated to version %q.\n", out.Integration.Name, out.Integration.Version)
				if out.WebhookSecret != "" {
					a.printf("\nNew webhook endpoint. Store its secret now; it is shown only once:\n  PURROS_WEBHOOK_SECRET=%s\n", out.WebhookSecret)
				}
			})
		},
	}
	update.Flags().StringVar(&updManifest, "manifest", "purros-integration.json", "path to the new manifest")
	update.Flags().BoolVar(&updApprove, "approve-sensitive", false, "approve the people:sensitive scope (Owner decision)")
	update.Flags().Var(updCfg, "config", "set a config value as key=value (repeatable)")
	cmd.AddCommand(update)

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List integrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				rows, err := pool.Query(cmd.Context(), `
					SELECT i.name, i.status, array_to_string(i.scopes, ' '), coalesce(i.health_status, '-'),
					       i.last_heartbeat_at, coalesce(string_agg(k.id || ' ' || k.display_prefix || '…', ', '), '')
					FROM integrations i LEFT JOIN api_keys k ON k.integration_id = i.id AND k.revoked_at IS NULL
					     AND (k.expires_at IS NULL OR k.expires_at > now())
					GROUP BY i.id ORDER BY i.name`)
				if err != nil {
					return err
				}
				type integ struct {
					Name          string     `json:"name"`
					Status        string     `json:"status"`
					Scopes        string     `json:"scopes"`
					Health        string     `json:"health"`
					LastHeartbeat *time.Time `json:"lastHeartbeatAt"`
					Keys          string     `json:"keys"`
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (integ, error) {
					var x integ
					return x, r.Scan(&x.Name, &x.Status, &x.Scopes, &x.Health, &x.LastHeartbeat, &x.Keys)
				})
				if err != nil {
					return err
				}
				return a.emit(list, func() {
					var out [][]string
					for _, x := range list {
						last := "-"
						if x.LastHeartbeat != nil {
							last = x.LastHeartbeat.Local().Format("2006-01-02 15:04")
						}
						out = append(out, []string{x.Name, x.Status, x.Health, last, x.Keys, x.Scopes})
					}
					a.table("NAME\tSTATUS\tHEALTH\tLAST HEARTBEAT\tKEYS\tSCOPES", out)
				})
			})
		},
	})

	for _, s := range []struct{ use, to, short string }{
		{"pause", "paused", "Pause an integration (its keys stop working, webhooks wait)"},
		{"resume", "active", "Resume a paused integration"},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:   s.use + " <name>",
			Short: s.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				err := inTx(ctx, func(_ config.Config, _ *pgxpool.Pool, tx pgx.Tx, _ *secure.Box) error {
					id, err := integrationID(ctx, tx, args[0])
					if err != nil {
						return err
					}
					_, err = integration.SetStatus(ctx, tx, id, s.to, cliBy)
					return err
				})
				if err != nil {
					return err
				}
				a.printf("Integration %s is now %s.\n", args[0], s.to)
				return nil
			},
		})
	}

	var grace time.Duration
	rotate := &cobra.Command{
		Use:   "rotate-key <name>",
		Short: "Issue a new API key; the old ones stop working after --grace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var out integration.Rotated
			err := inTx(ctx, func(_ config.Config, _ *pgxpool.Pool, tx pgx.Tx, _ *secure.Box) error {
				id, err := integrationID(ctx, tx, args[0])
				if err != nil {
					return err
				}
				out, err = integration.RotateKey(ctx, tx, id, grace, time.Now(), cliBy)
				return err
			})
			if err != nil {
				return err
			}
			return a.emit(out, func() {
				a.printf("New key for %s (shown only once):\n\n  PURROS_INTEGRATION_KEY=%s\n\n", args[0], out.APIKey)
				a.printf("The previous keys stop working at %s.\n", out.OldKeysExpireAt.Local().Format("2006-01-02 15:04"))
			})
		},
	}
	rotate.Flags().DurationVar(&grace, "grace", time.Hour, "how long the previous keys keep working (0 revokes them now, max 168h)")
	cmd.AddCommand(rotate)

	cmd.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an integration with its keys, logs and webhook endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirm(fmt.Sprintf("Remove integration %q? Its keys stop working immediately.", args[0])); err != nil {
				return err
			}
			ctx := cmd.Context()
			err := inTx(ctx, func(_ config.Config, _ *pgxpool.Pool, tx pgx.Tx, _ *secure.Box) error {
				id, err := integrationID(ctx, tx, args[0])
				if err != nil {
					return err
				}
				_, err = integration.Remove(ctx, tx, id, cliBy)
				return err
			})
			if err != nil {
				return err
			}
			a.printf("Integration %s removed.\n", args[0])
			return nil
		},
	})
	return cmd
}

func (a *app) webhooksCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "webhooks", Short: "List, re-enable and replay webhook endpoints",
		Long: `Manage webhook endpoints. Creating standalone endpoints, rotating secrets and viewing
deliveries is done in the API under /api/v1/webhook-endpoints (permission webhooks.manage);
see docs/api/webhooks.md.`}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List webhook endpoints with their status and pending or failed deliveries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				rows, err := pool.Query(cmd.Context(), `
					SELECT e.id, coalesce(i.name, '-'), e.url, e.status, e.consecutive_failures,
					       count(d.id) FILTER (WHERE d.status = 'pending'), count(d.id) FILTER (WHERE d.status = 'failed')
					FROM webhook_endpoints e LEFT JOIN integrations i ON i.id = e.integration_id
					LEFT JOIN webhook_deliveries d ON d.endpoint_id = e.id
					GROUP BY e.id, i.name ORDER BY e.id`)
				if err != nil {
					return err
				}
				type ep struct {
					ID          string `json:"id"`
					Integration string `json:"integration"`
					URL         string `json:"url"`
					Status      string `json:"status"`
					Failures    int    `json:"consecutiveFailures"`
					Pending     int    `json:"pending"`
					Failed      int    `json:"failed"`
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ep, error) {
					var e ep
					return e, r.Scan(&e.ID, &e.Integration, &e.URL, &e.Status, &e.Failures, &e.Pending, &e.Failed)
				})
				if err != nil {
					return err
				}
				return a.emit(list, func() {
					var out [][]string
					for _, e := range list {
						out = append(out, []string{e.ID, e.Integration, e.Status, fmt.Sprint(e.Failures), fmt.Sprint(e.Pending), fmt.Sprint(e.Failed), e.URL})
					}
					a.table("ID\tINTEGRATION\tSTATUS\tFAILURES\tPENDING\tFAILED\tURL", out)
				})
			})
		},
	})
	for _, s := range []struct{ use, status, short string }{
		{"enable", "active", "Re-enable a disabled endpoint (resets its failure count)"},
		{"disable", "disabled", "Disable an endpoint; its deliveries wait until it's enabled"},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:   s.use + " <endpointId>",
			Short: s.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				status := s.status
				err := inTx(ctx, func(_ config.Config, _ *pgxpool.Pool, tx pgx.Tx, _ *secure.Box) error {
					_, err := integration.UpdateEndpoint(ctx, tx, args[0], integration.EndpointPatch{Status: &status}, cliBy)
					return err
				})
				if err != nil {
					return err
				}
				a.printf("Endpoint %s is now %s.\n", args[0], status)
				return nil
			},
		})
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "retry-failed <endpointId>",
		Short: "Queue every failed delivery of an endpoint again",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var n int
			err := inTx(ctx, func(_ config.Config, _ *pgxpool.Pool, tx pgx.Tx, _ *secure.Box) (err error) {
				n, err = integration.RetryFailed(ctx, tx, args[0], cliBy)
				return err
			})
			if err != nil {
				return err
			}
			return a.emit(map[string]int{"queued": n}, func() { a.printf("%d delivery(ies) queued again.\n", n) })
		},
	})
	return cmd
}
