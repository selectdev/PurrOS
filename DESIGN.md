# PurrOS: Design

This document covers how PurrOS is built: architecture, data model, API conventions, UI system and operations. For *what* we build and *why*, see [PRODUCT.md](PRODUCT.md). For what is implemented so far, see [api/README.md](api/README.md).

---

## 1. Goals & constraints

- **Small, fast, easy to self-host.** The API is a single Go binary (`purros`) that also runs the background worker and admin commands. The minimum install is two containers: `api` and PostgreSQL.
- **API-first, API-separate.** The REST API is its own service. The web app is a client of the same public API that integrations use, so there is no hidden, UI-only business logic.
- **Correctness for quantities, hours and money.** Ledgers are append-only, writes are transactional, and there is no floating-point money (`numeric` in Postgres, `decimal.Decimal` in Go, decimal strings in JSON).
- **High-volume ingestion.** POS terminals and online stores send data continuously; ingestion must be idempotent, batch-friendly and never block on reporting.
- **Boring technology.** Go's standard library, PostgreSQL, and as few moving parts as possible. Redis is optional.

## 2. Tech stack

| Layer | Choice | Notes |
|---|---|---|
| API language | **Go** (1.26+) | Single static binary; low memory; fast startup; simple for contributors |
| HTTP | Go `net/http` + a small in-house router (`api/internal/httpx`) | Supports action paths such as `/employees/{id}:terminate`; one route declaration drives auth, scopes, feature checks and OpenAPI |
| Database | **PostgreSQL 16** via `pgx` | Hand-written SQL, no ORM; `numeric` mapped to `shopspring/decimal` |
| Migrations | `goose`, SQL files embedded in the binary | Applied automatically on `purros serve` (advisory-locked) or with `purros migrate` |
| Background jobs | **PostgreSQL** (`FOR UPDATE SKIP LOCKED`) | Outbox, webhook deliveries and scheduled jobs; no broker needed |
| Rate limiting / cache | In-process by default; **Redis 7** optional | Redis only needed to share rate limits across several API instances |
| Validation | `go-playground/validator` | Field errors reported by JSON path (`lines[0].quantity`) |
| API docs | OpenAPI 3.1 generated from route declarations and Go types | Served at `/api/v1/openapi.json` |
| People sign-in | Implemented in the API: server-side sessions, Argon2id, TOTP (passkeys, OIDC and SAML planned) | The web app never handles credentials itself |
| Web app | **Next.js**, TypeScript, Tailwind CSS (`web/`, planned) | A client of the public API; talks to it with the TypeScript SDK |
| File storage | Local disk or any S3-compatible bucket (`minio-go`) | See §12 |
| Email | SMTP, queued in PostgreSQL | See §12 |
| SDK | `@purros/sdk` (TypeScript, generated from OpenAPI; planned) | For the web app and integration authors |
| Testing | Go `testing` against real PostgreSQL; Playwright for the web app (planned) | See §11 |

## 3. Architecture

```
                                   ┌──────────────────────────────────────┐
  Browser ──► Web app (planned) ───►│                                      │◄── Integrations (POS, online
  (web UI, Employee Area,  REST    │   purros serve  (Go, single binary)  │    stores, timeclocks, HR…)
   kiosk, displays)                │                                      │    REST + API keys
                                   │   router → auth → feature → scope    │
                                   │   → rate limit → idempotency → module│
                                   │                                      │
                                   │   modules: platform, organization,   │
                                   │   people, time, sales, inventory, …  │
                                   │                                      │
                                   │   worker (in-process or `purros      │──► signed webhooks, email
                                   │   worker`): outbox, email, alerts,   │
                                   │   nightly backups                    │
                                   └──────────────┬───────────────────────┘
                                                  │ pgx
                                          ┌───────▼────────┐    ┌───────────────────┐
                                          │  PostgreSQL    │    │ Redis (optional)  │
                                          │  data, outbox, │    │ shared rate limits│
                                          │  job queue     │    └───────────────────┘
                                          └────────────────┘
```

PurrOS has no code that talks to third-party products. Each connection to another system is an **integration**: a separate program that calls `/api/v1` and receives webhooks (see §7).

### Key rules

