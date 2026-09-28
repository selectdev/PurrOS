// Package webhooks turns outbox events into signed webhook deliveries.
//
// The queue lives in PostgreSQL: the dispatcher fans each outbox event out to
// one delivery per subscribed endpoint, and deliverers claim due deliveries
// with FOR UPDATE SKIP LOCKED, so any number of workers can run in parallel.
package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/features"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/secure"
)

// Backoff between attempts: about 15 attempts over roughly 3 days.
var Backoff = []time.Duration{
	30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 4 * time.Hour, 6 * time.Hour, 8 * time.Hour, 12 * time.Hour, 12 * time.Hour, 12 * time.Hour,
}

// DisableAfter consecutive failed attempts disables an endpoint.
const DisableAfter = 25

type Worker struct {
	Pool     *pgxpool.Pool
	Features *features.Store
	Box      *secure.Box
	Log      *slog.Logger
	Client   *http.Client
	Now      func() time.Time

	PollInterval time.Duration
	Concurrency  int

	// Jobs run periodically alongside delivery, e.g. alert rule evaluation.
	Jobs []Job
}

// Job is periodic background work run by the worker.
type Job struct {
	Name  string
	Every time.Duration
	Run   func(ctx context.Context) error
}

func New(pool *pgxpool.Pool, fs *features.Store, box *secure.Box, log *slog.Logger) *Worker {
	return &Worker{
		Pool: pool, Features: fs, Box: box, Log: log,
		Client:       &http.Client{Timeout: 10 * time.Second},
		Now:          time.Now,
		PollInterval: time.Second,
		Concurrency:  8,
	}
}

// Run processes events and deliveries until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.Log.Info("worker started")
	tick := time.NewTicker(w.PollInterval)
	defer tick.Stop()
	lastCleanup := time.Time{}
	lastRun := make([]time.Time, len(w.Jobs))
	for {
		if _, err := w.Dispatch(ctx); err != nil && ctx.Err() == nil {
			w.Log.Error("dispatch events", "err", err)
		}
		if _, err := w.Deliver(ctx); err != nil && ctx.Err() == nil {
			w.Log.Error("deliver webhooks", "err", err)
		}
		if time.Since(lastCleanup) > time.Hour {
			if err := w.Cleanup(ctx); err != nil && ctx.Err() == nil {
				w.Log.Error("cleanup", "err", err)
			}
			lastCleanup = time.Now()
		}
		for i, j := range w.Jobs {
			if time.Since(lastRun[i]) < j.Every {
				continue
			}
			if err := j.Run(ctx); err != nil && ctx.Err() == nil {
				w.Log.Error("job", "job", j.Name, "err", err)
			}
			lastRun[i] = time.Now()
		}
		select {
		case <-ctx.Done():
			w.Log.Info("worker stopped")
			return nil
		case <-tick.C:
		}
	}
}

type outboxEvent struct {
	ID          string
	OrderingKey string
	Type        string
	Feature     string
	LocationID  *string
	Actor       json.RawMessage
	Payload     json.RawMessage
	CreatedAt   time.Time
}

