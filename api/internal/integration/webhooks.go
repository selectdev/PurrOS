package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/catalog"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/secure"
)

// Endpoint is a webhook endpoint: an integration's, or a standalone one.
type Endpoint struct {
	ID                  string     `json:"id"`
	IntegrationID       *string    `json:"integrationId" doc:"Set when the endpoint belongs to an integration's manifest"`
	URL                 string     `json:"url"`
	Description         string     `json:"description"`
	Events              []string   `json:"events" doc:"Subscribed event types; * means every event"`
	Status              string     `json:"status" doc:"active or disabled"`
	ConsecutiveFailures int        `json:"consecutiveFailures" doc:"Disabled automatically at 25"`
	DisabledAt          *time.Time `json:"disabledAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// EndpointInput creates a standalone endpoint.
type EndpointInput struct {
	URL         string   `json:"url" validate:"required,max=2000"`
	Events      []string `json:"events" validate:"required,min=1,max=200" doc:"Event types to receive, or [\"*\"] for all"`
	Description string   `json:"description,omitempty" validate:"max=500"`
}

// EndpointPatch changes an endpoint. An integration's URL and events come
// from its manifest and can't be changed here.
type EndpointPatch struct {
	URL         *string   `json:"url,omitempty" validate:"omitempty,max=2000"`
	Events      *[]string `json:"events,omitempty" validate:"omitempty,min=1,max=200"`
	Description *string   `json:"description,omitempty" validate:"omitempty,max=500"`
	Status      *string   `json:"status,omitempty" validate:"omitempty,oneof=active disabled" doc:"active re-enables a disabled endpoint and resets its failure count"`
}

const endpointCols = `id, integration_id, url, description, events, status, consecutive_failures, disabled_at, created_at, updated_at`

func scanEndpoint(row pgx.Row) (Endpoint, error) {
	var e Endpoint
	err := row.Scan(&e.ID, &e.IntegrationID, &e.URL, &e.Description, &e.Events, &e.Status,
		&e.ConsecutiveFailures, &e.DisabledAt, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, httpx.NotFound("Webhook endpoint not found.")
	}
	return e, err
}

// CheckEvents validates event types for a standalone endpoint: each must be
// known (or "*") and belong to an enabled feature.
func CheckEvents(ctx context.Context, fs FeatureChecker, evs []string) error {
	var errs []httpx.FieldError
	for i, e := range evs {
		path := fmt.Sprintf("events[%d]", i)
		if e == "*" {
			continue
		}
		feat, ok := catalog.Events[e]
		if !ok {
			errs = append(errs, httpx.FieldError{Path: path, Message: fmt.Sprintf("Unknown event %q", e)})
			continue
		}
		if on, err := fs.IsEnabled(ctx, feat); err != nil {
			return err
		} else if !on {
			errs = append(errs, httpx.FieldError{Path: path, Message: fmt.Sprintf("Event %q belongs to the %q feature, which is switched off", e, feat)})
		}
	}
	if len(errs) > 0 {
		return httpx.Validation(errs...)
	}
	return nil
}

func insertEndpoint(ctx context.Context, tx pgx.Tx, box *secure.Box, integrationID *string, in EndpointInput) (Endpoint, string, error) {
	secret := secure.NewWebhookSecret()
	ep, err := scanEndpoint(tx.QueryRow(ctx, `
		INSERT INTO webhook_endpoints (id, integration_id, url, secret_encrypted, events, description)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+endpointCols,
		ids.New(ids.WebhookEndpoint), integrationID, in.URL, box.Seal([]byte(secret)), in.Events, in.Description))
	return ep, secret, err
}

// CreatedEndpoint is a new endpoint with its signing secret, shown once.
type CreatedEndpoint struct {
	Endpoint Endpoint `json:"endpoint"`
	Secret   string   `json:"secret" doc:"Signing secret for PurrOS-Signature; shown only once"`
}

// CreateEndpoint adds a standalone endpoint. Check the events first.
func CreateEndpoint(ctx context.Context, tx pgx.Tx, box *secure.Box, in EndpointInput, by By) (CreatedEndpoint, error) {
	if err := CheckURL(in.URL); err != nil {
		return CreatedEndpoint{}, httpx.Validation(httpx.FieldError{Path: "url", Message: "Must be an http(s) URL without credentials"})
	}
	ep, secret, err := insertEndpoint(ctx, tx, box, nil, in)
	if err != nil {
		return CreatedEndpoint{}, err
	}
	return CreatedEndpoint{Endpoint: ep, Secret: secret},
		by.audit(ctx, tx, "webhook_endpoint.create", "webhook_endpoint", ep.ID, nil, ep)
}

