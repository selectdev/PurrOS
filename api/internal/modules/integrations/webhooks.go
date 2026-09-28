package integrations

import (
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/integration"
)

const webhookTag = "Webhooks"

func webhookRoute(r httpx.Route) httpx.Route {
	r.Tag, r.Auth, r.Permission = webhookTag, httpx.AuthUser, "webhooks.manage"
	return r
}

type endpointQuery struct {
	httpx.ListParams
	IntegrationID string `json:"integrationId,omitempty" doc:"Only this integration's endpoints, or none for standalone endpoints"`
	Status        string `json:"status,omitempty" doc:"active or disabled"`
}

type deliveryQuery struct {
	httpx.ListParams
	Status    string     `json:"status,omitempty" doc:"pending, succeeded or failed"`
	EventType string     `json:"eventType,omitempty"`
	From      *time.Time `json:"from,omitempty" doc:"Only deliveries created at or after this time"`
}

type QueuedDeliveries struct {
	Queued int `json:"queued" doc:"How many deliveries were queued again"`
}

func webhookRoutes() []httpx.Route {
	return []httpx.Route{
		webhookRoute(httpx.Route{
			Method: "GET", Path: "/webhook-endpoints", Summary: "List webhook endpoints",
			Query: endpointQuery{}, Response: httpx.Page[integration.Endpoint]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				list, err := integration.ListEndpoints(c, c.App.Pool, lp.AfterID, c.Query("integrationId"), c.Query("status"), lp.Limit+1)
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(e integration.Endpoint) string { return e.ID }), nil
			},
		}),
		webhookRoute(httpx.Route{
			Method: "POST", Path: "/webhook-endpoints", Summary: "Add a webhook endpoint",
			Description: "A standalone endpoint, not tied to an integration. The signing secret is returned only once.",
			Body:        integration.EndpointInput{}, Response: integration.CreatedEndpoint{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in integration.EndpointInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if err := integration.CheckEvents(c, c.App.Features, in.Events); err != nil {
					return nil, err
				}
				var out integration.CreatedEndpoint
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.CreateEndpoint(c, tx, c.App.Box, in, by(c))
					return err
				})
				return out, err
			},
		}),
		webhookRoute(httpx.Route{
			Method: "GET", Path: "/webhook-endpoints/{id}", Summary: "Get a webhook endpoint", Response: integration.Endpoint{},
			Handler: func(c *httpx.Ctx) (any, error) { return integration.GetEndpoint(c, c.App.Pool, c.Param("id")) },
		}),
		webhookRoute(httpx.Route{
			Method: "PATCH", Path: "/webhook-endpoints/{id}", Summary: "Update a webhook endpoint",
			Description: "Setting status to active re-enables an endpoint that was disabled after repeated failures and " +
				"resets its failure count; its waiting deliveries are then sent. An integration's URL and events come " +
				"from its manifest and can't be changed here.",
			Body: integration.EndpointPatch{}, Response: integration.Endpoint{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in integration.EndpointPatch
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if in.Events != nil {
					if err := integration.CheckEvents(c, c.App.Features, *in.Events); err != nil {
						return nil, err
					}
				}
				var out integration.Endpoint
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.UpdateEndpoint(c, tx, c.Param("id"), in, by(c))
					return err
				})
				return out, err
			},
		}),
		webhookRoute(httpx.Route{
			Method: "DELETE", Path: "/webhook-endpoints/{id}", Summary: "Delete a webhook endpoint",
			Description: "Standalone endpoints only, with their delivery log. Remove an integration's endpoint from its manifest.",
			Response:    integration.Endpoint{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out integration.Endpoint
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.DeleteEndpoint(c, tx, c.Param("id"), by(c))
					return err
				})
				return out, err
			},
		}),
		webhookRoute(httpx.Route{
			Method: "POST", Path: "/webhook-endpoints/{id}:rotate-secret", Summary: "Rotate an endpoint's signing secret",
			Description: "The new secret is returned only once, and the old one stops working immediately.",
			Response:    integration.RotatedSecret{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out integration.RotatedSecret
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.RotateSecret(c, tx, c.App.Box, c.Param("id"), by(c))
					return err
				})
				return out, err
			},
		}),
		webhookRoute(httpx.Route{
			Method: "POST", Path: "/webhook-endpoints/{id}:ping", Summary: "Send a test event",
			Description: "Queues a webhook.ping event for this endpoint only. It's signed and retried like any other " +
				"event; follow it with GET /webhook-deliveries/{id}.",
			Response: integration.Delivery{}, Status: 202,
			Handler: func(c *httpx.Ctx) (any, error) {
				var out integration.Delivery
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.Ping(c, tx, c.Param("id"), by(c))
					return err
				})
				return out, err
			},
		}),
		webhookRoute(httpx.Route{
			Method: "POST", Path: "/webhook-endpoints/{id}:retry-failed", Summary: "Queue every failed delivery again",
			Description: "For after a receiver was down longer than the retry schedule (about 3 days).",
			Response:    QueuedDeliveries{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var n int
				err := c.InTx(func(tx pgx.Tx) (err error) {
					n, err = integration.RetryFailed(c, tx, c.Param("id"), by(c))
					return err
				})
				return QueuedDeliveries{Queued: n}, err
			},
		}),
		webhookRoute(httpx.Route{
			Method: "GET", Path: "/webhook-endpoints/{id}/deliveries", Summary: "List an endpoint's deliveries",
			Description: "Oldest first. Use from to start at a time, and status=failed to find what didn't arrive.",
			Query:       deliveryQuery{}, Response: httpx.Page[integration.Delivery]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				from, err := timeQuery(c, "from")
				if err != nil {
					return nil, err
				}
				if _, err := integration.GetEndpoint(c, c.App.Pool, c.Param("id")); err != nil {
					return nil, err
				}
				list, err := integration.ListDeliveries(c, c.App.Pool, c.Param("id"), lp.AfterID,
					integration.DeliveryFilter{Status: c.Query("status"), EventType: c.Query("eventType"), From: from}, lp.Limit+1)
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(d integration.Delivery) string { return d.ID }), nil
			},
		}),
		webhookRoute(httpx.Route{
			Method: "GET", Path: "/webhook-deliveries/{id}", Summary: "Get a delivery",
			Description: "With the event body exactly as it is sent.", Response: integration.DeliveryDetail{},
			Handler: func(c *httpx.Ctx) (any, error) { return integration.GetDelivery(c, c.App.Pool, c.Param("id")) },
		}),
		webhookRoute(httpx.Route{
			Method: "POST", Path: "/webhook-deliveries/{id}:retry", Summary: "Send a delivery again",
			Description: "Queues it to be sent as soon as possible and restarts its retry schedule. Works for failed and " +
				"succeeded deliveries (e.g. to replay an event after fixing the receiver).",
			Response: integration.Delivery{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out integration.Delivery
				err := c.InTx(func(tx pgx.Tx) (err error) {
					out, err = integration.RetryDelivery(c, tx, c.Param("id"), by(c))
					return err
				})
				return out, err
			},
		}),
	}
}
