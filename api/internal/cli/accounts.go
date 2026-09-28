package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/config"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/spf13/cobra"
)

var cliActor = events.Actor{Type: "system", Name: "cli"}

type userRow struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	MFA         bool       `json:"mfaEnabled"`
	LockedUntil *time.Time `json:"lockedUntil"`
	LastSignIn  *time.Time `json:"lastSignInAt"`
	EmployeeID  *string    `json:"employeeId"`
}

const userSelect = `SELECT u.id, u.email, u.name, r.name, u.status, u.totp_enabled_at IS NOT NULL, u.locked_until, u.last_sign_in_at, u.employee_id
	FROM users u JOIN roles r ON r.id = u.role_id`

func scanUserRow(r pgx.Row) (userRow, error) {
	var u userRow
	err := r.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Status, &u.MFA, &u.LockedUntil, &u.LastSignIn, &u.EmployeeID)
	return u, err
}

func findUser(ctx context.Context, q db.Querier, email string) (userRow, error) {
	u, err := scanUserRow(q.QueryRow(ctx, userSelect+` WHERE lower(u.email) = lower($1)`, strings.TrimSpace(email)))
	if errors.Is(err, pgx.ErrNoRows) {
		return u, fmt.Errorf("no account with email %q (see `purros users list`)", email)
	}
	return u, err
}

// findRole resolves a role by ID, name (case-insensitive) or "owner"/"employee".
func findRole(ctx context.Context, q db.Querier, ref string) (id, name string, owner bool, err error) {
	var sys *string
	err = q.QueryRow(ctx, `SELECT id, name, system_key FROM roles WHERE id = $1 OR lower(name) = lower($1) OR system_key = lower($1)
		ORDER BY (id = $1) DESC LIMIT 1`, ref).Scan(&id, &name, &sys)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, fmt.Errorf("no role %q (see `purros roles list`)", ref)
	}
	return id, name, sys != nil && *sys == "owner", err
}

// otherOwners counts other Owners who can sign in (or accept their invitation).
func otherOwners(ctx context.Context, q db.Querier, userID string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM users u JOIN roles r ON r.id = u.role_id
		WHERE r.system_key = 'owner' AND u.status IN ('active', 'invited') AND u.id <> $1`, userID).Scan(&n)
	return n, err
}

func isOwnerUser(ctx context.Context, q db.Querier, userID string) (bool, error) {
	var owner bool
	err := q.QueryRow(ctx, `SELECT r.system_key IS NOT DISTINCT FROM 'owner' FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = $1`, userID).Scan(&owner)
	return owner, err
}

func revokeAll(ctx context.Context, q db.Querier, userID string) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return tag.RowsAffected(), err
}

func clearMFA(ctx context.Context, q db.Querier, userID string) error {
	if _, err := q.Exec(ctx, `UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE id = $1`, userID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = $1`, userID)
	return err
}

func audit(ctx context.Context, q db.Querier, action, userID string, after any) error {
	return events.Audit(ctx, q, events.AuditEntry{Actor: cliActor, Action: action, EntityType: "user", EntityID: userID, After: after})
}

// emailArg accepts the email as an argument or --email.
func emailArg(cmd *cobra.Command, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	if e, _ := cmd.Flags().GetString("email"); e != "" {
		return e, nil
	}
	return "", errors.New("give the account's email, e.g. `" + cmd.CommandPath() + " owner@example.com`")
}

func (a *app) userCommand(use, short string, run func(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, u userRow) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use + " <email>",
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			email, err := emailArg(cmd, args)
			if err != nil {
				return err
			}
			return withPool(cmd.Context(), false, func(cfg config.Config, pool *pgxpool.Pool) error {
				u, err := findUser(cmd.Context(), pool, email)
				if err != nil {
					return err
				}
				return run(cmd.Context(), pool, cfg, u)
			})
		},
	}
	cmd.Flags().String("email", "", "the account's email (or give it as an argument)")
	return cmd
}

