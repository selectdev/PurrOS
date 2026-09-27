// Package events writes audit log entries and outbox events. Both are written
// in the same transaction as the change they describe, so an event is never
// lost and never sent for a change that was rolled back.
package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/ids"
)

// Actor is who caused a change.
type Actor struct {
	Type string `json:"type"` // user | integration | api_key | system | employee
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

var System = Actor{Type: "system"}

// Event is an outbox event, delivered to webhooks by the worker.
type Event struct {
	Type       string // e.g. "employee.created"
	Feature    string // feature the event belongs to; disabled features' events aren't delivered
	LocationID string
	// OrderingKey groups events that must be delivered in order, usually
	// "entityType:entityID".
	OrderingKey string
	Actor       Actor
	Object      any // the resource as the API returns it
	Previous    any // changed fields' old values (update events), or nil
}

// Emit writes an event to the outbox.
func Emit(ctx context.Context, q db.Querier, e Event) (string, error) {
	data := map[string]any{"object": e.Object}
	if e.Previous != nil {
		data["previous"] = e.Previous
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal event: %w", err)
	}
	actor, _ := json.Marshal(e.Actor)
	id := ids.New(ids.Event)
	var loc *string
	if e.LocationID != "" {
		loc = &e.LocationID
	}
	_, err = q.Exec(ctx, `
		INSERT INTO outbox_events (id, type, feature, location_id, ordering_key, actor, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, e.Type, e.Feature, loc, e.OrderingKey, actor, payload)
	return id, err
}

// AuditEntry is one audit log record.
type AuditEntry struct {
	Actor      Actor
	Action     string // e.g. "employee.update"
	EntityType string
	EntityID   string
	Before     any
	After      any
	IP         string
	RequestID  string
}

// Audit writes an audit log entry.
func Audit(ctx context.Context, q db.Querier, e AuditEntry) error {
	var before, after []byte
	var err error
	if e.Before != nil {
		if before, err = json.Marshal(e.Before); err != nil {
			return err
		}
	}
	if e.After != nil {
		if after, err = json.Marshal(e.After); err != nil {
			return err
		}
	}
	_, err = q.Exec(ctx, `
		INSERT INTO audit_log (id, actor_type, actor_id, actor_name, action, entity_type, entity_id, before, after, ip, request_id)
		VALUES ($1, $2, nullif($3, ''), nullif($4, ''), $5, nullif($6, ''), nullif($7, ''), $8, $9, nullif($10, ''), nullif($11, ''))`,
		ids.New(ids.Audit), e.Actor.Type, e.Actor.ID, e.Actor.Name, e.Action, e.EntityType, e.EntityID,
		before, after, e.IP, e.RequestID)
	return err
}

// Diff returns the fields whose JSON values differ between before and after,
// with their old values. Used for the "previous" part of update events.
func Diff(before, after any) map[string]any {
	var b, a map[string]json.RawMessage
	bb, _ := json.Marshal(before)
	ab, _ := json.Marshal(after)
	_ = json.Unmarshal(bb, &b)
	_ = json.Unmarshal(ab, &a)
	out := map[string]any{}
	for k, v := range b {
		if k == "updatedAt" || k == "version" {
			continue
		}
		if string(a[k]) != string(v) {
			out[k] = v
		}
	}
	return out
}
