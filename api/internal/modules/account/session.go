// Package account serves sign-in for people (passwords, magic links,
// two-factor authentication, sessions), personal API keys, and the
// management of user accounts and roles.
package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/auth"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/mail"
	"github.com/selectdev/purros/api/internal/secure"
)

const tag = "Authentication"

// Lifetimes of single-use links.
var tokenTTL = map[string]time.Duration{
	"invitation":     7 * 24 * time.Hour,
	"magic_link":     15 * time.Minute,
	"password_reset": time.Hour,
}

// Sign-in protection.
const (
	lockAfterFailures = 10
	lockFor           = 15 * time.Minute
)

// sessionCookie builds the Set-Cookie header value.
func sessionCookie(c *httpx.Ctx, token string, maxAge int) string {
	ck := &http.Cookie{
		Name: httpx.SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(c.App.Config.URL, "https://"), MaxAge: maxAge,
	}
	return ck.String()
}

// startSession creates a session and returns the Set-Cookie header.
func startSession(c *httpx.Ctx, tx pgx.Tx, userID, method string, mfaPending, shared bool) (string, error) {
	token := secure.RandomToken(43)
	now := c.App.Now()
	_, err := tx.Exec(c, `INSERT INTO sessions (id, token_hash, user_id, method, mfa_pending, shared_device, ip, user_agent,
		created_at, last_seen_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10)`,
		ids.New(ids.Session), secure.HashAPIKey(token), userID, method, mfaPending, shared, c.ClientIP(),
		truncate(c.Req.UserAgent(), 300), now, now.Add(httpx.SessionAbsolute))
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(c, `UPDATE users SET last_sign_in_at = $2, failed_sign_ins = 0, locked_until = NULL WHERE id = $1`, userID, now); err != nil {
		return "", err
	}
	maxAge := int(httpx.SessionAbsolute.Seconds())
	if shared {
		maxAge = 0 // a browser-session cookie: never remembered on shared devices
	}
	return sessionCookie(c, token, maxAge), nil
}

func clearCookie(c *httpx.Ctx) string { return sessionCookie(c, "", -1) }

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// account is a user row as sign-in needs it.
type account struct {
	ID           string
	Email        string
	Name         string
	Status       string
	PasswordHash *string
	LockedUntil  *time.Time
	TOTPOn       bool
	Failed       int
}

func loadAccount(ctx context.Context, q db.Querier, where string, arg any) (account, error) {
	var a account
	err := q.QueryRow(ctx, `SELECT id, email, name, status, password_hash, locked_until, totp_enabled_at IS NOT NULL, failed_sign_ins
		FROM users WHERE `+where+` FOR UPDATE`, arg).
		Scan(&a.ID, &a.Email, &a.Name, &a.Status, &a.PasswordHash, &a.LockedUntil, &a.TOTPOn, &a.Failed)
	return a, err
}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// issueToken creates a single-use link token, replacing unused ones of the
// same purpose.
func issueToken(c *httpx.Ctx, tx pgx.Tx, userID, purpose string) (string, error) {
	if _, err := tx.Exec(c, `DELETE FROM auth_tokens WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`, userID, purpose); err != nil {
		return "", err
	}
	token, hash := auth.NewToken()
	_, err := tx.Exec(c, `INSERT INTO auth_tokens (id, token_hash, user_id, purpose, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		ids.New(ids.AuthToken), hash, userID, purpose, c.App.Now().Add(tokenTTL[purpose]))
	return token, err
}

var errBadToken = httpx.Validation(httpx.FieldError{Path: "token", Message: "This link is invalid, expired or already used"})

// redeemToken marks a token used and returns its user.
func redeemToken(c *httpx.Ctx, tx pgx.Tx, token, purpose string) (string, error) {
	var id, userID string
	var expires time.Time
	var used *time.Time
	err := tx.QueryRow(c, `SELECT id, user_id, expires_at, used_at FROM auth_tokens WHERE token_hash = $1 AND purpose = $2 FOR UPDATE`,
		auth.HashToken(token), purpose).Scan(&id, &userID, &expires, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errBadToken
	}
	if err != nil {
		return "", err
	}
	if used != nil || c.App.Now().After(expires) {
		return "", errBadToken
	}
	_, err = tx.Exec(c, `UPDATE auth_tokens SET used_at = now() WHERE id = $1`, id)
	return userID, err
}

// Links in emails point at the web app.
func link(c *httpx.Ctx, path, token string) string {
	return c.App.Config.URL + path + "?token=" + token
}

func companyName(c *httpx.Ctx, q db.Querier) string {
	var name string
	_ = q.QueryRow(c, `SELECT name FROM company LIMIT 1`).Scan(&name)
	if name == "" {
		name = "PurrOS"
	}
	return name
}

// emailFor renders a sign-in email.
func emailFor(c *httpx.Ctx, q db.Querier, kind, to, name, url string) mail.Message {
	company := companyName(c, q)
	var subject, body string
	switch kind {
	case "invitation":
		subject = "You're invited to " + company + " on PurrOS"
		body = fmt.Sprintf("Hi %s,\n\n%s has invited you to PurrOS.\n\nSet up your sign-in here (the link works for 7 days):\n%s\n", name, company, url)
	case "magic_link":
		subject = "Your sign-in link for " + company
		body = fmt.Sprintf("Hi %s,\n\nUse this link to sign in to %s. It works once, for 15 minutes:\n%s\n\nIf you didn't ask for it, you can ignore this email.\n", name, company, url)
	case "password_reset":
		subject = "Reset your " + company + " password"
		body = fmt.Sprintf("Hi %s,\n\nUse this link to choose a new password. It works once, for 1 hour:\n%s\n\nIf you didn't ask for it, you can ignore this email; your password hasn't changed.\n", name, url)
	}
	return mail.Message{To: to, Subject: subject, Kind: kind, Body: body}
}

// revokeSessions ends a user's sessions, except keep (may be "").
func revokeSessions(c *httpx.Ctx, tx pgx.Tx, userID, keep string) error {
	_, err := tx.Exec(c, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL AND id <> $2`, userID, keep)
	return err
}

// audit records an authentication event (no webhook). Before sign-in
// completes, the user themselves is the actor.
func audit(c *httpx.Ctx, q db.Querier, action, userID string, after any) error {
	if c.Principal != nil {
		return c.Record(q, httpx.Change{Action: action, EntityType: "user", EntityID: userID, After: after})
	}
	return events.Audit(c, q, events.AuditEntry{Actor: events.Actor{Type: "user", ID: userID}, Action: action,
		EntityType: "user", EntityID: userID, After: after, IP: c.ClientIP(), RequestID: c.RequestID})
}
