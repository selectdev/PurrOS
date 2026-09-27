package organization

import (
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/httpx"
)

type Permission struct {
	Permission string `json:"permission"`
	Reach      string `json:"reach" doc:"own_team, assigned_locations, assigned_departments or everyone"`
}

type Role struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	SystemKey   *string      `json:"systemKey" doc:"owner or employee for built-in roles"`
	Permissions []Permission `json:"permissions"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
}

type User struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	RoleID        string    `json:"roleId"`
	RoleName      string    `json:"roleName"`
	EmployeeID    *string   `json:"employeeId"`
	Status        string    `json:"status" doc:"invited, active or deactivated"`
	LocationIDs   []string  `json:"locationIds" doc:"Assigned locations"`
	OrgUnitIDs    []string  `json:"orgUnitIds" doc:"Assigned org units (and every location under them)"`
	DepartmentIDs []string  `json:"departmentIds"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type userQuery struct {
	httpx.ListParams
	RoleID string `json:"roleId,omitempty"`
	Status string `json:"status,omitempty"`
}

// accessRoutes are read-only views of roles and users. Roles, users and
// assignments are managed in the web app.
func accessRoutes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "GET", Path: "/roles", Tag: "Organization", Scope: "organization:read",
			Summary: "List roles and their permissions", Query: listQuery{}, Response: httpx.Page[Role]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `
					SELECT r.id, r.name, r.description, r.system_key, r.created_at, r.updated_at,
					       coalesce((SELECT jsonb_agg(jsonb_build_object('permission', p.permission, 'reach', p.reach) ORDER BY p.permission)
					                 FROM role_permissions p WHERE p.role_id = r.id), '[]')
					FROM roles r WHERE r.id > $1 AND ($2::timestamptz IS NULL OR r.updated_at >= $2)
					ORDER BY r.id LIMIT $3`, lp.AfterID, lp.UpdatedSince, lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Role, error) {
					var x Role
					err := r.Scan(&x.ID, &x.Name, &x.Description, &x.SystemKey, &x.CreatedAt, &x.UpdatedAt, &x.Permissions)
					return x, err
				})
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(r Role) string { return r.ID }), nil
			},
		},
		{
			Method: "GET", Path: "/users", Tag: "Organization", Scope: "organization:read",
			Summary: "List user accounts with their role and assignments", Query: userQuery{}, Response: httpx.Page[User]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `
					SELECT u.id, u.email, u.name, u.role_id, r.name, u.employee_id, u.status, u.created_at, u.updated_at,
					       coalesce((SELECT array_agg(a.location_id ORDER BY a.location_id) FROM user_location_assignments a
					                 WHERE a.user_id = u.id AND a.location_id IS NOT NULL), '{}'),
					       coalesce((SELECT array_agg(a.org_unit_id ORDER BY a.org_unit_id) FROM user_location_assignments a
					                 WHERE a.user_id = u.id AND a.org_unit_id IS NOT NULL), '{}'),
					       coalesce((SELECT array_agg(d.department_id ORDER BY d.department_id) FROM user_department_assignments d
					                 WHERE d.user_id = u.id), '{}')
					FROM users u JOIN roles r ON r.id = u.role_id
					WHERE u.id > $1 AND ($2::timestamptz IS NULL OR u.updated_at >= $2)
					  AND ($3 = '' OR u.role_id = $3) AND ($4 = '' OR u.status = $4)
					ORDER BY u.id LIMIT $5`, lp.AfterID, lp.UpdatedSince, c.Query("roleId"), c.Query("status"), lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) {
					var u User
					err := r.Scan(&u.ID, &u.Email, &u.Name, &u.RoleID, &u.RoleName, &u.EmployeeID, &u.Status, &u.CreatedAt, &u.UpdatedAt,
						&u.LocationIDs, &u.OrgUnitIDs, &u.DepartmentIDs)
					return u, err
				})
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(u User) string { return u.ID }), nil
			},
		},
	}
}
