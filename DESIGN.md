# PurrOS: Design

This document covers how PurrOS is built: architecture, data model, API conventions, UI system and operations. For *what* we build and *why*, see [PRODUCT.md](PRODUCT.md).

---

## 1. Goals & constraints

- **Single deployable app** plus one background worker. Self-hosters should run four containers (app, worker, Postgres, Redis) and nothing more.
- **API parity.** The UI uses the same domain services as the public API. There is no hidden, UI-only business logic.
- **Correctness for quantities, hours and money.** Ledgers are append-only, writes are transactional, and there is no floating-point money.
- **Type-safe end to end**: Prisma types flow from the schema through the services to API validation.
- **Boring technology.** Postgres, Redis, and a well-known web framework.

## 2. Tech stack

| Layer | Choice | Notes |
|---|---|---|
| Framework | **Next.js** (App Router) | Server Components for the UI, Route Handlers for `/api/v1` |
| Language | **TypeScript** (strict) | `strict: true`, `noUncheckedIndexedAccess: true` |
| Styling | **Tailwind CSS** | Design tokens via CSS variables (see §9) |
| UI primitives | Radix UI / shadcn/ui-style components | Accessible, unstyled primitives, owned in-repo |
| ORM | **Prisma** | PostgreSQL provider; migrations in `prisma/migrations` |
| Database | **PostgreSQL 16** | Source of truth |
| Cache / queues | **Redis 7** | BullMQ queues, rate limiting, short-lived cache |
| Validation | Zod | Shared between API input, forms, and OpenAPI generation |
| Auth | Auth.js (NextAuth) | Password, magic link, passkeys (WebAuthn), OIDC; SAML via a bridge (e.g. BoxyHQ SAML Jackson); separate API-key auth for `/api/v1` |
| Testing | Vitest, Playwright | Unit/integration, and E2E |
| Package manager | pnpm | |

## 3. Architecture

```
                ┌────────────────────────────────────────────┐
  Browser ─────▶│                Next.js app                 │
                │  ┌──────────────┐    ┌──────────────────┐  │
                │  │ UI (RSC +    │    │ /api/v1 Route    │◀─┼──── REST calls ────┐
                │  │ Server       │    │ Handlers         │  │                    │
                │  │ Actions)     │    │ (API-key auth)   │  │           ┌────────┴─────────┐
                │  └──────┬───────┘    └────────┬─────────┘  │           │ Integrations     │
                │         └──────────┬──────────┘            │           │ (separate procs, │
                │           ┌────────▼────────┐              │           │ custom-built:    │
                │           │ Domain services │              │           │ HRIS, timeclock, │
                │           │ (src/modules/*) │              │           │ inventory sync)  │
                │           └───┬─────────┬───┘              │           └────────▲─────────┘
                └───────────────┼─────────┼──────────────────┘                    │
                                │         │ enqueue                               │
                       Prisma   │         ▼                                       │
                         ┌──────▼───┐  ┌───────┐   ┌──────────────────┐           │
                         │ Postgres │  │ Redis │◀─▶│ Worker (BullMQ)  │── signed ─┘
                         └──────────┘  └───────┘   │ webhooks, imports│   webhooks
                                                   │ exports, cron    │
                                                   └──────────────────┘
```

PurrOS has no code that talks to third-party products. Each connection to another system is an **integration**: a separate program that calls `/api/v1` and receives webhooks (see §7).

### Key rules

- **Domain services are the only code that writes to the database.** Route Handlers and Server Actions parse input, check auth, and call a service. They never call Prisma directly.
- **Services are framework-agnostic.** They take a `Context` (actor, permissions, request ID, Prisma transaction client) and plain inputs. That makes them usable from the API, the UI, the worker and tests.
- **Side effects go through the outbox** (see §6). A service never calls a webhook or an external system inline.
- **The worker is the same codebase** with a different entrypoint (`src/worker/index.ts`), so it shares services and types.

### Repository layout