- **One route declaration per operation.** Each module exports `Routes()`: method, path, feature, scope, request/response types and handler. The router enforces authentication, the feature switch, the scope and rate limits before the handler runs, and the same declarations generate the OpenAPI document.
- **Modules own their SQL.** A module reads and writes only its own tables, except through small exported helpers (e.g. `organization.LocationResolver`).
- **Every change is audited and emits its event in the same transaction** (`c.Record(tx, Change{…})`). A handler never calls a webhook or external system inline.
- **Batch endpoints return a result per record.** Each record runs in its own savepoint, so one bad record never fails the batch.
- **The worker is the same binary.** `purros serve` runs it in-process by default (`PURROS_RUN_WORKER=true`); larger installs run extra `purros worker` containers.

### Feature switches

Every module except the platform core can be turned off by the Owner, and a disabled module must leave no trace in the product. Enforcement lives in one place:

- **Feature registry.** The registry (`api/internal/features`) lists every feature: `key` (e.g. `cash`, `scheduling`, `time.kiosk`, `displays.gamification`), its parent (sub-features use dotted keys such as `time.kiosk`) and `dependsOn`. Every route, permission, scope and webhook event declares the feature it belongs to.
- **Storage.** A `FeatureSetting { key, enabled, changedBy, changedAt }` table. Only the switch the Owner flips is stored; dependents are off because `IsEnabled` checks every requirement, and they return to their previous state when it is switched back on. Resolved state is cached in each process and invalidated through Postgres `LISTEN/NOTIFY`, so a change takes effect immediately in every API and worker instance.
- **Guards at every entry point.**
  - Web UI (planned): navigation, dashboards, settings and the Employee Area are built from `GET /api/v1/features`, so disabled features never render.
  - API: every route declares its feature; the router answers `404` with `code: "feature_disabled"` before the handler runs. Handlers call `c.RequireFeature(key)` for sub-features. The OpenAPI document is generated from enabled features only.
  - Permissions: the catalog served to the role editor and `GET /api/v1/permissions` excludes disabled features. Existing grants stay stored but are ignored while the feature is off.
  - Events and jobs: the outbox dispatcher drops event types of disabled features, subscriptions to them are rejected, and the worker skips their scheduled jobs.
  - Integrations: registration rejects scopes of disabled features, and existing keys lose those scopes while the feature is off.
- **Dependencies.** `dependsOn` forms a graph that is checked at enable and disable time. Enabling a feature returns the missing dependencies to enable together. Disabling returns the dependent features, which are disabled in the same transaction after confirmation. Cross-module reads, such as reports reading cash data, check `isEnabled()` and degrade gracefully (e.g. the KPI is omitted rather than shown as zero).
- **Data retention.** Disabling never deletes rows. Purging a feature's data will be a separate Owner-only action (`purros features purge <key>`, planned) that requires export confirmation, runs as a background job, and is audited.
- **Audit.** Every enable and disable is written to the audit log.

### Repository layout

```
api/                         Go API server, worker and CLI (the `purros` binary)
  cmd/purros/                main package
  internal/
    config/                  environment configuration, config/state directories
    db/                      pgx pool, transactions, advisory locks, embedded SQL migrations
    httpx/                   router, auth, reach checks, errors, validation, pagination, idempotency,
                             rate limits, batch helpers, OpenAPI generation
    features/                feature registry and switches
    catalog/                 permissions, scopes, webhook events, route → permission and reach tables
    events/                  audit log and transactional outbox
    webhooks/                outbox dispatcher and signed delivery worker
    crud/                    generic list/get/create/update/archive resources
    integration/             integration manifests, registration, keys, config, webhook endpoints and deliveries
                             (shared by the admin API and the CLI)
    ingest/                  batch ingestion (per-record savepoints and results)
    refs/                    item, location and employee references by ID, SKU or external ID
    auth/, secure/           password hashing and TOTP; encryption and signing with PURROS_SECRET
    mail/                    SMTP sender and email queue
    storage/                 local and S3 file storage
    backup/                  backups: snapshot, encryption, files, S3 upload, restore
    pdf/                     invoice PDFs
    ids/                     prefixed, sortable IDs
    modules/                 one package per area: platform, account, organization, people, timeclock,
                             scheduling, inventory, purchasing, sales, cash, operations, equipment,
                             communication, insights, employeearea, attachments, integrations (admin API)
    cli/                     `purros` subcommands
    server/                  wiring, health checks, API integration tests
    testutil/                test database and API harness
  Dockerfile
config/                      deployment configuration: purros.env, postgres.env (git-ignored; *.example committed)
state/                       runtime state for local runs: uploaded files, backups (git-ignored)
docs/                        documentation
examples/dev-manifest.json   integration manifest for local development
docker-compose.yml           api + PostgreSQL (+ optional Redis)
docker-compose.dev.yml       PostgreSQL for local development
web/                         Next.js web app (planned): dashboard, Employee Area, kiosk, team displays
packages/sdk/                @purros/sdk: typed API client, webhook verification (planned)
packages/integration-template/  starter repo for building an integration (planned)
```

