package account

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/catalog"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/mail"
	"github.com/selectdev/purros/api/internal/modules/organization"
)

const adminTag = "Users & Roles"

var reaches = []string{httpx.ReachOwnTeam, httpx.ReachLocations, httpx.ReachDepartments, httpx.ReachEveryone}

type Assignments struct {
	LocationIDs   *[]string `json:"locationIds,omitempty"`
	OrgUnitIDs    *[]string `json:"orgUnitIds,omitempty" doc:"Covers every location under these org units"`
	DepartmentIDs *[]string `json:"departmentIds,omitempty"`
}

type InviteInput struct {
	Email      string  `json:"email" validate:"required,email,max=320"`
	Name       string  `json:"name" validate:"required,max=200"`
	RoleID     string  `json:"roleId,omitempty" doc:"Defaults to the Employee role"`
	EmployeeID *string `json:"employeeId,omitempty" doc:"Link to an employee record (their Employee Area)"`
	Assignments
	SendEmail *bool `json:"sendEmail,omitempty" doc:"Default true. Without email, invitationUrl is returned to share by hand."`
}

type Invited struct {
	organization.User
	InvitationURL *string `json:"invitationUrl" doc:"Only when the invitation wasn't emailed. Treat it like a password."`
}

type UserPatch struct {
	Name       *string `json:"name,omitempty" validate:"omitempty,max=200"`
	RoleID     *string `json:"roleId,omitempty"`
	EmployeeID *string `json:"employeeId,omitempty" doc:"Empty string unlinks"`
	Assignments
}

type ResetSignInInput struct {
	RemovePassword bool  `json:"removePassword,omitempty"`
	RemoveMFA      bool  `json:"removeMfa,omitempty" doc:"Remove the authenticator app and recovery codes"`
	SendEmail      *bool `json:"sendEmail,omitempty" doc:"Email a link to set a new password (default true)"`
}

type ResetResult struct {
	URL *string `json:"url" doc:"Only when the link wasn't emailed"`
}

type RolePermission struct {
	Permission string `json:"permission" validate:"required"`
	Reach      string `json:"reach" validate:"required,oneof=own_team assigned_locations assigned_departments everyone"`
}

type RoleInput struct {
	Name        string           `json:"name" validate:"required,max=100"`
	Description string           `json:"description,omitempty" validate:"max=500"`
	Permissions []RolePermission `json:"permissions" validate:"dive"`
	MFARequired bool             `json:"mfaRequired,omitempty"`
}

type RolePatch struct {
	Name        *string           `json:"name,omitempty" validate:"omitempty,max=100"`
	Description *string           `json:"description,omitempty" validate:"omitempty,max=500"`
	Permissions *[]RolePermission `json:"permissions,omitempty" validate:"omitempty,dive"`
	MFARequired *bool             `json:"mfaRequired,omitempty"`
}

type AuthSettings struct {
	MagicLinkEnabled bool `json:"magicLinkEnabled"`
	MFARequired      bool `json:"mfaRequired" doc:"Everyone must use two-factor authentication"`
	EmailConfigured  bool `json:"emailConfigured" doc:"SMTP is set up (read-only)"`
}

type AuthSettingsPatch struct {
	MagicLinkEnabled *bool `json:"magicLinkEnabled,omitempty"`
	MFARequired      *bool `json:"mfaRequired,omitempty"`
}

// checkGrant enforces "no privilege escalation": people can only hand out
// permissions they hold themselves, with an equal or wider reach.
func checkGrant(actor *httpx.UserAccess, perms []RolePermission, path string) error {
	if actor.Owner {
		return nil
	}
	for i, p := range perms {
		field := fmt.Sprintf("%s[%d]", path, i)
		if p.Permission == "roles.manage" {
			return httpx.Validation(httpx.FieldError{Path: field, Message: "Only an Owner can grant roles.manage"})
		}
		held, ok := actor.Perms[p.Permission]
		if !ok || (held != httpx.ReachEveryone && held != p.Reach) {
			return httpx.Validation(httpx.FieldError{Path: field, Message: "You can only grant permissions you hold with an equal or wider reach"})
		}
	}
	return nil
}

