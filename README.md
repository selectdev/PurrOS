# PurrOS

**An open-source, self-hosted ERP for growing businesses, built around an integration API.**

PurrOS covers the day-to-day running of a business of 20–500 people, whether it has one site or many: employees, scheduling, time and attendance, cash, inventory, ordering, sales, checklists, equipment, team communication and reporting. It is built so the tools you already use (HR/employment platforms, point-of-sale, timeclocks, inventory scanners) can connect to it in an afternoon instead of a quarter.

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](LICENSE)
![Status: pre-alpha](https://img.shields.io/badge/status-pre--alpha-orange)

> **Project status:** PurrOS is in early development and not yet released. The **Go API server** covers every feature area below: about 235 endpoints for people, time, scheduling, inventory, purchasing, sales, cash, checklists, equipment, communication and reports, plus the organization structure, sign-in, roles, the Employee Area API, file uploads, email, signed webhooks with a delivery log and replay, integration management, feature switches, backups and an admin CLI. The **web app is not built yet**, so today you use PurrOS through the API and the `purros` CLI. Items marked *(planned)* below aren't built. See [api/README.md](api/README.md) for exactly what's implemented.

---

## Why PurrOS

Most ERP systems fall into one of two groups: enterprise suites that take months to roll out, or spreadsheets that fall apart after the tenth employee. PurrOS is aimed at the space in between.

- **API-first.** Anything you can do in the UI, you can do through a versioned REST API. Every change emits a signed webhook.
- **Built to integrate.** PurrOS has no hard-coded vendor integrations. You connect employment management software, timeclocks or inventory systems by writing a small integration against the public API, with a typed SDK and starter template to help (both planned; today, any OpenAPI client works).
- **Self-hosted.** Runs on your own infrastructure with Docker Compose. Your data stays with you.
- **Mid-range by design.** Includes what a growing business needs and leaves out what it doesn't. See [PRODUCT.md](PRODUCT.md) for scope and non-goals.
- **Open source.** Licensed under AGPL-3.0.

## Who it's for

PurrOS isn't built for one industry. It suits any business with staff, stock, cash or several locations:

| Kind of business | Typical use |
|---|---|
| Retail and multi-store chains | Store scheduling, till and deposit control, stock counts, transfers between stores |
| Restaurants, cafés and hospitality | Labor forecasting from sales, waste tracking, opening/closing and safety checklists |
| Franchise and multi-unit operators | District/regional hierarchy, cross-location reports, standard checklists everywhere |
| Clinics, salons, gyms and service businesses | Rosters by skill or certification, supplies usage, equipment maintenance |
| Distribution, wholesale and light manufacturing | Warehouse stock, purchasing, receiving, shift work |
| Field service and facilities | Crews, vehicles and equipment, inspections with photos, parts inventory |

Industry words are configurable. A *location* can be called a store, branch, site, clinic or warehouse, and ready-made templates (checklists, roles, reports) are starting points you can edit.

## Features

> The API implements each area below; screens for them arrive with the web app. Individual capabilities that aren't built yet are marked *(planned)*. See the [roadmap in PRODUCT.md](PRODUCT.md#9-roadmap).

| Area | What it covers |
|---|---|
| **Organization & Locations** | Company → region → district → location hierarchy (org units), location profiles |
| **People & HR** | Employee records, positions, skills and certifications, documents, onboarding |
| **Scheduling & Forecasting** | Demand forecasting, automatic schedules, availability, shift swaps, open shifts, labor rules |
| **Time & Attendance** | Kiosk timeclock and timeclock integrations, schedule-aware punching, breaks, timesheets, payroll export |
| **Cash Management** | Drawer/till counts, safe drops, safe counts, bank deposits, over/short, card and digital payment reconciliation |
| **Inventory** | Real-time stock, mobile counts, waste, transfers, usage recipes, actual vs expected usage, cost of goods |
| **Purchasing & Ordering** | Suggested orders, supplier catalogs, purchase orders, receiving, invoice matching |
| **Sales** | POS/e-commerce sales feeds, customers, sales orders, fulfilment, invoicing |
| **Forms, Checklists & Compliance** | Form builder, scheduled checklists, audits and inspections, sensor readings, corrective actions |
| **Equipment & Assets** | Asset register, preventive maintenance, repair tickets, warranty and lifecycle tracking |
| **Communication** | Announcements, company calendar; group and direct messages, shared files and links *(planned)* |
| **Team Displays** | Screens in the workplace showing goals, live KPIs, recognition and shift reminders |
| **Reports & Insights** | Dashboards, multi-location rollups, scheduled reports, alerts, recommended actions |
| **Employee Area** | Self-service portal where each employee sees and manages their own data |
| **Platform** | Accounts and roles, API keys, webhooks, audit log, integrations |

### Every feature is optional

Use only what your business needs. The **Owner** can switch any feature area above, and many smaller features inside them, on or off with `purros features enable|disable` (and under **Settings → Features** once the web app ships). A disabled feature is **gone completely**, not just hidden from one menu:

- **Not in the interface** *(with the web app)*. It disappears from navigation, search, the command palette, dashboards, reports, the Employee Area, Team Displays, notifications and settings pages.
- **Not in roles.** Its permissions disappear from the role editor, so nobody can be granted access to it.
- **Not in the API.** Its endpoints respond `404` with the code `feature_disabled`, it's removed from the OpenAPI spec, its webhook events stop firing and can't be subscribed to, and integrations can't request its scopes.
- **Not running.** Its background jobs, alerts and scheduled reports stop.

**Only the platform core is always on**: sign-in and accounts, roles, locations, settings, the audit log, and the API and webhook infrastructure.

**Smaller features can be switched off too**, for example the kiosk timeclock, photo on clock-in, shift swaps, estimated pay, payslips, gamification on Team Displays, direct messages, or the AI assistant.

**Dependencies are handled for you.** Some features build on others. Scheduling needs People and Time & Attendance, and suggested orders need Inventory and Purchasing. Turning a feature on offers to turn on what it needs. Turning one off lists the features that depend on it and turns those off as well, after you confirm.

**Your data is kept.** Turning a feature off doesn't delete anything. Turn it back on and everything is where you left it. If you want the data gone, the Owner will be able to export it and then permanently delete it as a separate, confirmed action *(planned: `purros features purge`)*.

Every change is recorded in the audit log. *(Planned: first-run setup that asks what kind of business you run and suggests a starting set of features.)*

### Organization & Locations

- **Flexible hierarchy.** Model the business as it is: company → regions → districts → locations, with as many levels as you need, named however you like. The hierarchy drives what people can see, how reports roll up and where settings apply.
- **Location profiles.** Address, timezone, currency, business-day cut-off, and departments or work areas. Opening hours *(planned)*.
- **Settings that inherit** *(planned)*. Set something once at company or region level and let locations inherit it, with local overrides where allowed.

### People & HR

- Employee records: personal and contact details, employment type and status, positions, pay rates with history, managers, and custom fields (free-form today; defined field types are planned).
- **Skills and certifications** (e.g. forklift licence, first aid, food handling, barista, cash handling) with expiry dates. The scheduler uses them. Reminders before they expire *(planned)*.
- **Documents**: contracts, IDs and certificates stored per employee, with expiry dates and a choice of what the employee can see.
- **Onboarding checklists** *(planned)*: collect documents, policy acknowledgments and training for new hires. Recruiting and applicant tracking stay in your own tools, connected through an integration.
- **Rehire, transfer and termination** workflows that keep history intact.

### Scheduling & Forecasting

- **Demand forecasting.** PurrOS forecasts the numbers that drive staffing, such as sales, transactions, foot traffic, orders, appointments or units shipped. It learns from your history, seasonality and holidays, and you can adjust the forecast by hand.
- **Staffing rules.** Turn the forecast into needed hours with rules such as "1 cashier per 40 transactions an hour" or "2 technicians per 10 appointments", plus fixed minimum coverage per area.
- **Automatic schedule builder** *(planned)*. Generates shifts to match demand and assigns the best-fitting people based on availability, skills, certifications, target hours, labor cost and fairness. Managers then adjust it with drag and drop.
- **Gap and overstaffing view.** Scheduled versus needed staff for each hour (`GET /staffing-needs`; graphs arrive with the web app) show where the schedule is short or over, suggest who could fill a gap, and point out skills that would give more coverage if people were trained.
- **Availability, time off and preferences** entered by employees and respected by the scheduler.
- **Shift swaps, open shifts and pick-ups**, with manager approval rules.
- **Labor rules engine.** Configurable rules per location or jurisdiction: breaks and meal periods, maximum hours, minimum rest between shifts, minor work restrictions, overtime thresholds, and overtime thresholds. Advance-notice (predictive scheduling) rules with premium-pay flags are planned. PurrOS applies the rules you configure. It does not give legal advice.
- **Schedule cost and budget.** Projected labor cost and labor as a percentage of sales, shown while you build the schedule.
- **Publish and notify.** Publishing emits `schedule.published` and `shift.changed` webhooks. Employee notifications and shift reminders *(planned)*.

### Time & Attendance

- **Kiosk timeclock** *(planned)*. A built-in, browser-based timeclock for a shared tablet or PC at each location. Staff clock in with a badge, QR code or personal clock code, with an optional photo. A clock code only records punches and never gives access to anyone's data.
- **Any other timeclock** (hardware terminals, mobile apps, access-control systems) can send punches through the API.
- **Schedule-aware punching** *(planned)*. Early clock-ins, late clock-outs and unscheduled shifts are blocked or flagged unless a manager approves them, which cuts unplanned overtime and punch abuse.
- **Breaks and attestations** *(planned)*. Break reminders and enforcement follow the labor rules, and staff can confirm at clock-out that they took their breaks.
- **Timesheets.** Built automatically from punches, with exception alerts (missed punches, missed breaks, overtime risk), manager then payroll approval, and pay period locking.
- **Payroll export** to any payroll provider as CSV (`GET /pay-periods/{id}/export`) or through an integration, including regular, overtime, holiday and premium hours by job code and cost center.

### Cash Management

For any business that takes cash or card payments at a counter:

- **Register-to-bank workflow.** Guided steps for opening, shift-change and closing drawer/till counts, cash skims and safe drops, safe counts, change orders and bank deposits.
- **Over/short tracking** per drawer, shift and employee, with thresholds that alert a manager.
- **Deposit verification.** Record deposit bag numbers and amounts, then match them against bank data from an integration or a statement import. Unmatched or late deposits are flagged.
- **Non-cash payment reconciliation.** Compare card, digital wallet, gift card and third-party platform totals in the POS with what the processor actually settled.
- **Paid-outs and petty cash** *(planned)* with receipt photos and approval limits.
- **Controls.** A full audit trail from the drawer to the bank. Blind counts and two-person verification for large amounts *(planned)*.
- Expected cash and sales totals come from your POS through an integration (`POST /api/v1/sales-summaries`).

### Inventory

- **Real-time stock.** Every receipt, sale, transfer, waste entry and count updates stock immediately through an append-only stock ledger, so you can see stock on hand and cost of goods sold at any moment without waiting for a month-end count.
- **Counts.** Counts can be daily, weekly, monthly or spot counts of chosen items. Counting by storage area on a phone with barcode scanning arrives with the web app.
- **Waste and shrink.** Log waste, spoilage, damage, theft and samples with reason codes and photos.
- **Transfers between locations** that the sending location ships and the receiving location confirms, with any differences flagged.
- **Usage recipes.** Define what one sold product or service uses (a meal uses its ingredients, a haircut uses product, a repair uses parts). PurrOS then calculates **expected (theoretical) usage** from sales and compares it with **actual usage** from counts.
- **Gain/loss and opportunity reports** *(planned)*. See which items, locations and periods lose the most money to variance, and by how much.
- **Batch and expiry tracking** *(planned)* for perishable or regulated items (optional per item).
- Multiple locations and warehouses, and weighted average costing. Bin locations and unit-of-measure conversions *(planned)*.

### Purchasing & Ordering

- **Suggested orders.** Calculated from par levels, current stock, forecast demand, what's already on order and each supplier's delivery schedule. The manager reviews and adjusts before sending.
- **Supplier catalogs and order guides** with pack sizes, prices, minimum order quantities and order cut-off days.
- **Purchase orders** with approval thresholds, sent by email or through a supplier integration.
- **Receiving**, with short, damaged and substituted items recorded.
- **Invoice matching.** Check the supplier's invoice against the PO and the goods received, and flag price changes.

### Sales

- **Sales feeds** from any POS or e-commerce platform (daily totals, hourly figures, or individual transactions) through the API. These feed forecasting, cash, inventory usage and reports.
- Customers and sales orders with stock reservation. Price lists and quotes *(planned)*.
- Pick, pack and ship, basic invoicing (PDF) and payment status, exportable to accounting.

### Forms, Checklists & Compliance

- **Form builder.** Turn any paper process into a digital form, with question types such as checkboxes, choices, numbers with allowed ranges, temperatures, text, photos, signatures, date and time, and item or asset pickers.
- **Scheduled checklists** *(planned)*. Opening and closing routines, hourly or per-shift checks, weekly cleaning, monthly safety walks, all assigned to a role, person or location and completed on a phone or tablet.
- **Real-time compliance alerts.** Failed answers (e.g. a fridge above 5 °C, a missing fire extinguisher) raise a `form.answer_failed` event immediately. Overdue-checklist alerts *(planned)*.
- **Corrective actions.** A failed item creates a follow-up task with an owner, a due date and photo evidence of the fix.
- **Audits and inspections** with scoring, such as district manager visits, health and safety audits, brand standards and vehicle inspections. Scores can be compared across locations.
- **Sensor readings.** Temperature, humidity or meter sensors can send readings through the API, which fill in checks automatically and raise alerts when readings are out of range.
- **Other uses:** employee evaluations, incident reports and customer complaint logs.
- A **template library** *(planned)* to start from: opening/closing, cleaning logs, food safety (HACCP), workplace safety, equipment checks and more.

### Equipment & Assets

- **Asset register** per location: make, model, serial number, purchase date, cost, warranty, supplier and service provider, and QR code labels that open the asset's page when scanned *(planned)*.
- **Preventive maintenance** schedules by time or usage, which create tasks automatically.
- **Repair tickets.** Staff report a problem with photos from their phone. The ticket is assigned to a technician or outside provider and tracked to completion.
- **History and cost.** Downtime, repairs and maintenance cost over each asset's lifetime help decide when to repair and when to replace.

### Communication

- **Announcements** to the whole company, a region, a location or a role, with read receipts and required acknowledgment for policies.
- **Messaging** *(planned)*: group chats (by location, team or custom group) and one-to-one messages. Managers can reach staff without sharing personal phone numbers.
- **Company calendar** for events, deadlines, deliveries, inspections and visits, which can be filtered by location.
- **Shared files and links** *(planned)*: a library of manuals, policies, training material and useful links, visible according to role and location.
- **Notifications** by email, web push, and SMS or other channels through an integration *(planned; today PurrOS emails invitations, sign-in links and password resets)*.

### Team Displays

Turn any screen in the workplace (a TV with a streaming stick, a smart TV browser, a tablet on the wall) into a live team board:

- Today's goals and live KPIs (e.g. sales vs target, service times, orders shipped, checklist completion).
- **Recognition and gamification**: shout-outs and display metrics today; leaderboards, team challenges and fundraising drives *(planned)*.
- Announcements, upcoming shifts and shift reminders, who's on shift, and celebrations such as work anniversaries (opt-in, planned).
- Screens are set up with a one-time pairing code *(planned)*, and each display shows only the information its display profile allows.

### Reports & Insights

- **Dashboards per role** *(planned)* that update close to real time: a supervisor sees their team, a district manager their district, an owner the whole company.
- **Multi-location rollups** by the organization hierarchy, with location rankings and comparisons against last week, last year, forecast and budget.
- **Built-in KPIs**: sales, labor cost and labor as a percentage of sales, sales per labor hour, overtime, cash over/short, cost of goods and variance, waste, checklist compliance, equipment downtime, and staff turnover.
- **Alerts** when a KPI crosses a threshold. **Scheduled reports** delivered by email *(planned)*.
- Seven **built-in reports** with CSV export, all available through the API. **Custom report builder** and Excel export *(planned)*.
- **Recommended actions.** PurrOS ranks today's biggest issues across your locations (e.g. overtime risk, cash shortages, missed checklists, high waste), explains why each was flagged using your own data, and suggests a next step and an owner. This works on rules out of the box. An **optional AI assistant** *(planned)* can be enabled with a model provider you choose, including self-hosted models. It is off by default, and no data leaves your server unless you configure it to.

## Employee Area

Every employee gets their own sign-in to the **Employee Area**, a self-service portal that shows everything PurrOS holds about them. Its API (`/api/v1/me/…`) is built; the screens arrive with the web app and will work on phones as well as desktops, so staff without a work computer can check their hours from anywhere.

### What employees can see

| Section | What's shown |
|---|---|
| **My schedule** | Upcoming shifts with location and role, open shifts they can pick up, and swap requests |
| **My time** | Every clock-in/out punch with its source (which timeclock or device), shifts, daily and weekly totals, overtime, and timesheet status (pending, approved, locked) |
| **My pay** | Current pay rate and rate history, an **estimated gross pay** per pay period (approved hours × rate, including overtime), and **payslips** when a payroll integration sends them in |
| **Time off** | Leave balances, accrual history, and past and upcoming requests |
| **My profile** | Personal and contact details, emergency contacts, position, department, manager, location, start date |
| **My documents** | Contracts, certifications and other files shared with them, with expiry dates |
| **My tasks** *(planned)* | Checklists and forms assigned to them, onboarding steps, and policies to acknowledge |
| **Messages** | Announcements to acknowledge; team chats and direct messages *(planned)* |
| **Activity** | A log of changes made to their record: who changed what, and when |

Estimated pay is clearly labelled as an estimate before taxes and deductions, because PurrOS does not run payroll itself. Payslips are the payroll provider's documents, pushed into PurrOS by an integration (`POST /api/v1/payslips`, `payroll:write` scope).

### What employees can do

- **Request punch corrections.** Flag a missed or wrong punch and give a reason. The manager approves or rejects it. The original punch is never overwritten, and the correction is kept in the audit log.
- **Request time off.** Submit requests against their balances and follow the approval status.
- **Set availability and manage shifts.** Enter when they can work, pick up open shifts, and offer or swap shifts with co-workers, subject to manager approval.
- **Complete tasks** *(planned)*. Fill in assigned checklists and forms, report equipment problems, and acknowledge policies.
- **Update contact info.** Phone, address and emergency contacts can be changed directly. Sensitive fields such as legal name or bank details stay with HR.
- **Export my data.** Download a complete copy of everything PurrOS stores about them (profile, punches, timesheets, pay, time off, documents, audit history) as JSON and CSV in a ZIP file.

Employees only ever see their own data. They never see co-workers' records, and managers see only their own team.

## Authentication

PurrOS uses one account system for everyone who signs in: owners, admins, HR, managers, supervisors and employees. What each person can see and do depends on their **role**, not on a separate login. Software such as integrations and scripts authenticates with **API keys**.

### Sign-in methods for people

| Method | Details |
|---|---|
| **Email + password** | Accounts are created by invitation. Passwords are hashed with Argon2id and must pass a strength policy. Resetting a password uses a single-use, time-limited email link. |
| **Single sign-on (SSO)** *(planned)* | OpenID Connect (Google Workspace, Microsoft Entra ID, Okta, Keycloak, Authentik and others) and SAML 2.0. Admins can require SSO for everyone, and can create and deactivate accounts automatically from the identity provider's user list. |
| **Passkeys** *(planned)* | Passwordless sign-in with Face ID, Touch ID, Windows Hello or a hardware security key (WebAuthn). |
| **Magic link** | A one-time sign-in link sent by email, valid for 15 minutes. |

Admins choose which methods are enabled (`PATCH /api/v1/settings/authentication`, and **Settings → Authentication** in the web app).

### Two-factor authentication

2FA is **optional** for every user and is off by default. Users can turn it on from their profile with an authenticator app (TOTP), or a passkey once passkeys ship, and they get one-time recovery codes. An Owner can make 2FA mandatory for chosen roles or for the whole company.

### Sessions

- Sessions are stored server-side and linked by a secure, `httpOnly`, `SameSite=Lax` cookie.
- Idle and absolute session timeouts can be configured. Sessions are signed out on password change.
- Users can see their active sessions and devices and sign them out. Admins can sign out any user.
- Sign-in attempts are rate-limited, and repeated failures lock the account for a short time.
- Sign-ins, failures, 2FA changes and password resets are all recorded in the audit log.

### Roles and permissions

Every account has **one role**, and each organization defines its **own roles** to match how it is structured: HR, Payroll, District Manager, Store Manager, Supervisor, Warehouse Lead, or anything else. Each role carries its own set of **permissions**, so two roles never have to share the same access.

**How roles work**

- **Roles are yours.** Create, rename, edit and delete roles (`/api/v1/roles`, and **Settings → Roles** in the web app). PurrOS doesn't hard-code job titles.
- **Permissions are fixed and fine-grained.** PurrOS defines the list of permissions (e.g. `employees.read`, `pay.read`, `timesheets.approve`, `inventory.adjust`, `purchase_orders.approve`, `roles.manage`), and a role is simply a chosen set of them. The full list is shown in the role editor and at `GET /api/v1/permissions`.
- **Each permission has a reach.** When you add a permission to a role, you also choose how far it reaches:
  - **Own team:** the person's direct and indirect reports
  - **Assigned locations** (or whole regions and districts in the hierarchy, including locations added to them later)
  - **Assigned departments**
  - **Everyone**

  The account then says *which* locations or departments it covers. This lets one "District Manager" role serve every district, with each account assigned its own stores.
- **Everyone keeps their Employee Area.** Any account linked to an employee record can always see its own data, whatever its role. Roles only add access to other people's data and to company operations.
- **Two system roles.** **Owner** has every permission and can't be edited or deleted, and at least one Owner must exist. **Employee** is the default role for new accounts and has no extra permissions. You can choose a different default.

**Example setup** (for a multi-location business)

| Role | Sample permissions | Reach |
|---|---|---|
| HR | `employees.read`, `employees.write`, `employees.sensitive.read`, `documents.manage`, `time_off.approve` | Everyone |
| Payroll | `timesheets.read`, `pay_periods.lock`, `pay.read`, `pay.write`, `payroll.export` | Everyone |
| District Manager | `employees.read`, `schedules.read`, `timesheets.approve`, `cash.read`, `inventory.read`, `audits.conduct`, `reports.read` | Assigned district |
| Store / Site Manager | `employees.read`, `schedules.manage`, `timesheets.approve`, `punches.correct`, `cash.manage`, `orders.create`, `checklists.manage`, `equipment.manage` | Assigned locations |
| Supervisor | `employees.read`, `schedules.read`, `timesheets.approve`, `time_off.approve`, `cash.count`, `checklists.complete` | Own team |
| Warehouse Lead | `inventory.read`, `inventory.adjust`, `stock_counts.manage`, `goods_receipts.create` | Assigned locations |

New installs start with a few roles like these as editable starting points. You can change or delete any of them.

**Safeguards**

- **No privilege escalation.** A user can only create or assign roles whose permissions they hold themselves, and only within their own reach. Only an Owner can grant `roles.manage`.
- **Changes apply immediately.** Editing a role takes effect at the account's next request, with no need to sign out.
- **Everything is audited.** Every change to roles, permissions and role assignments is written to the audit log, with the before and after state.
- **Sensitive data needs explicit permission.** Pay, bank details and national IDs have their own permissions (`pay.read`, `employees.sensitive.read`). Seeing an employee's record doesn't include them.

### API authentication

| Client | How it authenticates |
|---|---|
| **Integrations** | A scoped API key issued when the integration is registered (see [Integrations](#integrations)). Webhooks sent to the integration are signed with its own secret. |
| **Scripts and personal tools** | Personal API keys, available to roles with the `api_keys.personal` permission. A personal key carries the same role permissions and reach as the user who created it, never more. |

API keys are sent as `Authorization: Bearer <key>`. Only a hash is stored and the key is shown once. Keys can be given an expiry date, rotated or revoked at any time, and each key's last-used time and IP address are recorded (`purros api-keys list`).

## Tech stack

- **API:** [Go](https://go.dev/): one small binary (`purros`) that serves the REST API, runs the background worker and provides the admin CLI
- **Database:** **PostgreSQL 16**, which also holds the job queue, so no message broker is needed
- **Web app:** **[Next.js](https://nextjs.org/)**, **TypeScript** and **[Tailwind CSS](https://tailwindcss.com/)**, as a separate client of the API (planned)
- **Redis:** optional, only to share rate limits across several API containers
- **SMTP** for email (any provider) and **S3-compatible object storage** for files (or a local volume)

For the architecture, data model and API conventions, see [DESIGN.md](DESIGN.md).

---

## Quick start (self-hosted)

### Requirements

- Docker 24+ and Docker Compose v2
- 2 vCPU / 4 GB RAM minimum (fine for about 100 employees and moderate inventory volume)

### 1. Get the code

```bash
git clone https://github.com/selectdev/PurrOS.git
cd PurrOS
cp config/purros.env.example config/purros.env
cp config/postgres.env.example config/postgres.env
```

### 2. Configure

Edit `config/purros.env` and set at least the following (and put the same database password in `config/postgres.env`):

```dotenv
# Public URL where PurrOS will be reachable
PURROS_URL=https://erp.example.com

# Generate with: openssl rand -base64 32
PURROS_SECRET=change-me

DATABASE_URL=postgres://purros:CHANGE-ME@db:5432/purros?sslmode=disable

# Email via any SMTP server (invitations, sign-in links, notifications, reports)
SMTP_HOST=smtp.your-provider.example
SMTP_PORT=587
SMTP_USER=...
SMTP_PASSWORD=...
SMTP_FROM="Acme Operations <ops@acme.example>"

# File storage: "local" (Docker volume) or "s3" (AWS S3, MinIO, R2, Backblaze, Wasabi…)
STORAGE_DRIVER=s3
STORAGE_S3_BUCKET=acme-purros-files
STORAGE_S3_REGION=eu-central-1
STORAGE_S3_ENDPOINT=              # leave empty for AWS
STORAGE_S3_ACCESS_KEY_ID=...
STORAGE_S3_SECRET_ACCESS_KEY=...

# Nightly database backups into the "state" volume
PURROS_BACKUP_DIR=/var/lib/purros/backups
PURROS_BACKUP_PASSPHRASE=         # optional: encrypt backups
```

Email and S3 are optional but recommended for production. See [Email & file storage](docs/getting-started/email-and-storage.md) for setup, testing and moving existing files to S3.

### 3. Start

```bash
docker compose up -d                       # migrations run automatically on start
docker compose exec api purros setup --company "Acme Coffee" --owner-email you@example.com --timezone America/Chicago
docker compose exec api purros locations create --name "Store 101" --external-id 101 --timezone America/Chicago --cutoff 04:00
docker compose exec api purros doctor
```

Then register your first integration (see [Integrations](#integrations)) and open `PURROS_URL/api/v1/openapi.json` to explore the API.

### Services

| Service | Purpose |
|---|---|
| `api` | The `purros` Go binary: REST API at `/api/v1` and the background worker (webhooks, email, KPI alerts, nightly backups) |
| `db` | PostgreSQL 16: all data and the job queue |
| `redis` | Optional (`--profile redis`): shared rate limits when you run several `api` containers |
| `web` | Next.js web app (planned) |

Put a reverse proxy (Caddy, Traefik, nginx) in front of `api` for TLS.

### Upgrading

```bash
git fetch --tags && git checkout <new release tag>
docker compose build && docker compose up -d   # migrations run automatically
docker compose exec api purros doctor
```

Take a backup before every upgrade (`docker compose exec api purros backup create`). See [Backups & upgrades](docs/operations/backups-and-upgrades.md).

---

## API at a glance

The API's main job is **bringing data into PurrOS** from the other systems a business runs on: point-of-sale systems, online stores, delivery platforms, timeclocks and HR tools. An integration reads data from those services and sends it to PurrOS, which then uses it for inventory, cash, forecasting, scheduling and reports.

All endpoints live under `/api/v1`, use JSON, and authenticate with a bearer API key: an integration's key from `purros integrations register`, or a personal key from `POST /api/v1/auth/api-keys`.

### Bringing in data from POS systems and online stores

| Data from the POS or online store | Endpoint | What PurrOS uses it for |
|---|---|---|
| Sales transactions (items, quantities, prices, discounts, taxes, tenders, refunds, voids) | `POST /api/v1/sales/transactions:batch` | Stock deduction via usage recipes, expected vs actual usage, reports |
| Sales totals by day or hour (net sales, transaction count, guests, orders) | `POST /api/v1/sales-summaries` | Forecasting and scheduling, labor % of sales, dashboards |
| Tender totals per drawer or shift (cash, card, gift card, third-party platforms) | `POST /api/v1/cash/tenders` | Expected cash for drawer counts, card and platform reconciliation |
| Processor and platform settlements | `POST /api/v1/cash/settlements` | Checking that card and online payments were actually paid out |
| Online orders to fulfil | `PUT /api/v1/sales-orders/external/{externalId}` | Stock reservation, pick/pack/ship, invoicing |
| Product catalog (products, variants, SKUs, barcodes) | `PUT /api/v1/items/external/{externalId}` | Keeping PurrOS items matched to the POS or store catalog |
| Staff clock-ins recorded on the POS | `POST /api/v1/time/punches:batch` | Timesheets and payroll export |

Each record carries the source system's ID (`externalId`) and a `source` label (e.g. `pos:front-counter`, `web-store`), so re-sending the same data never creates duplicates and every figure can be traced back to where it came from. Batches accept up to 1,000 records and report the result for each one. Late or corrected data (refunds, voids, end-of-day corrections) is accepted and recalculates the affected reports.

```bash
# Send POS transactions
curl -X POST https://erp.example.com/api/v1/sales/transactions:batch \
  -H "Authorization: Bearer pk_live_..." \
  -H "Content-Type: application/json" \
  -d '{
    "source": "pos:front-counter",
    "transactions": [{
      "externalId": "txn-88213",
      "locationId": "loc_01H...",
      "occurredAt": "2026-09-27T12:41:07Z",
      "lines": [{ "itemSku": "LATTE-12", "quantity": "2", "unitPrice": "4.50", "discount": "0.00" }],
      "tenders": [{ "type": "card", "amount": "9.00" }],
      "tax": "0.72",
      "total": "9.72"
    }]
  }'
```

An integration can collect this data however suits the source system: by listening to the store's or POS's own webhooks, by polling its API every few minutes, or by importing an end-of-day export file.

### Other examples

```bash
# Create an employee
curl -X POST https://erp.example.com/api/v1/employees \
  -H "Authorization: Bearer pk_live_..." \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: 6f1c2a..." \
  -d '{"externalId":"hris-1042","firstName":"Dana","lastName":"Reyes","departmentId":"dep_01H..."}'

# Push timeclock punches in bulk
curl -X POST https://erp.example.com/api/v1/time/punches:batch \
  -H "Authorization: Bearer pk_live_..." \
  -H "Content-Type: application/json" \
  -d '{"source":"timeclock:lobby","punches":[{"employeeExternalId":"hris-1042","type":"in","at":"2026-09-27T08:02:11Z","deviceId":"lobby-01"}]}'

# Adjust stock
curl -X POST https://erp.example.com/api/v1/inventory/adjustments \
  -H "Authorization: Bearer pk_live_..." \
  -H "Content-Type: application/json" \
  -d '{"itemSku":"WID-001","locationId":"loc_01H...","quantity":"-3","reason":"damaged"}'
```

- **OpenAPI spec:** `GET /api/v1/openapi.json`, generated from the code and filtered by enabled features. Every endpoint is listed in the [endpoint index](docs/api/endpoints.md).
- **Webhooks (optional, outgoing):** when another system needs to hear about changes made in PurrOS, it can subscribe to events such as `employee.updated`, `pay_period.locked` or `stock.below_reorder_point`, either through an integration's manifest or as a standalone endpoint (`POST /api/v1/webhook-endpoints`). Payloads are signed with HMAC-SHA256, every delivery is logged, and missed ones can be replayed.
- **External IDs:** every core record accepts an `externalId`, so you can sync by your source system's ID without keeping a mapping table.

The full conventions (pagination, errors, idempotency, rate limits) are in [DESIGN.md → API](DESIGN.md#5-api-design).

## Integrations

PurrOS ships **no built-in integrations** for specific HR platforms, timeclocks, stores or payroll providers. To connect a system, you write an **integration**: a small program you run yourself that uses the PurrOS API and webhooks.

1. Use any language with an HTTP or OpenAPI client. *(A TypeScript SDK and integration template are planned.)*
2. Describe the integration in a manifest: which API scopes it needs and which webhook events it wants.
3. Register it with the API (`POST /api/v1/integrations`) or the CLI (`docker compose exec api purros integrations register --manifest purros-integration.json`). PurrOS checks the manifest, stores its config (secrets encrypted), and issues a scoped API key and a webhook signing secret.
4. Run it wherever you like: next to PurrOS in Docker Compose, as a serverless function, or as a cron job.
5. Manage it from then on through the same API: see its health, logs and ingestion batches, install new versions of its manifest, rotate its key without downtime, pause it or remove it. *(A **Settings → Integrations** page comes with the web app.)*

With the planned SDK, an integration will look like this:

```ts
import { PurrOS, verifyWebhook } from "@purros/sdk";

const purros = new PurrOS({ baseUrl: process.env.PURROS_URL, apiKey: process.env.PURROS_INTEGRATION_KEY });

// Forward punches from your timeclock
await purros.time.punches.batch([
  { employeeExternalId: "hris-1042", type: "in", at: new Date().toISOString(), deviceId: "lobby-01" },
]);
```

Integrations can't reach the database or PurrOS internals, so a buggy integration can't take the ERP down. See [DESIGN.md → Integrations](DESIGN.md#7-integrations).

---

## Local development

```bash
# Requirements: Go 1.26+, Docker (or a local PostgreSQL 16)
docker compose -f docker-compose.dev.yml up -d          # PostgreSQL on localhost:5432
cd api
export DATABASE_URL=postgres://purros:purros@localhost:5432/purros?sslmode=disable
export PURROS_SECRET=$(openssl rand -base64 32)
export PURROS_STATE_DIR=../state                          # uploaded files and backups
go run ./cmd/purros setup --company "Dev Co" --owner-email dev@example.com
go run ./cmd/purros locations create --name "Store 101" --external-id 101
go run ./cmd/purros serve                                 # http://localhost:8080
```

| Command (in `api/`) | Description |
|---|---|
| `go test ./...` | Unit tests; add `PURROS_TEST_DATABASE_URL=postgres://purros:purros@localhost:5432/postgres?sslmode=disable` to run the API integration tests too |
| `go vet ./...` and `gofmt -l .` | Static checks and formatting |
| `go run ./cmd/purros help` | All CLI commands |

See the [development guide](docs/development/README.md).

## Documentation

The full documentation is in [`docs/`](docs/README.md):

- [Getting started](docs/README.md#getting-started): installation, configuration, first-run setup, key concepts
- [Administration](docs/README.md#administration): organization, roles and permissions, authentication, feature switches
- [Feature guides](docs/README.md#feature-guides): one guide per feature, including the Employee Area
- [API](docs/api/README.md), [data ingestion](docs/api/data-ingestion.md) and [webhooks](docs/api/webhooks.md)
- [Integrations](docs/integrations/README.md): how to connect a POS, online store, timeclock, HR or payroll system
- [Operations](docs/README.md#operations): backups, upgrades, monitoring, security, CLI

## Project documents

- [PRODUCT.md](PRODUCT.md): vision, target users, scope, non-goals, roadmap
- [DESIGN.md](DESIGN.md): architecture, data model, API design, UI system, operations
- [api/README.md](api/README.md): the Go API server, what's implemented, and how to run it

## Contributing

Contributions are welcome. Before you start:

1. Read [PRODUCT.md](PRODUCT.md) to check that the change fits the scope.
2. For anything larger than a bug fix, open an issue to discuss it first.
3. Keep PRs focused, include tests, and make sure `gofmt -l .`, `go vet ./...` and `go test ./...` (with `PURROS_TEST_DATABASE_URL` set) pass in `api/`.
4. API changes must update the OpenAPI spec and must not break `/api/v1` (see the versioning policy in DESIGN.md).

## Security

Please don't report vulnerabilities in public issues. Use GitHub's private vulnerability reporting ("Report a vulnerability" under the Security tab).

## License

PurrOS is licensed under the [GNU Affero General Public License v3.0](LICENSE). You can use, modify and self-host it freely. If you offer a modified version as a network service, you must release your modifications under the same license.