func (a *app) usersCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "users", Short: "List and manage accounts, and recover access"}

	var status, role string
	list := &cobra.Command{
		Use:   "list",
		Short: "List accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				rows, err := pool.Query(cmd.Context(), userSelect+` WHERE ($1 = '' OR u.status = $1) AND ($2 = '' OR lower(r.name) = lower($2))
					ORDER BY u.email`, status, role)
				if err != nil {
					return err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (userRow, error) { return scanUserRow(r) })
				if err != nil {
					return err
				}
				return a.emit(list, func() {
					var out [][]string
					for _, u := range list {
						flags := []string{}
						if u.MFA {
							flags = append(flags, "2fa")
						}
						if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
							flags = append(flags, "locked")
						}
						if u.EmployeeID != nil {
							flags = append(flags, "employee")
						}
						last := "never"
						if u.LastSignIn != nil {
							last = u.LastSignIn.Local().Format("2006-01-02 15:04")
						}
						out = append(out, []string{u.Email, u.Name, u.Role, u.Status, strings.Join(flags, ","), last})
					}
					a.table("EMAIL\tNAME\tROLE\tSTATUS\tFLAGS\tLAST SIGN-IN", out)
				})
			})
		},
	}
	list.Flags().StringVar(&status, "status", "", "invited, active or deactivated")
	list.Flags().StringVar(&role, "role", "", "role name")
	cmd.AddCommand(list)

	cmd.AddCommand(a.userCommand("show", "Show one account", func(ctx context.Context, pool *pgxpool.Pool, _ config.Config, u userRow) error {
		var sessions int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`, u.ID).Scan(&sessions)
		var keys int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE user_id = $1 AND revoked_at IS NULL`, u.ID).Scan(&keys)
		return a.emit(map[string]any{"user": u, "activeSessions": sessions, "personalKeys": keys}, func() {
			locked := "no"
			if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
				locked = "until " + u.LockedUntil.Local().Format("15:04")
			}
			a.table("", [][]string{{"Email", u.Email}, {"Name", u.Name}, {"ID", u.ID}, {"Role", u.Role}, {"Status", u.Status},
				{"Two-factor", map[bool]string{true: "on", false: "off"}[u.MFA]}, {"Locked", locked},
				{"Active sessions", fmt.Sprint(sessions)}, {"Personal API keys", fmt.Sprint(keys)}})
		})
	}))

	var name, inviteRole, employeeID string
	invite := &cobra.Command{
		Use:   "invite <email>",
		Short: "Create an account and print its invitation link",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			email := strings.TrimSpace(args[0])
			if err := validEmail(email); err != nil {
				return err
			}
			if name == "" {
				name = email
			}
			return withPool(cmd.Context(), false, func(cfg config.Config, pool *pgxpool.Pool) error {
				ctx := cmd.Context()
				roleID, roleName, _, err := findRole(ctx, pool, inviteRole)
				if err != nil {
					return err
				}
				var id string
				err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
					var exists bool
					if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE lower(email) = lower($1))`, email).Scan(&exists); err != nil {
						return err
					}
					if exists {
						return fmt.Errorf("an account for %s already exists", email)
					}
					id = ids.New(ids.User)
					var emp any
					if employeeID != "" {
						emp = employeeID
					}
					if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, role_id, employee_id, status) VALUES ($1,$2,$3,$4,$5,'invited')`,
						id, email, name, roleID, emp); err != nil {
						if db.IsForeignKeyViolation(err) {
							return fmt.Errorf("no employee %q", employeeID)
						}
						return err
					}
					return audit(ctx, tx, "user.invite", id, map[string]any{"email": email, "role": roleName})
				})
				if err != nil {
					return err
				}
				link, err := SignInLink(ctx, pool, cfg.URL, email, false)
				if err != nil {
					return err
				}
				return a.emit(map[string]string{"id": id, "email": email, "role": roleName, "invitationUrl": link}, func() {
					a.printf("Invited %s as %s.\nShare this link privately (valid 7 days, works once):\n  %s\n", email, roleName, link)
				})
			})
		},
	}
	invite.Flags().StringVar(&name, "name", "", "the person's name")
	invite.Flags().StringVar(&inviteRole, "role", "employee", "role name or ID")
	invite.Flags().StringVar(&employeeID, "employee-id", "", "link to an employee record (Employee Area)")
	cmd.AddCommand(invite)

	var resetMFA bool
	link := a.userCommand("sign-in-link", "Print a one-time sign-in link (invitation or password reset)",
		func(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, u userRow) error {
			if u.Status == "deactivated" {
				return errors.New("this account is deactivated; reactivate it first")
			}
			l, err := SignInLink(ctx, pool, cfg.URL, u.Email, resetMFA)
			if err != nil {
				return err
			}
			if _, err := pool.Exec(ctx, `UPDATE users SET failed_sign_ins = 0, locked_until = NULL WHERE id = $1`, u.ID); err != nil {
				return err
			}
			return a.emit(map[string]string{"email": u.Email, "url": l}, func() {
				kind := "set a new password"
				if u.Status == "invited" {
					kind = "accept the invitation"
				}
				a.printf("One-time link for %s to %s (share it privately):\n  %s\n", u.Email, kind, l)
				if resetMFA {
					a.printf("Their authenticator app and recovery codes were removed.\n")
				}
			})
		})
	link.Flags().BoolVar(&resetMFA, "reset-mfa", false, "also remove their authenticator app and recovery codes")
	cmd.AddCommand(link)

	cmd.AddCommand(a.userCommand("unlock", "Clear a sign-in lockout", func(ctx context.Context, pool *pgxpool.Pool, _ config.Config, u userRow) error {
		if _, err := pool.Exec(ctx, `UPDATE users SET failed_sign_ins = 0, locked_until = NULL WHERE id = $1`, u.ID); err != nil {
			return err
		}
		a.printf("%s can sign in again.\n", u.Email)
		return audit(ctx, pool, "user.unlock", u.ID, nil)
	}))

	cmd.AddCommand(a.userCommand("reset-mfa", "Remove someone's authenticator app and recovery codes", func(ctx context.Context, pool *pgxpool.Pool, _ config.Config, u userRow) error {
		if err := a.confirm(fmt.Sprintf("Remove two-factor authentication for %s?", u.Email)); err != nil {
			return err
		}
		return db.InTx(ctx, pool, func(tx pgx.Tx) error {
			if err := clearMFA(ctx, tx, u.ID); err != nil {
				return err
			}
			if _, err := revokeAll(ctx, tx, u.ID); err != nil {
				return err
			}
			a.printf("Two-factor authentication removed for %s, and their sessions ended.\n", u.Email)
			return audit(ctx, tx, "user.mfa_reset", u.ID, nil)
		})
	}))

	cmd.AddCommand(a.userCommand("sign-out", "End all of someone's sessions", func(ctx context.Context, pool *pgxpool.Pool, _ config.Config, u userRow) error {
		n, err := revokeAll(ctx, pool, u.ID)
		if err != nil {
			return err
		}
		a.printf("Ended %d session(s) for %s.\n", n, u.Email)
		return audit(ctx, pool, "user.sign_out_everywhere", u.ID, map[string]any{"sessions": n})
	}))

	var newRole string
	setRole := a.userCommand("set-role", "Change someone's role (including making them an Owner)", func(ctx context.Context, pool *pgxpool.Pool, _ config.Config, u userRow) error {
		if newRole == "" {
			return errors.New("--role is required")
		}
		roleID, roleName, toOwner, err := findRole(ctx, pool, newRole)
		if err != nil {
			return err
		}
		return db.InTx(ctx, pool, func(tx pgx.Tx) error {
			wasOwner, err := isOwnerUser(ctx, tx, u.ID)
			if err != nil {
				return err
			}
			if wasOwner && !toOwner {
				if n, err := otherOwners(ctx, tx, u.ID); err != nil {
					return err
				} else if n == 0 {
					return errors.New("this is the last Owner; make someone else an Owner first")
				}
			}
			if toOwner && !wasOwner {
				if err := a.confirm(fmt.Sprintf("Make %s an Owner, with every permission?", u.Email)); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET role_id = $2, updated_at = now() WHERE id = $1`, u.ID, roleID); err != nil {
				return err
			}
			a.printf("%s is now %s.\n", u.Email, roleName)
			return audit(ctx, tx, "user.set_role", u.ID, map[string]any{"from": u.Role, "to": roleName})
		})
	})
	setRole.Flags().StringVar(&newRole, "role", "", "role name or ID (\"owner\" for the Owner role)")
	cmd.AddCommand(setRole)

	for _, s := range []struct{ use, short, to string }{
		{"deactivate", "Deactivate an account (signs them out and stops their personal keys)", "deactivated"},
		{"reactivate", "Reactivate a deactivated account", "active"},
	} {
		cmd.AddCommand(a.userCommand(s.use, s.short, func(ctx context.Context, pool *pgxpool.Pool, _ config.Config, u userRow) error {
			return db.InTx(ctx, pool, func(tx pgx.Tx) error {
				if s.to == "deactivated" {
					owner, err := isOwnerUser(ctx, tx, u.ID)
					if err != nil {
						return err
					}
					if owner {
						if n, err := otherOwners(ctx, tx, u.ID); err != nil {
							return err
						} else if n == 0 {
							return errors.New("this is the last Owner")
						}
					}
					if _, err := revokeAll(ctx, tx, u.ID); err != nil {
						return err
					}
				} else if u.Status != "deactivated" {
					return fmt.Errorf("%s isn't deactivated", u.Email)
				}
				if _, err := tx.Exec(ctx, `UPDATE users SET status = $2, updated_at = now() WHERE id = $1`, u.ID, s.to); err != nil {
					return err
				}
				a.printf("%s is now %s.\n", u.Email, s.to)
				return audit(ctx, tx, "user."+s.use, u.ID, nil)
			})
		}))
	}
	return cmd
}