// GetEndpoint loads an endpoint.
func GetEndpoint(ctx context.Context, q db.Querier, id string) (Endpoint, error) {
	return scanEndpoint(q.QueryRow(ctx, `SELECT `+endpointCols+` FROM webhook_endpoints WHERE id = $1`, id))
}

// ListEndpoints lists endpoints ordered by ID. integrationID "none" lists
// standalone endpoints only.
func ListEndpoints(ctx context.Context, q db.Querier, afterID, integrationID, status string, limit int) ([]Endpoint, error) {
	rows, err := q.Query(ctx, `SELECT `+endpointCols+` FROM webhook_endpoints
		WHERE id > $1 AND ($2 = '' OR ($2 = 'none' AND integration_id IS NULL) OR integration_id = $2)
		  AND ($3 = '' OR status = $3)
		ORDER BY id LIMIT $4`, afterID, integrationID, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Endpoint, error) { return scanEndpoint(r) })
}

func lockEndpoint(ctx context.Context, tx pgx.Tx, id string) (Endpoint, error) {
	return scanEndpoint(tx.QueryRow(ctx, `SELECT `+endpointCols+` FROM webhook_endpoints WHERE id = $1 FOR UPDATE`, id))
}

// UpdateEndpoint applies a patch. Check new events first.
func UpdateEndpoint(ctx context.Context, tx pgx.Tx, id string, p EndpointPatch, by By) (Endpoint, error) {
	before, err := lockEndpoint(ctx, tx, id)
	if err != nil {
		return before, err
	}
	if before.IntegrationID != nil && (p.URL != nil || p.Events != nil) {
		return before, httpx.Conflict("This endpoint belongs to an integration: change its URL and events in the integration's manifest.")
	}
	after := before
	if p.URL != nil {
		if err := CheckURL(*p.URL); err != nil {
			return before, httpx.Validation(httpx.FieldError{Path: "url", Message: "Must be an http(s) URL without credentials"})
		}
		after.URL = *p.URL
	}
	if p.Events != nil {
		after.Events = *p.Events
	}
	if p.Description != nil {
		after.Description = *p.Description
	}
	if p.Status != nil {
		after.Status = *p.Status
	}
	ep, err := scanEndpoint(tx.QueryRow(ctx, `
		UPDATE webhook_endpoints SET url = $2, events = $3, description = $4, status = $5,
			consecutive_failures = CASE WHEN $5 = 'active' AND status = 'disabled' THEN 0 ELSE consecutive_failures END,
			disabled_at = CASE WHEN $5 = 'active' THEN NULL WHEN status = 'active' THEN now() ELSE disabled_at END,
			updated_at = now()
		WHERE id = $1 RETURNING `+endpointCols, id, after.URL, after.Events, after.Description, after.Status))
	if err != nil {
		return ep, err
	}
	return ep, by.audit(ctx, tx, "webhook_endpoint.update", "webhook_endpoint", id, before, ep)
}

// DeleteEndpoint removes a standalone endpoint and its delivery log.
func DeleteEndpoint(ctx context.Context, tx pgx.Tx, id string, by By) (Endpoint, error) {
	before, err := lockEndpoint(ctx, tx, id)
	if err != nil {
		return before, err
	}
	if before.IntegrationID != nil {
		return before, httpx.Conflict("This endpoint belongs to an integration: remove webhooks from its manifest, or remove the integration.")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1`, id); err != nil {
		return before, err
	}
	return before, by.audit(ctx, tx, "webhook_endpoint.delete", "webhook_endpoint", id, before, nil)
}

// RotatedSecret is an endpoint's new signing secret.
type RotatedSecret struct {
	Secret string `json:"secret" doc:"New signing secret; shown only once. The old one stops working immediately."`
}

// RotateSecret gives an endpoint a new signing secret.
func RotateSecret(ctx context.Context, tx pgx.Tx, box *secure.Box, id string, by By) (RotatedSecret, error) {
	if _, err := lockEndpoint(ctx, tx, id); err != nil {
		return RotatedSecret{}, err
	}
	secret := secure.NewWebhookSecret()
	if _, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET secret_encrypted = $2, updated_at = now() WHERE id = $1`,
		id, box.Seal([]byte(secret))); err != nil {
		return RotatedSecret{}, err
	}
	return RotatedSecret{Secret: secret}, by.audit(ctx, tx, "webhook_endpoint.rotate_secret", "webhook_endpoint", id, nil, nil)
}

