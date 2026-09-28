// Package integrations serves the admin API for integrations and webhook
// endpoints: registering and updating integrations, their keys and config,
// their activity, and webhook endpoints with their delivery log. Only people
// can call it (integrations.manage, webhooks.manage); integration keys use
// /integrations/self instead.
package integrations

import (
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/integration"
)

const (
	tag  = "Integrations"
	perm = "integrations.manage"
)

func by(c *httpx.Ctx) integration.By {
	return integration.By{Actor: c.Actor(), IP: c.ClientIP(), RequestID: c.RequestID}
}

// problem turns manifest and config problems into a 422 with one field error each.
func problem(err error) error {
	inv, ok := errors.AsType[*integration.Invalid](err)
	if !ok {
		return err
	}
	fes := make([]httpx.FieldError, len(inv.Problems))
	for i, p := range inv.Problems {
		path := "manifest"
		if strings.HasPrefix(p, "config ") {
			path = "config"
		}
		fes[i] = httpx.FieldError{Path: path, Message: p}
	}
	return httpx.Validation(fes...)
}

// timeQuery reads an optional RFC 3339 query parameter.
func timeQuery(c *httpx.Ctx, name string) (*time.Time, error) {
	s := c.Query(name)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, httpx.Validation(httpx.FieldError{Path: name, Message: "Must be an RFC 3339 timestamp"})
	}
	return &t, nil
}

type RegisterInput struct {
	Manifest         integration.Manifest `json:"manifest" doc:"The integration's manifest (purros-integration.json)"`
	Config           map[string]any       `json:"config,omitempty" doc:"Values for the manifest's config fields"`
	ApproveSensitive bool                 `json:"approveSensitive,omitempty" doc:"Approve the people:sensitive scope (Owners only)"`
}

type UpdateManifestInput struct {
	Manifest         integration.Manifest `json:"manifest" doc:"The new manifest; name must stay the same"`
	Config           map[string]any       `json:"config,omitempty" doc:"Config values to set or change; null removes one"`
	ApproveSensitive bool                 `json:"approveSensitive,omitempty" doc:"Approve the people:sensitive scope (Owners only)"`
}

type ConfigInput struct {
	Values map[string]any `json:"values" validate:"required" doc:"Config values to set; null removes one"`
}

type ConfigOutput struct {
	Config map[string]any `json:"config" doc:"Current values; secrets show as ********"`
}

type RotateKeyInput struct {
	GraceMinutes *int `json:"graceMinutes,omitempty" validate:"omitempty,min=0,max=10080" doc:"How long the previous keys keep working (default 60; 0 revokes them now)"`
}

type IntegrationDetail struct {
	integration.Integration
	Manifest         integration.Manifest   `json:"manifest"`
	Config           map[string]any         `json:"config" doc:"Secrets show as ********"`
	APIKeys          []integration.Key      `json:"apiKeys"`
	WebhookEndpoints []integration.Endpoint `json:"webhookEndpoints"`
}

type listQuery struct {
	httpx.ListParams
	Status string `json:"status,omitempty" doc:"active or paused"`
}

type logQuery struct {
	httpx.ListParams
	Level string     `json:"level,omitempty" doc:"debug, info, warning or error"`
	From  *time.Time `json:"from,omitempty" doc:"Only entries at or after this time"`
}

type batchQuery struct {
	httpx.ListParams
	From         *time.Time `json:"from,omitempty" doc:"Only batches at or after this time"`
	RejectedOnly bool       `json:"rejectedOnly,omitempty" doc:"Only batches with rejected records"`
}

func checkSensitive(c *httpx.Ctx, approve bool) error {
	if approve && (c.Principal.User == nil || !c.Principal.User.Owner) {
		return httpx.Forbidden("Only an Owner can approve the people:sensitive scope.")
	}
	return nil
}

func detail(c *httpx.Ctx, q pgx.Tx, id string) (IntegrationDetail, error) {
	var d IntegrationDetail
	var err error
	if d.Integration, err = integration.Get(c, q, id); err != nil {
		return d, err
	}
	if d.Manifest, err = integration.StoredManifest(c, q, id); err != nil {
		return d, err
	}
	if d.Config, err = integration.MaskedConfig(c, q, c.App.Box, id); err != nil {
		return d, err
	}
	if d.APIKeys, err = integration.Keys(c, q, id); err != nil {
		return d, err
	}
	d.WebhookEndpoints, err = integration.ListEndpoints(c, q, "", id, "", 100)
	return d, err
}

func route(r httpx.Route) httpx.Route {
	r.Tag, r.Auth, r.Permission = tag, httpx.AuthUser, perm
	return r
}

