package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/secure"
)

// Integration is a registered integration.
type Integration struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	DisplayName     string     `json:"displayName"`
	Version         string     `json:"version"`
	Description     string     `json:"description"`
	Homepage        string     `json:"homepage"`
	Scopes          []string   `json:"scopes"`
	Status          string     `json:"status" doc:"active or paused"`
	HealthStatus    *string    `json:"healthStatus" doc:"ok, warning or error, from its last heartbeat"`
	HealthMessage   *string    `json:"healthMessage"`
	LastHeartbeatAt *time.Time `json:"lastHeartbeatAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// Key describes an API key without revealing it.
type Key struct {
	ID         string     `json:"id"`
	Prefix     string     `json:"prefix" doc:"First characters, to recognise the key"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	LastUsedIP *string    `json:"lastUsedIp"`
}

// By is who makes a change, for the audit log.
type By struct {
	Actor     events.Actor
	IP        string
	RequestID string
}

func (b By) audit(ctx context.Context, q db.Querier, action, entityType, id string, before, after any) error {
	return events.Audit(ctx, q, events.AuditEntry{Actor: b.Actor, Action: action, EntityType: entityType,
		EntityID: id, Before: before, After: after, IP: b.IP, RequestID: b.RequestID})
}

const cols = `id, name, display_name, version, description, homepage, scopes, status,
	health_status, health_message, last_heartbeat_at, created_at, updated_at`

func scan(row pgx.Row) (Integration, error) {
	var i Integration
	err := row.Scan(&i.ID, &i.Name, &i.DisplayName, &i.Version, &i.Description, &i.Homepage, &i.Scopes, &i.Status,
		&i.HealthStatus, &i.HealthMessage, &i.LastHeartbeatAt, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, httpx.NotFound("Integration not found.")
	}
	return i, err
}

// Get loads an integration by ID.
func Get(ctx context.Context, q db.Querier, id string) (Integration, error) {
	return scan(q.QueryRow(ctx, `SELECT `+cols+` FROM integrations WHERE id = $1`, id))
}

// GetByName loads an integration by its manifest name.
func GetByName(ctx context.Context, q db.Querier, name string) (Integration, error) {
	return scan(q.QueryRow(ctx, `SELECT `+cols+` FROM integrations WHERE name = $1`, name))
}

// List returns integrations ordered by ID, after afterID, optionally by status.
func List(ctx context.Context, q db.Querier, afterID, status string, limit int) ([]Integration, error) {
	rows, err := q.Query(ctx, `SELECT `+cols+` FROM integrations WHERE id > $1 AND ($2 = '' OR status = $2)
		ORDER BY id LIMIT $3`, afterID, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Integration, error) { return scan(r) })
}

// Keys lists an integration's API keys, newest first.
func Keys(ctx context.Context, q db.Querier, id string) ([]Key, error) {
	rows, err := q.Query(ctx, `SELECT id, display_prefix, created_at, expires_at, revoked_at, last_used_at, last_used_ip
		FROM api_keys WHERE integration_id = $1 ORDER BY id DESC`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Key, error) {
		var k Key
		err := r.Scan(&k.ID, &k.Prefix, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt, &k.LastUsedIP)
		return k, err
	})
}

// Manifest returns the stored manifest.
func StoredManifest(ctx context.Context, q db.Querier, id string) (Manifest, error) {
	var raw []byte
	var m Manifest
	if err := q.QueryRow(ctx, `SELECT manifest FROM integrations WHERE id = $1`, id).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return m, httpx.NotFound("Integration not found.")
		}
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}

// Registered is what registration returns. The key and secret are shown once.
type Registered struct {
	Integration       Integration `json:"integration"`
	APIKey            string      `json:"apiKey" doc:"Shown only once"`
	KeyID             string      `json:"keyId"`
	WebhookSecret     string      `json:"webhookSecret,omitempty" doc:"Shown only once; present when the manifest declares webhooks"`
	WebhookEndpointID string      `json:"webhookEndpointId,omitempty"`
}