// Delivery is one attempt series to send one event to one endpoint.
type Delivery struct {
	ID             string     `json:"id"`
	EndpointID     string     `json:"endpointId"`
	EventID        string     `json:"eventId"`
	EventType      string     `json:"eventType"`
	Status         string     `json:"status" doc:"pending, succeeded or failed (gave up after about 3 days)"`
	Attempts       int        `json:"attempts"`
	NextAttemptAt  *time.Time `json:"nextAttemptAt" doc:"When a pending delivery is tried next"`
	LastStatusCode *int       `json:"lastStatusCode"`
	LastError      *string    `json:"lastError"`
	CreatedAt      time.Time  `json:"createdAt"`
	DeliveredAt    *time.Time `json:"deliveredAt"`
}

// DeliveryDetail adds the event exactly as it is sent.
type DeliveryDetail struct {
	Delivery
	Event json.RawMessage `json:"event" doc:"The webhook body"`
}

const deliveryCols = `d.id, d.endpoint_id, d.event_id, ev.type, d.status, d.attempts,
	CASE WHEN d.status = 'pending' THEN d.next_attempt_at END, d.last_status_code, d.last_error, d.created_at, d.delivered_at`

func scanDelivery(row pgx.Row, extra ...any) (Delivery, error) {
	var d Delivery
	dest := append([]any{&d.ID, &d.EndpointID, &d.EventID, &d.EventType, &d.Status, &d.Attempts,
		&d.NextAttemptAt, &d.LastStatusCode, &d.LastError, &d.CreatedAt, &d.DeliveredAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, httpx.NotFound("Webhook delivery not found.")
	}
	return d, err
}

// DeliveryFilter narrows a delivery list.
type DeliveryFilter struct {
	Status    string
	EventType string
	From      *time.Time
}

// ListDeliveries lists an endpoint's deliveries, oldest first.
func ListDeliveries(ctx context.Context, q db.Querier, endpointID, afterID string, f DeliveryFilter, limit int) ([]Delivery, error) {
	rows, err := q.Query(ctx, `SELECT `+deliveryCols+` FROM webhook_deliveries d JOIN outbox_events ev ON ev.id = d.event_id
		WHERE d.endpoint_id = $1 AND d.id > $2 AND ($3 = '' OR d.status = $3) AND ($4 = '' OR ev.type = $4)
		  AND ($5::timestamptz IS NULL OR d.created_at >= $5)
		ORDER BY d.id LIMIT $6`, endpointID, afterID, f.Status, f.EventType, f.From, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Delivery, error) { return scanDelivery(r) })
}

// GetDelivery loads a delivery with the event body.
func GetDelivery(ctx context.Context, q db.Querier, id string) (DeliveryDetail, error) {
	var (
		d          DeliveryDetail
		created    time.Time
		loc        *string
		actor, pay json.RawMessage
	)
	var err error
	d.Delivery, err = scanDelivery(q.QueryRow(ctx, `SELECT `+deliveryCols+`, ev.created_at, ev.location_id, ev.actor, ev.payload
		FROM webhook_deliveries d JOIN outbox_events ev ON ev.id = d.event_id WHERE d.id = $1`, id),
		&created, &loc, &actor, &pay)
	if err != nil {
		return d, err
	}
	d.Event, err = json.Marshal(map[string]any{"id": d.EventID, "type": d.EventType, "createdAt": created.UTC(),
		"apiVersion": "v1", "actor": actor, "locationId": loc, "data": pay})
	return d, err
}