## 4. Data model

### Conventions

- **IDs:** prefixed ULIDs generated in the app layer (e.g. `emp_01J8Z…`, `itm_…`, `po_…`), stored as `text PRIMARY KEY`. Prefixes make IDs self-describing in logs and API payloads.
- **`externalId`:** optional on all syncable entities, with a unique index per entity type (per `source` for ingested records), so integrators can upsert by their own IDs.
- **Timestamps:** `created_at`, `updated_at` as `timestamptz` in UTC (`createdAt`, `updatedAt` in JSON). Business dates (e.g. pay period) use `date`.
- **Soft delete:** `archived_at` on master data (employees, items, suppliers, customers). Transactional records are never deleted; they are voided or reversed.
- **Money:** `numeric(19,4)` plus an ISO-4217 `currency` column. Never floating point.
- **Quantities:** `numeric(18,6)` in the item's unit of measure.
- **Optimistic concurrency:** a `version integer` column on mutable aggregates; updates with `If-Match` must match it.

### Core entities (simplified)

What exists today (tables in `api/internal/db/migrations/`), and what the target model adds:

| Area | Built | Planned |
|---|---|---|
| Platform | Company, FeatureSetting, Role, RolePermission, User (+ location and department assignments), Session, AuthToken, RecoveryCode, ApiKey, Integration, IntegrationLog, WebhookEndpoint, WebhookDelivery, OutboxEvent, AuditLog, IdempotencyRecord, IngestBatch, Email, Attachment, BackupRun | Passkey, SSO connection |
| Organization | OrgUnit (region/district/… hierarchy), Location, Department | LocationSetting (inherited), opening hours |
| People | Employee (free-form custom fields), EmployeeDocument, Skill, EmployeeSkill, PayRate (effective-dated), Payslip | CustomFieldDef, onboarding |
| Time | Punch, PunchCorrection, LaborRuleSet, Timesheet, PayPeriod, TimeOffType, TimeOffRequest, TimeOffLedger | Kiosk devices, break attestations |
| Scheduling | DemandDriver, ForecastAdjustment, StaffingRule, Shift, Availability, ShiftSwapRequest | Schedule builder runs |
| Inventory | Item, ItemMapping, StockMovement, StockLevel, UsageRecipe, StockCount (+ lines), Transfer (+ lines) | ItemVariant, UnitOfMeasure conversions, BinLocation, Batch |
| Purchasing | Supplier, SupplierCatalog, PurchaseOrder (+ lines), GoodsReceipt, SupplierInvoice | |
| Sales | SalesTransaction (+ lines), SalesSummary, Customer, SalesOrder (+ lines), Invoice | PriceList, Quote, Shipment |
| Cash | CashTender, CashSettlement, BankTransaction, CashCount, BankDeposit, BusinessDay | PaidOut, SafeCount |
| Operations | Form, FormSubmission, CorrectiveAction, Sensor, SensorReading | FormSchedule |
| Equipment | Asset, AssetMeterReading, WorkOrder | |
| Communication | Announcement, AnnouncementAck, CalendarEvent, Recognition, DisplayMetric | Conversation, Message, FileLink, Display, DisplayProfile |
| Insights | AlertRule (KPIs and reports are computed) | ReportDefinition, ReportSchedule, stored Recommendation |

### Stock ledger

Inventory correctness depends on this design:

- `StockMovement` is **append-only**: `(id, itemId, locationId, quantity (+/-), type, sourceType, sourceId, unitCost, occurredAt, createdBy)`.
- `StockLevel` is a **projection** `(itemId, locationId) → onHand, reserved`, updated **in the same transaction** as the movement insert, with `SELECT … FOR UPDATE` on the level row.
- Corrections are made with new movements (type `adjustment` or `reversal`), never by editing old ones.
- `purros doctor` verifies `StockLevel.onHand == SUM(StockMovement.quantity)` and reports any drift. *(A nightly check with an alert is planned.)*