```
prisma/
  schema.prisma
  migrations/
  seed.ts
src/
  app/
    (auth)/               # sign-in, SSO callbacks
    (employee)/           # Employee Area (self-service portal)
    (dashboard)/          # authenticated UI, one folder per module
      people/
      time/
      inventory/
      purchasing/
      sales/
      settings/
    api/v1/               # public REST API route handlers
  modules/                # domain logic, one folder per bounded context
    people/
      people.service.ts
      people.schemas.ts   # Zod input/output schemas
      people.events.ts    # event type definitions
      people.test.ts
    time/
    inventory/
    purchasing/
    sales/
    platform/             # users, roles, api keys, webhooks, audit
  lib/
    db.ts                 # Prisma client singleton
    auth/
    api/                  # API helpers: errors, pagination, idempotency
    redis.ts
    queue.ts
  components/
    ui/                   # design-system primitives
  worker/
    index.ts
    jobs/
packages/
  sdk/                    # @purros/sdk — typed API client, webhook verification (published to npm)
  integration-template/   # starter repo for building an integration
examples/
  integrations/           # small reference integrations (CSV timeclock bridge, webhook logger); not supported vendor integrations
```

## 4. Data model

### Conventions

- **IDs:** prefixed, sortable, generated in the app layer (e.g. `emp_01J8Z…`, `itm_…`, `po_…`), stored as `String @id`. Prefixes make IDs self-describing in logs and API payloads.
- **`externalId`:** optional on all syncable entities, unique per entity type (`@@unique([externalId])`), so integrators can upsert by their own IDs.
- **Timestamps:** `createdAt`, `updatedAt` on every table, stored as `timestamptz` in UTC. Business dates (e.g. pay period) use `date`.
- **Soft delete:** `archivedAt` on master data (employees, items, suppliers, customers). Transactional records are never deleted; they are voided or reversed.
- **Money:** `Decimal(19,4)` plus an ISO-4217 `currency` column. Never `Float`.
- **Quantities:** `Decimal(18,6)` in the item's base unit of measure.
- **Optimistic concurrency:** a `version Int` column on mutable aggregates; updates must match the version.

### Core entities (simplified)

```
People         Employee, Department, Position, Location, EmployeeDocument, CustomFieldDef
Time           Punch, PunchCorrectionRequest, Shift, Timesheet, TimesheetEntry, PayPeriod, OvertimeRule, TimeOffRequest, TimeOffBalance
Pay            PayRate (effective-dated history), Payslip (pushed in by a payroll integration)
Inventory      Item, ItemVariant, UnitOfMeasure, Warehouse, BinLocation, StockMovement, StockLevel, StockCount
Purchasing     Supplier, PurchaseOrder, PurchaseOrderLine, GoodsReceipt, GoodsReceiptLine
Sales          Customer, PriceList, SalesOrder, SalesOrderLine, Shipment, Invoice
Platform       User, Role, RolePermission, UserLocationAssignment, UserDepartmentAssignment, ApiKey, Integration, IntegrationConfig, WebhookEndpoint, WebhookDelivery, OutboxEvent, AuditLog, IdempotencyRecord
```

### Stock ledger

Inventory correctness depends on this design:

- `StockMovement` is **append-only**: `(id, itemId, locationId, quantity (+/-), type, sourceType, sourceId, unitCost, occurredAt, createdBy)`.
- `StockLevel` is a **projection** `(itemId, locationId) → onHand, reserved`, updated **in the same transaction** as the movement insert, with `SELECT … FOR UPDATE` on the level row.
- Corrections are made with new movements (type `adjustment` or `reversal`), never by editing old ones.
- A nightly job verifies `StockLevel.onHand == SUM(StockMovement.quantity)` and alerts on drift.

### Time data flow

`Punch` (raw, immutable, from the device/API) → **timesheet builder** (pairs in/out, applies rounding, breaks and overtime rules) → `TimesheetEntry` → approval → `PayPeriod` lock → export.
Raw punches are never modified. Manual corrections create entries flagged `source = manual` with a required reason, and they are audited.

## 5. API design

### Basics

- Base path: `/api/v1`. JSON only. Resource names are plural and kebab-case: `/employees`, `/purchase-orders`.
- Field names are camelCase. Timestamps use ISO-8601 UTC and decimals are sent as **strings** (`"12.5000"`).
- Auth: `Authorization: Bearer <api key>`. Keys are prefixed (`pk_live_…`). Only a SHA-256 hash is stored, and the key is shown once.
- **Scopes** per key, e.g. `people:read`, `people:write`, `time:write`, `inventory:write`.
- The OpenAPI 3.1 spec is generated from the Zod schemas and served at `/api/v1/openapi.json`.

### Standard operations