func validatePerms(perms []RolePermission) error {
	seen := map[string]bool{}
	for i, p := range perms {
		if !slices.ContainsFunc(catalog.Permissions, func(x catalog.Permission) bool { return x.Key == p.Permission }) {
			return httpx.Validation(httpx.FieldError{Path: fmt.Sprintf("permissions[%d].permission", i), Message: "Unknown permission"})
		}
		if seen[p.Permission] {
			return httpx.Validation(httpx.FieldError{Path: fmt.Sprintf("permissions[%d].permission", i), Message: "Listed twice"})
		}
		seen[p.Permission] = true
	}
	return nil
}

// checkAssignable returns an error unless actor may give role roleID to someone.
func checkAssignable(c *httpx.Ctx, q db.Querier, roleID string) (organization.Role, error) {
	role, err := organization.GetRole(c, q, roleID)
	if err != nil {
		var p *httpx.Problem
		if errors.As(err, &p) {
			return role, httpx.Validation(httpx.FieldError{Path: "roleId", Message: "Unknown role"})
		}
		return role, err
	}
	actor := c.Principal.User
	if role.SystemKey != nil && *role.SystemKey == "owner" && !actor.Owner {
		return role, httpx.Validation(httpx.FieldError{Path: "roleId", Message: "Only an Owner can make someone an Owner"})
	}
	perms := make([]RolePermission, len(role.Permissions))
	for i, p := range role.Permissions {
		perms[i] = RolePermission{Permission: p.Permission, Reach: p.Reach}
	}
	if err := checkGrant(actor, perms, "roleId"); err != nil {
		return role, httpx.Validation(httpx.FieldError{Path: "roleId", Message: "This role has permissions you can't grant"})
	}
	return role, nil
}

func isOwner(c *httpx.Ctx, q db.Querier, userID string) (bool, error) {
	var owner bool
	err := q.QueryRow(c, `SELECT r.system_key IS NOT DISTINCT FROM 'owner' FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = $1`, userID).Scan(&owner)
	return owner, err
}

// otherActiveOwners counts active Owners besides userID.
func otherActiveOwners(c *httpx.Ctx, q db.Querier, userID string) (int, error) {
	var n int
	err := q.QueryRow(c, `SELECT count(*) FROM users u JOIN roles r ON r.id = u.role_id
		WHERE r.system_key = 'owner' AND u.status = 'active' AND u.id <> $1`, userID).Scan(&n)
	return n, err
}