### Time data flow

`Punch` (raw, immutable, from the device/API) → **timesheet builder** (pairs in/out, applies rounding, breaks and overtime rules) → `TimesheetEntry` → approval → `PayPeriod` lock → export.
Raw punches are never modified. Manual corrections create entries flagged `source = manual` with a required reason, and they are audited.

## 5. API design

### Basics

- Base path: `/api/v1`. JSON only. Resource names are plural and kebab-case: `/employees`, `/purchase-orders`.
- Field names are camelCase. Timestamps use ISO-8601 UTC and decimals are sent as **strings** (`"12.5000"`).
- Auth: `Authorization: Bearer <api key>`. Keys are prefixed (`pk_live_…`). Only a SHA-256 hash is stored, and the key is shown once.
- **Scopes** per key, e.g. `people:read`, `people:write`, `time:write`, `inventory:write`.
- The OpenAPI 3.1 spec is generated from the route declarations and Go types and served at `/api/v1/openapi.json`.

### Standard operations

| Operation | Pattern |
|---|---|
| List | `GET /employees?limit=50&cursor=…&status=active&locationId=…` (filters are plain parameters; order is by ID, oldest first) |
| Get | `GET /employees/{id}` or `GET /employees/external/{externalId}` |
| Create | `POST /employees` |
| Update | `PATCH /employees/{id}` (partial, `If-Match: <version>` optional) |
| Upsert by external ID | `PUT /employees/external/{externalId}` |
| Archive | `DELETE /employees/{id}` (soft) |
| Bulk | `POST /time/punches:batch` (up to 1,000 items, per-item results) |
| Actions | `POST /timesheets/{id}:approve`, `POST /purchase-orders/{id}:receive` |
| Incremental sync | `GET /employees?updatedSince=2026-09-01T00:00:00Z` |

### Data ingestion (POS, online stores, other services)

The API's main workload is taking in data from external systems. Ingestion endpoints (`/sales/transactions:batch`, `/sales-summaries`, `/cash/tenders`, `/cash/settlements`, `/time/punches:batch`, plus upserts by `externalId` for items and sales orders) share these rules:

- **Idempotent by source.** Each record is unique on `(source, externalId)`. Re-sending updates the record instead of duplicating it, so integrations can safely retry or replay a whole day.
- **Fast accept, async processing.** A batch is validated, stored as raw records and acknowledged in one transaction (`202` with per-record results). Deriving stock movements, usage, cash expectations and KPI aggregates then happens in the worker, so a busy POS never waits on reporting.
- **Raw data is kept.** Raw ingested records are immutable and linked to what was derived from them, so any figure can be traced back to its source and recalculated if rules (e.g. usage recipes) change.
- **Late and corrected data.** Refunds, voids and backdated corrections are normal. They re-trigger the affected aggregates, and data arriving after a pay period or business day is locked is flagged for review instead of silently changing locked figures.
- **Unknown references.** A transaction line with an unknown SKU is still stored and shows up in an "unmapped items" queue for someone to match, rather than being rejected.
- **Higher limits.** Ingestion keys get a separate, higher rate-limit bucket than interactive API use.

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

The `type` is `https://purros.dev/errors/<code>`. Stable machine-readable `code` values include `validation_error`, `not_found`, `conflict`, `version_mismatch`, `insufficient_stock`, `period_locked`, `rate_limited`, `unauthorized`, `forbidden`.

### Idempotency

All `POST` endpoints accept an `Idempotency-Key` header. The key and a response hash are stored for 24 hours. A retry with the same key returns the original response, and a retry with the same key but a different body returns `409 conflict`. Bulk punch ingestion also de-duplicates on `(employeeId, type, at, deviceId)`.

### Rate limiting