func Routes() []httpx.Route {
	return append([]httpx.Route{
		route(httpx.Route{
			Method: "GET", Path: "/integrations", Summary: "List integrations",
			Query: listQuery{}, Response: httpx.Page[integration.Integration]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				list, err := integration.List(c, c.App.Pool, lp.AfterID, c.Query("status"), lp.Limit+1)
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(i integration.Integration) string { return i.ID }), nil
			},
		}),
		route(httpx.Route{
			Method: "POST", Path: "/integrations", Summary: "Register an integration",
			Description: "Validates the manifest, stores the config (secrets encrypted), issues a scoped API key and " +
				"creates the webhook endpoint. The API key and webhook secret are returned only once.",
			Body: RegisterInput{}, Response: integration.Registered{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in RegisterInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if err := checkSensitive(c, in.ApproveSensitive); err != nil {
					return nil, err
				}
				if err := in.Manifest.Validate(c, c.App.Features, in.ApproveSensitive); err != nil {
					return nil, problem(err)
				}
				var out integration.Registered
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.Register(c, tx, c.App.Box, in.Manifest, in.Config, by(c))
					return err
				})
				return out, problem(err)
			},
		}),
		route(httpx.Route{
			Method: "GET", Path: "/integrations/{id}", Summary: "Get an integration",
			Description: "With its manifest, config (secrets masked), API keys and webhook endpoints.",
			Response:    IntegrationDetail{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var d IntegrationDetail
				err := c.InTx(func(tx pgx.Tx) (err error) { d, err = detail(c, tx, c.Param("id")); return err })
				return d, err
			},
		}),
		route(httpx.Route{
			Method: "PUT", Path: "/integrations/{id}/manifest", Summary: "Replace an integration's manifest",
			Description: "For a new version of the integration: updates its scopes, config fields and webhook endpoint. " +
				"The name can't change. A webhookSecret is returned only when the update adds an endpoint.",
			Body: UpdateManifestInput{}, Response: integration.Updated{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in UpdateManifestInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if err := checkSensitive(c, in.ApproveSensitive); err != nil {
					return nil, err
				}
				if err := in.Manifest.Validate(c, c.App.Features, in.ApproveSensitive); err != nil {
					return nil, problem(err)
				}
				var out integration.Updated
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.UpdateManifest(c, tx, c.App.Box, c.Param("id"), in.Manifest, in.Config, by(c))
					return err
				})
				return out, problem(err)
			},
		}),
		route(httpx.Route{
			Method: "PATCH", Path: "/integrations/{id}/config", Summary: "Change an integration's config values",
			Body: ConfigInput{}, Response: ConfigOutput{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in ConfigInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out ConfigOutput
				err := c.InTx(func(tx pgx.Tx) error {
					if err := integration.SetConfig(c, tx, c.App.Box, c.Param("id"), in.Values, by(c)); err != nil {
						return err
					}
					var err error
					out.Config, err = integration.MaskedConfig(c, tx, c.App.Box, c.Param("id"))
					return err
				})
				return out, problem(err)
			},
		}),
		statusRoute("pause", "paused", "Pause an integration",
			"Its API keys are refused and its webhook deliveries wait until it's resumed."),
		statusRoute("resume", "active", "Resume a paused integration", "Waiting webhook deliveries are sent."),
		route(httpx.Route{
			Method: "POST", Path: "/integrations/{id}:rotate-key", Summary: "Issue a new API key",
			Description: "The previous keys keep working for graceMinutes (default 60), so the integration can switch " +
				"without downtime. The new key is returned only once.",
			Body: RotateKeyInput{}, Response: integration.Rotated{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in RotateKeyInput
				if err := c.DecodeOptional(&in); err != nil {
					return nil, err
				}
				grace := 60
				if in.GraceMinutes != nil {
					grace = *in.GraceMinutes
				}
				var out integration.Rotated
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.RotateKey(c, tx, c.Param("id"), time.Duration(grace)*time.Minute, c.App.Now(), by(c))
					return err
				})
				return out, err
			},
		}),
		route(httpx.Route{
			Method: "DELETE", Path: "/integrations/{id}", Summary: "Remove an integration",
			Description: "Deletes it with its API keys, config, logs and webhook endpoint. Records it sent stay, and the " +
				"audit log keeps its name.",
			Response: integration.Integration{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out integration.Integration
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.Remove(c, tx, c.Param("id"), by(c))
					return err
				})
				return out, err
			},
		}),
		route(httpx.Route{
			Method: "GET", Path: "/integrations/{id}/logs", Summary: "List an integration's log messages",
			Description: "Messages it sent to POST /integrations/self/logs, oldest first. Use from to start at a time.",
			Query:       logQuery{}, Response: httpx.Page[integration.LogEntry]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				from, err := timeQuery(c, "from")
				if err != nil {
					return nil, err
				}
				if _, err := integration.Get(c, c.App.Pool, c.Param("id")); err != nil {
					return nil, err
				}
				list, err := integration.Logs(c, c.App.Pool, c.Param("id"), lp.AfterID, c.Query("level"), from, lp.Limit+1)
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(l integration.LogEntry) string { return l.ID }), nil
			},
		}),
		route(httpx.Route{
			Method: "GET", Path: "/integrations/{id}/batches", Summary: "List an integration's ingestion batches",
			Description: "Every batch it sent, with how many records were created, updated and rejected, oldest first.",
			Query:       batchQuery{}, Response: httpx.Page[integration.Batch]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				from, err := timeQuery(c, "from")
				if err != nil {
					return nil, err
				}
				if _, err := integration.Get(c, c.App.Pool, c.Param("id")); err != nil {
					return nil, err
				}
				list, err := integration.Batches(c, c.App.Pool, c.Param("id"), lp.AfterID, from,
					c.Query("rejectedOnly") == "true", lp.Limit+1)
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(b integration.Batch) string { return b.ID }), nil
			},
		}),
	}, webhookRoutes()...)
}

func statusRoute(action, status, summary, desc string) httpx.Route {
	return route(httpx.Route{
		Method: "POST", Path: "/integrations/{id}:" + action, Summary: summary, Description: desc,
		Response: integration.Integration{},
		Handler: func(c *httpx.Ctx) (any, error) {
			var out integration.Integration
			err := c.InTx(func(tx pgx.Tx) (err error) {
				out, err = integration.SetStatus(c, tx, c.Param("id"), status, by(c))
				return err
			})
			return out, err
		},
	})
}
