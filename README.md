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
- **Built to integrate through plugins.** PurrOS has no hard-coded vendor integrations. You connect employment management software, timeclocks or inventory systems by writing a small plugin against the public API, with a typed SDK and starter template to help.
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
| **Platform** | Users, roles and permissions, API keys, webhooks, audit log, integrations |

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

## Integrations are plugins

PurrOS ships **no built-in integrations** for specific HR platforms, timeclocks, stores or payroll providers. To connect a system, you write a **plugin**: a small program you run yourself that uses the PurrOS API and webhooks.

1. Start from the plugin template (`packages/plugin-template`) or any language with an OpenAPI client.
2. Describe the plugin in a manifest: which API scopes it needs and which webhook events it wants.
3. Register it under **Settings → Plugins**. PurrOS issues a scoped API key and a webhook signing secret.
4. Run it wherever you like: next to PurrOS in Docker Compose, as a serverless function, or as a cron job.

```ts
import { PurrOS, verifyWebhook } from "@purros/sdk";

const purros = new PurrOS({ baseUrl: process.env.PURROS_URL, apiKey: process.env.PURROS_PLUGIN_KEY });

// Forward punches from your timeclock
await purros.time.punches.batch([
  { employeeExternalId: "hris-1042", type: "in", at: new Date().toISOString(), deviceId: "lobby-01" },
]);
```

Plugins can't reach the database or PurrOS internals, so a buggy plugin can't take the ERP down. See [DESIGN.md → Plugins](DESIGN.md#7-plugins).

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
