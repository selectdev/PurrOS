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
| **Platform endpoints** | `GET /me`, `GET /features`, `GET /permissions`, `GET /integrations/self`, `GET /integrations/self/config`, `POST /integrations/self/health`, `POST /integrations/self/logs` |
| **Organization** | `GET /org-units`, `GET /locations`, `GET /locations/{id}`, `GET /locations/external/{externalId}`, `GET /departments`. Business days respect each location's time zone and cut-off. |
| **People** | Employees: list (filters, `updatedSince`), create, get, get by external ID, partial update with `If-Match`, upsert by external ID, `:terminate`, archive. Integrations can write only their own `integrationData` namespace. |
| **Time** | `POST /time/punches:batch` (de-duplicated, per-record results), `GET /time/punches` |
| **Sales feeds** | `POST /sales/transactions:batch` (idempotent on source + external ID, tender checks, item matching, unmapped-item queue), `GET /sales/transactions`, `POST/GET /sales-summaries`, `GET /sales/unmapped-items`, `POST /sales/unmapped-items:map` |
| **Inventory** | Items: list, create, get, patch, get and upsert by external ID |
| **Webhooks** | Transactional outbox; HMAC-SHA256 signed deliveries; per-record ordering; retries over ~3 days; endpoints auto-disabled after repeated failures; events of disabled features dropped |
| **CLI** | `serve`, `worker`, `migrate`, `doctor`, `setup`, `locations create/list`, `integrations register/list`, `api-keys revoke`, `features list/enable/disable`, `version` |

Everything else in the docs (sign-in for people, roles enforcement for personal keys, the web app, scheduling, cash, stock, purchasing, forms, equipment, communication, reports, email, S3 storage and backups) is planned.

## Quick start

```bash
export DATABASE_URL="postgres://purros:purros@localhost:5432/purros?sslmode=disable"
export PURROS_SECRET="$(openssl rand -base64 32)"

go run ./cmd/purros setup --company "Acme Coffee" --owner-email owner@example.com --timezone America/Chicago
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