// Register stores an integration, issues its API key and creates its webhook
// endpoint inside tx. Validate the manifest first.
func Register(ctx context.Context, tx pgx.Tx, box *secure.Box, m Manifest, config map[string]any, by By) (Registered, error) {
	var out Registered
	cfg, err := normalizeConfig(ctx, tx, m.Config, config)
	if err != nil {
		return out, err
	}
	if m.DisplayName == "" {
		m.DisplayName = m.Name
	}
	manifestJSON, _ := json.Marshal(m)
	cfgJSON, _ := json.Marshal(cfg)
	id := ids.New(ids.Integration)
	_, err = tx.Exec(ctx, `
		INSERT INTO integrations (id, name, display_name, version, description, homepage, scopes, manifest, config_encrypted)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, m.Name, m.DisplayName, m.Version, m.Description, m.Homepage, m.Scopes, manifestJSON, box.Seal(cfgJSON))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return out, httpx.Conflict(fmt.Sprintf("An integration named %q is already registered.", m.Name))
		}
		return out, err
	}
	if out.KeyID, out.APIKey, err = issueKey(ctx, tx, id, m.DisplayName); err != nil {
		return out, err
	}
	if m.Webhooks != nil {
		ep, secret, err := insertEndpoint(ctx, tx, box, &id, EndpointInput{URL: m.Webhooks.URL, Events: m.Webhooks.Events})
		if err != nil {
			return out, err
		}
		out.WebhookSecret, out.WebhookEndpointID = secret, ep.ID
	}
	if out.Integration, err = Get(ctx, tx, id); err != nil {
		return out, err
	}
	return out, by.audit(ctx, tx, "integration.register", "integration", id, nil,
		map[string]any{"name": m.Name, "scopes": m.Scopes, "configKeys": slices.Sorted(maps.Keys(cfg))})
}

func issueKey(ctx context.Context, tx pgx.Tx, integrationID, name string) (keyID, plain string, err error) {
	plain, hash, display := secure.NewAPIKey()
	keyID = ids.New(ids.APIKey)
	_, err = tx.Exec(ctx, `INSERT INTO api_keys (id, kind, display_prefix, hash, integration_id, name)
		VALUES ($1, 'integration', $2, $3, $4, $5)`, keyID, display, hash, integrationID, name)
	return keyID, plain, err
}

// Updated is what a manifest update returns.
type Updated struct {
	Integration       Integration `json:"integration"`
	WebhookEndpointID string      `json:"webhookEndpointId,omitempty"`
	WebhookSecret     string      `json:"webhookSecret,omitempty" doc:"Only when the update adds a webhook endpoint; shown once"`
}

// UpdateManifest replaces an integration's manifest (e.g. for a new version):
// its scopes, descriptive fields, config fields and webhook endpoint. The name
// can't change. Config values for fields the new manifest drops are removed,
// and config may add or change values (nil removes one). Validate first.
func UpdateManifest(ctx context.Context, tx pgx.Tx, box *secure.Box, id string, m Manifest, config map[string]any, by By) (Updated, error) {
	var out Updated
	before, err := lock(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if m.Name != before.Name {
		return out, httpx.Validation(httpx.FieldError{Path: "manifest.name", Message: fmt.Sprintf("Must stay %q; register a new integration to change it", before.Name)})
	}
	current, err := openConfig(ctx, tx, box, id)
	if err != nil {
		return out, err
	}
	declared := map[string]bool{}
	for _, f := range m.Config {
		declared[f.Key] = true
	}
	maps.DeleteFunc(current, func(k string, _ any) bool { return !declared[k] })
	cfg, err := normalizeConfig(ctx, tx, m.Config, merge(current, config))
	if err != nil {
		return out, err
	}
	if m.DisplayName == "" {
		m.DisplayName = m.Name
	}
	manifestJSON, _ := json.Marshal(m)
	cfgJSON, _ := json.Marshal(cfg)
	if _, err := tx.Exec(ctx, `UPDATE integrations SET display_name = $2, version = $3, description = $4, homepage = $5,
		scopes = $6, manifest = $7, config_encrypted = $8, updated_at = now() WHERE id = $1`,
		id, m.DisplayName, m.Version, m.Description, m.Homepage, m.Scopes, manifestJSON, box.Seal(cfgJSON)); err != nil {
		return out, err
	}
	var epID string
	err = tx.QueryRow(ctx, `SELECT id FROM webhook_endpoints WHERE integration_id = $1 ORDER BY id LIMIT 1`, id).Scan(&epID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	switch {
	case m.Webhooks == nil:
		if _, err := tx.Exec(ctx, `DELETE FROM webhook_endpoints WHERE integration_id = $1`, id); err != nil {
			return out, err
		}
	case epID != "":
		if _, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET url = $2, events = $3, updated_at = now() WHERE id = $1`,
			epID, m.Webhooks.URL, m.Webhooks.Events); err != nil {
			return out, err
		}
		out.WebhookEndpointID = epID
	default:
		ep, secret, err := insertEndpoint(ctx, tx, box, &id, EndpointInput{URL: m.Webhooks.URL, Events: m.Webhooks.Events})
		if err != nil {
			return out, err
		}
		out.WebhookEndpointID, out.WebhookSecret = ep.ID, secret
	}
	if out.Integration, err = Get(ctx, tx, id); err != nil {
		return out, err
	}
	return out, by.audit(ctx, tx, "integration.update_manifest", "integration", id,
		map[string]any{"version": before.Version, "scopes": before.Scopes},
		map[string]any{"version": m.Version, "scopes": m.Scopes})
}

