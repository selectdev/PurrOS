package account

import (
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/auth"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/secure"
)

const recoveryCodeCount = 10

type TOTPSetup struct {
	Secret     string `json:"secret" doc:"Base32 secret, for manual entry"`
	OTPAuthURL string `json:"otpauthUrl" doc:"Show as a QR code for authenticator apps"`
}

type RecoveryCodes struct {
	RecoveryCodes []string `json:"recoveryCodes" doc:"Shown once. Each works one time."`
}

type DisableMFAInput struct {
	Password string `json:"password,omitempty" validate:"max=256" doc:"Required when a password is set"`
	Code     string `json:"code" validate:"required,max=20" doc:"Authenticator or recovery code"`
}

// checkSecondFactor accepts a TOTP code (not reused) or an unused recovery code.
func checkSecondFactor(c *httpx.Ctx, tx pgx.Tx, userID, code string) (bool, string, error) {
	var sealed []byte
	var last *int64
	if err := tx.QueryRow(c, `SELECT totp_secret, totp_last_step FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&sealed, &last); err != nil {
		return false, "", err
	}
	if sealed != nil {
		secret, err := c.App.Box.Open(sealed)
		if err != nil {
			return false, "", err
		}
		lastStep := int64(0)
		if last != nil {
			lastStep = *last
		}
		if step, ok := auth.VerifyTOTP(string(secret), code, c.App.Now(), lastStep); ok {
			_, err := tx.Exec(c, `UPDATE users SET totp_last_step = $2 WHERE id = $1`, userID, step)
			return true, "totp", err
		}
	}
	tag, err := tx.Exec(c, `UPDATE recovery_codes SET used_at = now() WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
		userID, auth.HashRecoveryCode(code))
	if err != nil {
		return false, "", err
	}
	return tag.RowsAffected() == 1, "recovery_code", nil
}