// Dispatch fans pending outbox events out to webhook deliveries. It returns
// the number of events processed.
func (w *Worker) Dispatch(ctx context.Context) (int, error) {
	n := 0
	err := pgx.BeginFunc(ctx, w.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, ordering_key, type, feature, location_id, actor, payload, created_at FROM outbox_events
			WHERE dispatched_at IS NULL ORDER BY created_at, id LIMIT 200 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		evs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (outboxEvent, error) {
			var e outboxEvent
			err := r.Scan(&e.ID, &e.OrderingKey, &e.Type, &e.Feature, &e.LocationID, &e.Actor, &e.Payload, &e.CreatedAt)
			return e, err
		})
		if err != nil || len(evs) == 0 {
			return err
		}
		type endpoint struct {
			id     string
			events []string
		}
		// Disabled endpoints and paused integrations get deliveries too: they
		// wait (see Deliver) and are sent once the endpoint is active again.
		erows, err := tx.Query(ctx, `SELECT e.id, e.events FROM webhook_endpoints e`)
		if err != nil {
			return err
		}
		endpoints, err := pgx.CollectRows(erows, func(r pgx.CollectableRow) (endpoint, error) {
			var e endpoint
			return e, r.Scan(&e.id, &e.events)
		})
		if err != nil {
			return err
		}
		eventIDs := make([]string, 0, len(evs))
		for _, ev := range evs {
			eventIDs = append(eventIDs, ev.ID)
			on, err := w.Features.IsEnabled(ctx, ev.Feature)
			if err != nil {
				return err
			}
			if !on {
				continue // disabled features' events are never delivered
			}
			for _, ep := range endpoints {
				if !slices.Contains(ep.events, ev.Type) && !slices.Contains(ep.events, "*") {
					continue
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO webhook_deliveries (id, endpoint_id, event_id, ordering_key) VALUES ($1, $2, $3, $4)
					ON CONFLICT (endpoint_id, event_id) DO NOTHING`,
					ids.New(ids.WebhookDelivery), ep.id, ev.ID, ev.OrderingKey); err != nil {
					return err
				}
			}
		}
		n = len(evs)
		_, err = tx.Exec(ctx, `UPDATE outbox_events SET dispatched_at = now() WHERE id = ANY($1)`, eventIDs)
		return err
	})
	return n, err
}

type delivery struct {
	ID         string
	Attempts   int
	EndpointID string
	URL        string
	Secret     []byte
	Event      outboxEvent
}

// Deliver sends due deliveries. It returns the number attempted.
func (w *Worker) Deliver(ctx context.Context) (int, error) {
	// Claim a batch with a lease so a crashed worker's claims expire. Only the
	// earliest pending delivery per (endpoint, ordering key) is eligible, so
	// events about the same record arrive in order.
	rows, err := w.Pool.Query(ctx, `
		WITH due AS (
			SELECT d.id FROM webhook_deliveries d
			JOIN webhook_endpoints we ON we.id = d.endpoint_id AND we.status = 'active'
			LEFT JOIN integrations i ON i.id = we.integration_id
			WHERE d.status = 'pending' AND d.next_attempt_at <= now()
			  AND (i.id IS NULL OR i.status = 'active')
			  AND NOT EXISTS (
				SELECT 1 FROM webhook_deliveries p
				WHERE p.endpoint_id = d.endpoint_id AND p.ordering_key = d.ordering_key
				  AND p.status = 'pending' AND p.event_id < d.event_id)
			ORDER BY d.next_attempt_at LIMIT 50 FOR UPDATE OF d SKIP LOCKED
		)
		UPDATE webhook_deliveries d SET next_attempt_at = now() + interval '2 minutes'
		FROM due, webhook_endpoints e, outbox_events ev
		WHERE d.id = due.id AND e.id = d.endpoint_id AND ev.id = d.event_id
		RETURNING d.id, d.attempts, e.id, e.url, e.secret_encrypted,
		          ev.id, ev.type, ev.feature, ev.location_id, ev.actor, ev.payload, ev.created_at`)
	if err != nil {
		return 0, err
	}
	ds, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (delivery, error) {
		var d delivery
		err := r.Scan(&d.ID, &d.Attempts, &d.EndpointID, &d.URL, &d.Secret,
			&d.Event.ID, &d.Event.Type, &d.Event.Feature, &d.Event.LocationID, &d.Event.Actor, &d.Event.Payload, &d.Event.CreatedAt)
		return d, err
	})
	if err != nil {
		return 0, err
	}
	sem := make(chan struct{}, w.Concurrency)
	var wg sync.WaitGroup
	for _, d := range ds {
		wg.Add(1)
		sem <- struct{}{}
		go func(d delivery) {
			defer wg.Done()
			defer func() { <-sem }()
			w.deliverOne(ctx, d)
		}(d)
	}
	wg.Wait()
	return len(ds), nil
}

// Envelope is the JSON body of every webhook.
type Envelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	CreatedAt  time.Time       `json:"createdAt"`
	APIVersion string          `json:"apiVersion"`
	Actor      json.RawMessage `json:"actor"`
	LocationID *string         `json:"locationId,omitempty"`
	Data       json.RawMessage `json:"data"`
}

func (w *Worker) deliverOne(ctx context.Context, d delivery) {
	body, _ := json.Marshal(Envelope{
		ID: d.Event.ID, Type: d.Event.Type, CreatedAt: d.Event.CreatedAt.UTC(), APIVersion: "v1",
		Actor: d.Event.Actor, LocationID: d.Event.LocationID, Data: d.Event.Payload,
	})
	secret, err := w.Box.Open(d.Secret)
	if err != nil {
		w.record(ctx, d, 0, fmt.Errorf("decrypt endpoint secret: %w", err))
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(body))
	if err != nil {
		w.record(ctx, d, 0, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "PurrOS-Webhooks/1")
	req.Header.Set("PurrOS-Event-Id", d.Event.ID)
	req.Header.Set("PurrOS-Event-Type", d.Event.Type)
	req.Header.Set("PurrOS-Signature", secure.SignWebhook(string(secret), w.Now(), body))
	resp, err := w.Client.Do(req)
	if err != nil {
		w.record(ctx, d, 0, err)
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		w.record(ctx, d, resp.StatusCode, fmt.Errorf("receiver returned HTTP %d", resp.StatusCode))
		return
	}
	w.record(ctx, d, resp.StatusCode, nil)
}

func (w *Worker) record(ctx context.Context, d delivery, code int, deliveryErr error) {
	attempts := d.Attempts + 1
	var codePtr *int
	if code != 0 {
		codePtr = &code
	}
	err := pgx.BeginFunc(ctx, w.Pool, func(tx pgx.Tx) error {
		if deliveryErr == nil {
			if _, err := tx.Exec(ctx, `
				UPDATE webhook_deliveries SET status='succeeded', attempts=$2, last_status_code=$3, last_error=NULL,
					delivered_at=now() WHERE id=$1`, d.ID, attempts, codePtr); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET consecutive_failures=0 WHERE id=$1`, d.EndpointID)
			return err
		}
		msg := deliveryErr.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
		if attempts > len(Backoff) {
			if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET status='failed', attempts=$2, last_status_code=$3,
				last_error=$4 WHERE id=$1`, d.ID, attempts, codePtr, msg); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET attempts=$2, last_status_code=$3, last_error=$4,
				next_attempt_at=$5 WHERE id=$1`, d.ID, attempts, codePtr, msg, w.Now().Add(Backoff[attempts-1])); err != nil {
				return err
			}
		}
		var failures int
		if err := tx.QueryRow(ctx, `UPDATE webhook_endpoints SET consecutive_failures=consecutive_failures+1, updated_at=now()
			WHERE id=$1 RETURNING consecutive_failures`, d.EndpointID).Scan(&failures); err != nil {
			return err
		}
		if failures >= DisableAfter {
			if _, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET status='disabled', disabled_at=now() WHERE id=$1 AND status='active'`, d.EndpointID); err != nil {
				return err
			}
			w.Log.Warn("webhook endpoint disabled after repeated failures", "endpoint", d.EndpointID)
		}
		return nil
	})
	if err != nil {
		w.Log.Error("record delivery", "delivery", d.ID, "err", err)
	}
	if deliveryErr != nil {
		w.Log.Info("webhook delivery failed", "delivery", d.ID, "event", d.Event.Type, "attempt", attempts, "err", deliveryErr)
	}
}

// Cleanup removes expired idempotency records.
func (w *Worker) Cleanup(ctx context.Context) error {
	_, err := w.Pool.Exec(ctx, `DELETE FROM idempotency_records WHERE created_at < now() - interval '24 hours'`)
	return err
}
