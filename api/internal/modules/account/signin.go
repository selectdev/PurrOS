package account

import (
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/auth"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/mail"
)

type SignInInput struct {
	Email        string `json:"email" validate:"required,max=320"`
	Password     string `json:"password" validate:"required,max=256"`
	SharedDevice bool   `json:"sharedDevice,omitempty" doc:"Short idle timeout and a cookie that isn't remembered"`
}

type SignInResult struct {
	MFARequired bool `json:"mfaRequired" doc:"Send a code to POST /auth/sign-in/mfa to finish signing in"`
}

type MFAInput struct {
	Code string `json:"code" validate:"required,max=20" doc:"6-digit authenticator code or a recovery code"`
}

type EmailInput struct {
	Email string `json:"email" validate:"required,max=320"`
}

type TokenInput struct {
	Token        string `json:"token" validate:"required,max=100"`
	SharedDevice bool   `json:"sharedDevice,omitempty"`
}

type TokenPasswordInput struct {
	Token    string `json:"token" validate:"required,max=100"`
	Password string `json:"password" validate:"required,max=256"`
}

type ChangePasswordInput struct {
	CurrentPassword string `json:"currentPassword,omitempty" validate:"max=256" doc:"Required when a password is set"`
	NewPassword     string `json:"newPassword" validate:"required,max=256"`
}

type Accepted struct {
	Message string `json:"message"`
}

