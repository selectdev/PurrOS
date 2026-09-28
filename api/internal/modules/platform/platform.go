// Package platform serves the always-on core endpoints: the caller's identity,
// features, permissions, the OpenAPI document and integration self-service.
package platform

import (
	"encoding/json"

	"github.com/selectdev/purros/api/internal/catalog"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/integration"
)

// Version is set at build time.
var Version = "0.1.0-dev"

type Me struct {
	Type        string                   `json:"type" doc:"integration or personal"`
	KeyID       string                   `json:"keyId"`
	Integration *integration.Integration `json:"integration,omitempty"`
	UserID      string                   `json:"userId,omitempty"`
	Scopes      []string                 `json:"scopes"`
}

type FeatureState struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

type List[T any] struct {
	Data []T `json:"data"`
}

type HealthInput struct {
	Status  string `json:"status" validate:"required,oneof=ok warning error"`
	Message string `json:"message,omitempty" validate:"max=500"`
}

type LogInput struct {
	Level   string          `json:"level,omitempty" validate:"omitempty,oneof=debug info warning error"`
	Message string          `json:"message" validate:"required,max=2000"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type Accepted struct {
	OK bool `json:"ok"`
}

func loadIntegration(c *httpx.Ctx) (integration.Integration, error) {
	return integration.Get(c, c.App.Pool, c.Principal.IntegrationID)
}

func Routes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "GET", Path: "/openapi.json", Tag: "Platform", Auth: httpx.AuthNone,
			Summary: "OpenAPI document for enabled features",
			Handler: func(c *httpx.Ctx) (any, error) { return c.App.OpenAPI(c, Version) },
		},
		{
			Method: "GET", Path: "/me", Tag: "Platform",
			Summary: "The calling API key", Response: Me{},
			Handler: func(c *httpx.Ctx) (any, error) {
				p := c.Principal
				me := Me{Type: p.Kind, KeyID: p.KeyID, UserID: p.UserID, Scopes: p.Scopes}
				if p.Kind == "integration" {
					i, err := loadIntegration(c)
					if err != nil {
						return nil, err
					}
					me.Integration = &i
				}
				if me.Scopes == nil {
					me.Scopes = []string{}
				}
				return me, nil
			},
		},
		{
			Method: "GET", Path: "/features", Tag: "Platform",
			Summary: "Features and whether they are enabled", Response: List[FeatureState]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				out := make([]FeatureState, 0, len(features.Registry))
				for _, f := range features.Registry {
					on, err := c.App.Features.IsEnabled(c, f.Key)
					if err != nil {
						return nil, err
					}
					out = append(out, FeatureState{Key: f.Key, Name: f.Name, Enabled: on, DependsOn: f.DependsOn})
				}
				return List[FeatureState]{Data: out}, nil
			},
		},
		{
			Method: "GET", Path: "/permissions", Tag: "Platform",
			Summary: "Permission catalog for enabled features", Response: List[catalog.Permission]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				out := []catalog.Permission{}
				for _, p := range catalog.Permissions {
					on, err := c.App.Features.IsEnabled(c, p.Feature)
					if err != nil {
						return nil, err
					}
					if on {
						out = append(out, p)
					}
				}
				return List[catalog.Permission]{Data: out}, nil
			},
		},
		{
			Method: "GET", Path: "/integrations/self", Tag: "Integrations", Auth: httpx.AuthIntegration,
			Summary: "The calling integration's registration", Response: integration.Integration{},
			Handler: func(c *httpx.Ctx) (any, error) { return loadIntegration(c) },
		},
		{
			Method: "GET", Path: "/integrations/self/config", Tag: "Integrations", Auth: httpx.AuthIntegration,
			Summary:     "Config values entered by the admin",
			Description: "Secrets are returned decrypted. Only call this over TLS.",
			Response:    map[string]any{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return integration.Config(c, c.App.Pool, c.App.Box, c.Principal.IntegrationID)
			},
		},
		{
			Method: "POST", Path: "/integrations/self/health", Tag: "Integrations", Auth: httpx.AuthIntegration,
			Summary: "Send a heartbeat and status message", Body: HealthInput{}, Response: Accepted{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in HealthInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				_, err := c.App.Pool.Exec(c, `
					UPDATE integrations SET health_status = $2, health_message = nullif($3, ''),
						last_heartbeat_at = now(), updated_at = now()
					WHERE id = $1`, c.Principal.IntegrationID, in.Status, in.Message)
				return Accepted{OK: true}, err
			},
		},
		{
			Method: "POST", Path: "/integrations/self/logs", Tag: "Integrations", Auth: httpx.AuthIntegration,
			Summary: "Add a log message to the integration's log", Body: LogInput{}, Response: Accepted{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in LogInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if in.Level == "" {
					in.Level = "info"
				}
				var data []byte
				if len(in.Data) > 0 {
					data = in.Data
				}
				_, err := c.App.Pool.Exec(c, `
					INSERT INTO integration_logs (id, integration_id, level, message, data)
					VALUES ($1, $2, $3, $4, $5)`,
					ids.New(ids.IntegrationLog), c.Principal.IntegrationID, in.Level, in.Message, data)
				return Accepted{OK: true}, err
			},
		},
	}
}
