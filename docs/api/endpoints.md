# Endpoint index

All paths are relative to `/api/v1`. This index is generated from the API's route table. The full request and response schemas are in the OpenAPI spec at `/api/v1/openapi.json` (it lists only enabled features), or the interactive docs at `/docs/api`.

Endpoints of switched-off features return `404 feature_disabled`. The **Feature** column is the feature switch each endpoint belongs to.

## Who can call what

| Column | Meaning |
|---|---|
| **Integration key** | The scope an integration key needs. `—` means integration keys can't call it. |
| **People** | The [permission](../admin/permissions-reference.md) a person needs, through a signed-in session or a personal API key. `self` means any signed-in person, for their own data only; `(session)` means a browser session is required, not a personal key; `anyone` means any signed-in person; `—` means people can't call it (data feeds are for integrations). Owners can call everything open to people. |

When a person holds the permission with a reach narrower than **Everyone**, the request must stay inside it: filter lists with `locationId` (or `employeeId`), and records are checked against their location or employee. Anything outside returns `403 out_of_reach`. See [Users, roles & permissions](../admin/users-and-roles.md#reach).

Conventions used throughout:

- Lists are cursor-paginated (`limit`, `cursor` → `nextCursor`) and most accept `updatedSince`.
- Resources with an `externalId` can be read and upserted at `…/external/{externalId}`.
- `DELETE` archives (soft delete); history is kept.
- `PATCH` is a JSON merge patch and accepts `If-Match` with the record's `version`.
- Batch endpoints (`202`) are idempotent on `source` + `externalId` and return per-record results. See [Data ingestion](data-ingestion.md).
- Actions on a record use a colon: `POST /employees/{id}:terminate`.

## Authentication

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /auth/api-keys` | — | self (session) | — | Your personal API keys |
| `POST /auth/api-keys` | — | `api_keys.personal` (session) | — | Create a personal API key |
| `DELETE /auth/api-keys/{id}` | — | self (session) | — | Revoke one of your personal API keys |
| `POST /auth/invitations:accept` | public | public | — | Accept an invitation and set a password |
| `POST /auth/magic-link` | public | public | — | Email a single-use sign-in link |
| `POST /auth/magic-link:redeem` | public | public | — | Sign in with a magic link |
| `POST /auth/mfa/recovery-codes:regenerate` | — | self (session) | — | Replace your recovery codes |
| `POST /auth/mfa/totp:confirm` | — | self (session) | — | Turn on two-factor authentication with a first code |
| `POST /auth/mfa/totp:disable` | — | self (session) | — | Turn off two-factor authentication |
| `POST /auth/mfa/totp:setup` | — | self (session) | — | Start setting up an authenticator app |
| `POST /auth/password` | — | self (session) | — | Change your password |
| `POST /auth/password-reset` | public | public | — | Email a password reset link |
| `POST /auth/password-reset:complete` | public | public | — | Choose a new password with a reset link |
| `GET /auth/session` | — | self | — | The signed-in person: role, permissions, assignments and enabled features |
| `GET /auth/sessions` | — | self (session) | — | Your active sessions and devices |
| `DELETE /auth/sessions/{id}` | — | self (session) | — | Sign out one of your sessions |
| `POST /auth/sign-in` | public | public | — | Sign in with email and password |
| `POST /auth/sign-in/mfa` | — | self (session) | — | Finish signing in with an authenticator or recovery code |
| `POST /auth/sign-out` | — | self (session) | — | Sign out this session |

## Users & Roles

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `POST /roles` | — | `roles.manage` (Everyone) | — | Create a role |
| `PATCH /roles/{id}` | — | `roles.manage` (Everyone) | — | Change a role |
| `DELETE /roles/{id}` | — | `roles.manage` (Everyone) | — | Delete a role |
| `GET /settings/authentication` | — | `settings.manage` (Everyone) | — | Sign-in settings |
| `PATCH /settings/authentication` | — | `settings.manage` (Everyone) | — | Change sign-in settings |
| `POST /users` | — | `users.manage` (Everyone) | — | Invite someone |
| `PATCH /users/{id}` | — | `users.manage` (Everyone) | — | Change someone's name, role, employee link or assignments |
| `POST /users/{id}:deactivate` | — | `users.manage` (Everyone) | — | Deactivate an account |
| `POST /users/{id}:reactivate` | — | `users.manage` (Everyone) | — | Reactivate an account |
| `POST /users/{id}:reset-sign-in` | — | `users.manage` (Everyone) | — | Reset someone's sign-in |

## Platform

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /features` | any key | anyone | — | Features and whether they are enabled |
| `GET /me` | any key | anyone | — | The calling API key |
| `GET /openapi.json` | public | public | — | OpenAPI document for enabled features |
| `GET /permissions` | any key | anyone | — | Permission catalog for enabled features |

## Integration self-service

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /integrations/self` | integration key | — | — | The calling integration's registration |
| `GET /integrations/self/config` | integration key | — | — | Config values entered by the admin |
| `POST /integrations/self/health` | integration key | — | — | Send a heartbeat and status message |
| `POST /integrations/self/logs` | integration key | — | — | Add a log message shown in the admin UI |

## Attachments

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /attachments` | `attachments:read` | `attachments.read` | — | List attachments |
| `POST /attachments` | `attachments:write` | anyone | — | Upload a file |
| `GET /attachments/{id}` | `attachments:read` | uploader, the employee concerned, or `attachments.read` | — | Get an attachment's details |
| `DELETE /attachments/{id}` | `attachments:write` | uploader, or `attachments.manage` | — | Delete an attachment |
| `GET /attachments/{id}/content` | `attachments:read` | uploader, the employee concerned, or `attachments.read` | — | Download an attachment |
| `GET /me/attachments` | — | self | — | Files you uploaded or that concern you |

## Organization

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /departments` | `organization:read` | anyone | — | List departments |
| `GET /locations` | `organization:read` | anyone | — | List locations |
| `GET /locations/external/{externalId}` | `organization:read` | anyone | — | Get a location by external ID |
| `GET /locations/{id}` | `organization:read` | anyone | — | Get a location |
| `GET /org-units` | `organization:read` | anyone | — | List org units (regions, districts…) |
| `GET /roles` | `organization:read` | `users.read` | — | List roles and their permissions |
| `GET /roles/{id}` | `organization:read` | `users.read` | — | Get a role |
| `GET /users` | `organization:read` | `users.read` | — | List user accounts with their role and assignments |
| `GET /users/{id}` | `organization:read` | `users.read` | — | Get a user |

## People & HR

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /employees` | `people:read` | `employees.read` | people | List employees |
| `POST /employees` | `people:write` | `employees.write` | people | Create an employee |
| `GET /employees/external/{externalId}` | `people:read` | `employees.read` | people | Get an employee by external ID |
| `PUT /employees/external/{externalId}` | `people:write` | `employees.write` | people | Create or replace an employee by external ID |
| `GET /employees/{employeeId}/documents` | `people:read` | `documents.read` | people.documents | List employee documents |
| `POST /employees/{employeeId}/documents` | `people:write` | `documents.manage` | people.documents | Create an employee document |
| `GET /employees/{employeeId}/documents/{id}` | `people:read` | `documents.read` | people.documents | Get an employee document |
| `PATCH /employees/{employeeId}/documents/{id}` | `people:write` | `documents.manage` | people.documents | Update an employee document (partial) |
| `DELETE /employees/{employeeId}/documents/{id}` | `people:write` | `documents.manage` | people.documents | Archive an employee document |
| `GET /employees/{id}` | `people:read` | `employees.read` | people | Get an employee |
| `PATCH /employees/{id}` | `people:write` | `employees.write` | people | Update an employee (partial) |
| `DELETE /employees/{id}` | `people:write` | `employees.write` | people | Archive an employee |
| `GET /employees/{id}/pay-rates` | `payroll:read` | `pay.read` | people | Pay rate history (newest first) |
| `POST /employees/{id}/pay-rates` | `payroll:write` | `pay.write` | people | Add a pay rate |
| `GET /employees/{id}/skills` | `people:read` | `employees.read` | people.skills | An employee's skills and certifications |
| `PUT /employees/{id}/skills/{skillId}` | `people:write` | `skills.manage` | people.skills | Give an employee a skill or certification |
| `DELETE /employees/{id}/skills/{skillId}` | `people:write` | `skills.manage` | people.skills | Remove a skill from an employee |
| `POST /employees/{id}:rehire` | `people:write` | `employees.write` | people | Rehire a terminated employee |
| `POST /employees/{id}:terminate` | `people:write` | `employees.write` | people | Terminate an employee |
| `POST /employees/{id}:transfer` | `people:write` | `employees.write` | people | Transfer an employee (location, department, position or manager) |
| `GET /payslips` | `payroll:read` | `pay.read` | employee_area.payslips | List payslips |
| `POST /payslips` | `payroll:write` | — | employee_area.payslips | Send payslips from your payroll provider |
| `GET /skills` | `people:read` | `employees.read` | people.skills | List skills |
| `POST /skills` | `people:write` | `skills.manage` | people.skills | Create a skill |
| `GET /skills/external/{externalId}` | `people:read` | `employees.read` | people.skills | Get a skill by external ID |
| `PUT /skills/external/{externalId}` | `people:write` | `skills.manage` | people.skills | Create or replace a skill by external ID |
| `GET /skills/{id}` | `people:read` | `employees.read` | people.skills | Get a skill |
| `PATCH /skills/{id}` | `people:write` | `skills.manage` | people.skills | Update a skill (partial) |
| `DELETE /skills/{id}` | `people:write` | `skills.manage` | people.skills | Archive a skill |

## Time & Attendance

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /labor-rule-sets` | `time:read` | `timesheets.read` | time | List labor rule sets |
| `POST /labor-rule-sets` | `time:write` | `labor_rules.manage` | time | Create a labor rule set |
| `GET /labor-rule-sets/{id}` | `time:read` | `timesheets.read` | time | Get a labor rule set |
| `PATCH /labor-rule-sets/{id}` | `time:write` | `labor_rules.manage` | time | Update a labor rule set (partial) |
| `GET /pay-periods` | `payroll:read` | `timesheets.read` | time | List pay periods (newest first) |
| `POST /pay-periods` | `payroll:write` | `pay_periods.lock` | time | Create a pay period |
| `GET /pay-periods/{id}` | `payroll:read` | `timesheets.read` | time | Get a pay period |
| `GET /pay-periods/{id}/export` | `payroll:read` | `payroll.export` | time.payroll_export | Export hours for payroll |
| `POST /pay-periods/{id}:lock` | `payroll:write` | `pay_periods.lock` | time | Lock a pay period (final payroll approval) |
| `GET /punch-corrections` | `time:read` | `punches.correct` | time | List punch corrections |
| `POST /punch-corrections/{id}:approve` | `time:write` | `punches.correct` | time | Approve a punch correction |
| `POST /punch-corrections/{id}:reject` | `time:write` | `punches.correct` | time | Reject a punch correction |
| `POST /time-off/adjustments` | `time:write` | `time_off.approve` | time.time_off | Add or deduct time-off hours (accruals, corrections) |
| `GET /time-off/balances` | `time:read` | `time_off.read` | time.time_off | Time-off balances per employee and type |
| `GET /time-off/requests` | `time:read` | `time_off.read` | time.time_off | List time-off requests |
| `POST /time-off/requests` | `time:write` | `time_off.approve` | time.time_off | Request time off |
| `POST /time-off/requests/{id}:approve` | `time:write` | `time_off.approve` | time.time_off | Approve a time-off request (deducts the balance) |
| `POST /time-off/requests/{id}:cancel` | `time:write` | `time_off.approve` | time.time_off | Cancel a time-off request (restores the balance if it was approved) |
| `POST /time-off/requests/{id}:reject` | `time:write` | `time_off.approve` | time.time_off | Reject a time-off request |
| `GET /time-off/types` | `time:read` | anyone | time.time_off | List time off types |
| `POST /time-off/types` | `time:write` | `settings.manage` | time.time_off | Create a time off type |
| `GET /time-off/types/external/{externalId}` | `time:read` | anyone | time.time_off | Get a time off type by external ID |
| `PUT /time-off/types/external/{externalId}` | `time:write` | `settings.manage` | time.time_off | Create or replace a time off type by external ID |
| `GET /time-off/types/{id}` | `time:read` | anyone | time.time_off | Get a time off type |
| `PATCH /time-off/types/{id}` | `time:write` | `settings.manage` | time.time_off | Update a time off type (partial) |
| `DELETE /time-off/types/{id}` | `time:write` | `settings.manage` | time.time_off | Archive a time off type |
| `GET /time/punches` | `time:read` | `punches.read` | time | List punches |
| `POST /time/punches:batch` | `time:write` | — | time | Send punches in bulk |
| `GET /timesheets` | `time:read` | `timesheets.read` | time | List timesheets |
| `GET /timesheets/{id}` | `time:read` | `timesheets.read` | time | Get a timesheet with daily detail and exceptions |
| `POST /timesheets/{id}:approve` | `time:write` | `timesheets.approve` | time | Approve a timesheet (manager step) |
| `POST /timesheets/{id}:reject` | `time:write` | `timesheets.approve` | time | Reject a timesheet |
| `POST /timesheets:build` | `time:write` | `timesheets.approve` | time | Build or refresh timesheets from punches |

## Scheduling & Forecasting

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /availability` | `scheduling:read` | `schedules.read` | scheduling | List availabilitys |
| `POST /availability` | `scheduling:write` | `schedules.manage` | scheduling | Create an availability |
| `GET /availability/{id}` | `scheduling:read` | `schedules.read` | scheduling | Get an availability |
| `PATCH /availability/{id}` | `scheduling:write` | `schedules.manage` | scheduling | Update an availability (partial) |
| `DELETE /availability/{id}` | `scheduling:write` | `schedules.manage` | scheduling | Archive an availability |
| `POST /demand-drivers` | `scheduling:write` | — | scheduling | Send demand drivers (appointments, foot traffic, orders…) |
| `GET /forecasts` | `scheduling:read` | `forecasts.read` | scheduling.forecasting | Forecast a demand driver |
| `POST /forecasts/adjustments` | `scheduling:write` | `forecasts.manage` | scheduling.forecasting | Adjust a day's forecast by a percentage |
| `GET /schedules` | `scheduling:read` | `schedules.read` | scheduling | A location's schedule with hours and estimated cost |
| `POST /schedules:publish` | `scheduling:write` | `schedules.publish` | scheduling | Publish draft shifts for a location and period |
| `GET /shift-swaps` | `scheduling:read` | `schedules.read` | scheduling.shift_swaps | List shift swap requests |
| `POST /shift-swaps` | `scheduling:write` | `schedules.manage` | scheduling.shift_swaps | Request a shift swap |
| `POST /shift-swaps/{id}:approve` | `scheduling:write` | `shift_swaps.approve` | scheduling.shift_swaps | Approve a swap (reassigns the shift) |
| `POST /shift-swaps/{id}:reject` | `scheduling:write` | `shift_swaps.approve` | scheduling.shift_swaps | Reject a swap |
| `GET /shifts` | `scheduling:read` | `schedules.read` | scheduling | List shifts |
| `POST /shifts` | `scheduling:write` | `schedules.manage` | scheduling | Create a shift |
| `GET /shifts/external/{externalId}` | `scheduling:read` | `schedules.read` | scheduling | Get a shift by external ID |
| `PUT /shifts/external/{externalId}` | `scheduling:write` | `schedules.manage` | scheduling | Create or replace a shift by external ID |
| `GET /shifts/{id}` | `scheduling:read` | `schedules.read` | scheduling | Get a shift |
| `PATCH /shifts/{id}` | `scheduling:write` | `schedules.manage` | scheduling | Update a shift (partial) |
| `POST /shifts/{id}:claim` | `scheduling:write` | `schedules.manage` | scheduling.open_shifts | Assign an open shift to an employee |
| `GET /staffing-needs` | `scheduling:read` | `schedules.read` | scheduling | Staff needed vs scheduled, hour by hour |
| `GET /staffing-rules` | `scheduling:read` | `schedules.read` | scheduling | List staffing rules |
| `POST /staffing-rules` | `scheduling:write` | `staffing_rules.manage` | scheduling | Create a staffing rule |
| `GET /staffing-rules/{id}` | `scheduling:read` | `schedules.read` | scheduling | Get a staffing rule |
| `PATCH /staffing-rules/{id}` | `scheduling:write` | `staffing_rules.manage` | scheduling | Update a staffing rule (partial) |
| `DELETE /staffing-rules/{id}` | `scheduling:write` | `staffing_rules.manage` | scheduling | Archive a staffing rule |

## Cash Management

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `POST /cash/bank-transactions` | `cash:write` | — | cash | Send bank transactions for deposit verification |
| `GET /cash/business-days` | `cash:read` | `cash.read` | cash | Daily cash summary for a location |
| `POST /cash/business-days/{locationId}/{date}:close` | `cash:write` | `cash.manage` | cash | Close a business day |
| `GET /cash/counts` | `cash:read` | `cash.read` | cash | List counts |
| `POST /cash/counts` | `cash:write` | `cash.count` | cash | Record a drawer, skim or safe count |
| `GET /cash/deposits` | `cash:read` | `cash.read` | cash | List deposits |
| `POST /cash/deposits` | `cash:write` | `cash.deposits` | cash | Record a bank deposit |
| `POST /cash/settlements` | `cash:write` | — | cash | Send card processor and platform payouts |
| `POST /cash/tenders` | `cash:write` | — | cash | Send tender totals per drawer or shift from the POS |

## Inventory

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `POST /inventory/adjustments` | `inventory:write` | `inventory.adjust` | inventory | Adjust stock |
| `POST /inventory/waste` | `inventory:write` | `waste.record` | inventory.waste | Record waste or shrink |
| `GET /items` | `inventory:read` | `inventory.read` | inventory | List items |
| `POST /items` | `inventory:write` | `items.manage` | inventory | Create an item |
| `GET /items/external/{externalId}` | `inventory:read` | `inventory.read` | inventory | Get an item by external ID |
| `PUT /items/external/{externalId}` | `inventory:write` | `items.manage` | inventory | Create or replace an item by external ID |
| `GET /items/{id}` | `inventory:read` | `inventory.read` | inventory | Get an item |
| `PATCH /items/{id}` | `inventory:write` | `items.manage` | inventory | Update an item (partial) |
| `GET /stock-counts` | `inventory:read` | `inventory.read` | inventory | List stock counts |
| `POST /stock-counts` | `inventory:write` | `stock_counts.count` | inventory | Enter a stock count |
| `GET /stock-counts/{id}` | `inventory:read` | `inventory.read` | inventory | Get a stock count with variances |
| `POST /stock-counts/{id}:cancel` | `inventory:write` | `stock_counts.manage` | inventory | Cancel an open count |
| `POST /stock-counts/{id}:post` | `inventory:write` | `stock_counts.manage` | inventory | Post a count to the stock ledger |
| `GET /stock-levels` | `inventory:read` | `inventory.read` | inventory | Stock on hand, reserved and available |
| `POST /stock-levels:configure` | `inventory:write` | `items.manage` | inventory | Set par level and reorder point for an item at a location |
| `GET /stock-movements` | `inventory:read` | `inventory.read` | inventory | The stock ledger |
| `GET /transfers` | `inventory:read` | `inventory.read` | inventory.transfers | List transfers |
| `POST /transfers` | `inventory:write` | `transfers.manage` | inventory.transfers | Send stock to another location |
| `GET /transfers/{id}` | `inventory:read` | `inventory.read` | inventory.transfers | Get a transfer |
| `POST /transfers/{id}:receive` | `inventory:write` | `transfers.manage` | inventory.transfers | Confirm what arrived |
| `GET /usage-recipes/{itemId}` | `inventory:read` | `inventory.read` | inventory.usage_recipes | What one unit of a sold item uses |
| `PUT /usage-recipes/{itemId}` | `inventory:write` | `usage_recipes.manage` | inventory.usage_recipes | Replace a usage recipe |

## Purchasing & Ordering

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /purchase-orders` | `purchasing:read` | `purchasing.read` | purchasing | List purchase orders |
| `POST /purchase-orders` | `purchasing:write` | `orders.create` | purchasing | Create a purchase order |
| `GET /purchase-orders/{id}` | `purchasing:read` | `purchasing.read` | purchasing | Get a purchase order |
| `POST /purchase-orders/{id}:approve` | `purchasing:write` | `purchase_orders.approve` | purchasing | Approve a purchase order |
| `POST /purchase-orders/{id}:cancel` | `purchasing:write` | `orders.create` | purchasing | Cancel a purchase order |
| `POST /purchase-orders/{id}:receive` | `purchasing:write` | `goods_receipts.create` | purchasing | Receive a delivery |
| `POST /purchase-orders/{id}:send` | `purchasing:write` | `orders.create` | purchasing | Mark a purchase order as sent to the supplier |
| `GET /suggested-orders` | `purchasing:read` | `orders.create` | purchasing.suggested_orders | Suggested order for a supplier and location |
| `GET /supplier-invoices` | `purchasing:read` | `purchasing.read` | purchasing | List supplier invoices |
| `POST /supplier-invoices` | `purchasing:write` | `invoices.match` | purchasing | Record a supplier invoice and match it |
| `POST /supplier-invoices/{id}:approve` | `purchasing:write` | `invoices.match` | purchasing.invoice_matching | Approve a supplier invoice (accept any differences) |
| `POST /supplier-invoices/{id}:dispute` | `purchasing:write` | `invoices.match` | purchasing.invoice_matching | Dispute a supplier invoice |
| `GET /suppliers` | `purchasing:read` | `purchasing.read` | purchasing | List suppliers |
| `POST /suppliers` | `purchasing:write` | `suppliers.manage` | purchasing | Create a supplier |
| `GET /suppliers/external/{externalId}` | `purchasing:read` | `purchasing.read` | purchasing | Get a supplier by external ID |
| `PUT /suppliers/external/{externalId}` | `purchasing:write` | `suppliers.manage` | purchasing | Create or replace a supplier by external ID |
| `GET /suppliers/{id}` | `purchasing:read` | `purchasing.read` | purchasing | Get a supplier |
| `PATCH /suppliers/{id}` | `purchasing:write` | `suppliers.manage` | purchasing | Update a supplier (partial) |
| `DELETE /suppliers/{id}` | `purchasing:write` | `suppliers.manage` | purchasing | Archive a supplier |
| `GET /suppliers/{id}/catalog` | `purchasing:read` | `purchasing.read` | purchasing | A supplier's catalog (order guide) |
| `PUT /suppliers/{id}/catalog` | `purchasing:write` | `suppliers.manage` | purchasing | Replace a supplier's catalog |

## Sales

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /customers` | `sales:read` | `sales.read` | sales.orders | List customers |
| `POST /customers` | `sales:write` | `customers.manage` | sales.orders | Create a customer |
| `GET /customers/external/{externalId}` | `sales:read` | `sales.read` | sales.orders | Get a customer by external ID |
| `PUT /customers/external/{externalId}` | `sales:write` | `customers.manage` | sales.orders | Create or replace a customer by external ID |
| `GET /customers/{id}` | `sales:read` | `sales.read` | sales.orders | Get a customer |
| `PATCH /customers/{id}` | `sales:write` | `customers.manage` | sales.orders | Update a customer (partial) |
| `DELETE /customers/{id}` | `sales:write` | `customers.manage` | sales.orders | Archive a customer |
| `GET /invoices` | `sales:read` | `sales.read` | sales.invoicing | List invoices |
| `GET /invoices/{id}` | `sales:read` | `sales.read` | sales.invoicing | Get an invoice |
| `GET /invoices/{id}/pdf` | `sales:read` | `sales.read` | sales.invoicing | Download an invoice as PDF |
| `POST /invoices/{id}:pay` | `sales:write` | `invoices.issue` | sales.invoicing | Record payment of an invoice |
| `POST /invoices/{id}:void` | `sales:write` | `invoices.issue` | sales.invoicing | Void an invoice |
| `GET /sales-orders` | `sales:read` | `sales.read` | sales.orders | List sales orders |
| `POST /sales-orders` | `sales:write` | `sales_orders.manage` | sales.orders | Create a sales order (reserves stock) |
| `PUT /sales-orders/external/{externalId}` | `sales:write` | `sales_orders.manage` | sales.orders | Create or update an order from another system (e.g. an online store) |
| `GET /sales-orders/{id}` | `sales:read` | `sales.read` | sales.orders | Get a sales order |
| `POST /sales-orders/{id}:cancel` | `sales:write` | `sales_orders.manage` | sales.orders | Cancel an order (releases reserved stock) |
| `POST /sales-orders/{id}:invoice` | `sales:write` | `invoices.issue` | sales.invoicing | Issue an invoice for an order |
| `POST /sales-orders/{id}:ship` | `sales:write` | `sales_orders.manage` | sales.orders | Ship an order (deducts stock) |
| `GET /sales-summaries` | `sales:read` | `sales.read` | sales.feeds | List sales summaries |
| `POST /sales-summaries` | `sales:write` | — | sales.feeds | Send hourly or daily sales totals |
| `GET /sales/transactions` | `sales:read` | `sales.read` | sales.feeds | List sales transactions |
| `POST /sales/transactions:batch` | `sales:write` | — | sales.feeds | Send sales transactions from a POS or online store |
| `GET /sales/unmapped-items` | `sales:read` | `sales.read` | sales.feeds | Items in sales feeds that aren't linked to a PurrOS item |
| `POST /sales/unmapped-items:map` | `sales:write` | `sales.unmapped.resolve` | sales.feeds | Link an unmapped item reference to a PurrOS item |

## Forms, Checklists & Compliance

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /audits` | `operations:read` | `audits.read` | operations.audits | List scored audits |
| `GET /corrective-actions` | `operations:read` | `corrective_actions.manage` | operations.corrective_actions | List corrective actions |
| `POST /corrective-actions` | `operations:write` | `corrective_actions.manage` | operations.corrective_actions | Create a corrective action |
| `GET /corrective-actions/{id}` | `operations:read` | `corrective_actions.manage` | operations.corrective_actions | Get a corrective action |
| `PATCH /corrective-actions/{id}` | `operations:write` | `corrective_actions.manage` | operations.corrective_actions | Update a corrective action (partial) |
| `POST /corrective-actions/{id}:close` | `operations:write` | `corrective_actions.manage` | operations.corrective_actions | Close a corrective action with its resolution |
| `GET /form-submissions` | `operations:read` | `checklists.manage` | operations | List submissions |
| `POST /form-submissions` | `operations:write` | `checklists.complete` | operations | Submit a completed form or checklist |
| `GET /form-submissions/{id}` | `operations:read` | `checklists.manage` | operations | Get a submission with per-question results |
| `GET /forms` | `operations:read` | `checklists.complete` | operations | List forms |
| `POST /forms` | `operations:write` | `forms.manage` | operations | Create a form |
| `GET /forms/external/{externalId}` | `operations:read` | `checklists.complete` | operations | Get a form by external ID |
| `PUT /forms/external/{externalId}` | `operations:write` | `forms.manage` | operations | Create or replace a form by external ID |
| `GET /forms/{id}` | `operations:read` | `checklists.complete` | operations | Get a form |
| `PATCH /forms/{id}` | `operations:write` | `forms.manage` | operations | Update a form (partial) |
| `DELETE /forms/{id}` | `operations:write` | `forms.manage` | operations | Archive a form |
| `POST /sensor-readings` | `operations:write` | — | operations.sensors | Send sensor readings |
| `GET /sensors` | `operations:read` | `sensors.manage` | operations.sensors | List sensors |
| `POST /sensors` | `operations:write` | `sensors.manage` | operations.sensors | Create a sensor |
| `GET /sensors/external/{externalId}` | `operations:read` | `sensors.manage` | operations.sensors | Get a sensor by external ID |
| `PUT /sensors/external/{externalId}` | `operations:write` | `sensors.manage` | operations.sensors | Create or replace a sensor by external ID |
| `GET /sensors/{id}` | `operations:read` | `sensors.manage` | operations.sensors | Get a sensor |
| `PATCH /sensors/{id}` | `operations:write` | `sensors.manage` | operations.sensors | Update a sensor (partial) |
| `DELETE /sensors/{id}` | `operations:write` | `sensors.manage` | operations.sensors | Archive a sensor |
| `GET /sensors/{id}/readings` | `operations:read` | `sensors.manage` | operations.sensors | Recent readings for a sensor (newest first, up to 1,000) |

## Equipment & Assets

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /assets` | `equipment:read` | `equipment.read` | equipment | List assets |
| `POST /assets` | `equipment:write` | `equipment.manage` | equipment | Create an asset |
| `GET /assets/external/{externalId}` | `equipment:read` | `equipment.read` | equipment | Get an asset by external ID |
| `PUT /assets/external/{externalId}` | `equipment:write` | `equipment.manage` | equipment | Create or replace an asset by external ID |
| `GET /assets/{id}` | `equipment:read` | `equipment.read` | equipment | Get an asset |
| `PATCH /assets/{id}` | `equipment:write` | `equipment.manage` | equipment | Update an asset (partial) |
| `DELETE /assets/{id}` | `equipment:write` | `equipment.manage` | equipment | Archive an asset |
| `POST /assets/{id}/meter-readings` | `equipment:write` | `equipment.manage` | equipment | Record a meter reading (hours, km…) |
| `POST /assets/{id}:maintained` | `equipment:write` | `equipment.manage` | equipment.maintenance | Record that maintenance was done |
| `GET /maintenance/due` | `equipment:read` | `equipment.read` | equipment.maintenance | Assets due for maintenance by date or meter |
| `GET /work-orders` | `equipment:read` | `equipment.read` | equipment.work_orders | List work orders |
| `POST /work-orders` | `equipment:write` | `work_orders.create` | equipment.work_orders | Create a work order |
| `GET /work-orders/external/{externalId}` | `equipment:read` | `equipment.read` | equipment.work_orders | Get a work order by external ID |
| `PUT /work-orders/external/{externalId}` | `equipment:write` | `work_orders.manage` | equipment.work_orders | Create or replace a work order by external ID |
| `GET /work-orders/{id}` | `equipment:read` | `equipment.read` | equipment.work_orders | Get a work order |
| `PATCH /work-orders/{id}` | `equipment:write` | `work_orders.manage` | equipment.work_orders | Update a work order (partial) |

## Communication

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /announcements` | `communication:read` | anyone | communication.announcements | List announcements |
| `POST /announcements` | `communication:write` | `announcements.send` | communication.announcements | Create an announcement |
| `GET /announcements/{id}` | `communication:read` | anyone | communication.announcements | Get an announcement |
| `PATCH /announcements/{id}` | `communication:write` | `announcements.send` | communication.announcements | Update an announcement (partial) |
| `DELETE /announcements/{id}` | `communication:write` | `announcements.send` | communication.announcements | Archive an announcement |
| `GET /announcements/{id}/acknowledgments` | `communication:read` | `announcements.send` | communication.announcements | Who has acknowledged an announcement |
| `POST /announcements/{id}:acknowledge` | `communication:write` | `announcements.send` | communication.announcements | Record that an employee read an announcement |
| `GET /calendar-events` | `communication:read` | anyone | communication.calendar | List calendar events |
| `POST /calendar-events` | `communication:write` | `calendar.manage` | communication.calendar | Create a calendar event |
| `GET /calendar-events/external/{externalId}` | `communication:read` | anyone | communication.calendar | Get a calendar event by external ID |
| `PUT /calendar-events/external/{externalId}` | `communication:write` | `calendar.manage` | communication.calendar | Create or replace a calendar event by external ID |
| `GET /calendar-events/{id}` | `communication:read` | anyone | communication.calendar | Get a calendar event |
| `PATCH /calendar-events/{id}` | `communication:write` | `calendar.manage` | communication.calendar | Update a calendar event (partial) |
| `DELETE /calendar-events/{id}` | `communication:write` | `calendar.manage` | communication.calendar | Archive a calendar event |

## Team Displays

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /display-metrics` | `reports:read` | anyone | displays | Live display metrics |
| `POST /display-metrics` | `reports:write` | — | displays | Push live numbers to team displays |
| `GET /recognitions` | `communication:read` | anyone | displays.gamification | Recent shout-outs (newest first) |
| `POST /recognitions` | `communication:write` | `recognition.give` | displays.gamification | Post a shout-out |

## Reports & Insights

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /alert-rules` | `reports:read` | `reports.read` | insights | List alert rules |
| `POST /alert-rules` | `reports:write` | `alerts.manage` | insights | Create an alert rule |
| `GET /alert-rules/{id}` | `reports:read` | `reports.read` | insights | Get an alert rule |
| `PATCH /alert-rules/{id}` | `reports:write` | `alerts.manage` | insights | Update an alert rule (partial) |
| `DELETE /alert-rules/{id}` | `reports:write` | `alerts.manage` | insights | Archive an alert rule |
| `GET /kpis` | `reports:read` | `reports.read` | insights | Headline KPIs for a date range |
| `GET /recommendations` | `reports:read` | `recommendations.read` | insights.recommendations | Recommended actions |
| `GET /reports` | `reports:read` | `reports.read` | insights | List the available reports |
| `GET /reports/{key}` | `reports:read` | `reports.read` | insights | Run a report |

## Employee Area

| Method & path | Integration key | People | Feature | Description |
|---|---|---|---|---|
| `GET /me/activity` | — | self | employee_area | Changes made to your record: who, what and when |
| `GET /me/announcements` | — | self | employee_area | Announcements for you, newest first |
| `POST /me/announcements/{id}:acknowledge` | — | self | employee_area | Acknowledge an announcement |
| `GET /me/availability` | — | self | employee_area | Your availability |
| `POST /me/availability` | — | self | employee_area | Add an availability window |
| `DELETE /me/availability/{id}` | — | self | employee_area | Remove an availability window |
| `GET /me/documents` | — | self | employee_area | Documents shared with you |
| `GET /me/export` | — | self | employee_area | Download everything PurrOS stores about you (ZIP of JSON and CSV) |
| `GET /me/open-shifts` | — | self | employee_area | Published open shifts you can pick up |
| `GET /me/pay` | — | self | employee_area | Your pay rate history and estimated gross pay per pay period |
| `GET /me/payslips` | — | self | employee_area | Your payslips |
| `GET /me/profile` | — | self | employee_area | Your profile |
| `PATCH /me/profile` | — | self | employee_area | Update your contact details and emergency contacts |
| `GET /me/punch-corrections` | — | self | employee_area | Your punch correction requests |
| `POST /me/punch-corrections` | — | self | employee_area | Ask your manager to fix a missed or wrong punch |
| `POST /me/punch-corrections/{id}:cancel` | — | self | employee_area | Cancel a pending correction request |
| `GET /me/punches` | — | self | employee_area | Your punches (from/to are RFC 3339 times) |
| `GET /me/shift-swaps` | — | self | employee_area | Swap requests you made or were offered |
| `POST /me/shift-swaps` | — | self | employee_area | Offer one of your shifts to a co-worker |
| `GET /me/shifts` | — | self | employee_area | Your published shifts (from/to are RFC 3339 times) |
| `POST /me/shifts/{id}:claim` | — | self | employee_area | Pick up an open shift at your location |
| `GET /me/time-off/balances` | — | self | employee_area | Your time-off balances |
| `GET /me/time-off/requests` | — | self | employee_area | Your time-off requests |
| `POST /me/time-off/requests` | — | self | employee_area | Request time off |
| `POST /me/time-off/requests/{id}:cancel` | — | self | employee_area | Cancel one of your time-off requests |
| `GET /me/timesheets` | — | self | employee_area | Your timesheets |

## Webhooks

Webhook endpoints are managed in the UI (**Settings → Webhooks**) or declared in an integration's manifest. See [Webhooks](webhooks.md).