// setAssignments replaces the assignments given (nil fields are unchanged).
func setAssignments(c *httpx.Ctx, tx pgx.Tx, userID string, a Assignments) error {
	check := func(table, path string, list []string) error {
		for i, id := range list {
			var ok bool
			if err := tx.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, id).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return httpx.Validation(httpx.FieldError{Path: fmt.Sprintf("%s[%d]", path, i), Message: "Unknown ID"})
			}
		}
		return nil
	}
	if a.LocationIDs != nil {
		if err := check("locations", "locationIds", *a.LocationIDs); err != nil {
			return err
		}
		if _, err := tx.Exec(c, `DELETE FROM user_location_assignments WHERE user_id = $1 AND location_id IS NOT NULL`, userID); err != nil {
			return err
		}
		for _, id := range *a.LocationIDs {
			if _, err := tx.Exec(c, `INSERT INTO user_location_assignments (user_id, location_id) VALUES ($1, $2)`, userID, id); err != nil {
				return err
			}
		}
	}
	if a.OrgUnitIDs != nil {
		if err := check("org_units", "orgUnitIds", *a.OrgUnitIDs); err != nil {
			return err
		}
		if _, err := tx.Exec(c, `DELETE FROM user_location_assignments WHERE user_id = $1 AND org_unit_id IS NOT NULL`, userID); err != nil {
			return err
		}
		for _, id := range *a.OrgUnitIDs {
			if _, err := tx.Exec(c, `INSERT INTO user_location_assignments (user_id, org_unit_id) VALUES ($1, $2)`, userID, id); err != nil {
				return err
			}
		}
	}
	if a.DepartmentIDs != nil {
		if err := check("departments", "departmentIds", *a.DepartmentIDs); err != nil {
			return err
		}
		if _, err := tx.Exec(c, `DELETE FROM user_department_assignments WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for _, id := range *a.DepartmentIDs {
			if _, err := tx.Exec(c, `INSERT INTO user_department_assignments (user_id, department_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func linkEmployee(c *httpx.Ctx, tx pgx.Tx, userID string, employeeID *string) error {
	if employeeID == nil {
		return nil
	}
	if *employeeID == "" {
		_, err := tx.Exec(c, `UPDATE users SET employee_id = NULL WHERE id = $1`, userID)
		return err
	}
	var exists bool
	var linked *string
	err := tx.QueryRow(c, `SELECT true, (SELECT id FROM users WHERE employee_id = $1 AND id <> $2) FROM employees WHERE id = $1`,
		*employeeID, userID).Scan(&exists, &linked)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Validation(httpx.FieldError{Path: "employeeId", Message: "Unknown employee"})
	}
	if err != nil {
		return err
	}
	if linked != nil {
		return httpx.Validation(httpx.FieldError{Path: "employeeId", Message: "Already linked to another account"})
	}
	_, err = tx.Exec(c, `UPDATE users SET employee_id = $2 WHERE id = $1`, userID, *employeeID)
	return err
}

// sendLink emails a link, or returns it when email is off or not wanted.
func sendLink(c *httpx.Ctx, tx pgx.Tx, kind, userID, email, name, path string, sendEmail *bool) (*string, error) {
	token, err := issueToken(c, tx, userID, kind)
	if err != nil {
		return nil, err
	}
	url := link(c, path, token)
	if (sendEmail == nil || *sendEmail) && c.App.Config.SMTP.Enabled() {
		_, err := mail.Queue(c, tx, emailFor(c, tx, kind, email, name, url))
		return nil, err
	}
	return &url, nil
}

func adminRoutes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "POST", Path: "/users", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary:     "Invite someone",
			Description: "Needs users.manage (reach Everyone). You can only give roles whose permissions you hold yourself.",
			Body:        InviteInput{}, Response: Invited{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				if err := c.RequireEveryone("users.manage"); err != nil {
					return nil, err
				}
				var in InviteInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Invited
				err := c.InTx(func(tx pgx.Tx) error {
					if in.RoleID == "" {
						if err := tx.QueryRow(c, `SELECT id FROM roles WHERE system_key = 'employee'`).Scan(&in.RoleID); err != nil {
							return err
						}
					}
					if _, err := checkAssignable(c, tx, in.RoleID); err != nil {
						return err
					}
					var exists bool
					if err := tx.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM users WHERE lower(email) = $1)`, normalizeEmail(in.Email)).Scan(&exists); err != nil {
						return err
					}
					if exists {
						return httpx.Conflict("An account with this email already exists.")
					}
					id := ids.New(ids.User)
					if _, err := tx.Exec(c, `INSERT INTO users (id, email, name, role_id, status) VALUES ($1, $2, $3, $4, 'invited')`,
						id, strings.TrimSpace(in.Email), in.Name, in.RoleID); err != nil {
						return err
					}
					if err := linkEmployee(c, tx, id, in.EmployeeID); err != nil {
						return err
					}
					if err := setAssignments(c, tx, id, in.Assignments); err != nil {
						return err
					}
					var err error
					if out.InvitationURL, err = sendLink(c, tx, "invitation", id, in.Email, in.Name, "/invitation", in.SendEmail); err != nil {
						return err
					}
					if out.User, err = organization.GetUser(c, tx, id); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "user.invite", EntityType: "user", EntityID: id, After: out.User})
				})
				return out, err
			},
		},
		{
			Method: "PATCH", Path: "/users/{id}", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary: "Change someone's name, role, employee link or assignments", Body: UserPatch{}, Response: organization.User{},
			Handler: func(c *httpx.Ctx) (any, error) {
				if err := c.RequireEveryone("users.manage"); err != nil {
					return nil, err
				}
				var in UserPatch
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out organization.User
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := organization.GetUser(c, tx, c.Param("id"))
					if err != nil {
						return err
					}
					if _, err := tx.Exec(c, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, before.ID); err != nil {
						return err
					}
					owner, err := isOwner(c, tx, before.ID)
					if err != nil {
						return err
					}
					if owner && !c.Principal.User.Owner {
						return httpx.Forbidden("Only an Owner can change an Owner's account.")
					}
					if in.RoleID != nil && *in.RoleID != before.RoleID {
						if before.ID == c.Principal.UserID && !c.Principal.User.Owner {
							return httpx.Forbidden("You can't change your own role.")
						}
						role, err := checkAssignable(c, tx, *in.RoleID)
						if err != nil {
							return err
						}
						if owner && (role.SystemKey == nil || *role.SystemKey != "owner") {
							if n, err := otherActiveOwners(c, tx, before.ID); err != nil {
								return err
							} else if n == 0 {
								return httpx.Conflict("This is the last Owner. Make someone else an Owner first.")
							}
						}
						if _, err := tx.Exec(c, `UPDATE users SET role_id = $2 WHERE id = $1`, before.ID, *in.RoleID); err != nil {
							return err
						}
					}
					if in.Name != nil {
						if _, err := tx.Exec(c, `UPDATE users SET name = $2 WHERE id = $1`, before.ID, *in.Name); err != nil {
							return err
						}
					}
					if err := linkEmployee(c, tx, before.ID, in.EmployeeID); err != nil {
						return err
					}
					if err := setAssignments(c, tx, before.ID, in.Assignments); err != nil {
						return err
					}
					if _, err := tx.Exec(c, `UPDATE users SET updated_at = now() WHERE id = $1`, before.ID); err != nil {
						return err
					}
					if out, err = organization.GetUser(c, tx, before.ID); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "user.update", EntityType: "user", EntityID: out.ID, Before: before, After: out})
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/users/{id}:deactivate", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary: "Deactivate an account", Description: "Signs them out everywhere and stops their personal API keys. History is kept.",
			Response: organization.User{},
			Handler:  func(c *httpx.Ctx) (any, error) { return setStatus(c, "deactivated") },
		},
		{
			Method: "POST", Path: "/users/{id}:reactivate", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary: "Reactivate an account", Response: organization.User{},
			Handler: func(c *httpx.Ctx) (any, error) { return setStatus(c, "active") },
		},
		{
			Method: "POST", Path: "/users/{id}:reset-sign-in", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary:     "Reset someone's sign-in",
			Description: "Signs them out, optionally removes their password and authenticator, and sends a link to set a new password.",
			Body:        ResetSignInInput{}, Response: ResetResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				if err := c.RequireEveryone("users.manage"); err != nil {
					return nil, err
				}
				var in ResetSignInInput
				if err := c.DecodeOptional(&in); err != nil {
					return nil, err
				}
				var out ResetResult
				err := c.InTx(func(tx pgx.Tx) error {
					a, err := loadAccount(c, tx, "id = $1", c.Param("id"))
					if errors.Is(err, pgx.ErrNoRows) {
						return httpx.NotFound("User not found.")
					}
					if err != nil {
						return err
					}
					if owner, err := isOwner(c, tx, a.ID); err != nil {
						return err
					} else if owner && !c.Principal.User.Owner {
						return httpx.Forbidden("Only an Owner can reset an Owner's sign-in.")
					}
					if in.RemovePassword {
						if _, err := tx.Exec(c, `UPDATE users SET password_hash = NULL WHERE id = $1`, a.ID); err != nil {
							return err
						}
					}
					if in.RemoveMFA {
						if _, err := tx.Exec(c, `UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE id = $1`, a.ID); err != nil {
							return err
						}
						if _, err := tx.Exec(c, `DELETE FROM recovery_codes WHERE user_id = $1`, a.ID); err != nil {
							return err
						}
					}
					if _, err := tx.Exec(c, `UPDATE users SET failed_sign_ins = 0, locked_until = NULL WHERE id = $1`, a.ID); err != nil {
						return err
					}
					if err := revokeSessions(c, tx, a.ID, ""); err != nil {
						return err
					}
					kind, path := "password_reset", "/reset-password"
					if a.Status == "invited" {
						kind, path = "invitation", "/invitation"
					}
					if out.URL, err = sendLink(c, tx, kind, a.ID, a.Email, a.Name, path, in.SendEmail); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "user.reset_sign_in", EntityType: "user", EntityID: a.ID,
						After: map[string]any{"removePassword": in.RemovePassword, "removeMfa": in.RemoveMFA}})
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/roles", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary:     "Create a role",
			Description: "Needs roles.manage (reach Everyone). You can only include permissions you hold with an equal or wider reach; only an Owner can grant roles.manage.",
			Body:        RoleInput{}, Response: organization.Role{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				if err := c.RequireEveryone("roles.manage"); err != nil {
					return nil, err
				}
				var in RoleInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if err := validatePerms(in.Permissions); err != nil {
					return nil, err
				}
				if err := checkGrant(c.Principal.User, in.Permissions, "permissions"); err != nil {
					return nil, err
				}
				var out organization.Role
				err := c.InTx(func(tx pgx.Tx) error {
					id := ids.New(ids.Role)
					if _, err := tx.Exec(c, `INSERT INTO roles (id, name, description, mfa_required) VALUES ($1, $2, $3, $4)`,
						id, in.Name, in.Description, in.MFARequired); err != nil {
						if db.IsUniqueViolation(err) {
							return httpx.Conflict("A role with this name already exists.")
						}
						return err
					}
					if err := setRolePerms(c, tx, id, in.Permissions); err != nil {
						return err
					}
					var err error
					if out, err = organization.GetRole(c, tx, id); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "role.create", EntityType: "role", EntityID: id, After: out})
				})
				return out, err
			},
		},
		{
			Method: "PATCH", Path: "/roles/{id}", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary: "Change a role", Description: "The Owner role can't be changed. Changes apply on each member's next request.",
			Body: RolePatch{}, Response: organization.Role{},
			Handler: func(c *httpx.Ctx) (any, error) {
				if err := c.RequireEveryone("roles.manage"); err != nil {
					return nil, err
				}
				var in RolePatch
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out organization.Role
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := organization.GetRole(c, tx, c.Param("id"))
					if err != nil {
						return err
					}
					if before.SystemKey != nil && *before.SystemKey == "owner" {
						return httpx.Forbidden("The Owner role has every permission and can't be changed.")
					}
					if c.Principal.User.RoleID == before.ID && !c.Principal.User.Owner {
						return httpx.Forbidden("You can't change your own role.")
					}
					if in.Permissions != nil {
						if err := validatePerms(*in.Permissions); err != nil {
							return err
						}
						// Removing a permission is also a grant decision: you must hold the old ones too.
						old := make([]RolePermission, len(before.Permissions))
						for i, p := range before.Permissions {
							old[i] = RolePermission{Permission: p.Permission, Reach: p.Reach}
						}
						if err := checkGrant(c.Principal.User, old, "permissions"); err != nil {
							return httpx.Forbidden("This role has permissions you don't hold, so you can't change them.")
						}
						if err := checkGrant(c.Principal.User, *in.Permissions, "permissions"); err != nil {
							return err
						}
						if err := setRolePerms(c, tx, before.ID, *in.Permissions); err != nil {
							return err
						}
					}
					name, desc, mfa := before.Name, before.Description, before.MFARequired
					if in.Name != nil {
						name = *in.Name
					}
					if in.Description != nil {
						desc = *in.Description
					}
					if in.MFARequired != nil {
						mfa = *in.MFARequired
					}
					if _, err := tx.Exec(c, `UPDATE roles SET name = $2, description = $3, mfa_required = $4, updated_at = now() WHERE id = $1`,
						before.ID, name, desc, mfa); err != nil {
						if db.IsUniqueViolation(err) {
							return httpx.Conflict("A role with this name already exists.")
						}
						return err
					}
					if out, err = organization.GetRole(c, tx, before.ID); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "role.update", EntityType: "role", EntityID: out.ID, Before: before, After: out})
				})
				return out, err
			},
		},
		{
			Method: "DELETE", Path: "/roles/{id}", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary: "Delete a role", Description: "Built-in roles and roles that still have members can't be deleted.", Status: 204,
			Handler: func(c *httpx.Ctx) (any, error) {
				if err := c.RequireEveryone("roles.manage"); err != nil {
					return nil, err
				}
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := organization.GetRole(c, tx, c.Param("id"))
					if err != nil {
						return err
					}
					if before.SystemKey != nil {
						return httpx.Forbidden("Built-in roles can't be deleted.")
					}
					var members int
					if err := tx.QueryRow(c, `SELECT count(*) FROM users WHERE role_id = $1`, before.ID).Scan(&members); err != nil {
						return err
					}
					if members > 0 {
						return httpx.Conflict(fmt.Sprintf("%d account(s) still have this role. Give them another role first.", members))
					}
					if _, err := tx.Exec(c, `DELETE FROM roles WHERE id = $1`, before.ID); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "role.delete", EntityType: "role", EntityID: before.ID, Before: before})
				})
				return httpx.Result{Status: 204}, err
			},
		},
		{
			Method: "GET", Path: "/settings/authentication", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary: "Sign-in settings", Response: AuthSettings{},
			Handler: func(c *httpx.Ctx) (any, error) {
				if err := c.RequireEveryone("settings.manage"); err != nil {
					return nil, err
				}
				return loadAuthSettings(c, c.App.Pool)
			},
		},
		{
			Method: "PATCH", Path: "/settings/authentication", Tag: adminTag, Auth: httpx.AuthUser, Permission: httpx.PermSelf,
			Summary: "Change sign-in settings", Body: AuthSettingsPatch{}, Response: AuthSettings{},
			Handler: func(c *httpx.Ctx) (any, error) {
				if err := c.RequireEveryone("settings.manage"); err != nil {
					return nil, err
				}
				var in AuthSettingsPatch
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out AuthSettings
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := loadAuthSettings(c, tx)
					if err != nil {
						return err
					}
					if _, err := tx.Exec(c, `UPDATE company SET magic_link_enabled = coalesce($1, magic_link_enabled),
						mfa_required = coalesce($2, mfa_required), updated_at = now()`, in.MagicLinkEnabled, in.MFARequired); err != nil {
						return err
					}
					if out, err = loadAuthSettings(c, tx); err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "settings.authentication.update", EntityType: "company", Before: before, After: out})
				})
				return out, err
			},
		},
	}
}

func loadAuthSettings(c *httpx.Ctx, q db.Querier) (AuthSettings, error) {
	s := AuthSettings{EmailConfigured: c.App.Config.SMTP.Enabled()}
	err := q.QueryRow(c, `SELECT magic_link_enabled, mfa_required FROM company LIMIT 1`).Scan(&s.MagicLinkEnabled, &s.MFARequired)
	return s, err
}

func setRolePerms(c *httpx.Ctx, tx pgx.Tx, roleID string, perms []RolePermission) error {
	if _, err := tx.Exec(c, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return err
	}
	for _, p := range perms {
		if _, err := tx.Exec(c, `INSERT INTO role_permissions (role_id, permission, reach) VALUES ($1, $2, $3)`, roleID, p.Permission, p.Reach); err != nil {
			return err
		}
	}
	return nil
}

func setStatus(c *httpx.Ctx, status string) (any, error) {
	if err := c.RequireEveryone("users.manage"); err != nil {
		return nil, err
	}
	var out organization.User
	err := c.InTx(func(tx pgx.Tx) error {
		before, err := organization.GetUser(c, tx, c.Param("id"))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(c, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, before.ID); err != nil {
			return err
		}
		owner, err := isOwner(c, tx, before.ID)
		if err != nil {
			return err
		}
		if owner && !c.Principal.User.Owner {
			return httpx.Forbidden("Only an Owner can change an Owner's account.")
		}
		if status == "deactivated" {
			if before.ID == c.Principal.UserID {
				return httpx.Forbidden("You can't deactivate your own account.")
			}
			if owner {
				if n, err := otherActiveOwners(c, tx, before.ID); err != nil {
					return err
				} else if n == 0 {
					return httpx.Conflict("This is the last Owner.")
				}
			}
			if err := revokeSessions(c, tx, before.ID, ""); err != nil {
				return err
			}
		} else if before.Status != "deactivated" {
			return httpx.Conflict("This account isn't deactivated.")
		}
		if _, err := tx.Exec(c, `UPDATE users SET status = $2, updated_at = now() WHERE id = $1`, before.ID, status); err != nil {
			return err
		}
		if out, err = organization.GetUser(c, tx, before.ID); err != nil {
			return err
		}
		action := "user.deactivate"
		if status == "active" {
			action = "user.reactivate"
		}
		return c.Record(tx, httpx.Change{Action: action, EntityType: "user", EntityID: out.ID, Before: before, After: out})
	})
	return out, err
}

// Routes returns the sign-in, account and administration routes.
func Routes() []httpx.Route {
	var out []httpx.Route
	out = append(out, signInRoutes()...)
	out = append(out, mfaRoutes()...)
	out = append(out, keyRoutes()...)
	out = append(out, adminRoutes()...)
	return out
}