| Operation | Pattern |
|---|---|
| List | `GET /employees?limit=50&cursor=…&filter[status]=active&sort=-updatedAt` |
| Get | `GET /employees/{id}` or `GET /employees/external/{externalId}` |
| Create | `POST /employees` |
| Update | `PATCH /employees/{id}` (partial, `If-Match: <version>` optional) |
| Upsert by external ID | `PUT /employees/external/{externalId}` |
| Archive | `DELETE /employees/{id}` (soft) |
| Bulk | `POST /time/punches:batch` (up to 1,000 items, per-item results) |
| Actions | `POST /timesheets/{id}:approve`, `POST /purchase-orders/{id}:receive` |
| Incremental sync | `GET /employees?updatedSince=2026-09-01T00:00:00Z` |

### Pagination

Cursor-based:

```json
{ "data": [ … ], "nextCursor": "eyJpZCI6ImVtcF8wMUo4…", "hasMore": true }
```

### Errors

Uses the RFC 9457 Problem Details format:

```json
{
  "type": "https://purros.dev/errors/validation",
  "title": "Validation failed",
  "status": 422,
  "code": "validation_error",
  "requestId": "req_01J8Z…",
  "errors": [{ "path": "lastName", "message": "Required" }]
}
```

Stable machine-readable `code` values include `validation_error`, `not_found`, `conflict`, `version_mismatch`, `insufficient_stock`, `period_locked`, `rate_limited`, `unauthorized`, `forbidden`.

### Idempotency

All `POST` endpoints accept an `Idempotency-Key` header. The key and a response hash are stored for 24 hours. A retry with the same key returns the original response, and a retry with the same key but a different body returns `409 conflict`. Bulk punch ingestion also de-duplicates on `(employeeId, type, at, deviceId)`.

### Rate limiting

A Redis token bucket per API key, 600 requests/min by default and configurable. Responses include `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset`. Over the limit, the API returns `429` with `Retry-After`.

### Webhooks

- Events are named `<entity>.<verb>`: `employee.created`, `employee.updated`, `punch.received`, `timesheet.approved`, `pay_period.locked`, `stock.level_changed`, `stock.below_reorder_point`, `purchase_order.received`, `sales_order.shipped`, `invoice.issued`.
- Envelope:

```json
{
  "id": "evt_01J8Z…",
  "type": "timesheet.approved",
  "createdAt": "2026-09-27T17:04:00Z",
  "data": { "object": { … full resource … }, "previous": { … changed fields … } }
}
```

- Signed with `PurrOS-Signature: t=<unix>,v1=<hex HMAC-SHA256(secret, t + "." + body)>`. Receivers should reject timestamps older than 5 minutes.
- Delivery is at-least-once with exponential backoff (up to 3 days, about 15 attempts). An endpoint is auto-disabled after sustained failures and the admin is notified. Delivery logs can be viewed and deliveries replayed in the UI.

### Versioning

- `/api/v1` does not get breaking changes. Adding fields, endpoints and event types is allowed and non-breaking, so **clients must ignore unknown fields**.
- A breaking change requires `/api/v2`, and v1 stays supported for at least 12 months after v2 ships.
- Deprecations are announced through the `Deprecation` and `Sunset` headers and in the changelog.

## 6. Events, outbox & background jobs

1. A service writes its domain change **and** an `OutboxEvent` row in the same Prisma transaction.
2. The worker polls the outbox (or is nudged via Redis pub/sub), then fans out to:
   - webhook deliveries (`WebhookDelivery` rows plus a BullMQ job each)
   - internal subscribers (e.g. low-stock alerts, timesheet rebuild)
3. Outbox rows are marked dispatched once they are processed. This guarantees that no event is lost if Redis or the worker is down.

BullMQ queues: `webhooks`, `imports`, `exports`, `scheduled` (cron-style jobs: ledger verification, reorder checks, document expiry reminders, data retention).

## 7. Integrations

PurrOS does not include integrations for specific vendors (HR platforms, timeclocks, stores, payroll providers). Each integration is custom code that uses the public API. This keeps the core small and stable, and it means an integration has no powers that an ordinary API client lacks.

### What an integration is

- A **separate program** in any language, deployed however the owner likes: a container next to PurrOS, a serverless function, or a cron job.
- It talks to PurrOS **only** through `/api/v1` (to read and write data) and webhooks (to be told about changes). It never touches the database, Redis or PurrOS internals.
- PurrOS never runs integration code. A broken integration can fail its own requests, but it cannot crash, slow down or corrupt the ERP beyond what its API scopes allow.

### Registration and the manifest

An admin registers an integration under **Settings → Integrations**, either by filling in a form or by uploading a manifest:

