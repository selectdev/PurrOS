# Development guide

How to work on PurrOS itself. Read [DESIGN.md](../../DESIGN.md) first for the architecture and the reasons behind it.

PurrOS is a monorepo:

| Directory | What | Language |
|---|---|---|
| `api/` | The API server, background worker and CLI: the `purros` binary | Go |
| `web/` | The web app: dashboard, Employee Area, kiosk, team displays (planned) | Next.js, TypeScript, Tailwind CSS |
| `packages/sdk/` | `@purros/sdk`, the typed API client (planned) | TypeScript |
| `docs/` | This documentation | Markdown |

## API: local setup

Requirements: Go 1.26+ and PostgreSQL 16 (Docker is the easiest way to get one).

```bash
git clone https://github.com/selectdev/PurrOS.git
cd PurrOS
docker compose -f docker-compose.dev.yml up -d            # PostgreSQL on localhost:5432 (user/password: purros)

cd api
export DATABASE_URL="postgres://purros:purros@localhost:5432/purros?sslmode=disable"
export PURROS_SECRET="$(openssl rand -base64 32)"
export PURROS_URL="http://localhost:8080"

go run ./cmd/purros setup --company "Dev Co" --owner-email dev@example.com --timezone America/Chicago
go run ./cmd/purros locations create --name "Store 101" --external-id 101 --timezone America/Chicago --cutoff 04:00
go run ./cmd/purros integrations register --manifest ../examples/dev-manifest.json   # prints an API key: export it as KEY
go run ./cmd/purros serve                                   # http://localhost:8080
```

`examples/dev-manifest.json` is a minimal manifest for local work:

```json
{
  "name": "dev",
  "scopes": ["organization:read", "people:read", "people:write", "time:read", "time:write",
             "sales:read", "sales:write", "inventory:read", "inventory:write"]
}
```

Try it:

```bash
curl -s localhost:8080/api/v1/me -H "Authorization: Bearer $KEY"
curl -s localhost:8080/api/v1/openapi.json | head
```

## Commands

Run these in `api/`:

| Command | What it does |
|---|---|
| `go run ./cmd/purros serve` | Run the API and worker |
| `go run ./cmd/purros help` | List all CLI commands |
| `gofmt -l .` | List badly formatted files (should print nothing) |
| `go vet ./...` | Static checks |
| `go test ./...` | Unit tests |
| `PURROS_TEST_DATABASE_URL=postgres://purros:purros@localhost:5432/postgres?sslmode=disable go test -race ./...` | Unit **and** API integration tests (each test creates and drops its own database) |
| `go build -o purros ./cmd/purros` | Build the binary |

Before opening a pull request, `gofmt -l .` must print nothing and `go vet` and the full test run (with `PURROS_TEST_DATABASE_URL`) must pass. CI runs the same checks.

## API code layout

```
api/
  cmd/purros/              main: hands off to internal/cli
  internal/
    cli/                   subcommands (serve, worker, migrate, setup, locations, integrations, features, doctor)
    config/                environment variables
    db/                    connection pool, transactions, migrations/ (SQL, embedded)
    httpx/                 the HTTP framework: router, auth, errors, validation, pagination,
                           idempotency, rate limits, batch helpers, OpenAPI generation
    features/              feature registry and switches
    catalog/               permissions, scopes and webhook events, each tied to a feature
    events/                audit log and transactional outbox
    webhooks/              outbox dispatcher and signed delivery worker
    modules/<feature>/     one package per feature, exporting Routes()
    server/                wiring, health checks, API integration tests
    testutil/              fresh-database test harness
```

## Rules to follow

1. **Declare, don't hand-wire.** Every endpoint is a `httpx.Route` with its `Feature`, `Scope`, `Body`/`Response` types and `Handler`. The router enforces auth, the feature switch, the scope and rate limits, and generates OpenAPI from the same declaration.
2. **Every feature is optional.** Give routes the right `Feature`. For sub-features checked inside a handler, call `c.RequireFeature("time.kiosk")`. Never assume another feature is on.
3. **Record every change.** Inside the transaction that makes a change, call `c.Record(tx, httpx.Change{...})`. That writes the audit entry and the webhook event together.
4. **Batch endpoints never fail the whole batch for one bad record.** Validate each record with `httpx.ValidateItem`, run it in `httpx.Savepoint`, and return an `httpx.BatchResult`.
5. **Idempotent ingestion.** Ingested records are unique on `(source, external_id)`, and re-sending updates them.
6. **No breaking API changes** in v1. Add fields and endpoints, don't rename or remove them. Clients must ignore unknown fields.
7. **Money and quantities are decimals** (`decimal.Decimal`, `numeric` columns, strings in JSON), never floats.
8. **Nothing vendor-specific in core.** Connections to specific products belong in integrations.
9. **Errors are Problems.** Return `httpx.Validation(...)`, `httpx.NotFound(...)` and similar. Any other error becomes a logged `500` with a request ID.

## Adding a feature module

1. Add the feature (and any sub-features) to `internal/features/features.go`, and its permissions, scopes and events to `internal/catalog/catalog.go`. The `TestCatalogsReferenceRealFeatures` test keeps these consistent.
2. Add a migration in `internal/db/migrations/` (`000NN_name.sql` with `-- +goose Up` / `-- +goose Down`). Follow the conventions: prefixed text IDs, `external_id` where syncable, `created_at`/`updated_at`, `numeric` for money and quantities, and a `version` column on mutable records.
3. Create `internal/modules/<feature>/` with the types, SQL and `Routes()`, and register it in `internal/server/server.go`.
4. Add a prefix to `internal/ids` for new entity types.
5. Write integration tests in `internal/server/` using `testutil.New`. Include a test showing the feature is unreachable when disabled.
6. Document it: the guide in `docs/guides/`, endpoints in `docs/api/endpoints.md`, and events in `docs/api/webhooks.md`.

## Documentation

Docs live in `docs/` as Markdown. Update them in the same pull request as the change. Write for the reader of each section (employees, managers, admins or integrators), use plain language, and mark planned behavior as planned.

## Commit and PR conventions

- Small, focused pull requests that explain what changed and why.
- Link the issue being fixed.
- Include screenshots for UI changes.
- Contributions are accepted under the project's AGPL-3.0 license.