func lock(ctx context.Context, tx pgx.Tx, id string) (Integration, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM integrations WHERE id = $1 FOR UPDATE`, id))
}

func openConfig(ctx context.Context, q db.Querier, box *secure.Box, id string) (map[string]any, error) {
	var sealed []byte
	if err := q.QueryRow(ctx, `SELECT config_encrypted FROM integrations WHERE id = $1`, id).Scan(&sealed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NotFound("Integration not found.")
		}
		return nil, err
	}
	cfg := map[string]any{}
	if len(sealed) == 0 {
		return cfg, nil
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return nil, err
	}
	return cfg, json.Unmarshal(plain, &cfg)
}

// Config returns an integration's config values, decrypted.
func Config(ctx context.Context, q db.Querier, box *secure.Box, id string) (map[string]any, error) {
	return openConfig(ctx, q, box, id)
}

// MaskedConfig returns config values with secrets replaced by "********".
func MaskedConfig(ctx context.Context, q db.Querier, box *secure.Box, id string) (map[string]any, error) {
	m, err := StoredManifest(ctx, q, id)
	if err != nil {
		return nil, err
	}
	cfg, err := openConfig(ctx, q, box, id)
	if err != nil {
		return nil, err
	}
	for _, f := range m.Config {
		if _, set := cfg[f.Key]; set && f.Type == "secret" {
			cfg[f.Key] = "********"
		}
	}
	return cfg, nil
}

// SetConfig changes config values: keys in values are set, and nil values
// are removed. Required fields must stay set.
func SetConfig(ctx context.Context, tx pgx.Tx, box *secure.Box, id string, values map[string]any, by By) error {
	if _, err := lock(ctx, tx, id); err != nil {
		return err
	}
	m, err := StoredManifest(ctx, tx, id)
	if err != nil {
		return err
	}
	current, err := openConfig(ctx, tx, box, id)
	if err != nil {
		return err
	}
	cfg, err := normalizeConfig(ctx, tx, m.Config, merge(current, values))
	if err != nil {
		return err
	}
	cfgJSON, _ := json.Marshal(cfg)
	if _, err := tx.Exec(ctx, `UPDATE integrations SET config_encrypted = $2, updated_at = now() WHERE id = $1`,
		id, box.Seal(cfgJSON)); err != nil {
		return err
	}
	return by.audit(ctx, tx, "integration.set_config", "integration", id, nil,
		map[string]any{"changedKeys": slices.Sorted(maps.Keys(values))})
}

func merge(current, updates map[string]any) map[string]any {
	out := maps.Clone(current)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range updates {
		if v == nil {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}

// normalizeConfig checks values against the declared fields and converts
// strings (from the CLI's --config key=value) to numbers, booleans and JSON.
func normalizeConfig(ctx context.Context, q db.Querier, fields []ConfigField, values map[string]any) (map[string]any, error) {
	out := map[string]any{}
	var problems []string
	byKey := map[string]ConfigField{}
	for _, f := range fields {
		byKey[f.Key] = f
	}
	for _, k := range slices.Sorted(maps.Keys(values)) {
		v := values[k]
		f, ok := byKey[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("config %q isn't declared in the manifest", k))
			continue
		}
		val, err := convert(ctx, q, f, v)
		if err != nil {
			problems = append(problems, fmt.Sprintf("config %q %s", k, err))
			continue
		}
		out[k] = val
	}
	for _, f := range fields {
		if _, given := values[f.Key]; f.Required && !given {
			problems = append(problems, fmt.Sprintf("config %q is required", f.Key))
		}
	}
	if len(problems) > 0 {
		return nil, &Invalid{Problems: problems}
	}
	return out, nil
}

func convert(ctx context.Context, q db.Querier, f ConfigField, v any) (any, error) {
	s, isString := v.(string)
	switch f.Type {
	case "string", "secret":
		if !isString {
			return nil, errors.New("must be a string")
		}
		return s, nil
	case "number":
		switch n := v.(type) {
		case float64:
			return n, nil
		case json.Number:
			return n.Float64()
		case string:
			x, err := strconv.ParseFloat(n, 64)
			if err != nil {
				return nil, errors.New("must be a number")
			}
			return x, nil
		}
		return nil, errors.New("must be a number")
	case "boolean":
		if b, ok := v.(bool); ok {
			return b, nil
		}
		if b, err := strconv.ParseBool(s); isString && err == nil {
			return b, nil
		}
		return nil, errors.New("must be true or false")
	case "json":
		if isString {
			var x any
			if json.Unmarshal([]byte(s), &x) == nil {
				return x, nil
			}
		}
		return v, nil
	case "select":
		if !isString || !slices.Contains(f.Options, s) {
			return nil, fmt.Errorf("must be one of %v", f.Options)
		}
		return s, nil
	case "location":
		if !isString {
			return nil, errors.New("must be a location ID")
		}
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM locations WHERE id = $1)`, s).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("must be the ID of an existing location")
		}
		return s, nil
	}
	return nil, fmt.Errorf("has unknown type %q", f.Type)
}