A fixed window per API key or session, 600 requests/min by default (3,000 for ingestion endpoints) and configurable. Counters are in memory, or in Redis when `REDIS_URL` is set so several API containers share them. Responses include `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset`. Over the limit, the API returns `429` with `Retry-After`.

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
- Delivery is at-least-once with exponential backoff (up to 3 days, about 15 attempts). An endpoint is disabled after 25 consecutive failures; while disabled (or while its integration is paused) its deliveries wait and are sent when it's re-enabled. *(Notifying the admin is planned.)*
- Endpoints are either an integration's (from its manifest) or standalone (`POST /webhook-endpoints`, `webhooks.manage`). Every delivery is kept with its attempts and last error; admins can view the exact body sent, send a `webhook.ping` test event, and replay single deliveries or everything that failed.

### Versioning

- `/api/v1` does not get breaking changes. Adding fields, endpoints and event types is allowed and non-breaking, so **clients must ignore unknown fields**.
- A breaking change requires `/api/v2`, and v1 stays supported for at least 12 months after v2 ships.
- Deprecations are announced through the `Deprecation` and `Sunset` headers and in the changelog.

## 6. Events, outbox & background jobs

1. A handler writes its domain change, an audit entry **and** an `outbox_events` row in the same transaction. Each event carries an **ordering key** (`entityType:entityID`).
2. The worker's dispatcher claims undispatched events (`FOR UPDATE SKIP LOCKED`), drops events of disabled features, and creates one `webhook_deliveries` row per subscribed endpoint, including disabled endpoints and paused integrations, whose deliveries simply wait.
3. Deliverers claim due deliveries of active endpoints with a short lease. Only the earliest pending delivery per *(endpoint, ordering key)* is eligible, so events about the same record arrive in order while different records are delivered in parallel.
4. Failed deliveries back off (30 s → 12 h, about 15 attempts over ~3 days); an endpoint with 25 consecutive failures is disabled.

All queues live in PostgreSQL, so nothing is lost if a worker restarts and no message broker is needed. Email, KPI alert evaluation and nightly backups already run in the worker the same way; future job types (imports, exports, scheduled reports, ledger verification) will too.

## 7. Integrations

PurrOS does not include integrations for specific vendors (HR platforms, timeclocks, stores, payroll providers). Each integration is custom code that uses the public API. This keeps the core small and stable, and it means an integration has no powers that an ordinary API client lacks.

### What an integration is

- A **separate program** in any language, deployed however the owner likes: a container next to PurrOS, a serverless function, or a cron job.
- It talks to PurrOS **only** through `/api/v1` (to read and write data) and webhooks (to be told about changes). It never touches the database, Redis or PurrOS internals.
- PurrOS never runs integration code. A broken integration can fail its own requests, but it cannot crash, slow down or corrupt the ERP beyond what its API scopes allow.

### Registration and the manifest

An admin registers an integration from a manifest, with `POST /api/v1/integrations` (`integrations.manage`) or `purros integrations register --manifest …`. Both use `api/internal/integration`, so they validate and record the same way; a **Settings → Integrations** page in the web app will use the same API:

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

