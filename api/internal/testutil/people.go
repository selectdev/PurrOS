package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/selectdev/purros/api/internal/auth"
	"github.com/selectdev/purros/api/internal/ids"
)

// Origin is the origin the test server's PURROS_URL is set to.
const Origin = "http://test"

// UserSpec describes a test account.
type UserSpec struct {
	Email       string
	Password    string            // default "correct horse battery"
	Perms       map[string]string // permission → reach; a new role is created
	RoleID      string            // use an existing role instead of Perms
	EmployeeID  string
	LocationIDs []string
}

// DefaultPassword is used when UserSpec.Password is empty.
const DefaultPassword = "correct horse battery"

// CreateUser adds an active account with a password and returns its ID.
func (e *Env) CreateUser(s UserSpec) string {
	e.T.Helper()
	ctx := context.Background()
	if s.Password == "" {
		s.Password = DefaultPassword
	}
	if s.RoleID == "" {
		if s.Perms == nil {
			if err := e.Pool.QueryRow(ctx, `SELECT id FROM roles WHERE system_key = 'employee'`).Scan(&s.RoleID); err != nil {
				e.T.Fatal(err)
			}
		} else {
			s.RoleID = ids.New(ids.Role)
			if _, err := e.Pool.Exec(ctx, `INSERT INTO roles (id, name) VALUES ($1, $2)`, s.RoleID, "Role for "+s.Email); err != nil {
				e.T.Fatal(err)
			}
			for p, reach := range s.Perms {
				if _, err := e.Pool.Exec(ctx, `INSERT INTO role_permissions (role_id, permission, reach) VALUES ($1, $2, $3)`, s.RoleID, p, reach); err != nil {
					e.T.Fatal(err)
				}
			}
		}
	}
	id := ids.New(ids.User)
	var emp any
	if s.EmployeeID != "" {
		emp = s.EmployeeID
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO users (id, email, name, role_id, employee_id, status, password_hash)
		VALUES ($1, $2, $2, $3, $4, 'active', $5)`, id, s.Email, s.RoleID, emp, auth.HashPassword(s.Password)); err != nil {
		e.T.Fatal(err)
	}
	for _, l := range s.LocationIDs {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO user_location_assignments (user_id, location_id) VALUES ($1, $2)`, id, l); err != nil {
			e.T.Fatal(err)
		}
	}
	return id
}

// OwnerID returns the Owner account created by setup, activating it with
// the default password.
func (e *Env) OwnerID() string {
	e.T.Helper()
	var id string
	if err := e.Pool.QueryRow(context.Background(), `UPDATE users SET status = 'active', password_hash = $1
		WHERE email = 'owner@test' RETURNING id`, auth.HashPassword(DefaultPassword)).Scan(&id); err != nil {
		e.T.Fatal(err)
	}
	return id
}

// Session is a signed-in browser session.
type Session struct {
	env    *Env
	Cookie string
}

// SignIn signs in with a password and fails the test unless it works.
func (e *Env) SignIn(email, password string) *Session {
	e.T.Helper()
	if password == "" {
		password = DefaultPassword
	}
	s := &Session{env: e}
	r := s.Do("POST", "/api/v1/auth/sign-in", map[string]any{"email": email, "password": password}).Expect(e.T, 200)
	if r.Get("mfaRequired") == true {
		e.T.Fatalf("sign-in needs 2FA; use SignInStep")
	}
	return s
}

// Do sends a request with the session cookie and a same-origin Origin header.
// Set-Cookie responses update the session.
func (s *Session) Do(method, path string, body any, headers ...string) Response {
	s.env.T.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		r = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, s.env.Server.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", Origin)
	if s.Cookie != "" {
		req.Header.Set("Cookie", s.Cookie)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.env.T.Fatal(err)
	}
	defer resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == "purros_session" {
			if ck.MaxAge < 0 || ck.Value == "" {
				s.Cookie = ""
			} else {
				s.Cookie = ck.Name + "=" + ck.Value
			}
		}
	}
	raw, _ := io.ReadAll(resp.Body)
	out := Response{Status: resp.StatusCode, Header: resp.Header, Raw: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.Body)
	}
	return out
}

// Emails returns queued emails as (to, kind, body) for tests.
func (e *Env) Emails() []map[string]string {
	e.T.Helper()
	rows, err := e.Pool.Query(context.Background(), `SELECT to_address, kind, coalesce(body_text, '') FROM emails ORDER BY created_at, id`)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]string
	for rows.Next() {
		var to, kind, body string
		if err := rows.Scan(&to, &kind, &body); err != nil {
			e.T.Fatal(err)
		}
		out = append(out, map[string]string{"to": to, "kind": kind, "body": body})
	}
	return out
}

// NewSession returns a session that isn't signed in yet.
func (e *Env) NewSession() *Session { return &Session{env: e} }
