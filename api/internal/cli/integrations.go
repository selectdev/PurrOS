package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/catalog"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/secure"
)

// Manifest is purros-integration.json (docs/integrations/README.md).
type Manifest struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Homepage    string   `json:"homepage"`
	Scopes      []string `json:"scopes"`
	Webhooks    *struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	} `json:"webhooks"`
	Config []ConfigField `json:"config"`
}

type ConfigField struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// Validate checks a manifest against the scope and event catalogs and the
// enabled features.
func (m *Manifest) Validate(ctx context.Context, fs *features.Store, approveSensitive bool) error {
	var problems []string
	if !nameRe.MatchString(m.Name) {
		problems = append(problems, "name must be 2–63 lowercase letters, digits or dashes")
	}
	if m.DisplayName == "" {
		m.DisplayName = m.Name
	}
	if len(m.Scopes) == 0 {
		problems = append(problems, "scopes must list at least one scope")
	}
	for _, s := range m.Scopes {
		feat, ok := catalog.ScopeFeature(s)
		if !ok {
			problems = append(problems, fmt.Sprintf("unknown scope %q", s))
			continue
		}
		if on, _ := fs.IsEnabled(ctx, feat); !on {
			problems = append(problems, fmt.Sprintf("scope %q belongs to the %q feature, which is switched off", s, feat))
		}
		if s == "people:sensitive" && !approveSensitive {
			problems = append(problems, "scope people:sensitive needs Owner approval: re-run with --approve-sensitive")
		}
	}
	if m.Webhooks != nil {
		u, err := url.Parse(m.Webhooks.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			problems = append(problems, "webhooks.url must be an http(s) URL")
		}
		if len(m.Webhooks.Events) == 0 {
			problems = append(problems, "webhooks.events must list at least one event")
		}
		for _, e := range m.Webhooks.Events {
			feat, ok := catalog.Events[e]
			if !ok {
				problems = append(problems, fmt.Sprintf("unknown event %q", e))
				continue
			}
			if on, _ := fs.IsEnabled(ctx, feat); !on {
				problems = append(problems, fmt.Sprintf("event %q belongs to the %q feature, which is switched off", e, feat))
			}
		}
	}
	for _, f := range m.Config {
		if !slices.Contains([]string{"string", "number", "boolean", "secret", "json", "location", "select"}, f.Type) {
			problems = append(problems, fmt.Sprintf("config %q has unknown type %q", f.Key, f.Type))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid manifest:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// Registered is what registration returns. The key and secret are shown once.
type Registered struct {
	IntegrationID string
	APIKey        string
	WebhookSecret string
}

// RegisterIntegration stores an integration, issues its API key and creates
// its webhook endpoint, all in one transaction.
func RegisterIntegration(ctx context.Context, pool *pgxpool.Pool, box *secure.Box, m Manifest, config map[string]any) (Registered, error) {
	for _, f := range m.Config {
		if _, ok := config[f.Key]; f.Required && !ok {
			return Registered{}, fmt.Errorf("config %q is required (pass --config %s=…)", f.Key, f.Key)
		}
	}
	manifestJSON, _ := json.Marshal(m)
	cfgJSON, _ := json.Marshal(config)
	var out Registered
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		out.IntegrationID = ids.New(ids.Integration)
		_, err := tx.Exec(ctx, `
			INSERT INTO integrations (id, name, display_name, version, description, homepage, scopes, manifest, config_encrypted)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			out.IntegrationID, m.Name, m.DisplayName, m.Version, m.Description, m.Homepage, m.Scopes, manifestJSON, box.Seal(cfgJSON))
		if err != nil {
			if strings.Contains(err.Error(), "integrations_name_key") {
				return fmt.Errorf("an integration named %q is already registered", m.Name)
			}
			return err
		}
		plain, hash, display := secure.NewAPIKey()
		if _, err := tx.Exec(ctx, `
			INSERT INTO api_keys (id, kind, display_prefix, hash, integration_id, name)
			VALUES ($1, 'integration', $2, $3, $4, $5)`,
			ids.New(ids.APIKey), display, hash, out.IntegrationID, m.DisplayName); err != nil {
			return err
		}
		out.APIKey = plain
		if m.Webhooks != nil {
			out.WebhookSecret = secure.NewWebhookSecret()
			if _, err := tx.Exec(ctx, `
				INSERT INTO webhook_endpoints (id, integration_id, url, secret_encrypted, events)
				VALUES ($1, $2, $3, $4, $5)`,
				ids.New(ids.WebhookEndpoint), out.IntegrationID, m.Webhooks.URL, box.Seal([]byte(out.WebhookSecret)), m.Webhooks.Events); err != nil {
				return err
			}
		}
		return events.Audit(ctx, tx, events.AuditEntry{
			Actor: events.Actor{Type: "system", Name: "cli"}, Action: "integration.register",
			EntityType: "integration", EntityID: out.IntegrationID,
			After: map[string]any{"name": m.Name, "scopes": m.Scopes},
		})
	})
	return out, err
}

func loadManifest(path string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}