Validation checks the name, that every scope exists and belongs to an enabled feature, that `people:sensitive` was approved by an Owner, that every webhook event exists, is enabled and is **covered by the scopes** (an event needs a scope of its feature, so an integration can't receive employee records through webhooks without a `people:` scope), and that config fields have known types (`string`, `number`, `boolean`, `secret`, `json`, `location`, `select` with `options`).

When the integration is registered, PurrOS, in one transaction:

1. Creates an `Integration` record and an **API key restricted to the declared scopes**. The key is shown once.
2. Creates the webhook endpoint with its own signing secret, shown once.
3. Stores the `config` values the admin enters, checked and converted to their declared types, encrypted as a whole with a `PURROS_SECRET`-derived key. Admins see `secret` fields masked; the integration reads the values with `GET /api/v1/integrations/self/config`.
4. Records the integration as the actor on every change it makes, so the audit log shows "Acme Timeclock Bridge" rather than an anonymous key.

After registration, admins manage it through the same API (or CLI):

- **Update the manifest** for a new version (`PUT /integrations/{id}/manifest`). The name is fixed; scopes, config fields and the webhook endpoint follow the new manifest, which is validated again. Config values of dropped fields are removed.
- **Change config** (`PATCH /integrations/{id}/config`); required fields must stay set.
- **Pause and resume.** While paused its keys return `401`, and events for its endpoint queue but aren't sent until it's resumed.
- **Rotate its key** with a grace period (`POST /integrations/{id}:rotate-key`): older keys get an `expires_at` instead of being revoked, so a deployment can switch keys without downtime.
- **See its activity**: log messages it sent, and every ingestion batch with created, updated and rejected counts.
- **Remove it**: keys, config, logs and endpoint are deleted; the audit log keeps its name.

### Integration data mapping

Integrations keep IDs in sync with **`externalId`** (see §4) and the `PUT …/external/{externalId}` upsert endpoints, so most integrations need no state of their own. For more than one external ID per record, integrations can store namespaced metadata on records:

```json
"integrationData": { "acme-timeclock-bridge": { "badgeId": "00417" } }
```

Only the owning integration can write its namespace. Any caller with read scope on the record can read it.

### SDK and template (planned)

None of these exist yet; today integrations use the OpenAPI spec with any HTTP client or client generator.

- **`@purros/sdk`** (TypeScript, generated from the OpenAPI spec): typed client, automatic pagination, idempotency keys, retries on `429`/`5xx`, `verifyWebhook(rawBody, signatureHeader, secret)`, and typed event payloads.
- **`packages/integration-template`**: a minimal Node service with a webhook endpoint, a scheduled sync loop, a config loader and a Dockerfile.
- **Examples** in `examples/integrations/`: a generic CSV/REST timeclock bridge and a webhook logger. These are teaching material, not supported vendor integrations.

Integrations in other languages use the OpenAPI spec with any client generator.

### Integration API surface

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/integrations/self` | The calling integration's registration, scopes and status |
| `GET /api/v1/integrations/self/config` | Admin-entered config values (secrets decrypted, over TLS only) |
| `POST /api/v1/integrations/self/health` | Heartbeat and status (`ok`, `warning`, `error`) with a message, shown to admins |
| `POST /api/v1/integrations/self/logs` | Optional sync summaries (e.g. "imported 42 punches"), shown to admins at `GET /integrations/{id}/logs` |

The admin side (`/integrations`, `/webhook-endpoints`, `/webhook-deliveries`) is people-only: integration keys get `403`, so an integration can't widen its own scopes or read another's config.

### Compatibility

Integrations depend only on `/api/v1`, so the API versioning policy (§5) is also the integration compatibility policy. An integration written against v1 keeps working across every PurrOS 1.x release.

## 8. Security

- **AuthN (people):** a single account system for all users, admins and employees alike. Sign-in methods: email + password (Argon2id) and magic links (single use, 15 min). Passkeys (WebAuthn) and SSO via OIDC or SAML 2.0 with optional SCIM provisioning are planned. Admins enable methods per install.
- **2FA:** optional for every user (TOTP plus recovery codes; passkeys planned). It is off by default, and an Owner can make it mandatory per role or company-wide.
- **Sessions:** stored server-side in Postgres (only a hash of the token) and linked by a secure `httpOnly`, `SameSite=Lax` cookie, with a 12-hour idle timeout (15 minutes on shared devices) and a 30-day absolute timeout. A new token is issued when the second factor completes. Sessions are revoked on password change, reset and deactivation, and users can see and revoke their own sessions. Sign-in is rate-limited per IP and account, with a 15-minute lockout after 10 failures.
- **AuthN (software):** API keys for `/api/v1`, either integration keys (scoped at registration) or personal keys (requires `api_keys.personal`; they act with the creating user's role and reach, never more). Keys can be given an expiry and revoked, and their last-used time and IP are recorded.
- **AuthZ:** organization-defined roles on top of a fixed permission catalog (see §8.1). Every route is mapped to a permission in one table (`internal/catalog/routes.go`), and the server refuses to start if a route is missing. When a permission's reach is narrower than Everyone, the request pipeline requires a target inside it (a `locationId` or `employeeId` the handler filters by, a path parameter, or a location/employee looked up for the record in `internal/catalog/reach.go`); CRUD resources check each record's location inside the transaction. Anything the pipeline can't narrow is refused (`out_of_reach`) rather than shown in full. The Employee Area (`/me`) pins every request to the caller's own employee.
- **Audit log:** every mutating service call records actor (user, API key or integration), action, entity, before/after diff, IP, and request ID. The audit log is append-only.
- **Data protection:** secrets and webhook signing keys are encrypted at rest with `PURROS_SECRET`-derived keys. Pay data needs its own permission (`pay.read`). Sensitive employee fields (national ID, bank details) are planned, encrypted at the column level and shown only with `employees.sensitive.read`.
- **Web security:** strict CSP and security headers on every API response, CSRF protection for cookie-based web sessions (state-changing requests must carry PurrOS's own `Origin`), rate-limited sign-in, and Argon2id password hashing.
- **Dependencies:** Dependabot/Renovate and `govulncheck` in CI *(planned; CI runs gofmt, vet, the race detector and the full test suite today)*.

### 8.1 Roles & permissions model

- **Permission catalog (code-defined).** Permissions are declared in code by each module as `<resource>.<action>` (`employees.read`, `employees.sensitive.read`, `pay.read`, `timesheets.approve`, `punches.correct`, `inventory.adjust`, `purchase_orders.approve`, `roles.manage`, `users.manage`, `integrations.manage`, `settings.manage`, `audit.read`, `api_keys.personal`, …). The catalog is versioned with the app. New permissions are never granted to existing roles automatically, except Owner. It is served at `GET /api/v1/permissions` with a description of each permission. Permissions of disabled features are excluded (see §3, Feature switches).
- **Roles (data-defined).** A `Role` is a name, a description and a set of `RolePermission { permission, reach }` rows, where `reach ∈ { own_team, assigned_locations, assigned_departments, everyone }`. Organizations create whatever roles they need (HR, District Manager, Supervisor…). New installs get editable starter roles from a seed, not from code.
- **Assignment.** Each `User` has exactly one `roleId`, plus `assignedLocationIds[]` and `assignedDepartmentIds[]`, which give the `assigned_*` reaches their concrete values. `own_team` is resolved from the reporting lines on the linked `Employee` (direct and indirect reports).
- **System roles.** `Owner` has every permission, is immutable and undeletable, and at least one Owner must exist. `Employee` has no permissions, is the configurable default role, and cannot be deleted while it is the default.
- **Self-access is not a permission.** A user linked to an `Employee` record can always use the Employee Area (`/me` routes) for their own data, whatever their role.
- **Evaluation.** `can(ctx, permission, target?)` looks up the role's grant for that permission and checks the target against its reach. List queries add a SQL condition for the permission's reach, so filtering happens in the database rather than after loading. Grants are loaded for each request, so role and assignment edits apply on the next request.
- **Escalation guard.** Creating or editing a role, or assigning one to a user, requires `roles.manage` / `users.manage` **and** that every permission involved is held by the actor with an equal or wider reach. Granting `roles.manage` itself is Owner-only.
- **Integration scopes** (`people:read`, `time:write`, …) are coarse API-key scopes for integrations and are separate from people's roles. Each scope maps to a fixed set of catalog permissions with reach `everyone`.
- **Audit.** Role creation, edits, deletion and user role/assignment changes are audited with before/after diffs.

## 9. UI design system

*This section is the plan for the web app (`web/`), which isn't built yet.*

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

- The API is a compiled Go binary with a pooled pgx connection; typical single-record reads are a single indexed query.
- Every list endpoint is keyset-paginated by time-sortable ID (max `limit=200`), with indexes on `updated_at`, `external_id` and foreign keys.
- Ingestion batches (up to 1,000 records) run in one transaction with a savepoint per record, and look-ups (locations, employees, items) are cached per batch.
- Hot paths avoid writes: API-key `last_used_at` is updated at most once a minute; feature state is cached in-process and invalidated by `NOTIFY`.
- Derived data (stock usage, KPIs) is computed by the worker, never inside ingestion requests.

## 11. Testing strategy

| Layer | Tooling | What |
|---|---|---|
| Unit | Go `testing` | Pure logic: feature dependencies, signatures, encryption, limits, business dates |
| API integration | Go `testing` + real PostgreSQL (`PURROS_TEST_DATABASE_URL`) | Each test gets a fresh database and a running API: auth, scopes, feature switches, ingestion, idempotency, webhooks |
| API contract | Go `testing` + OpenAPI | The generated spec is checked; a breaking-change check is planned |
| E2E | Playwright | Web app flows (when `web/` lands) |

CI (`.github/workflows/api.yml`) runs `gofmt`, `go vet`, the race detector and all tests against PostgreSQL on every push and PR.

## 12. Operations

### Configuration

All configuration lives in environment variables (`PURROS_URL`, `PURROS_SECRET`, `DATABASE_URL`, `REDIS_URL`, `SMTP_*`, `STORAGE_*`, `PURROS_BACKUP_*`, `LOG_LEVEL`), usually kept in `config/purros.env` and loaded automatically by the `purros` command. Everything PurrOS writes at runtime (uploaded files, backups) goes under `PURROS_STATE_DIR` (`/var/lib/purros` in the image). The app refuses to start with an insecure default secret. See [Configuration](docs/getting-started/configuration.md).

### File storage

A `StorageDriver` interface with `local` (Docker volume) and `s3` (any S3-compatible service) implementations. The database stores only file metadata (key, type, size, SHA-256, owner, visibility). Buckets stay private: uploads are streamed through the API (signed PUT URLs direct from the browser are planned), and downloads are permission-checked, then redirected to short-lived signed GET URLs. Retention jobs for files PurrOS no longer needs are planned. `purros storage migrate` moves files between drivers with checksum verification. S3 is required to run more than one `api` instance.

### Email

Outgoing email uses SMTP only (any provider). Messages (invitations, sign-in links, password resets today) are rendered from templates, queued in PostgreSQL, sent by the worker with rate limiting, and retried every few minutes for up to 24 hours. Translated templates are planned. A delivery log keeps recipient, subject, type and status, but not the body.

### Backups

- Postgres is the only stateful service that must be backed up. Redis, when used, holds only rate-limit counters.
- Built-in backups without external tools (the image has no `pg_dump`): every table is copied with `COPY` in one REPEATABLE READ snapshot into a tar.gz with a manifest (versions, row counts, SHA-256 per table), optionally encrypted (Argon2id key, AES-256-GCM in authenticated chunks). The worker runs them daily into `PURROS_BACKUP_DIR` and prunes old ones. Restore verifies the whole file first, rebuilds the schema at the backup's version, loads the data with foreign keys re-validated, then migrates forward. Backups include uploaded files by default with local storage (`PURROS_BACKUP_FILES`); with S3 storage, rely on bucket versioning and replication or turn that on too. Backups can also be uploaded to an S3 bucket with their own retention. Upgrades are manual; the CLI doesn't download releases.
- `purros doctor` checks configuration, connectivity, migrations, company setup, the Owner account, the encryption secret, email, the outbox, webhook endpoints, the stock ledger, location time zones, file storage and backups.

### Migrations & upgrades

- SQL migrations (goose) are embedded in the binary and applied on `purros serve` start under a Postgres advisory lock, or explicitly with `purros migrate`. They must be forward-only and safe to run while the API is live (expand → migrate data → contract across releases).
- The release notes flag any migration that needs downtime.

### Observability

- Structured JSON logs (`log/slog`) with `requestId` on every line; every response has an `X-Request-Id` header.
- `/api/health` (liveness) and `/api/ready` (database reachable, migrations applied).
- `purros status` shows queues, email and backups. *(Planned: OpenTelemetry traces and metrics, and a Prometheus endpoint for queue depth, webhook failure rate and request latency.)*

## 13. Decisions log

| # | Decision | Rationale |
|---|---|---|
| 1 | API in Go, separate from the Next.js web app | Performance for high-volume ingestion, a single small binary for self-hosters, simple contributor experience; the web app uses the same public API as integrations |
| 2 | PostgreSQL with hand-written SQL (pgx), no ORM | Full control over transactions, locking and upserts for ledgers and ingestion |
| 3 | PostgreSQL-backed job queue; Redis optional | One less service to run; jobs are enqueued in the same transaction as the change |
| 4 | Transactional outbox for events | No lost or phantom webhooks |
| 5 | Append-only stock ledger | Auditability, and stock can be reconstructed at any point in time |
| 6 | Prefixed string IDs | Self-describing, sortable, safe to expose |
| 7 | AGPL-3.0 | Keeps hosted forks open while allowing free self-hosting |
| 8 | Integrations are custom, out-of-process programs using only the public API; none for specific vendors in core | Core stays small and stable, integrations can be written in any language, and a broken integration can't take down the ERP |
| 9 | One route declaration drives routing, auth, scopes, feature checks and OpenAPI | The spec can't drift from the code, and a disabled feature can't leak through a forgotten check |
| 10 | Per-record ordering keys for webhooks | Receivers see events about one record in order without serializing all deliveries |