```json
{
  "name": "acme-timeclock-bridge",
  "displayName": "Acme Timeclock Bridge",
  "version": "1.2.0",
  "description": "Forwards punches from Acme terminals to PurrOS.",
  "homepage": "https://github.com/example/acme-timeclock-bridge",
  "scopes": ["people:read", "time:write"],
  "webhooks": {
    "url": "https://integrations.internal/acme-bridge/webhooks",
    "events": ["employee.created", "employee.updated", "employee.archived"]
  },
  "config": [
    { "key": "acmeBaseUrl", "type": "string", "required": true },
    { "key": "acmeApiToken", "type": "secret", "required": true }
  ]
}
```

When the integration is registered, PurrOS:

1. Creates an `Integration` record and an **API key restricted to the declared scopes**. The key is shown once.
2. Creates the webhook subscription with its own signing secret.
3. Stores the `config` values the admin enters, encrypting fields of type `secret`. The integration reads them with `GET /api/v1/integrations/self/config`.
4. Records the integration as the actor on every change it makes, so the audit log shows "Acme Timeclock Bridge" rather than an anonymous key.

Admins can pause an integration (its key and webhooks stop working), rotate its credentials, see its recent API calls and webhook deliveries, and remove it.

### Integration data mapping

Integrations keep IDs in sync with **`externalId`** (see §4) and the `PUT …/external/{externalId}` upsert endpoints, so most integrations need no state of their own. For more than one external ID per record, integrations can store namespaced metadata on records:

```json
"integrationData": { "acme-timeclock-bridge": { "badgeId": "00417" } }
```

Only the owning integration can write its namespace. Any caller with read scope on the record can read it.

### SDK and template

- **`@purros/sdk`** (TypeScript, generated from the OpenAPI spec): typed client, automatic pagination, idempotency keys, retries on `429`/`5xx`, `verifyWebhook(req, secret)`, and typed event payloads.
- **`packages/integration-template`**: a minimal Node service with a webhook endpoint, a scheduled sync loop, a config loader and a Dockerfile.
- **Examples** in `examples/integrations/`: a generic CSV/REST timeclock bridge and a webhook logger. These are teaching material, not supported vendor integrations.

Integrations in other languages use the OpenAPI spec with any client generator.

