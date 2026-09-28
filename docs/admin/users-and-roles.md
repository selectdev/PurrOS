# Users, roles & permissions

## Accounts

Everyone who signs in has an **account**: owners, HR, managers, supervisors and employees. Each account has:

- a **role**, which decides what they can do
- **assignments**: the locations, org units and departments their role applies to, where the role uses those reaches
- optionally a link to an **employee record**, which gives them their [Employee Area](../guides/employee-area.md)

Accounts are managed with the API (`/api/v1/users`, permission `users.manage`) or the `purros users` CLI, and under **Settings → Users** once the web app ships.

| Action | Notes |
|---|---|
| Invite | `POST /users` or `purros users invite`. Sends an email with a sign-in link (or prints it, without SMTP). You pick the role and assignments. |
| Link to employee | Set `employeeId` when inviting or with `PATCH /users/{id}`. |
| Change role or assignments | `PATCH /users/{id}` or `purros users set-role`. Takes effect on the user's next request. |
| Deactivate | `POST /users/{id}:deactivate` or `purros users deactivate`. Signs the user out everywhere and blocks sign-in. History is kept. |
| Reset sign-in | `POST /users/{id}:reset-sign-in` or `purros users sign-in-link [--reset-mfa]`. Sends a new sign-in link and can remove their 2FA. |

With SSO provisioning (SCIM, planned), accounts will be created and deactivated automatically from your identity provider. See [Authentication](authentication.md#automatic-provisioning-scim).

## Roles

Every account has exactly **one role**. Your organization creates its own roles (`POST /api/v1/roles`, and **Settings → Roles** once the web app ships). PurrOS doesn't fix any job titles.

A role is:

- a **name** and description, e.g. "District Manager"
- a set of **permissions**, each with a **reach**

### Reach

| Reach | The permission applies to… |
|---|---|
| **Own team** | The user's direct and indirect reports, based on the reporting lines in People & HR |
| **Assigned locations** | Locations assigned to the user, or all locations inside org units assigned to them |
| **Assigned departments** | Departments assigned to the user |
| **Everyone** | The whole company |

The same permission can have different reaches in different roles. Supervisors might approve timesheets for their own team, district managers for their assigned district, and payroll for everyone.

### System roles

| Role | Rules |
|---|---|
| **Owner** | Has every permission, can't be edited or deleted, and at least one Owner must exist. Only Owners can switch [features](feature-switches.md) on and off or grant `roles.manage`. |
| **Employee** | Has no extra permissions and is the default for new accounts. You can pick another default role. |

Any account linked to an employee record can always use the Employee Area for its **own** data, whatever its role.

### Starter roles

New installs get a few editable roles suggested by the business type chosen in [first-run setup](../getting-started/first-run-setup.md). For example:

| Role | Sample permissions | Reach |
|---|---|---|
| HR | `employees.read`, `employees.write`, `employees.sensitive.read`, `documents.manage`, `time_off.approve` | Everyone |
| Payroll | `timesheets.read`, `pay_periods.lock`, `pay.read`, `pay.write`, `payroll.export` | Everyone |
| District Manager | `employees.read`, `schedules.read`, `timesheets.approve`, `cash.read`, `inventory.read`, `audits.conduct`, `reports.read` | Assigned locations (a district) |
| Store / Site Manager | `employees.read`, `schedules.manage`, `timesheets.approve`, `punches.correct`, `cash.manage`, `orders.create`, `checklists.manage`, `equipment.manage` | Assigned locations |
| Supervisor | `employees.read`, `schedules.read`, `timesheets.approve`, `time_off.approve`, `cash.count`, `checklists.complete` | Own team |

The full list of permissions is in the [permissions reference](permissions-reference.md).

## Safeguards

- **No privilege escalation.** You can only create, edit or assign a role if you hold every permission in it with an equal or wider reach. Only an Owner can grant `roles.manage`.
- **Sensitive data is separate.** Pay (`pay.read`), bank details and national IDs (`employees.sensitive.read`) have their own permissions. Being able to see an employee doesn't include them.
- **Disabled features.** Permissions of switched-off features are hidden from the role editor and ignored.
- **Audited.** Creating, editing and deleting roles, and changing anyone's role or assignments, are recorded in the audit log with before and after values.

## Checking access

Every user's profile has an **Access** tab that lists what they can do and where, resolved from their role and assignments. Admins can use it to answer "why can (or can't) this person see that?".

## API

| Endpoint | Who | Notes |
|---|---|---|
| `GET /api/v1/permissions` | any | The permission catalog for enabled features |
| `GET /api/v1/roles`, `GET /api/v1/roles/{id}` | `organization:read` / `users.read` | Roles and their permissions |
| `GET /api/v1/users`, `GET /api/v1/users/{id}` | `organization:read` / `users.read` | Accounts, roles and assignments |
| `POST /api/v1/users` | `users.manage` | Invite. The invitation is emailed, or its link returned when email is off or `sendEmail` is false |
| `PATCH /api/v1/users/{id}` | `users.manage` | Name, role, employee link and assignments |
| `POST /api/v1/users/{id}:deactivate`, `:reactivate` | `users.manage` | Deactivating signs the person out everywhere and stops their personal keys |
| `POST /api/v1/users/{id}:reset-sign-in` | `users.manage` | Signs them out, optionally removes their password and 2FA, and sends a link to set a new password |
| `POST /api/v1/roles`, `PATCH /api/v1/roles/{id}`, `DELETE /api/v1/roles/{id}` | `roles.manage` | With the escalation rules above |

Managing accounts and roles needs `users.manage` or `roles.manage` with reach **Everyone**, and works only for people (sessions and personal keys). Integration keys can read roles and users but never change them, so an integration can't escalate access.

The Owner role can't be changed, the last active Owner can't be demoted or deactivated, and nobody can change their own role or deactivate themselves.
