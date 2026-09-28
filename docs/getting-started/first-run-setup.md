# First-run setup

## Today: with the CLI

The web app and its setup wizard aren't built yet. Set up a new install from the server:

```bash
docker compose exec api purros setup                      # company, first Owner, optional first location
docker compose exec api purros locations create --name "Store 102" --external-id 102 --timezone America/Chicago
docker compose exec api purros features list              # everything starts enabled
docker compose exec api purros features disable displays  # switch off what you don't use
docker compose exec api purros users invite hr@example.com --role …
docker compose exec api purros integrations register --manifest purros-integration.json
docker compose exec api purros doctor
```

Build the rest through the API (as the Owner, with a personal API key or a signed-in session): regions and districts with `POST /api/v1/org-units`, more locations with `POST /api/v1/locations`, departments with `POST /api/v1/departments` (see [Organization & locations](../admin/organization-and-locations.md#api)), roles with `POST /api/v1/roles`, and employees with `POST /api/v1/employees` or `PUT /api/v1/employees/external/{externalId}` from an HR integration. Integrations can also be registered with `POST /api/v1/integrations`. See the [CLI reference](../operations/cli.md).

## Planned: the setup wizard

The first time the Owner signs in to the web app, a setup wizard will walk through the basics. You'll be able to skip any step and come back later under **Settings**.

### 1. Company details

Company name, logo, default currency, timezone, language, and the first day of the week. The logo appears in the header, on invoices and on team displays.

### 2. Type of business

Pick what fits best: retail, restaurant or hospitality, franchise or multi-unit, clinic or personal services, distribution or wholesale, light manufacturing, field service, or other.

This only chooses **starting suggestions**: which features to enable, starter roles, checklist templates and what to call a location ("store", "branch", "site", "clinic"…). Nothing is locked in.

### 3. Features

The wizard shows the suggested features for your business type. Switch on only what you'll use now. You can enable more at any time, and anything you switch off disappears completely from the product. See [Feature switches](../admin/feature-switches.md).

| If you… | Start with |
|---|---|
| Mainly need staff records and hours | People & HR, Time & Attendance, Employee Area |
| Take cash at a counter | Add Cash Management and Sales (sales feeds) |
| Carry stock | Add Inventory and Purchasing & Ordering |
| Run several locations | Add Reports & Insights and Forms & Checklists |
| Build rotas every week | Add Scheduling & Forecasting |

### 4. Organization and locations

Create your hierarchy and locations. A single-location business just creates one location. A multi-location business can add levels such as regions and districts. See [Organization & locations](../admin/organization-and-locations.md).

For each location, set the address, timezone and opening hours. Departments or work areas (e.g. Front of house, Kitchen, Warehouse, Service desk) are optional.

### 5. Roles

The wizard creates a few **starter roles** that match your business type, for example HR, Payroll, District Manager, Store Manager, Supervisor and Employee. Rename, edit or delete them to match how your organization actually works. See [Users, roles & permissions](../admin/users-and-roles.md).

### 6. Sign-in methods

Choose how people sign in: email and password, magic links, passkeys and/or single sign-on. You can require SSO, and decide whether 2FA is required for any roles. See [Authentication](../admin/authentication.md).

### 7. Bring in your people

Choose one or more ways to add employees:

- **Import a CSV.** Download the template, fill it in and upload it. PurrOS checks every row and shows problems before anything is saved.
- **Connect your HR system** through an [integration](../integrations/recipes.md#hr-and-employment-software).
- **Add them by hand.**

Then **invite** them. Each person gets an email with a sign-in link and lands in their [Employee Area](../guides/employee-area.md). People without email can be added without an account and still clock in at a [kiosk timeclock](../guides/time-and-attendance.md#kiosk-timeclock) with a badge or clock code.

### 8. Connect your other systems

If you use a POS, an online store, a timeclock or payroll software, see [Integrations](../integrations/README.md). Most businesses connect their POS early, because sales data drives forecasting, cash and inventory usage.

## After setup

A **setup checklist** stays on the Owner's dashboard until the key steps are done. Suggested next reads:

- [Key concepts](concepts.md)
- [Backups & upgrades](../operations/backups-and-upgrades.md)
- The [feature guides](../README.md#feature-guides) for what you enabled