### Integration API surface

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/integrations/self` | The calling integration's registration, scopes and status |
| `GET /api/v1/integrations/self/config` | Admin-entered config values (secrets decrypted, over TLS only) |
| `POST /api/v1/integrations/self/health` | Heartbeat and status message, shown in the admin UI |
| `POST /api/v1/integrations/self/logs` | Optional sync summaries (e.g. "imported 42 punches") shown in the admin UI |

### Compatibility

Integrations depend only on `/api/v1`, so the API versioning policy (§5) is also the integration compatibility policy. An integration written against v1 keeps working across every PurrOS 1.x release.

## 8. Security

- **AuthN (people):** a single account system for all users, admins and employees alike. Sign-in methods: email + password (Argon2id), magic link (single use, 15 min), passkeys (WebAuthn), and SSO via OIDC or SAML 2.0 with optional SCIM provisioning. Admins enable methods per install and can require SSO.
- **2FA:** optional for every user (TOTP or passkey, plus recovery codes). It is off by default, and an Owner can make it mandatory per role or company-wide.
- **Sessions:** stored server-side (Postgres, cached in Redis) and linked by a secure `httpOnly`, `SameSite=Lax` cookie. Idle and absolute timeouts are configurable. Sessions are revoked on password change, and users can see and revoke their own sessions. Sign-in is rate-limited with temporary lockout.
- **AuthN (software):** API keys for `/api/v1`, either integration keys (scoped at registration) or personal keys (requires `api_keys.personal`; they act with the creating user's role and reach, never more). Keys can be given an expiry and can be rotated, and their last-used time and IP are shown.
- **AuthZ:** organization-defined roles on top of a fixed permission catalog (see §8.1). Checks are enforced in services, not only in routes.
- **Audit log:** every mutating service call records actor (user, API key or integration), action, entity, before/after diff, IP, and request ID. The audit log is append-only.
- **Data protection:** secrets and webhook signing keys are encrypted at rest with `PURROS_SECRET`-derived keys. Sensitive employee fields (national ID, bank details) are encrypted at the column level and masked in the UI and API unless the caller has explicit permission.
- **Web security:** CSRF protection for Server Actions, strict CSP, rate-limited login, and Argon2id password hashing.
- **Dependencies:** Renovate/Dependabot plus `pnpm audit` in CI.

### 8.1 Roles & permissions model

- **Permission catalog (code-defined).** Permissions are declared in code by each module as `<resource>.<action>` (`employees.read`, `employees.sensitive.read`, `pay.read`, `timesheets.approve`, `punches.correct`, `inventory.adjust`, `purchase_orders.approve`, `roles.manage`, `users.manage`, `integrations.manage`, `settings.manage`, `audit.read`, `api_keys.personal`, …). The catalog is versioned with the app. New permissions are never granted to existing roles automatically, except Owner. It is served at `GET /api/v1/permissions` with a description of each permission.
- **Roles (data-defined).** A `Role` is a name, a description and a set of `RolePermission { permission, reach }` rows, where `reach ∈ { own_team, assigned_locations, assigned_departments, everyone }`. Organizations create whatever roles they need (HR, District Manager, Supervisor…). New installs get editable starter roles from a seed, not from code.
- **Assignment.** Each `User` has exactly one `roleId`, plus `assignedLocationIds[]` and `assignedDepartmentIds[]`, which give the `assigned_*` reaches their concrete values. `own_team` is resolved from the reporting lines on the linked `Employee` (direct and indirect reports).
- **System roles.** `Owner` has every permission, is immutable and undeletable, and at least one Owner must exist. `Employee` has no permissions, is the configurable default role, and cannot be deleted while it is the default.
- **Self-access is not a permission.** A user linked to an `Employee` record can always use the Employee Area (`/me` routes) for their own data, whatever their role.
- **Evaluation.** `can(ctx, permission, target?)` looks up the role's grant for that permission and checks the target against its reach. List queries use `scopeWhere(ctx, permission)`, which returns a Prisma `where` fragment so filtering happens in SQL rather than after loading. Resolved grants are cached in Redis per user and invalidated when the role or the assignment changes, so edits apply on the next request.
- **Escalation guard.** Creating or editing a role, or assigning one to a user, requires `roles.manage` / `users.manage` **and** that every permission involved is held by the actor with an equal or wider reach. Granting `roles.manage` itself is Owner-only.
- **Integration scopes** (`people:read`, `time:write`, …) are coarse API-key scopes for integrations and are separate from people's roles. Each scope maps to a fixed set of catalog permissions with reach `everyone`.
- **Audit.** Role creation, edits, deletion and user role/assignment changes are audited with before/after diffs.

## 9. UI design system

### Tone

Serious, calm and professional. PurrOS is used all day by people doing operational work, so the interface should stay out of their way. The name is the only playful part. The UI uses no mascots, no confetti and no jokes in copy.

### Principles

1. **Information density that scales.** Tables are the main surface. Offer a comfortable (default) and a compact density.
2. **Keyboard first for power users.** Command palette (`⌘K`/`Ctrl K`), shortcuts for common actions, full keyboard navigation in tables and forms.
3. **Plain status.** Status is shown with a colour plus a text label, never colour alone (e.g. `● Approved`, `● Pending`, `● Locked`).
4. **No surprise mutations.** Destructive and ledger-affecting actions (stock adjustments, pay period lock) show what will change and ask for confirmation.
5. **Tablet-ready.** Warehouse and receiving screens work at 768px with touch targets of at least 44px and barcode-scanner input (keyboard wedge).
6. **Accessible.** Target WCAG 2.2 AA, with visible focus rings and correct labels and roles.

### Tokens (Tailwind + CSS variables)

Colours are defined as CSS variables and mapped in `tailwind.config.ts`, so light and dark themes (and later a custom brand accent) only swap variables.

| Token | Light | Dark | Use |
|---|---|---|---|
| `--background` | `#FFFFFF` | `#0B0D12` | Page background |
| `--surface` | `#F7F8FA` | `#12151C` | Cards, sidebars |
| `--border` | `#E3E6EB` | `#232833` | Dividers, inputs |
| `--foreground` | `#11141A` | `#E8EAEE` | Primary text |
| `--muted` | `#5B6472` | `#98A1B0` | Secondary text |
| `--primary` | `#1F4FD1` | `#5B82F0` | Primary actions, links, focus |
| `--success` | `#1B7F4B` | `#3BB27A` | Approved, received, in stock |
| `--warning` | `#A15C00` | `#E0A23A` | Pending, low stock |
| `--danger` | `#B42318` | `#F0685E` | Errors, destructive, out of stock |