// SetStatus pauses ("paused") or resumes ("active") an integration. While
// paused its API keys are refused and its webhook deliveries wait.
func SetStatus(ctx context.Context, tx pgx.Tx, id, status string, by By) (Integration, error) {
	before, err := lock(ctx, tx, id)
	if err != nil {
		return before, err
	}
	if _, err := tx.Exec(ctx, `UPDATE integrations SET status = $2, updated_at = now() WHERE id = $1`, id, status); err != nil {
		return before, err
	}
	action := map[string]string{"paused": "integration.pause", "active": "integration.resume"}[status]
	if err := by.audit(ctx, tx, action, "integration", id, map[string]any{"status": before.Status}, map[string]any{"status": status}); err != nil {
		return before, err
	}
	return Get(ctx, tx, id)
}

// Rotated is a new API key; the old keys stop working at OldKeysExpireAt.
type Rotated struct {
	KeyID           string    `json:"keyId"`
	APIKey          string    `json:"apiKey" doc:"Shown only once"`
	OldKeysExpireAt time.Time `json:"oldKeysExpireAt" doc:"When the previous keys stop working"`
}

// MaxRotationGrace is the longest the previous keys may keep working.
const MaxRotationGrace = 7 * 24 * time.Hour

// RotateKey issues a new API key. The integration's other keys keep working
// for grace (0 revokes them at once), so it can switch without downtime.
func RotateKey(ctx context.Context, tx pgx.Tx, id string, grace time.Duration, now time.Time, by By) (Rotated, error) {
	var out Rotated
	i, err := lock(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if grace < 0 || grace > MaxRotationGrace {
		return out, httpx.Validation(httpx.FieldError{Path: "graceMinutes", Message: "Must be between 0 and 10080 (7 days)"})
	}
	out.OldKeysExpireAt = now.Add(grace).UTC()
	if grace == 0 {
		_, err = tx.Exec(ctx, `UPDATE api_keys SET revoked_at = $2 WHERE integration_id = $1 AND revoked_at IS NULL`, id, now)
	} else {
		_, err = tx.Exec(ctx, `UPDATE api_keys SET expires_at = least(coalesce(expires_at, $2), $2)
			WHERE integration_id = $1 AND revoked_at IS NULL`, id, out.OldKeysExpireAt)
	}
	if err != nil {
		return out, err
	}
	if out.KeyID, out.APIKey, err = issueKey(ctx, tx, id, i.DisplayName); err != nil {
		return out, err
	}
	return out, by.audit(ctx, tx, "integration.rotate_key", "integration", id, nil,
		map[string]any{"keyId": out.KeyID, "oldKeysExpireAt": out.OldKeysExpireAt})
}

// Remove deletes an integration with its keys, logs and webhook endpoints.
// Records it created stay; the audit log keeps its name.
func Remove(ctx context.Context, tx pgx.Tx, id string, by By) (Integration, error) {
	before, err := lock(ctx, tx, id)
	if err != nil {
		return before, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM integrations WHERE id = $1`, id); err != nil {
		return before, err
	}
	return before, by.audit(ctx, tx, "integration.remove", "integration", id,
		map[string]any{"name": before.Name, "scopes": before.Scopes}, nil)
}