// RetryDelivery queues a delivery to be sent again as soon as possible,
// restarting its retry schedule.
func RetryDelivery(ctx context.Context, tx pgx.Tx, id string, by By) (Delivery, error) {
	var endpointID string
	if err := tx.QueryRow(ctx, `SELECT endpoint_id FROM webhook_deliveries WHERE id = $1 FOR UPDATE`, id).Scan(&endpointID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Delivery{}, httpx.NotFound("Webhook delivery not found.")
		}
		return Delivery{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET status = 'pending', attempts = 0, next_attempt_at = now(),
		delivered_at = NULL WHERE id = $1`, id); err != nil {
		return Delivery{}, err
	}
	if err := by.audit(ctx, tx, "webhook_delivery.retry", "webhook_delivery", id, nil, nil); err != nil {
		return Delivery{}, err
	}
	d, err := GetDelivery(ctx, tx, id)
	return d.Delivery, err
}

// RetryFailed queues every failed delivery of an endpoint again, e.g. after
// the receiver was down for days. It returns how many were queued.
func RetryFailed(ctx context.Context, tx pgx.Tx, endpointID string, by By) (int, error) {
	if _, err := lockEndpoint(ctx, tx, endpointID); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET status = 'pending', attempts = 0, next_attempt_at = now()
		WHERE endpoint_id = $1 AND status = 'failed'`, endpointID)
	if err != nil {
		return 0, err
	}
	n := int(tag.RowsAffected())
	return n, by.audit(ctx, tx, "webhook_endpoint.retry_failed", "webhook_endpoint", endpointID, nil, map[string]any{"queued": n})
}

// Ping queues a webhook.ping event for one endpoint, to test the receiver
// and its signature check. It is delivered like any other event.
func Ping(ctx context.Context, tx pgx.Tx, endpointID string, by By) (Delivery, error) {
	ep, err := lockEndpoint(ctx, tx, endpointID)
	if err != nil {
		return Delivery{}, err
	}
	actor, _ := json.Marshal(by.Actor)
	payload, _ := json.Marshal(map[string]any{"object": map[string]any{"endpointId": ep.ID, "message": "Test event from PurrOS"}})
	eventID := ids.New(ids.Event)
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (id, type, feature, ordering_key, actor, payload, dispatched_at)
		VALUES ($1, $2, 'core', $3, $4, $5, now())`, eventID, catalog.PingEvent, "ping:"+ep.ID, actor, payload); err != nil {
		return Delivery{}, err
	}
	deliveryID := ids.New(ids.WebhookDelivery)
	if _, err := tx.Exec(ctx, `INSERT INTO webhook_deliveries (id, endpoint_id, event_id, ordering_key) VALUES ($1, $2, $3, $4)`,
		deliveryID, ep.ID, eventID, "ping:"+ep.ID); err != nil {
		return Delivery{}, err
	}
	if err := by.audit(ctx, tx, "webhook_endpoint.ping", "webhook_endpoint", ep.ID, nil, nil); err != nil {
		return Delivery{}, err
	}
	d, err := GetDelivery(ctx, tx, deliveryID)
	return d.Delivery, err
}

// Batch is one ingestion batch an integration sent.
type Batch struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind" doc:"What was sent, e.g. sales_transactions or punches"`
	Source    string    `json:"source"`
	Received  int       `json:"received"`
	Created   int       `json:"created"`
	Updated   int       `json:"updated"`
	Rejected  int       `json:"rejected"`
	CreatedAt time.Time `json:"createdAt"`
}

// Batches lists an integration's ingestion batches, oldest first.
func Batches(ctx context.Context, q db.Querier, integrationID, afterID string, from *time.Time, rejectedOnly bool, limit int) ([]Batch, error) {
	rows, err := q.Query(ctx, `SELECT id, kind, source, received, created, updated, rejected, created_at FROM ingest_batches
		WHERE integration_id = $1 AND id > $2 AND ($3::timestamptz IS NULL OR created_at >= $3) AND (NOT $4 OR rejected > 0)
		ORDER BY id LIMIT $5`, integrationID, afterID, from, rejectedOnly, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Batch, error) {
		var b Batch
		err := r.Scan(&b.ID, &b.Kind, &b.Source, &b.Received, &b.Created, &b.Updated, &b.Rejected, &b.CreatedAt)
		return b, err
	})
}

// LogEntry is a message an integration sent to POST /integrations/self/logs.
type LogEntry struct {
	ID        string          `json:"id"`
	Level     string          `json:"level"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Logs lists an integration's log messages, oldest first.
func Logs(ctx context.Context, q db.Querier, integrationID, afterID, level string, from *time.Time, limit int) ([]LogEntry, error) {
	rows, err := q.Query(ctx, `SELECT id, level, message, data, created_at FROM integration_logs
		WHERE integration_id = $1 AND id > $2 AND ($3 = '' OR level = $3) AND ($4::timestamptz IS NULL OR created_at >= $4)
		ORDER BY id LIMIT $5`, integrationID, afterID, level, from, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (LogEntry, error) {
		var l LogEntry
		err := r.Scan(&l.ID, &l.Level, &l.Message, &l.Data, &l.CreatedAt)
		return l, err
	})
}
