package httpx

import (
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/events"
)

// Actor returns the events.Actor for the caller.
func (c *Ctx) Actor() events.Actor {
	p := c.Principal
	switch {
	case p == nil:
		return events.System
	case p.Kind == "integration":
		return events.Actor{Type: "integration", ID: p.IntegrationID, Name: p.IntegrationDisplayName}
	case p.User != nil:
		return events.Actor{Type: "user", ID: p.UserID, Name: p.User.Name}
	case p.UserID != "":
		return events.Actor{Type: "user", ID: p.UserID}
	default:
		return events.Actor{Type: "api_key", ID: p.KeyID}
	}
}

// Change records an audited change and emits its event in one call, inside
// the caller's transaction. eventType may be empty for audit-only changes.
type Change struct {
	Action     string // audit action, e.g. "employee.update"
	EventType  string // webhook event, e.g. "employee.updated"
	Feature    string
	EntityType string
	EntityID   string
	LocationID string
	Before     any
	After      any
}

func (c *Ctx) Record(q db.Querier, ch Change) error {
	actor := c.Actor()
	if err := events.Audit(c, q, events.AuditEntry{
		Actor: actor, Action: ch.Action, EntityType: ch.EntityType, EntityID: ch.EntityID,
		Before: ch.Before, After: ch.After, IP: c.ClientIP(), RequestID: c.RequestID,
	}); err != nil {
		return err
	}
	if ch.EventType == "" {
		return nil
	}
	var prev any
	if ch.Before != nil && ch.After != nil {
		prev = events.Diff(ch.Before, ch.After)
	}
	_, err := events.Emit(c, q, events.Event{
		Type: ch.EventType, Feature: ch.Feature, LocationID: ch.LocationID,
		OrderingKey: ch.EntityType + ":" + ch.EntityID,
		Actor:       actor, Object: ch.After, Previous: prev,
	})
	return err
}
