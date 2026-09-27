# PurrOS API

The PurrOS API server, background worker and admin CLI: one Go binary called `purros`.

```bash
purros serve          # REST API on :8080 + background worker
purros help           # every command
```

For architecture and conventions see [DESIGN.md](../DESIGN.md), for the API itself see [docs/api](../docs/api/README.md), and for contributing see the [development guide](../docs/development/README.md).

## Status

PurrOS is in early development. This is what the API does **today**:

| Area | Implemented |
|---|---|
| **Platform** | API-key auth with scopes; feature switches (`404 feature_disabled`, instant across processes); RFC 9457 errors with field paths; cursor pagination; `Idempotency-Key`; per-key rate limits (in memory, or Redis); request IDs; audit log; security headers; `/api/health`, `/api/ready`; OpenAPI 3.1 at `/api/v1/openapi.json`, generated from the code and filtered by enabled features |
| **Sign-in & accounts** | Email + password (Argon2id), magic links, authenticator-app 2FA with recovery codes, server-side sessions (idle and absolute timeouts, shared-device mode, origin checks), invitations, password reset, lockout after repeated failures, personal API keys; inviting and managing users and roles with no-escalation rules; company-wide or per-role mandatory 2FA |
| **Roles & reach** | Every endpoint maps to a permission. Sessions and personal keys act with the person's role; a reach narrower than Everyone limits them to their assigned locations, departments or team. Integration keys keep using scopes |
| **Employee Area** | `/me`: profile and contact details, punches and corrections, timesheets, shifts, open shifts, swaps, availability, time off, pay history and estimates, payslips, shared documents, announcements, activity history, and a ZIP export of everything |
| **Attachments & storage** | File uploads (proof, photos, receipts, documents…) streamed to local disk or any S3-compatible bucket, with content-based type checks, size limits, SHA-256 checksums, reach-based access, and signed-URL downloads from S3 |
| **Email** | SMTP with STARTTLS or implicit TLS, sent by the worker from a queue with retries; invitation, sign-in link and password reset emails |
| **Platform endpoints** | `/me`, `/features`, `/permissions`, integration self-service (`/integrations/self`, config, health, logs) |
| **Organization** | Org units, locations (time zone and business-day cut-off), departments, roles with permissions, users with assignments (read-only) |
| **People & HR** | Employees (upsert by external ID, `If-Match`, `:transfer`, `:terminate`, `:rehire`), documents, skills, pay rates, payslips |
| **Time & Attendance** | Punch batches; labor rule sets; timesheets (`:build`, `:approve`, `:reject`) with overtime; pay periods (`:lock`, CSV export); time-off types, requests, balances and adjustments |
| **Scheduling** | Demand drivers, forecasts and adjustments, staffing rules and needs, shifts with conflict checks, publishing, open-shift claims, swaps, availability |
| **Inventory** | Items, stock ledger with weighted-average cost, levels and reorder points, adjustments, waste, counts, transfers, usage recipes (sales deplete stock) |
| **Purchasing** | Suppliers and catalogs, suggested orders, purchase orders (approval limit, send, receive, cancel), supplier invoices with matching |
| **Sales** | POS transaction and summary feeds (idempotent, unmapped-item queue), customers, sales orders (reserve, ship, cancel), invoices (PDF, pay, void) |
| **Cash** | Tenders, card settlements, bank transactions with deposit matching, drawer counts with over/short, deposits, business-day close |
| **Operations** | Forms and checklists with pass/fail rules, submissions, scored audits, corrective actions, sensors and readings with out-of-range events |
| **Equipment** | Asset register, meter readings, preventive maintenance (by date or meter) that opens work orders, repair work orders |
| **Communication** | Announcements with acknowledgments, calendar events, recognitions, display metrics |
| **Reports & Insights** | KPIs, seven built-in reports (JSON or CSV), rule-based recommendations, KPI alert rules evaluated by the worker |
| **Webhooks** | Transactional outbox; HMAC-SHA256 signed deliveries; per-record ordering; retries over ~3 days; endpoints auto-disabled after repeated failures; events of disabled features dropped |
| **CLI** | Guided `setup` and `init`; `status` and `doctor` (secret, SMTP, stock ledger and backup checks); `users`, `roles` and `recover owner` for account recovery; `secret check/rotate`; built-in `backup create/list/download/verify/inspect/restore/prune` with optional encryption, uploaded files included, S3 uploads with retention, and nightly scheduling; `storage test/init/verify/migrate`; `migrate status`; locations, integrations, API keys and features; `--json`, `--yes` and `--env-file` everywhere |

Every endpoint is listed in the [endpoint index](../docs/api/endpoints.md).

Still planned: passkeys, single sign-on (OIDC and SAML) and SCIM, the web app, direct browser-to-bucket uploads, messaging, scheduled and custom reports, the AI assistant, and backups.

## Quick start

```bash
export DATABASE_URL="postgres://purros:purros@localhost:5432/purros?sslmode=disable"
export PURROS_SECRET="$(openssl rand -base64 32)"

go run ./cmd/purros setup --company "Acme Coffee" --owner-email owner@example.com --timezone America/Chicago
# or run `setup` with no flags for a guided setup; prints the Owner's sign-in link
go run ./cmd/purros locations create --name "Store 101" --external-id 101 --timezone America/Chicago --cutoff 04:00
go run ./cmd/purros integrations register --manifest purros-integration.json   # prints the API key once
go run ./cmd/purros serve
```

Send a sale:

```bash
curl -X POST localhost:8080/api/v1/sales/transactions:batch \
  -H "Authorization: Bearer $PURROS_INTEGRATION_KEY" -H "Content-Type: application/json" \
  -d '{"source":"pos:store-101","transactions":[{"externalId":"txn-1","locationExternalId":"101",
       "occurredAt":"2026-09-27T12:41:07Z","lines":[{"itemSku":"LATTE-12","quantity":"2","unitPrice":"4.50"}],
       "tenders":[{"type":"card","amount":"9.00"}],"total":"9.00"}]}'
```

## Tests

```bash
go test ./...                                                     # unit tests
PURROS_TEST_DATABASE_URL="postgres://purros:purros@localhost:5432/postgres?sslmode=disable" \
  go test -race ./...                                             # + API integration tests
```

Integration tests create a fresh database per test, run the real HTTP server, and check auth, scopes, feature switches, validation, ingestion, idempotency, pagination, the OpenAPI document and webhook delivery (signatures, ordering and retries).

## Docker

```bash
docker build -t purros-api .
docker run --rm -p 8080:8080 -e DATABASE_URL=... -e PURROS_SECRET=... purros-api
```

The image is a static binary on a distroless base, running as a non-root user. See `docker-compose.yml` in the repository root.
