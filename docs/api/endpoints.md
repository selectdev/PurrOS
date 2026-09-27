# Endpoint index

All paths are relative to `/api/v1`. This index lists the resources. The full request and response schemas are in the OpenAPI spec at `/api/v1/openapi.json`, or the interactive docs at `/docs/api`.

**R** = read scope, **W** = write scope. Endpoints of switched-off features return `404 feature_disabled`.

## Platform

| Method & path | Scope | Description |
|---|---|---|
| `GET /me` | any | The calling key: type, user or integration, scopes |
| `GET /features` | any | Enabled features and smaller features |
| `GET /permissions` | any | Permission catalog for enabled features |
| `GET /integrations/self` | integration key | Registration, scopes, status |
| `GET /integrations/self/config` | integration key | Admin-entered config (secrets decrypted) |
| `POST /integrations/self/health` | integration key | Heartbeat and status message |
| `POST /integrations/self/logs` | integration key | Sync summaries shown in the admin UI |
| `GET /openapi.json` | none | OpenAPI spec |

## Organization

| Method & path | Scope |
|---|---|
| `GET /org-units` | `organization:read` |
| `GET /locations`, `GET /locations/{id}`, `GET /locations/external/{externalId}` | `organization:read` |
| `GET /departments` | `organization:read` |
| `GET /roles`, `GET /users` | `organization:read` |

## People & HR

| Method & path | Scope |
|---|---|
| `GET /employees`, `GET /employees/{id}`, `GET /employees/external/{externalId}` | `people` R |
| `POST /employees`, `PATCH /employees/{id}`, `PUT /employees/external/{externalId}` | `people` W |
| `POST /employees/{id}:transfer`, `:terminate`, `:rehire` | `people` W |
| `GET/POST /employees/{id}/documents` | `people` R/W |
| `GET /skills`, `GET/POST /employees/{id}/skills` | `people` R/W |
| `GET/POST /employees/{id}/pay-rates` | `payroll` R/W |
| `POST /payslips`, `GET /payslips` | `payroll` W/R |

## Time & Attendance

| Method & path | Scope |
|---|---|
| `POST /time/punches:batch` | `time` W |
| `GET /time/punches` | `time` R |
| `GET /timesheets`, `GET /timesheets/{id}` | `time` R |
| `GET /time-off/requests`, `POST /time-off/requests`, `GET /time-off/balances` | `time` R/W |
| `GET /pay-periods`, `GET /pay-periods/{id}/export?template=…` | `payroll` R |

## Scheduling & Forecasting

| Method & path | Scope |
|---|---|
| `POST /demand-drivers` (batch) | `scheduling` W |
| `GET /forecasts` | `scheduling` R |
| `GET /schedules`, `GET /shifts` | `scheduling` R |
| `POST /shifts`, `PATCH /shifts/{id}` | `scheduling` W |
| `GET /availability` | `scheduling` R |

## Cash Management

| Method & path | Scope |
|---|---|
| `POST /cash/tenders` | `cash` W |
| `POST /cash/settlements` | `cash` W |
| `POST /cash/bank-transactions` | `cash` W |
| `GET /cash/business-days`, `GET /cash/counts`, `GET /cash/deposits` | `cash` R |

## Inventory

| Method & path | Scope |
|---|---|
| `GET /items`, `GET /items/{id}`, `GET /items/external/{externalId}` | `inventory` R |
| `POST /items`, `PATCH /items/{id}`, `PUT /items/external/{externalId}` | `inventory` W |
| `GET /stock-levels`, `GET /stock-movements` | `inventory` R |
| `POST /inventory/adjustments`, `POST /inventory/waste` | `inventory` W |
| `GET/POST /stock-counts` | `inventory` R/W |
| `GET/POST /transfers`, `POST /transfers/{id}:receive` | `inventory` R/W |
| `GET/PUT /usage-recipes/{itemId}` | `inventory` R/W |

## Purchasing & Ordering

| Method & path | Scope |
|---|---|
| `GET/POST /suppliers`, `GET/PUT /suppliers/{id}/catalog` | `purchasing` R/W |
| `GET /suggested-orders` | `purchasing` R |
| `GET/POST /purchase-orders`, `POST /purchase-orders/{id}:approve`, `:send`, `:receive` | `purchasing` R/W |
| `POST /supplier-invoices`, `GET /supplier-invoices` | `purchasing` W/R |

## Sales

| Method & path | Scope |
|---|---|
| `POST /sales/transactions:batch` | `sales` W |
| `GET /sales/transactions` | `sales` R |
| `POST /sales-summaries`, `GET /sales-summaries` | `sales` W/R |
| `GET/POST /customers`, `PUT /customers/external/{externalId}` | `sales` R/W |
| `GET/POST /sales-orders`, `PUT /sales-orders/external/{externalId}` | `sales` R/W |
| `POST /sales-orders/{id}:ship`, `:cancel` | `sales` W |
| `GET /invoices`, `GET /invoices/{id}/pdf` | `sales` R |

## Forms, Checklists & Compliance

| Method & path | Scope |
|---|---|
| `GET /forms`, `GET /form-submissions` | `operations` R |
| `POST /form-submissions` | `operations` W |
| `GET /corrective-actions`, `GET /audits` | `operations` R |
| `POST /sensor-readings` (batch) | `operations` W |

## Equipment & Assets

| Method & path | Scope |
|---|---|
| `GET/POST /assets`, `PUT /assets/external/{externalId}` | `equipment` R/W |
| `POST /assets/{id}/meter-readings` | `equipment` W |
| `GET/POST /work-orders`, `PATCH /work-orders/{id}` | `equipment` R/W |

## Communication & Team Displays

| Method & path | Scope |
|---|---|
| `GET/POST /announcements` | `communication` R/W |
| `GET/POST /calendar-events` | `communication` R/W |
| `POST /recognitions` | `communication` W |
| `POST /display-metrics` | `reports` W |

## Reports & Insights

| Method & path | Scope |
|---|---|
| `GET /kpis` | `reports` R |
| `GET /reports/{reportKey}` | `reports` R |
| `GET /recommendations` | `reports` R |

## Webhooks

Webhook endpoints are managed in the UI (**Settings → Webhooks**) or declared in an integration's manifest. See [Webhooks](webhooks.md).
