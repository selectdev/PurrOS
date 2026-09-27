package httpx

import (
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/secure"
)

func (a *App) authenticate(c *Ctx) (*Principal, *Problem) {
	h := c.Req.Header.Get("Authorization")
	token, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || !strings.HasPrefix(token, secure.APIKeyPrefix) {
		return nil, Unauthorized("Send an API key as: Authorization: Bearer pk_live_…")
	}

	var (
		p          Principal
		expiresAt  *time.Time
		revokedAt  *time.Time
		intStatus  *string
		intScopes  []string
		lastUsedAt *time.Time
	)
	err := a.Pool.QueryRow(c, `
		SELECT k.id, k.kind, coalesce(k.integration_id, ''), coalesce(k.user_id, ''),
		       k.expires_at, k.revoked_at, k.last_used_at,
		       coalesce(i.name, ''), coalesce(i.display_name, ''), i.status, coalesce(i.scopes, '{}')
		FROM api_keys k
		LEFT JOIN integrations i ON i.id = k.integration_id
		WHERE k.hash = $1`, secure.HashAPIKey(token)).
		Scan(&p.KeyID, &p.Kind, &p.IntegrationID, &p.UserID, &expiresAt, &revokedAt, &lastUsedAt,
			&p.IntegrationName, &p.IntegrationDisplayName, &intStatus, &intScopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, Unauthorized("Invalid API key.")
	}
	if err != nil {
		a.Log.Error("authenticate", "err", err)
		return nil, Internal()
	}
	now := a.Now()
	if revokedAt != nil {
		return nil, Unauthorized("This API key has been revoked.")
	}
	if expiresAt != nil && now.After(*expiresAt) {
		return nil, Unauthorized("This API key has expired.")
	}
	switch p.Kind {
	case "integration":
		if intStatus != nil && *intStatus == "paused" {
			return nil, Unauthorized("This integration is paused.")
		}
		p.Scopes = intScopes
	case "personal":
		// Personal keys act with the user's role; role-based checks for
		// personal keys arrive with the web UI. Until then they are read-only
		// on the platform core.
		p.Scopes = []string{"organization:read"}
	}

	// Record usage at most once a minute per key to avoid a write per request.
	if lastUsedAt == nil || now.Sub(*lastUsedAt) > time.Minute {
		_, err := a.Pool.Exec(c, `UPDATE api_keys SET last_used_at = $2, last_used_ip = $3 WHERE id = $1`,
			p.KeyID, now, c.ClientIP())
		if err != nil {
			a.Log.Warn("record key usage", "err", err)
		}
	}
	return &p, nil
}