func (a *app) rolesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "roles", Short: "List roles"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List roles with their members and permissions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withPool(cmd.Context(), false, func(_ config.Config, pool *pgxpool.Pool) error {
				rows, err := pool.Query(cmd.Context(), `SELECT r.id, r.name, coalesce(r.system_key, ''), r.mfa_required,
					(SELECT count(*) FROM users u WHERE u.role_id = r.id AND u.status <> 'deactivated'),
					coalesce((SELECT string_agg(p.permission || ':' || p.reach, ' ' ORDER BY p.permission) FROM role_permissions p WHERE p.role_id = r.id), '')
					FROM roles r ORDER BY r.system_key NULLS LAST, r.name`)
				if err != nil {
					return err
				}
				type role struct {
					ID          string   `json:"id"`
					Name        string   `json:"name"`
					System      string   `json:"systemKey,omitempty"`
					MFARequired bool     `json:"mfaRequired"`
					Members     int      `json:"members"`
					Permissions []string `json:"permissions"`
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (role, error) {
					var x role
					var perms string
					err := r.Scan(&x.ID, &x.Name, &x.System, &x.MFARequired, &x.Members, &perms)
					x.Permissions = strings.Fields(perms)
					return x, err
				})
				if err != nil {
					return err
				}
				return a.emit(list, func() {
					var out [][]string
					for _, r := range list {
						perms := fmt.Sprintf("%d", len(r.Permissions))
						if r.System == "owner" {
							perms = "all"
						}
						out = append(out, []string{r.Name, r.ID, fmt.Sprint(r.Members), perms, map[bool]string{true: "yes", false: ""}[r.MFARequired]})
					}
					a.table("ROLE\tID\tMEMBERS\tPERMISSIONS\t2FA REQUIRED", out)
				})
			})
		},
	})
	return cmd
}