// SessionInfo describes the signed-in person.
type SessionInfo struct {
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"user"`
	Role struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Owner bool   `json:"owner"`
	} `json:"role"`
	EmployeeID    *string           `json:"employeeId"`
	Permissions   map[string]string `json:"permissions" doc:"Permission → reach. Owners have every permission."`
	LocationIDs   []string          `json:"locationIds" doc:"Assigned locations, including those under assigned org units"`
	DepartmentIDs []string          `json:"departmentIds"`
	Features      []string          `json:"features" doc:"Enabled features"`
	MFA           struct {
		Enabled            bool `json:"enabled"`
		Pending            bool `json:"pending" doc:"The second sign-in step is still due"`
		EnrollmentRequired bool `json:"enrollmentRequired"`
	} `json:"mfa"`
}

type SessionEntry struct {
	ID         string    `json:"id"`
	Current    bool      `json:"current"`
	Method     string    `json:"method"`
	IP         *string   `json:"ip"`
	UserAgent  *string   `json:"userAgent"`
	Shared     bool      `json:"sharedDevice"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type sessionList struct {
	Data []SessionEntry `json:"data"`
}

var badCredentials = httpx.Unauthorized("Incorrect email or password.")

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sessionInfo(c *httpx.Ctx) (SessionInfo, error) {
	p := c.Principal
	u := p.User
	var s SessionInfo
	s.User.ID, s.User.Email, s.User.Name = u.UserID, u.Email, u.Name
	s.Role.ID, s.Role.Name, s.Role.Owner = u.RoleID, u.RoleName, u.Owner
	if u.EmployeeID != "" {
		s.EmployeeID = &u.EmployeeID
	}
	s.Permissions = u.Perms
	s.LocationIDs, s.DepartmentIDs = keys(u.LocationIDs), keys(u.DepartmentIDs)
	feats, err := c.App.Features.Enabled(c)
	if err != nil {
		return s, err
	}
	s.Features = feats
	if err := c.App.Pool.QueryRow(c, `SELECT totp_enabled_at IS NOT NULL FROM users WHERE id = $1`, u.UserID).Scan(&s.MFA.Enabled); err != nil {
		return s, err
	}
	s.MFA.Pending, s.MFA.EnrollmentRequired = p.MFAPending, u.MFAEnrollRequired
	return s, nil
}

// requestLink sends a magic link or password reset email. The response is the
// same whether or not the account exists.
func requestLink(c *httpx.Ctx, purpose, path string) (any, error) {
	var in EmailInput
	if err := c.Decode(&in); err != nil {
		return nil, err
	}
	email := normalizeEmail(in.Email)
	if err := c.RateLimit("link:ip:"+c.ClientIP(), 10, time.Minute); err != nil {
		return nil, err
	}
	if err := c.RateLimit("link:acct:"+email, 3, 15*time.Minute); err != nil {
		return nil, err
	}
	if !c.App.Config.SMTP.Enabled() {
		return nil, httpx.Conflict("Email isn't set up on this PurrOS server, so sign-in links can't be sent. Ask an administrator.")
	}
	if purpose == "magic_link" {
		var on bool
		if err := c.App.Pool.QueryRow(c, `SELECT magic_link_enabled FROM company LIMIT 1`).Scan(&on); err != nil {
			return nil, err
		}
		if !on {
			return nil, httpx.Forbidden("Magic-link sign-in is turned off.")
		}
	}
	err := c.InTx(func(tx pgx.Tx) error {
		a, err := loadAccount(c, tx, "lower(email) = $1", email)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && a.Status != "active") {
			return nil
		}
		if err != nil {
			return err
		}
		token, err := issueToken(c, tx, a.ID, purpose)
		if err != nil {
			return err
		}
		if _, err := mail.Queue(c, tx, emailFor(c, tx, purpose, a.Email, a.Name, link(c, path, token))); err != nil {
			return err
		}
		return audit(c, tx, "user."+purpose+"_requested", a.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	return httpx.Result{Status: 202, Body: Accepted{Message: "If an account exists for this email, a link is on its way."}}, nil
}

// finishWithToken redeems a link and signs the user in.
func finishWithToken(c *httpx.Ctx, token, purpose, method, password string, shared bool) (any, error) {
	var cookie string
	var mfa bool
	err := c.InTx(func(tx pgx.Tx) error {
		userID, err := redeemToken(c, tx, token, purpose)
		if err != nil {
			return err
		}
		a, err := loadAccount(c, tx, "id = $1", userID)
		if err != nil {
			return err
		}
		switch {
		case purpose == "invitation" && a.Status != "invited" && a.Status != "active":
			return errBadToken
		case purpose != "invitation" && a.Status != "active":
			return errBadToken
		}
		if password != "" {
			if msg := auth.CheckPasswordPolicy(password, a.Email); msg != "" {
				return httpx.Validation(httpx.FieldError{Path: "password", Message: msg})
			}
			if _, err := tx.Exec(c, `UPDATE users SET password_hash = $2, password_changed_at = now() WHERE id = $1`,
				a.ID, auth.HashPassword(password)); err != nil {
				return err
			}
			if err := revokeSessions(c, tx, a.ID, ""); err != nil {
				return err
			}
		}
		if purpose == "invitation" {
			if _, err := tx.Exec(c, `UPDATE users SET status = 'active', updated_at = now() WHERE id = $1`, a.ID); err != nil {
				return err
			}
		}
		mfa = a.TOTPOn
		if cookie, err = startSession(c, tx, a.ID, method, mfa, shared); err != nil {
			return err
		}
		return audit(c, tx, "user.sign_in", a.ID, map[string]any{"method": method, "mfaPending": mfa})
	})
	if err != nil {
		return nil, err
	}
	return httpx.Result{Body: SignInResult{MFARequired: mfa}, Headers: map[string]string{"Set-Cookie": cookie}}, nil
}

func signInRoutes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "POST", Path: "/auth/sign-in", Tag: tag, Auth: httpx.AuthNone,
			Summary:     "Sign in with email and password",
			Description: "Sets the session cookie. When the account has two-factor authentication, mfaRequired is true and the session can only call POST /auth/sign-in/mfa until the second step is done. Repeated failures lock the account for 15 minutes.",
			Body:        SignInInput{}, Response: SignInResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in SignInInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				email := normalizeEmail(in.Email)
				if err := c.RateLimit("signin:ip:"+c.ClientIP(), 30, time.Minute); err != nil {
					return nil, err
				}
				if err := c.RateLimit("signin:acct:"+email, 20, time.Minute); err != nil {
					return nil, err
				}
				var cookie string
				var mfa bool
				var fail error
				err := c.InTx(func(tx pgx.Tx) error {
					a, err := loadAccount(c, tx, "lower(email) = $1", email)
					if errors.Is(err, pgx.ErrNoRows) {
						auth.BurnTime(in.Password)
						fail = badCredentials
						return nil
					}
					if err != nil {
						return err
					}
					now := c.App.Now()
					if a.LockedUntil != nil && now.Before(*a.LockedUntil) {
						fail = httpx.Unauthorized("Too many failed attempts. Try again after " + a.LockedUntil.UTC().Format("15:04 UTC") + ", or reset your password.")
						return nil
					}
					ok := false
					if a.PasswordHash != nil && a.Status == "active" {
						if ok, err = auth.VerifyPassword(*a.PasswordHash, in.Password); err != nil {
							return err
						}
					} else {
						auth.BurnTime(in.Password)
					}
					if !ok {
						fail = badCredentials
						failed := a.Failed + 1
						var lockedUntil *time.Time
						if failed >= lockAfterFailures {
							t := now.Add(lockFor)
							lockedUntil, failed = &t, 0
						}
						if _, err := tx.Exec(c, `UPDATE users SET failed_sign_ins = $2, locked_until = $3 WHERE id = $1`, a.ID, failed, lockedUntil); err != nil {
							return err
						}
						action := "user.sign_in_failed"
						if lockedUntil != nil {
							action = "user.locked"
						}
						return audit(c, tx, action, a.ID, map[string]any{"ip": c.ClientIP()})
					}
					mfa = a.TOTPOn
					if cookie, err = startSession(c, tx, a.ID, "password", mfa, in.SharedDevice); err != nil {
						return err
					}
					return audit(c, tx, "user.sign_in", a.ID, map[string]any{"method": "password", "mfaPending": mfa})
				})
				if err != nil {
					return nil, err
				}
				if fail != nil {
					return nil, fail
				}
				return httpx.Result{Body: SignInResult{MFARequired: mfa}, Headers: map[string]string{"Set-Cookie": cookie}}, nil
			},
		},
		{
			Method: "POST", Path: "/auth/sign-in/mfa", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf, AllowMFAPending: true,
			Summary: "Finish signing in with an authenticator or recovery code", Body: MFAInput{}, Response: SignInResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in MFAInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if !c.Principal.MFAPending {
					return nil, httpx.Conflict("This session has already completed sign-in.")
				}
				if err := c.RateLimit("mfa:"+c.Principal.UserID, 10, time.Minute); err != nil {
					return nil, err
				}
				var cookie string
				var fail error
				err := c.InTx(func(tx pgx.Tx) error {
					ok, how, err := checkSecondFactor(c, tx, c.Principal.UserID, in.Code)
					if err != nil {
						return err
					}
					if !ok {
						// Wrong codes count towards the lockout; at the limit the
						// half-signed-in session ends too.
						fail = httpx.Validation(httpx.FieldError{Path: "code", Message: "Incorrect code"})
						var failed int
						if err := tx.QueryRow(c, `UPDATE users SET failed_sign_ins = failed_sign_ins + 1 WHERE id = $1
							RETURNING failed_sign_ins`, c.Principal.UserID).Scan(&failed); err != nil {
							return err
						}
						if failed >= lockAfterFailures {
							fail = httpx.Unauthorized("Too many incorrect codes. Sign in again later.")
							if _, err := tx.Exec(c, `UPDATE users SET failed_sign_ins = 0, locked_until = $2 WHERE id = $1`,
								c.Principal.UserID, c.App.Now().Add(lockFor)); err != nil {
								return err
							}
							if _, err := tx.Exec(c, `UPDATE sessions SET revoked_at = now() WHERE id = $1`, c.Principal.SessionID); err != nil {
								return err
							}
						}
						return audit(c, tx, "user.mfa_failed", c.Principal.UserID, nil)
					}
					// A new session token after the privilege change.
					if _, err := tx.Exec(c, `UPDATE sessions SET revoked_at = now() WHERE id = $1`, c.Principal.SessionID); err != nil {
						return err
					}
					var shared bool
					var method string
					if err := tx.QueryRow(c, `SELECT shared_device, method FROM sessions WHERE id = $1`, c.Principal.SessionID).Scan(&shared, &method); err != nil {
						return err
					}
					if cookie, err = startSession(c, tx, c.Principal.UserID, method, false, shared); err != nil {
						return err
					}
					return audit(c, tx, "user.mfa_verified", c.Principal.UserID, map[string]any{"with": how})
				})
				if err != nil {
					return nil, err
				}
				if fail != nil {
					return nil, fail
				}
				return httpx.Result{Body: SignInResult{}, Headers: map[string]string{"Set-Cookie": cookie}}, nil
			},
		},
		{
			Method: "POST", Path: "/auth/sign-out", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf,
			AllowMFAPending: true, Summary: "Sign out this session", Status: 204,
			Handler: func(c *httpx.Ctx) (any, error) {
				if _, err := c.App.Pool.Exec(c, `UPDATE sessions SET revoked_at = now() WHERE id = $1`, c.Principal.SessionID); err != nil {
					return nil, err
				}
				return httpx.Result{Status: 204, Headers: map[string]string{"Set-Cookie": clearCookie(c)}}, nil
			},
		},
		{
			Method: "GET", Path: "/auth/session", Tag: tag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			AllowMFAPending: true, AllowMFAEnroll: true,
			Summary: "The signed-in person: role, permissions, assignments and enabled features", Response: SessionInfo{},
			Handler: func(c *httpx.Ctx) (any, error) { return sessionInfo(c) },
		},
		{
			Method: "POST", Path: "/auth/magic-link", Tag: tag, Auth: httpx.AuthNone,
			Summary: "Email a single-use sign-in link", Description: "Needs email (SMTP). Always answers 202, whether or not the account exists.",
			Body: EmailInput{}, Response: Accepted{}, Status: 202,
			Handler: func(c *httpx.Ctx) (any, error) { return requestLink(c, "magic_link", "/sign-in/link") },
		},
		{
			Method: "POST", Path: "/auth/magic-link:redeem", Tag: tag, Auth: httpx.AuthNone,
			Summary: "Sign in with a magic link", Body: TokenInput{}, Response: SignInResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in TokenInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				return finishWithToken(c, in.Token, "magic_link", "magic_link", "", in.SharedDevice)
			},
		},
		{
			Method: "POST", Path: "/auth/password-reset", Tag: tag, Auth: httpx.AuthNone,
			Summary: "Email a password reset link", Description: "Always answers 202, whether or not the account exists.",
			Body: EmailInput{}, Response: Accepted{}, Status: 202,
			Handler: func(c *httpx.Ctx) (any, error) { return requestLink(c, "password_reset", "/reset-password") },
		},
		{
			Method: "POST", Path: "/auth/password-reset:complete", Tag: tag, Auth: httpx.AuthNone,
			Summary: "Choose a new password with a reset link", Description: "Signs out every other session and signs in.",
			Body: TokenPasswordInput{}, Response: SignInResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in TokenPasswordInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				return finishWithToken(c, in.Token, "password_reset", "password_reset", in.Password, false)
			},
		},
		{
			Method: "POST", Path: "/auth/invitations:accept", Tag: tag, Auth: httpx.AuthNone,
			Summary: "Accept an invitation and set a password", Body: TokenPasswordInput{}, Response: SignInResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in TokenPasswordInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				return finishWithToken(c, in.Token, "invitation", "invitation", in.Password, false)
			},
		},
		{
			Method: "POST", Path: "/auth/password", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf,
			Summary: "Change your password", Description: "Signs out your other sessions.",
			Body: ChangePasswordInput{}, Status: 204,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in ChangePasswordInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if err := c.RateLimit("password:"+c.Principal.UserID, 5, time.Minute); err != nil {
					return nil, err
				}
				var fail error
				err := c.InTx(func(tx pgx.Tx) error {
					a, err := loadAccount(c, tx, "id = $1", c.Principal.UserID)
					if err != nil {
						return err
					}
					if a.PasswordHash != nil {
						ok, err := auth.VerifyPassword(*a.PasswordHash, in.CurrentPassword)
						if err != nil {
							return err
						}
						if !ok {
							fail = httpx.Validation(httpx.FieldError{Path: "currentPassword", Message: "Incorrect password"})
							return nil
						}
					}
					if msg := auth.CheckPasswordPolicy(in.NewPassword, a.Email); msg != "" {
						fail = httpx.Validation(httpx.FieldError{Path: "newPassword", Message: msg})
						return nil
					}
					if _, err := tx.Exec(c, `UPDATE users SET password_hash = $2, password_changed_at = now() WHERE id = $1`,
						a.ID, auth.HashPassword(in.NewPassword)); err != nil {
						return err
					}
					if err := revokeSessions(c, tx, a.ID, c.Principal.SessionID); err != nil {
						return err
					}
					return audit(c, tx, "user.password_changed", a.ID, nil)
				})
				if err == nil {
					err = fail
				}
				return httpx.Result{Status: 204}, err
			},
		},
		{
			Method: "GET", Path: "/auth/sessions", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf,
			Summary: "Your active sessions and devices", Response: sessionList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT id, method, ip, user_agent, shared_device, created_at, last_seen_at
					FROM sessions WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now() ORDER BY last_seen_at DESC`, c.Principal.UserID)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (SessionEntry, error) {
					var s SessionEntry
					err := r.Scan(&s.ID, &s.Method, &s.IP, &s.UserAgent, &s.Shared, &s.CreatedAt, &s.LastSeenAt)
					s.Current = s.ID == c.Principal.SessionID
					return s, err
				})
				if list == nil {
					list = []SessionEntry{}
				}
				return sessionList{Data: list}, err
			},
		},
		{
			Method: "DELETE", Path: "/auth/sessions/{id}", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf,
			Summary: "Sign out one of your sessions", Status: 204,
			Handler: func(c *httpx.Ctx) (any, error) {
				tag, err := c.App.Pool.Exec(c, `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
					c.Param("id"), c.Principal.UserID)
				if err != nil {
					return nil, err
				}
				if tag.RowsAffected() == 0 {
					return nil, httpx.NotFound("Session not found.")
				}
				return httpx.Result{Status: 204}, nil
			},
		},
	}
}
