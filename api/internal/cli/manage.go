package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/secure"
	"github.com/spf13/cobra"
)

func (a *app) locationsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "locations", Short: "Add and list locations"}
	in := LocationInput{}
	create := &cobra.Command{
		Use:   "create",
		Short: "Add a location",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				id, err := CreateLocation(cmd.Context(), pool, in)
				if err != nil {
					return err
				}
				return a.emit(map[string]string{"id": id, "name": in.Name}, func() { a.printf("Location %q created: %s\n", in.Name, id) })
			})
		},
	}
	f := create.Flags()
	f.StringVar(&in.Name, "name", "", "location name")
	f.StringVar(&in.Code, "code", "", "short code, e.g. 101")
	f.StringVar(&in.ExternalID, "external-id", "", "ID of this location in your POS or other systems")
	f.StringVar(&in.Timezone, "timezone", "UTC", "time zone, e.g. Europe/London")
	f.StringVar(&in.Currency, "currency", "USD", "currency (ISO 4217)")
	f.StringVar(&in.Cutoff, "cutoff", "00:00", "time the business day ends, e.g. 04:00")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List locations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				rows, err := pool.Query(cmd.Context(), `SELECT id, name, coalesce(external_id, ''), timezone, currency, status FROM locations ORDER BY name`)
				if err != nil {
					return err
				}
				type loc struct {
					ID         string `json:"id"`
					Name       string `json:"name"`
					ExternalID string `json:"externalId"`
					Timezone   string `json:"timezone"`
					Currency   string `json:"currency"`
					Status     string `json:"status"`
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (loc, error) {
					var l loc
					return l, r.Scan(&l.ID, &l.Name, &l.ExternalID, &l.Timezone, &l.Currency, &l.Status)
				})
				if err != nil {
					return err
				}
				return a.emit(list, func() {
					var out [][]string
					for _, l := range list {
						out = append(out, []string{l.ID, l.Name, l.ExternalID, l.Timezone, l.Currency, l.Status})
					}
					a.table("ID\tNAME\tEXTERNAL ID\tTIMEZONE\tCURRENCY\tSTATUS", out)
				})
			})
		},
	})
	return cmd
}

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

func (a *app) integrationsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "integrations", Short: "Register, list, pause and resume integrations"}
	var manifestPath string
	var approveSensitive bool
	cfgVals := kvFlags{}
	register := &cobra.Command{
		Use:   "register",
		Short: "Register an integration from its manifest; prints its API key once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			return withPool(ctx, true, func(cfg config.Config, pool *pgxpool.Pool) error {
				m, err := loadManifest(manifestPath)
				if err != nil {
					return err
				}
				if err := m.Validate(ctx, features.NewStore(pool), approveSensitive); err != nil {
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
				return a.emit(reg, func() {
					a.printf("Integration %q registered (%s).\n\n", m.DisplayName, reg.IntegrationID)
					a.printf("  PURROS_INTEGRATION_KEY=%s\n", reg.APIKey)
					if reg.WebhookSecret != "" {
						a.printf("  PURROS_WEBHOOK_SECRET=%s\n", reg.WebhookSecret)
					}
					a.printf("\nStore these now: they are shown only once.\n")
				})
			})
		},
	}
	register.Flags().StringVar(&manifestPath, "manifest", "purros-integration.json", "path to the manifest")
	register.Flags().BoolVar(&approveSensitive, "approve-sensitive", false, "approve the people:sensitive scope (Owner decision)")
	register.Flags().Var(cfgVals, "config", "config value as key=value (repeatable)")
	cmd.AddCommand(register)

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
	for _, s := range []struct{ use, to string }{{"pause", "paused"}, {"resume", "active"}} {
		s := s
		cmd.AddCommand(&cobra.Command{
			Use:   s.use + " <name>",
			Short: map[string]string{"pause": "Pause an integration (its keys stop working)", "resume": "Resume a paused integration"}[s.use],
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
					var id string
					err := pool.QueryRow(cmd.Context(), `UPDATE integrations SET status = $2, updated_at = now() WHERE name = $1 RETURNING id`, args[0], s.to).Scan(&id)
					if errors.Is(err, pgx.ErrNoRows) {
						return fmt.Errorf("no integration %q", args[0])
					}
					if err != nil {
						return err
					}
					a.printf("Integration %s is now %s.\n", args[0], s.to)
					return events.Audit(cmd.Context(), pool, events.AuditEntry{Actor: cliActor, Action: "integration." + s.use, EntityType: "integration", EntityID: id})
				})
			},
		})
	}
	return cmd
}

