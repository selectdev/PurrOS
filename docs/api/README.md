# API overview

The PurrOS REST API gives other systems access to everything the web interface can do. Its main job is **bringing data in** from point-of-sale systems, online stores, timeclocks, HR tools and other services. **Webhooks** send changes back out.

- Base URL: `https://<your-purros>/api/v1`
- Format: JSON (UTF-8)
- Spec: `GET /api/v1/openapi.json` (OpenAPI 3.1), with interactive docs at `/docs/api`
- SDK: [`@purros/sdk`](../integrations/building-an-integration.md) for TypeScript. For other languages, generate a client from the OpenAPI spec.

The spec only contains endpoints of [enabled features](../admin/feature-switches.md).

## Authentication

There are three ways to call the API:

| Caller | How | Access |
|---|---|---|
| **Integration key** | `Authorization: Bearer pk_live_…` from registering an [integration](../integrations/README.md) | The **scopes** in its manifest, company-wide |
| **Personal key** | `Authorization: Bearer pk_live_…` created at `POST /auth/api-keys` by a user whose role has `api_keys.personal` | That user's own role permissions and reach, never more |
| **Session** | The `purros_session` cookie set by `POST /auth/sign-in` (the web app) | The signed-in person's role permissions and reach |

```http
GET /api/v1/employees HTTP/1.1
Host: erp.example.com
Authorization: Bearer pk_live_3c9f…
```

Keys are shown once, can have an expiry, and can be revoked. Use `GET /api/v1/me` to check what a key is, or `GET /api/v1/auth/session` to see a person's role, permissions and assignments.

### People: permissions and reach

Requests from people (sessions and personal keys) are checked against their role instead of scopes. The [endpoint index](endpoints.md) lists the permission each endpoint needs. When the role holds the permission with a reach narrower than **Everyone** (own team, assigned locations or assigned departments):

- list endpoints must be narrowed with `locationId` or `employeeId` inside that reach
- single records, and actions on them, are checked against the record's location or employee
- anything else returns `403 out_of_reach`

Data feeds (batch ingestion, tenders, settlements, sensor readings…) are for integration keys only. Sessions also:

- must send state-changing requests from the PurrOS origin (the `Origin` header is checked), which protects against cross-site request forgery
- may need a second step (`POST /auth/sign-in/mfa`) before anything else works; see [Authentication](../admin/authentication.md)

## Scopes

Integration keys are limited to the scopes they declared:

| Scope | Covers |
|---|---|
| `organization:read` | Org units, locations, departments, roles, users (read-only) |
| `organization:write` | Create, update and archive org units, locations and departments |
| `attachments:read`, `attachments:write` | Upload, download and delete files ([Attachments](attachments.md)) |
| `people:read`, `people:write` | Employees, documents, skills, onboarding |
| `payroll:read`, `payroll:write` | Pay rates, pay period exports, payslips |
| `time:read`, `time:write` | Punches, timesheets, time off |
| `scheduling:read`, `scheduling:write` | Demand drivers, forecasts, schedules, shifts, availability |
| `cash:read`, `cash:write` | Tenders, settlements, bank transactions, counts, deposits |
| `inventory:read`, `inventory:write` | Items, stock, movements, counts, waste, transfers, usage recipes |
| `purchasing:read`, `purchasing:write` | Suppliers, catalogs, suggested orders, purchase orders, receipts, supplier invoices |
| `sales:read`, `sales:write` | Sales transactions and summaries, customers, sales orders, invoices |
| `operations:read`, `operations:write` | Forms, submissions, corrective actions, audits, sensor readings |
| `equipment:read`, `equipment:write` | Assets, meter readings, work orders |
| `communication:read`, `communication:write` | Announcements, calendar events, recognitions |
| `reports:read`, `reports:write` | Reports, KPIs, recommendations. `write` is for pushing custom display metrics. |
| `people:sensitive` | Sensitive employee fields (national ID, bank details). Needs Owner approval at registration. |
| `notifications:deliver` | Receive `notification.requested` events to deliver SMS or chat messages |

Sensitive employee fields are never returned to integration keys without the `people:sensitive` scope.

