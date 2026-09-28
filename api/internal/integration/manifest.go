// Package integration registers and manages integrations: their manifest,
// API keys, config values and webhook endpoint. The admin API
// (modules/integrations) and the `purros integrations` CLI both use it.
package integration

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/selectdev/purros/api/internal/catalog"
)

// Manifest is purros-integration.json (docs/integrations/README.md#the-manifest).
type Manifest struct {
	Name        string         `json:"name" doc:"Unique, 2–63 lowercase letters, digits or dashes"`
	DisplayName string         `json:"displayName,omitempty"`
	Version     string         `json:"version,omitempty"`
	Description string         `json:"description,omitempty"`
	Homepage    string         `json:"homepage,omitempty"`
	Scopes      []string       `json:"scopes"`
	Webhooks    *WebhookConfig `json:"webhooks,omitempty"`
	Config      []ConfigField  `json:"config,omitempty"`
}

// WebhookConfig is where the integration receives events.
type WebhookConfig struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// ConfigField is a setting the admin fills in when registering.
type ConfigField struct {
	Key      string   `json:"key"`
	Type     string   `json:"type" doc:"string, number, boolean, secret, json, location or select"`
	Label    string   `json:"label,omitempty"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty" doc:"Allowed values for type select"`
}

// ConfigTypes are the allowed ConfigField types.
var ConfigTypes = []string{"string", "number", "boolean", "secret", "json", "location", "select"}

// Invalid lists everything wrong with a manifest or config.
type Invalid struct{ Problems []string }

func (e *Invalid) Error() string {
	return "invalid manifest:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// FeatureChecker reports whether a feature is enabled (features.Store).
type FeatureChecker interface {
	IsEnabled(ctx context.Context, key string) (bool, error)
}

var (
	nameRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	configKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
)

// Validate checks a manifest against the scope and event catalogs and the
// enabled features. people:sensitive needs approveSensitive (an Owner's decision).
func (m *Manifest) Validate(ctx context.Context, fs FeatureChecker, approveSensitive bool) error {
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
	enabled := func(feat string) bool {
		on, _ := fs.IsEnabled(ctx, feat)
		return on
	}
	for _, s := range m.Scopes {
		feat, ok := catalog.ScopeFeature(s)
		if !ok {
			problems = append(problems, fmt.Sprintf("unknown scope %q", s))
			continue
		}
		if !enabled(feat) {
			problems = append(problems, fmt.Sprintf("scope %q belongs to the %q feature, which is switched off", s, feat))
		}
		if s == "people:sensitive" && !approveSensitive {
			problems = append(problems, "scope people:sensitive needs an Owner's approval (approveSensitive)")
		}
	}
	if m.Webhooks != nil {
		if err := CheckURL(m.Webhooks.URL); err != nil {
			problems = append(problems, "webhooks.url "+err.Error())
		}
		if len(m.Webhooks.Events) == 0 {
			problems = append(problems, "webhooks.events must list at least one event")
		}
		for _, e := range m.Webhooks.Events {
			feat, ok := catalog.Events[e]
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("unknown event %q", e))
			case !enabled(feat):
				problems = append(problems, fmt.Sprintf("event %q belongs to the %q feature, which is switched off", e, feat))
			case !catalog.ScopesCoverEvent(m.Scopes, e):
				need, scope, _ := catalog.EventScopeFeature(e)
				if scope == "" {
					scope = "a " + need + ":… scope"
				}
				problems = append(problems, fmt.Sprintf("event %q needs %s", e, scope))
			}
		}
	}
	seen := map[string]bool{}
	for _, f := range m.Config {
		switch {
		case !configKeyRe.MatchString(f.Key):
			problems = append(problems, fmt.Sprintf("config key %q must start with a letter and use letters, digits or _", f.Key))
		case seen[f.Key]:
			problems = append(problems, fmt.Sprintf("config key %q is declared twice", f.Key))
		}
		seen[f.Key] = true
		if !slices.Contains(ConfigTypes, f.Type) {
			problems = append(problems, fmt.Sprintf("config %q has unknown type %q", f.Key, f.Type))
		}
		if f.Type == "select" && len(f.Options) == 0 {
			problems = append(problems, fmt.Sprintf("config %q of type select needs options", f.Key))
		}
	}
	if len(problems) > 0 {
		return &Invalid{Problems: problems}
	}
	return nil
}

// CheckURL checks a webhook URL.
func CheckURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("must be an http(s) URL")
	}
	if u.User != nil {
		return fmt.Errorf("must not contain credentials")
	}
	return nil
}
