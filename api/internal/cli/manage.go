package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/features"
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
	f.StringVar(&in.Timezone, "timezone", "", "time zone, e.g. Europe/London (default: the company's)")
	f.StringVar(&in.Currency, "currency", "", "currency (ISO 4217) (default: the company's)")
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