Scopes also decide which [webhook events](webhooks.md#subscribing) an integration may subscribe to. Managing integrations and webhook endpoints (`/integrations`, `/webhook-endpoints`) is for people only (`integrations.manage`, `webhooks.manage`); an integration uses `/integrations/self` instead.

## Conventions

| Topic | Rule |
|---|---|
| Resource names | Plural, kebab-case: `/purchase-orders`, `/sales-summaries` |
| Field names | camelCase |
| IDs | Prefixed strings, e.g. `emp_01J8Z…`, `loc_…`, `itm_…`. Treat them as opaque. |
| Timestamps | ISO 8601 in UTC: `2026-09-27T12:41:07Z` |
| Business dates | `YYYY-MM-DD` in the location's time zone, e.g. `"businessDate": "2026-09-27"` |
| Money and quantities | **Decimal strings**: `"12.50"`, `"0.250000"`. Never floats. |
| Currency | ISO 4217 code alongside amounts, defaulting to the location's currency |
| External IDs | Any record you sync can carry your system's ID in `externalId` |
| Unknown fields | New fields may be added at any time, so **ignore fields you don't recognize** |

### Operations

| Operation | Pattern |
|---|---|
| List | `GET /employees?limit=50&cursor=…&filter[status]=active&sort=-updatedAt` |
| Get | `GET /employees/{id}` or `GET /employees/external/{externalId}` |
| Create | `POST /employees` |
| Update | `PATCH /employees/{id}` (partial). Send `If-Match: <version>` to avoid overwriting someone else's change. |
| Upsert by external ID | `PUT /employees/external/{externalId}` |
| Archive | `DELETE /employees/{id}` (soft, history kept) |
| Batch | `POST /time/punches:batch` (up to 1,000 records, result per record) |
| Actions | `POST /timesheets/{id}:approve`, `POST /purchase-orders/{id}:receive` |
| Changes since | `GET /employees?updatedSince=2026-09-01T00:00:00Z` |

### Pagination

Lists use cursors. The default `limit` is 50 and the maximum is 200.

```json
{
  "data": [ { "id": "emp_01J8Z…", "firstName": "Dana" } ],
  "nextCursor": "eyJpZCI6ImVtcF8wMUo4…",
  "hasMore": true
}
```

Pass `cursor=<nextCursor>` to get the next page.

### Filtering and sorting

`filter[field]=value` (and `filter[field][gte]=…`, `[lte]`, `[in]=a,b`), `sort=field` or `sort=-field`, and `fields=id,firstName` to return fewer fields. Most lists accept `locationId`.

## Errors

Errors use RFC 9457 Problem Details with a stable `code`:

```json
{
  "type": "https://purros.dev/errors/validation",
  "title": "Validation failed",
  "status": 422,
  "code": "validation_error",
  "requestId": "req_01J8Z…",
  "errors": [{ "path": "lines[0].quantity", "message": "Must be a decimal string" }]
}
```

| Status | `code` | Meaning |
|---|---|---|
| 400 | `bad_request` | Malformed JSON or parameters |
| 401 | `unauthorized` | Missing, invalid, expired or revoked key, or wrong sign-in details |
| 401 | `session_expired` | The session ended; sign in again |
| 401 | `mfa_required` | Finish signing in at `POST /auth/sign-in/mfa` |
| 403 | `forbidden` | The key lacks the scope, or the person lacks the permission |
| 403 | `out_of_reach` | The person's permission doesn't reach this location or employee |
| 403 | `mfa_enrollment_required` | The company requires two-factor authentication; set it up first |
| 404 | `not_found` | No such record |
| 404 | `feature_disabled` | The feature is switched off |
| 409 | `conflict` | Duplicate, or an idempotency key reused with a different body |
| 409 | `version_mismatch` | `If-Match` didn't match the current version |
| 409 | `period_locked` | The business day or pay period is locked |
| 422 | `validation_error` | Field errors in `errors[]` |
| 422 | `insufficient_stock` | Not enough stock to reserve or ship |
| 429 | `rate_limited` | Slow down, and see `Retry-After` |
| 5xx | `internal_error` | Retry with backoff, and quote `requestId` if reporting it |

Every response includes an `X-Request-Id` header.

## Idempotency

Send an `Idempotency-Key` header on any `POST`. If a request with the same key is repeated within 24 hours, you get the original response instead of a duplicate. The same key with a different body returns `409 conflict`.

Ingestion endpoints are also idempotent by `(source, externalId)`, so re-sending the same record updates it rather than duplicating it. See [Data ingestion](data-ingestion.md).

## Rate limits

| Key type | Default |
|---|---|
| Interactive / personal keys | 600 requests per minute |
| Keys sending data feeds | 3,000 requests per minute |

Every response has `RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset`. When over the limit you get `429` with `Retry-After`. Use batch endpoints for volume: one batch of 1,000 records counts as one request.

## Versioning

- `/api/v1` gets **no breaking changes**. New endpoints, fields and event types can be added at any time.
- A breaking change means a new `/api/v2`, and v1 remains supported for at least 12 months after that.
- Deprecated endpoints send `Deprecation` and `Sunset` headers and are listed in the changelog.

## Next

- [Endpoint index](endpoints.md)
- [Data ingestion](data-ingestion.md)
- [Webhooks](webhooks.md)