- **Typography:** Inter (UI) and JetBrains Mono (IDs, SKUs, API keys). Base size 14px. Tabular numerals (`font-variant-numeric: tabular-nums`) in every numeric column.
- **Spacing:** Tailwind's 4px scale. Page padding `p-6`, card padding `p-4`, table rows 40px (comfortable) / 32px (compact).
- **Radius:** `rounded-md` (6px) for controls and `rounded-lg` (8px) for cards. No pill-shaped buttons.
- **Elevation:** borders instead of shadows. Shadows are used only for overlays (menus, dialogs).
- **Icons:** Lucide at 16px/20px, stroke 1.75.

### Layout

- A fixed left sidebar holds the module navigation (People, Time, Inventory, Purchasing, Sales), with Settings at the bottom.
- The top bar holds global search / command palette, location switcher, notifications and user menu.
- Pages follow a standard structure: **header** (title, primary action, secondary actions), **filter bar**, **content** (table or detail), with detail views opening as a full page (deep-linkable) rather than as modals.
- Record detail pages show a **tabbed body** (Overview, Activity/Audit, related records) and a **right-hand summary panel**.

### Core components

`DataTable` (server-side sort/filter/paginate, column chooser, saved views, CSV export) · `Form` (Zod-driven, inline validation) · `StatusBadge` · `QuantityInput` (UoM aware) · `MoneyDisplay` · `DateRangePicker` · `EmployeePicker` / `ItemPicker` (async search) · `ConfirmDialog` · `EmptyState` · `ActivityTimeline`.

## 10. Performance

- Server Components render lists on the server, and client JS is limited to interactive parts.
- Every list endpoint is paginated (max `limit=200`), with indexes on `(archivedAt, updatedAt)`, `externalId`, and all foreign keys.
- Redis caches permission sets and reference data (units of measure, locations) with explicit invalidation on write.
- Bulk ingestion uses `createMany` inside chunked transactions, and timesheet rebuilds are debounced per employee in the worker.

## 11. Testing strategy

| Layer | Tooling | What |
|---|---|---|
| Unit | Vitest | Pure logic: overtime rules, UoM conversion, costing |
| Service / integration | Vitest + real Postgres (Docker) | Services with transactions, ledger invariants, permission checks |
| API contract | Vitest + OpenAPI | Responses validate against the generated spec, and the diff check fails CI on breaking changes |
| E2E | Playwright | Critical flows: onboard employee → punches → approve → export; PO → receive → stock; SO → ship → invoice |

CI runs lint, typecheck, unit and integration tests, the OpenAPI breaking-change check, and a build on every PR.

## 12. Operations

### Configuration

All configuration lives in environment variables (`PURROS_URL`, `PURROS_SECRET`, `DATABASE_URL`, `REDIS_URL`, `SMTP_*`, `OIDC_*`, `LOG_LEVEL`, `STORAGE_*`). The app refuses to start with an insecure default secret.

### File storage

Attachments go to the local filesystem (a Docker volume) by default, or to any S3-compatible store (MinIO, AWS S3, Backblaze).

### Backups

- Postgres is the only stateful service that must be backed up. Redis holds only queues and cache, which can be rebuilt from the outbox.
- A documented `pg_dump` cron example is provided, plus backup of the attachments volume or bucket.
- `purros doctor` CLI: checks connectivity, pending migrations, ledger integrity and queue health.

### Migrations & upgrades

- Prisma migrations run with `prisma migrate deploy`. They must be forward-only and safe to run while the app is live (expand → migrate data → contract across releases).
- The release notes flag any migration that needs downtime.

### Observability

- Structured JSON logs (pino) with `requestId` on every line.
- `/api/health` (liveness) and `/api/ready` (DB and Redis reachable).
- Optional OpenTelemetry traces and metrics export, plus a Prometheus endpoint for queue depth, webhook failure rate and request latency.

## 13. Decisions log

| # | Decision | Rationale |
|---|---|---|
| 1 | Single Next.js app for UI + API | One deployable and shared types/services. Separating them later is possible because services are framework-agnostic |
| 2 | PostgreSQL via Prisma | Strong transactional guarantees for ledgers, and a mature ecosystem |
| 3 | Redis + BullMQ for jobs | Reliable retries and scheduling without adding another broker |
| 4 | Transactional outbox for events | No lost or phantom webhooks |
| 5 | Append-only stock ledger | Auditability, and stock can be reconstructed at any point in time |
| 6 | Prefixed string IDs | Self-describing, sortable, safe to expose |
| 7 | AGPL-3.0 | Keeps hosted forks open while allowing free self-hosting |
| 8 | Integrations are custom, out-of-process programs using only the public API; none for specific vendors in core | Core stays small and stable, integrations can be written in any language, and a broken integration can't take down the ERP |