func newRecoveryCodes(c *httpx.Ctx, tx pgx.Tx, userID string) ([]string, error) {
	if _, err := tx.Exec(c, `DELETE FROM recovery_codes WHERE user_id = $1`, userID); err != nil {
		return nil, err
	}
	codes := auth.NewRecoveryCodes(recoveryCodeCount)
	for _, code := range codes {
		if _, err := tx.Exec(c, `INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, auth.HashRecoveryCode(code)); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

func mfaRoutes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "POST", Path: "/auth/mfa/totp:setup", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf, AllowMFAEnroll: true,
			Summary:     "Start setting up an authenticator app",
			Description: "Returns a new secret. Two-factor authentication is on once POST /auth/mfa/totp:confirm succeeds.",
			Response:    TOTPSetup{},
			Handler: func(c *httpx.Ctx) (any, error) {
				u := c.Principal.User
				var on bool
				if err := c.App.Pool.QueryRow(c, `SELECT totp_enabled_at IS NOT NULL FROM users WHERE id = $1`, u.UserID).Scan(&on); err != nil {
					return nil, err
				}
				if on {
					return nil, httpx.Conflict("Two-factor authentication is already on. Turn it off first to change authenticator.")
				}
				secret := auth.NewTOTPSecret()
				if _, err := c.App.Pool.Exec(c, `UPDATE users SET totp_secret = $2, totp_last_step = NULL WHERE id = $1`,
					u.UserID, c.App.Box.Seal([]byte(secret))); err != nil {
					return nil, err
				}
				return TOTPSetup{Secret: secret, OTPAuthURL: auth.TOTPURL(companyName(c, c.App.Pool), u.Email, secret)}, nil
			},
		},
		{
			Method: "POST", Path: "/auth/mfa/totp:confirm", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf, AllowMFAEnroll: true,
			Summary: "Turn on two-factor authentication with a first code", Body: MFAInput{}, Response: RecoveryCodes{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in MFAInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if err := c.RateLimit("mfa:"+c.Principal.UserID, 10, time.Minute); err != nil {
					return nil, err
				}
				var out RecoveryCodes
				var fail error
				err := c.InTx(func(tx pgx.Tx) error {
					var sealed []byte
					var on bool
					if err := tx.QueryRow(c, `SELECT totp_secret, totp_enabled_at IS NOT NULL FROM users WHERE id = $1 FOR UPDATE`,
						c.Principal.UserID).Scan(&sealed, &on); err != nil {
						return err
					}
					if on {
						fail = httpx.Conflict("Two-factor authentication is already on.")
						return nil
					}
					if sealed == nil {
						fail = httpx.Conflict("Start with POST /auth/mfa/totp:setup.")
						return nil
					}
					secret, err := c.App.Box.Open(sealed)
					if err != nil {
						return err
					}
					step, ok := auth.VerifyTOTP(string(secret), in.Code, c.App.Now(), 0)
					if !ok {
						fail = httpx.Validation(httpx.FieldError{Path: "code", Message: "Incorrect code. Check your device's clock."})
						return nil
					}
					if _, err := tx.Exec(c, `UPDATE users SET totp_enabled_at = now(), totp_last_step = $2 WHERE id = $1`, c.Principal.UserID, step); err != nil {
						return err
					}
					if out.RecoveryCodes, err = newRecoveryCodes(c, tx, c.Principal.UserID); err != nil {
						return err
					}
					return audit(c, tx, "user.mfa_enabled", c.Principal.UserID, nil)
				})
				if err == nil {
					err = fail
				}
				return out, err
			},
		},
		{
			Method: "POST", Path: "/auth/mfa/totp:disable", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf,
			Summary: "Turn off two-factor authentication", Description: "Not allowed when your company or role requires it.",
			Body: DisableMFAInput{}, Status: 204,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in DisableMFAInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if err := c.RateLimit("mfa:"+c.Principal.UserID, 10, time.Minute); err != nil {
					return nil, err
				}
				var fail error
				err := c.InTx(func(tx pgx.Tx) error {
					var required bool
					if err := tx.QueryRow(c, `SELECT coalesce((SELECT mfa_required FROM company LIMIT 1), false) OR r.mfa_required
						FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = $1`, c.Principal.UserID).Scan(&required); err != nil {
						return err
					}
					if required {
						fail = httpx.Conflict("Your company requires two-factor authentication for your account.")
						return nil
					}
					a, err := loadAccount(c, tx, "id = $1", c.Principal.UserID)
					if err != nil {
						return err
					}
					if !a.TOTPOn {
						fail = httpx.Conflict("Two-factor authentication is already off.")
						return nil
					}
					if a.PasswordHash != nil {
						if ok, err := auth.VerifyPassword(*a.PasswordHash, in.Password); err != nil {
							return err
						} else if !ok {
							fail = httpx.Validation(httpx.FieldError{Path: "password", Message: "Incorrect password"})
							return nil
						}
					}
					ok, _, err := checkSecondFactor(c, tx, a.ID, in.Code)
					if err != nil {
						return err
					}
					if !ok {
						fail = httpx.Validation(httpx.FieldError{Path: "code", Message: "Incorrect code"})
						return nil
					}
					if _, err := tx.Exec(c, `UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE id = $1`, a.ID); err != nil {
						return err
					}
					if _, err := tx.Exec(c, `DELETE FROM recovery_codes WHERE user_id = $1`, a.ID); err != nil {
						return err
					}
					return audit(c, tx, "user.mfa_disabled", a.ID, nil)
				})
				if err == nil {
					err = fail
				}
				return httpx.Result{Status: 204}, err
			},
		},
		{
			Method: "POST", Path: "/auth/mfa/recovery-codes:regenerate", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf,
			Summary: "Replace your recovery codes", Body: MFAInput{}, Response: RecoveryCodes{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in MFAInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if err := c.RateLimit("mfa:"+c.Principal.UserID, 10, time.Minute); err != nil {
					return nil, err
				}
				var out RecoveryCodes
				var fail error
				err := c.InTx(func(tx pgx.Tx) error {
					ok, _, err := checkSecondFactor(c, tx, c.Principal.UserID, in.Code)
					if err != nil {
						return err
					}
					if !ok {
						fail = httpx.Validation(httpx.FieldError{Path: "code", Message: "Incorrect code"})
						return nil
					}
					if out.RecoveryCodes, err = newRecoveryCodes(c, tx, c.Principal.UserID); err != nil {
						return err
					}
					return audit(c, tx, "user.recovery_codes_regenerated", c.Principal.UserID, nil)
				})
				if err == nil {
					err = fail
				}
				return out, err
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Personal API keys
// ---------------------------------------------------------------------------

type PersonalKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix" doc:"First characters, to recognise the key"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type NewKeyInput struct {
	Name      string     `json:"name" validate:"required,max=100"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type NewKey struct {
	PersonalKey
	Key string `json:"key" doc:"Shown once"`
}

type keyList struct {
	Data []PersonalKey `json:"data"`
}

func requireKeysPermission(c *httpx.Ctx) error {
	if !c.Principal.User.Has("api_keys.personal") {
		return httpx.Forbidden("Your role doesn't have the api_keys.personal permission.")
	}
	return nil
}

func keyRoutes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "GET", Path: "/auth/api-keys", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf,
			Summary: "Your personal API keys", Response: keyList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT id, name, display_prefix, expires_at, last_used_at, created_at FROM api_keys
					WHERE user_id = $1 AND kind = 'personal' AND revoked_at IS NULL ORDER BY created_at DESC`, c.Principal.UserID)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PersonalKey, error) {
					var k PersonalKey
					return k, r.Scan(&k.ID, &k.Name, &k.Prefix, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt)
				})
				if list == nil {
					list = []PersonalKey{}
				}
				return keyList{Data: list}, err
			},
		},
		{
			Method: "POST", Path: "/auth/api-keys", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf,
			Summary:     "Create a personal API key",
			Description: "Needs api_keys.personal. The key acts with your own role and reach, and stops working if your account is deactivated.",
			Body:        NewKeyInput{}, Response: NewKey{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				if err := requireKeysPermission(c); err != nil {
					return nil, err
				}
				var in NewKeyInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if in.ExpiresAt != nil && !in.ExpiresAt.After(c.App.Now()) {
					return nil, httpx.Validation(httpx.FieldError{Path: "expiresAt", Message: "Must be in the future"})
				}
				plain, hash, display := secure.NewAPIKey()
				out := NewKey{Key: plain, PersonalKey: PersonalKey{ID: ids.New(ids.APIKey), Name: in.Name, Prefix: display, ExpiresAt: in.ExpiresAt}}
				err := c.InTx(func(tx pgx.Tx) error {
					if err := tx.QueryRow(c, `INSERT INTO api_keys (id, kind, display_prefix, hash, user_id, name, expires_at)
						VALUES ($1, 'personal', $2, $3, $4, $5, $6) RETURNING created_at`,
						out.ID, display, hash, c.Principal.UserID, in.Name, in.ExpiresAt).Scan(&out.CreatedAt); err != nil {
						return err
					}
					return audit(c, tx, "api_key.create", c.Principal.UserID, out.PersonalKey)
				})
				return out, err
			},
		},
		{
			Method: "DELETE", Path: "/auth/api-keys/{id}", Tag: tag, Auth: httpx.AuthSession, Permission: httpx.PermSelf,
			Summary: "Revoke one of your personal API keys", Status: 204,
			Handler: func(c *httpx.Ctx) (any, error) {
				err := c.InTx(func(tx pgx.Tx) error {
					tag, err := tx.Exec(c, `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND kind = 'personal' AND revoked_at IS NULL`,
						c.Param("id"), c.Principal.UserID)
					if err != nil {
						return err
					}
					if tag.RowsAffected() == 0 {
						return httpx.NotFound("API key not found.")
					}
					return audit(c, tx, "api_key.revoke", c.Principal.UserID, map[string]any{"id": c.Param("id")})
				})
				return httpx.Result{Status: 204}, err
			},
		},
	}
}