// ---------------------------------------------------------------------------
// recover
// ---------------------------------------------------------------------------

func (a *app) recoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Get back in when nobody can sign in",
		Long: `Break-glass recovery, run on the server. Every action is recorded in the audit log.

  purros recover owner <email>     make an account an active Owner and print a sign-in link
  purros users sign-in-link <email> [--reset-mfa]   a link for any account
  purros users unlock <email>      clear a lockout`,
	}
	var create, keepMFA bool
	var name string
	owner := &cobra.Command{
		Use:   "owner <email>",
		Short: "Make an account an active Owner, clear its lockout and 2FA, and print a sign-in link",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			email := strings.TrimSpace(args[0])
			ctx := cmd.Context()
			return withPool(ctx, false, func(cfg config.Config, pool *pgxpool.Pool) error {
				var ownerRole string
				if err := pool.QueryRow(ctx, `SELECT id FROM roles WHERE system_key = 'owner'`).Scan(&ownerRole); err != nil {
					return errors.New("PurrOS isn't set up yet: run `purros setup`")
				}
				u, err := findUser(ctx, pool, email)
				if err != nil && !create {
					return fmt.Errorf("%w; add --create to create a new Owner account", err)
				}
				if err := a.confirm(fmt.Sprintf("Give %s full Owner access and print a sign-in link?", email)); err != nil {
					return err
				}
				created := false
				err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
					if u.ID == "" {
						created = true
						if err := validEmail(email); err != nil {
							return err
						}
						if name == "" {
							name = email
						}
						u.ID, u.Email, u.Status = ids.New(ids.User), email, "invited"
						if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, role_id, status) VALUES ($1,$2,$3,$4,'invited')`,
							u.ID, email, name, ownerRole); err != nil {
							return err
						}
					}
					status := "active"
					if u.Status == "invited" {
						status = "invited"
					}
					if _, err := tx.Exec(ctx, `UPDATE users SET role_id = $2, status = $3, failed_sign_ins = 0, locked_until = NULL, updated_at = now()
						WHERE id = $1`, u.ID, ownerRole, status); err != nil {
						return err
					}
					if !keepMFA {
						if err := clearMFA(ctx, tx, u.ID); err != nil {
							return err
						}
					}
					if _, err := revokeAll(ctx, tx, u.ID); err != nil {
						return err
					}
					return audit(ctx, tx, "recover.owner", u.ID, map[string]any{"email": email, "created": created, "mfaReset": !keepMFA})
				})
				if err != nil {
					return err
				}
				l, err := SignInLink(ctx, pool, cfg.URL, email, false)
				if err != nil {
					return err
				}
				return a.emit(map[string]string{"email": email, "url": l}, func() {
					a.printf("%s is now an Owner. Sign in with this one-time link (share it privately):\n  %s\n", email, l)
				})
			})
		},
	}
	owner.Flags().BoolVar(&create, "create", false, "create the account if it doesn't exist")
	owner.Flags().StringVar(&name, "name", "", "name for a new account")
	owner.Flags().BoolVar(&keepMFA, "keep-mfa", false, "keep their authenticator app")
	cmd.AddCommand(owner)
	return cmd
}
