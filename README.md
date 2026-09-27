# PurrOS

**An open-source, self-hosted ERP for growing businesses, built around an integration API.**

PurrOS covers the core of running a business of 20–500 people: employees, time and attendance, inventory, and purchasing and sales. It is built so the tools you already use (HR/employment platforms, timeclocks, inventory scanners) can connect to it in an afternoon instead of a quarter.

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](LICENSE)
![Status: pre-alpha](https://img.shields.io/badge/status-pre--alpha-orange)

> **Project status:** PurrOS is in early development. The APIs, schema and install steps below describe the planned v1 and may change before the first tagged release.

---

## Why PurrOS

Most ERP systems fall into one of two groups: enterprise suites that take months to roll out, or spreadsheets that fall apart after the tenth employee. PurrOS is aimed at the space in between.

- **API-first.** Anything you can do in the UI, you can do through a versioned REST API. Every change emits a signed webhook.
- **Built to integrate.** PurrOS has no hard-coded vendor integrations. You connect employment management software, timeclocks or inventory systems by writing a small integration against the public API, with a typed SDK and starter template to help.
- **Self-hosted.** Runs on your own infrastructure with Docker Compose. Your data stays with you.
- **Mid-range by design.** Includes what a growing business needs and leaves out what it doesn't. See [PRODUCT.md](PRODUCT.md) for scope and non-goals.
- **Open source.** Licensed under AGPL-3.0.

## Modules (v1)

| Module | What it covers |
|---|---|
| **People** | Employee records, departments, positions, managers, employment status, custom fields |
| **Time & Attendance** | Clock-in/out punches (from any timeclock), shifts, timesheets, approvals, overtime rules, payroll export |
| **Inventory** | Items and SKUs, units of measure, warehouses and bin locations, stock levels, transfers, adjustments, stock counts |
| **Purchasing** | Suppliers, purchase orders, goods receipts |
| **Sales** | Customers, sales orders, fulfilment, basic invoicing |
| **Employee Area** | Self-service portal where each employee sees and manages their own data |
| **Platform** | Users, roles and permissions, API keys, webhooks, audit log, integrations |

## Employee Area

Every employee gets their own sign-in to the **Employee Area**, a self-service portal that shows everything PurrOS holds about them. It works on phones as well as desktops, so staff without a work computer can check their hours from anywhere.

### What employees can see

| Section | What's shown |
|---|---|
| **My time** | Every clock-in/out punch with its source (which timeclock or device), shifts, daily and weekly totals, overtime, and timesheet status (pending, approved, locked) |
| **My pay** | Current pay rate and rate history, an **estimated gross pay** per pay period (approved hours × rate, including overtime), and **payslips** when a payroll integration sends them in |
| **Time off** | Leave balances, accrual history, and past and upcoming requests |
| **My profile** | Personal and contact details, emergency contacts, position, department, manager, location, start date |
| **My documents** | Contracts, certifications and other files shared with them, with expiry dates |
| **Activity** | A log of changes made to their record: who changed what, and when |

Estimated pay is clearly labelled as an estimate before taxes and deductions, because PurrOS does not run payroll itself. Payslips are the payroll provider's documents, pushed into PurrOS by an integration (`POST /api/v1/payslips`, `payroll:write` scope).

### What employees can do

- **Request punch corrections.** Flag a missed or wrong punch and give a reason. The manager approves or rejects it. The original punch is never overwritten, and the correction is kept in the audit log.
- **Request time off.** Submit requests against their balances and follow the approval status.
- **Update contact info.** Phone, address and emergency contacts can be changed directly. Sensitive fields such as legal name or bank details go to HR for approval.
- **Export my data.** Download a complete copy of everything PurrOS stores about them (profile, punches, timesheets, pay, time off, documents, audit history) as JSON and CSV in a ZIP file.

Employees only ever see their own data. They never see co-workers' records, and managers see only their own team.

## Authentication

PurrOS uses one account system for everyone who signs in: owners, admins, HR, managers, warehouse staff and employees. What each person can see and do depends on their **role**, not on a separate login. Software such as integrations and scripts authenticates with **API keys**.

### Sign-in methods for people

| Method | Details |
|---|---|
| **Email + password** | Accounts are created by invitation. Passwords are hashed with Argon2id and can optionally be checked against known-breached password lists. Resetting a password uses a single-use, time-limited email link. |
| **Single sign-on (SSO)** | OpenID Connect (Google Workspace, Microsoft Entra ID, Okta, Keycloak, Authentik and others) and SAML 2.0. Admins can require SSO for everyone, and can create and deactivate accounts automatically from the identity provider's user list. |
| **Passkeys** | Passwordless sign-in with Face ID, Touch ID, Windows Hello or a hardware security key (WebAuthn). |
| **Magic link** | A one-time sign-in link sent by email, valid for 15 minutes. |

Admins choose which methods are enabled under **Settings → Authentication**.

### Two-factor authentication

2FA is **optional** for every user and is off by default. Users can turn it on from their profile with an authenticator app (TOTP) or a passkey, and they get one-time recovery codes. An Owner can make 2FA mandatory for chosen roles or for the whole company.

### Sessions

- Sessions are stored server-side and linked by a secure, `httpOnly`, `SameSite=Lax` cookie.
- Idle and absolute session timeouts can be configured. Sessions are signed out on password change.
- Users can see their active sessions and devices and sign them out. Admins can sign out any user.
- Sign-in attempts are rate-limited, and repeated failures lock the account for a short time.
- Sign-ins, failures, 2FA changes and password resets are all recorded in the audit log.

### Roles and permissions

Every account has **one role**, and each organization defines its **own roles** to match how it is structured: HR, Payroll, District Manager, Store Manager, Supervisor, Warehouse Lead, or anything else. Each role carries its own set of **permissions**, so two roles never have to share the same access.

**How roles work**

- **Roles are yours.** Create, rename, edit and delete roles under **Settings → Roles**. PurrOS doesn't hard-code job titles.
- **Permissions are fixed and fine-grained.** PurrOS defines the list of permissions (e.g. `employees.read`, `pay.read`, `timesheets.approve`, `inventory.adjust`, `purchase_orders.approve`, `roles.manage`), and a role is simply a chosen set of them. The full list is shown in the role editor and at `GET /api/v1/permissions`.
- **Each permission has a reach.** When you add a permission to a role, you also choose how far it reaches:
  - **Own team:** the person's direct and indirect reports
  - **Assigned locations**
  - **Assigned departments**
  - **Everyone**

  The account then says *which* locations or departments it covers. This lets one "District Manager" role serve every district, with each account assigned its own stores.
- **Everyone keeps their Employee Area.** Any account linked to an employee record can always see its own data, whatever its role. Roles only add access to other people's data and to company operations.
- **Two system roles.** **Owner** has every permission and can't be edited or deleted, and at least one Owner must exist. **Employee** is the default role for new accounts and has no extra permissions. You can choose a different default.

**Example setup**

| Role | Sample permissions | Reach |
|---|---|---|
| HR | `employees.read`, `employees.write`, `employees.sensitive.read`, `documents.manage`, `time_off.approve` | Everyone |
| Payroll | `timesheets.read`, `pay_periods.lock`, `pay.read`, `pay.write`, `payroll.export` | Everyone |
| District Manager | `employees.read`, `timesheets.approve`, `punches.correct`, `time_off.approve`, `inventory.read`, `reports.read` | Assigned locations |
| Supervisor | `employees.read`, `timesheets.approve`, `time_off.approve` | Own team |
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

API keys are sent as `Authorization: Bearer <key>`. Only a hash is stored and the key is shown once. Keys can be given an expiry date, rotated or revoked at any time, and each key's last-used time and IP address are shown in the admin UI.

## Tech stack

- **[Next.js](https://nextjs.org/)** (App Router): web UI and REST API in one app
- **TypeScript** end to end
- **[Tailwind CSS](https://tailwindcss.com/)** for the UI
- **[Prisma](https://www.prisma.io/)** ORM on **PostgreSQL**
- **Redis** for background jobs (webhook delivery, imports, exports), caching and rate limiting

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
cp .env.example .env
```

### 2. Configure

Edit `.env` and set at least the following:

```dotenv
# Public URL where PurrOS will be reachable
PURROS_URL=https://erp.example.com

# Generate with: openssl rand -base64 32
PURROS_SECRET=change-me

DATABASE_URL=postgresql://purros:purros@db:5432/purros
REDIS_URL=redis://redis:6379
```

### 3. Start

```bash
docker compose up -d
docker compose exec app npx prisma migrate deploy
docker compose exec app npm run purros -- setup   # create the first admin user
```

Open `PURROS_URL` and sign in.

### Services

| Service | Purpose |
|---|---|
| `app` | Next.js web UI and `/api/v1` |
| `worker` | Background jobs: webhook delivery, imports, scheduled exports |
| `db` | PostgreSQL 16 |
| `redis` | Redis 7 (queues, cache, rate limits) |

Put a reverse proxy (Caddy, Traefik, nginx) in front of `app` for TLS.

### Upgrading

```bash
git pull
docker compose pull && docker compose up -d
docker compose exec app npx prisma migrate deploy
```

Back up PostgreSQL before every upgrade. See [DESIGN.md → Operations](DESIGN.md#12-operations).

---

## API at a glance

All endpoints live under `/api/v1`, use JSON, and authenticate with a bearer API key that you create under **Settings → API keys**.

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
  -d '{"punches":[{"employeeExternalId":"hris-1042","type":"in","at":"2026-09-27T08:02:11Z","deviceId":"lobby-01"}]}'

# Adjust stock
curl -X POST https://erp.example.com/api/v1/inventory/adjustments \
  -H "Authorization: Bearer pk_live_..." \
  -H "Content-Type: application/json" \
  -d '{"itemSku":"WID-001","locationId":"loc_01H...","quantity":-3,"reason":"damaged"}'
```

- **OpenAPI spec:** `GET /api/v1/openapi.json`. Interactive docs are served at `/docs/api`.
- **Webhooks:** subscribe to events such as `employee.updated`, `timesheet.approved` or `stock.level_changed`. Payloads are signed with HMAC-SHA256.
- **External IDs:** every core record accepts an `externalId`, so you can sync by your source system's ID without keeping a mapping table.

The full conventions (pagination, errors, idempotency, rate limits) are in [DESIGN.md → API](DESIGN.md#5-api-design).

## Integrations

PurrOS ships **no built-in integrations** for specific HR platforms, timeclocks, stores or payroll providers. To connect a system, you write an **integration**: a small program you run yourself that uses the PurrOS API and webhooks.

1. Start from the integration template (`packages/integration-template`) or any language with an OpenAPI client.
2. Describe the integration in a manifest: which API scopes it needs and which webhook events it wants.
3. Register it under **Settings → Integrations**. PurrOS issues a scoped API key and a webhook signing secret.
4. Run it wherever you like: next to PurrOS in Docker Compose, as a serverless function, or as a cron job.

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
# Requirements: Node.js 22 LTS, pnpm 9, Docker (for Postgres + Redis)
pnpm install
docker compose -f docker-compose.dev.yml up -d   # Postgres + Redis only
cp .env.example .env
pnpm prisma migrate dev
pnpm db:seed          # demo company, employees, items
pnpm dev              # app on http://localhost:3000
pnpm worker:dev       # background worker
```

Useful scripts:

| Command | Description |
|---|---|
| `pnpm lint` | ESLint + Prettier check |
| `pnpm typecheck` | `tsc --noEmit` |
| `pnpm test` | Unit tests (Vitest) |
| `pnpm test:e2e` | End-to-end tests (Playwright) |
| `pnpm prisma studio` | Browse the database |

## Project documents

- [PRODUCT.md](PRODUCT.md): vision, target users, scope, non-goals, roadmap
- [DESIGN.md](DESIGN.md): architecture, data model, API design, UI system, operations

## Contributing

Contributions are welcome. Before you start:

1. Read [PRODUCT.md](PRODUCT.md) to check that the change fits the scope.
2. For anything larger than a bug fix, open an issue to discuss it first.
3. Keep PRs focused, include tests, and make sure `pnpm lint && pnpm typecheck && pnpm test` passes.
4. API changes must update the OpenAPI spec and must not break `/api/v1` (see the versioning policy in DESIGN.md).

## Security

Please don't report vulnerabilities in public issues. Use GitHub's private vulnerability reporting ("Report a vulnerability" under the Security tab).

## License

PurrOS is licensed under the [GNU Affero General Public License v3.0](LICENSE). You can use, modify and self-host it freely. If you offer a modified version as a network service, you must release your modifications under the same license.
