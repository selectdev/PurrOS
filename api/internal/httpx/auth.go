package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/secure"
)

// SessionCookie is the name of the session cookie.
const SessionCookie = "purros_session"

// Session lifetimes (docs/admin/authentication.md#sessions).
const (
	SessionIdle          = 12 * time.Hour
	SessionAbsolute      = 30 * 24 * time.Hour
	SharedDeviceIdle     = 15 * time.Minute
	sessionTouchInterval = time.Minute
)

// UserAccess is what a person may do: their role's permissions with reach,
// and the assignments reach is measured against.
type UserAccess struct {
	UserID     string
	Email      string
	Name       string
	RoleID     string
	RoleName   string
	EmployeeID string // "" when the account isn't linked to an employee
	Owner      bool
	// Perms maps permission → reach (own_team, assigned_locations,
	// assigned_departments or everyone).
	Perms map[string]string
	// LocationIDs are assigned locations, including every location under
	// assigned org units.
	LocationIDs   map[string]bool
	DepartmentIDs map[string]bool
	// MFAEnrollRequired: 2FA is mandatory for this user and not set up yet.
	MFAEnrollRequired bool
}

func (a *App) authenticate(c *Ctx) (*Principal, *Problem) {
	h := c.Req.Header.Get("Authorization")
	if token, ok := strings.CutPrefix(h, "Bearer "); ok {
		if !strings.HasPrefix(token, secure.APIKeyPrefix) {
			return nil, Unauthorized("Send an API key as: Authorization: Bearer pk_live_…")
		}
		return a.authenticateKey(c, token)
	}
	if ck, err := c.Req.Cookie(SessionCookie); err == nil && ck.Value != "" {
		return a.authenticateSession(c, ck.Value)
	}
	return nil, Unauthorized("Sign in, or send an API key as: Authorization: Bearer pk_live_…")
}

func (a *App) authenticateKey(c *Ctx, token string) (*Principal, *Problem) {
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
		// Personal keys act with the user's own role and reach.
		u, prob := a.loadUser(c, p.UserID)
		if prob != nil {
			return nil, prob
		}
		u.MFAEnrollRequired = false // enrolment happens in a browser session
		p.User = u
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

func (a *App) authenticateSession(c *Ctx, token string) (*Principal, *Problem) {
	var (
		p                 = Principal{Kind: "session"}
		lastSeen, expires time.Time
		revokedAt         *time.Time
		shared            bool
	)
	err := a.Pool.QueryRow(c, `SELECT id, user_id, mfa_pending, shared_device, last_seen_at, expires_at, revoked_at
		FROM sessions WHERE token_hash = $1`, secure.HashAPIKey(token)).
		Scan(&p.SessionID, &p.UserID, &p.MFAPending, &shared, &lastSeen, &expires, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, SessionExpired()
	}
	if err != nil {
		a.Log.Error("authenticate session", "err", err)
		return nil, Internal()
	}
	now := a.Now()
	idle := SessionIdle
	if shared {
		idle = SharedDeviceIdle
	}
	if revokedAt != nil || now.After(expires) || now.Sub(lastSeen) > idle {
		return nil, SessionExpired()
	}
	if prob := a.checkOrigin(c); prob != nil {
		return nil, prob
	}
	u, prob := a.loadUser(c, p.UserID)
	if prob != nil {
		return nil, prob
	}
	p.User = u
	if now.Sub(lastSeen) > sessionTouchInterval {
		if _, err := a.Pool.Exec(c, `UPDATE sessions SET last_seen_at = $2 WHERE id = $1`, p.SessionID, now); err != nil {
			a.Log.Warn("touch session", "err", err)
		}
	}
	return &p, nil
}

// checkOrigin guards cookie-authenticated requests against cross-site request
// forgery: state-changing requests must come from PurrOS's own origin.
func (a *App) checkOrigin(c *Ctx) *Problem {
	switch c.Req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	want, err := url.Parse(a.Config.URL)
	if err != nil {
		return Internal()
	}
	origin := c.Req.Header.Get("Origin")
	if origin == "" {
		if c.Req.Header.Get("Sec-Fetch-Site") == "same-origin" {
			return nil
		}
		return Forbidden("Cross-site request refused: send the Origin header.")
	}
	got, err := url.Parse(origin)
	if err != nil || got.Scheme != want.Scheme || got.Host != want.Host {
		return Forbidden("Cross-site request refused.")
	}
	return nil
}

// loadUser loads an active user's role, permissions and assignments.
func (a *App) loadUser(ctx context.Context, userID string) (*UserAccess, *Problem) {
	u, err := LoadUserAccess(ctx, a.Pool, userID)
	if errors.Is(err, errInactiveUser) {
		return nil, Unauthorized("This account is not active.")
	}
	if err != nil {
		a.Log.Error("load user access", "err", err)
		return nil, Internal()
	}
	return u, nil
}

var errInactiveUser = errors.New("user not active")

// LoadUserAccess resolves what an active user may do.
func LoadUserAccess(ctx context.Context, q db.Querier, userID string) (*UserAccess, error) {
	u := &UserAccess{UserID: userID, Perms: map[string]string{}, LocationIDs: map[string]bool{}, DepartmentIDs: map[string]bool{}}
	var status string
	var sysKey, empID *string
	var totpOn, companyMFA, roleMFA bool
	err := q.QueryRow(ctx, `SELECT u.email, u.name, u.status, u.employee_id, r.id, r.name, r.system_key,
			u.totp_enabled_at IS NOT NULL, coalesce((SELECT mfa_required FROM company LIMIT 1), false), r.mfa_required
		FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = $1`, userID).
		Scan(&u.Email, &u.Name, &status, &empID, &u.RoleID, &u.RoleName, &sysKey, &totpOn, &companyMFA, &roleMFA)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errInactiveUser
	}
	if err != nil {
		return nil, err
	}
	if status != "active" {
		return nil, errInactiveUser
	}
	if empID != nil {
		u.EmployeeID = *empID
	}
	u.Owner = sysKey != nil && *sysKey == "owner"
	u.MFAEnrollRequired = !totpOn && (companyMFA || roleMFA)

	rows, err := q.Query(ctx, `SELECT permission, reach FROM role_permissions WHERE role_id = $1`, u.RoleID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var perm, reach string
		if err := rows.Scan(&perm, &reach); err != nil {
			rows.Close()
			return nil, err
		}
		u.Perms[perm] = reach
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `
		WITH RECURSIVE units AS (
			SELECT org_unit_id AS id FROM user_location_assignments WHERE user_id = $1 AND org_unit_id IS NOT NULL
			UNION SELECT o.id FROM org_units o JOIN units ON o.parent_id = units.id
		)
		SELECT location_id FROM user_location_assignments WHERE user_id = $1 AND location_id IS NOT NULL
		UNION SELECT l.id FROM locations l JOIN units ON l.org_unit_id = units.id
		UNION SELECT '#' || department_id FROM user_department_assignments WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if dep, ok := strings.CutPrefix(id, "#"); ok {
			u.DepartmentIDs[dep] = true
		} else {
			u.LocationIDs[id] = true
		}
	}
	return u, rows.Err()
}