func (a *app) apiKeysCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "api-keys", Short: "List and revoke API keys"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List active API keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				rows, err := pool.Query(cmd.Context(), `SELECT k.id, k.kind, k.display_prefix, coalesce(i.name, u.email, ''), k.name,
					k.last_used_at, k.expires_at FROM api_keys k
					LEFT JOIN integrations i ON i.id = k.integration_id LEFT JOIN users u ON u.id = k.user_id
					WHERE k.revoked_at IS NULL ORDER BY k.created_at`)
				if err != nil {
					return err
				}
				type key struct {
					ID       string     `json:"id"`
					Kind     string     `json:"kind"`
					Prefix   string     `json:"prefix"`
					Owner    string     `json:"owner"`
					Name     string     `json:"name"`
					LastUsed *time.Time `json:"lastUsedAt"`
					Expires  *time.Time `json:"expiresAt"`
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (key, error) {
					var k key
					return k, r.Scan(&k.ID, &k.Kind, &k.Prefix, &k.Owner, &k.Name, &k.LastUsed, &k.Expires)
				})
				if err != nil {
					return err
				}
				return a.emit(list, func() {
					var out [][]string
					for _, k := range list {
						used := "never"
						if k.LastUsed != nil {
							used = k.LastUsed.Local().Format("2006-01-02 15:04")
						}
						out = append(out, []string{k.ID, k.Kind, k.Prefix + "…", k.Owner, k.Name, used})
					}
					a.table("ID\tKIND\tKEY\tBELONGS TO\tNAME\tLAST USED", out)
				})
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <keyId>",
		Short: "Revoke an API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				tag, err := pool.Exec(cmd.Context(), `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, args[0])
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return fmt.Errorf("no active key %q", args[0])
				}
				a.printf("Key revoked.\n")
				return events.Audit(cmd.Context(), pool, events.AuditEntry{Actor: cliActor, Action: "api_key.revoke", EntityType: "api_key", EntityID: args[0]})
			})
		},
	})
	return cmd
}

func (a *app) featuresCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "features", Short: "Show and switch features on and off"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Show every feature and whether it is on",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				store := features.NewStore(pool)
				type feat struct {
					Key     string `json:"key"`
					Name    string `json:"name"`
					Enabled bool   `json:"enabled"`
				}
				var list []feat
				for _, f := range features.Registry {
					on, err := store.IsEnabled(cmd.Context(), f.Key)
					if err != nil {
						return err
					}
					list = append(list, feat{f.Key, f.Name, on})
				}
				return a.emit(list, func() {
					var out [][]string
					for i, f := range list {
						key := f.Key
						if features.Registry[i].Parent() != "" {
							key = "  " + key
						}
						out = append(out, []string{key, map[bool]string{true: "on", false: "off"}[f.Enabled], f.Name})
					}
					a.table("FEATURE\tSTATUS\tNAME", out)
				})
			})
		},
	})
	for _, enable := range []bool{true, false} {
		enable := enable
		use, short := "disable <key>", "Switch a feature off (with what depends on it)"
		if enable {
			use, short = "enable <key>", "Switch a feature on (with what it needs)"
		}
		cmd.AddCommand(&cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				return withPool(ctx, false, func(_ config.Config, pool *pgxpool.Pool) error {
					store := features.NewStore(pool)
					var changed []string
					var err error
					if enable {
						changed, err = store.Enable(ctx, args[0], "cli")
					} else {
						changed, err = store.Disable(ctx, args[0], "cli")
					}
					if err != nil {
						return err
					}
					sort.Strings(changed)
					verb, action := "Switched off", "feature.disable"
					if enable {
						verb, action = "Switched on", "feature.enable"
					}
					a.printf("%s: %s\n", verb, strings.Join(changed, ", "))
					return events.Audit(ctx, pool, events.AuditEntry{Actor: cliActor, Action: action,
						EntityType: "feature", EntityID: args[0], After: map[string]any{"changed": changed}})
				})
			},
		})
	}
	return cmd
}
