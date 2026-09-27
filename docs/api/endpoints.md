# Endpoint index

All paths are relative to `/api/v1`. This index is generated from the API's route table. The full request and response schemas are in the OpenAPI spec at `/api/v1/openapi.json` (it lists only enabled features), or the interactive docs at `/docs/api`.

Endpoints of switched-off features return `404 feature_disabled`. The **Feature** column is the feature switch each endpoint belongs to.

Conventions used throughout:

- Lists are cursor-paginated (`limit`, `cursor` → `nextCursor`) and most accept `updatedSince`.
- Resources with an `externalId` can be read and upserted at `…/external/{externalId}`.
- `DELETE` archives (soft delete); history is kept.
- `PATCH` is a JSON merge patch and accepts `If-Match` with the record's `version`.
- Batch endpoints (`202`) are idempotent on `source` + `externalId` and return per-record results. See [Batch ingestion](data-ingestion.md).
- Actions on a record use a colon: `POST /employees/{id}:terminate`.

## Platform

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /features` | any key | — | Features and whether they are enabled |
| `GET /me` | any key | — | The calling API key |
| `GET /openapi.json` | none | — | OpenAPI document for enabled features |
| `GET /permissions` | any key | — | Permission catalog for enabled features |

## Integration self-service

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /integrations/self` | integration key | — | The calling integration's registration |
| `GET /integrations/self/config` | integration key | — | Config values entered by the admin |
| `POST /integrations/self/health` | integration key | — | Send a heartbeat and status message |
| `POST /integrations/self/logs` | integration key | — | Add a log message shown in the admin UI |

## Organization

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /departments` | `organization:read` | — | List departments |
| `GET /locations` | `organization:read` | — | List locations |
| `GET /locations/external/{externalId}` | `organization:read` | — | Get a location by external ID |
| `GET /locations/{id}` | `organization:read` | — | Get a location |
| `GET /org-units` | `organization:read` | — | List org units (regions, districts…) |
| `GET /roles` | `organization:read` | — | List roles and their permissions |
| `GET /users` | `organization:read` | — | List user accounts with their role and assignments |

## People & HR

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /employees` | `people:read` | people | List employees |
| `POST /employees` | `people:write` | people | Create an employee |
| `GET /employees/external/{externalId}` | `people:read` | people | Get an employee by external ID |
| `PUT /employees/external/{externalId}` | `people:write` | people | Create or replace an employee by external ID |
| `GET /employees/{employeeId}/documents` | `people:read` | people.documents | List employee documents |
| `POST /employees/{employeeId}/documents` | `people:write` | people.documents | Create an employee document |
| `GET /employees/{employeeId}/documents/{id}` | `people:read` | people.documents | Get an employee document |
| `PATCH /employees/{employeeId}/documents/{id}` | `people:write` | people.documents | Update an employee document (partial) |
| `DELETE /employees/{employeeId}/documents/{id}` | `people:write` | people.documents | Archive an employee document |
| `GET /employees/{id}` | `people:read` | people | Get an employee |
| `PATCH /employees/{id}` | `people:write` | people | Update an employee (partial) |
| `DELETE /employees/{id}` | `people:write` | people | Archive an employee |
| `GET /employees/{id}/pay-rates` | `payroll:read` | people | Pay rate history (newest first) |
| `POST /employees/{id}/pay-rates` | `payroll:write` | people | Add a pay rate |
| `GET /employees/{id}/skills` | `people:read` | people.skills | An employee's skills and certifications |
| `PUT /employees/{id}/skills/{skillId}` | `people:write` | people.skills | Give an employee a skill or certification |
| `DELETE /employees/{id}/skills/{skillId}` | `people:write` | people.skills | Remove a skill from an employee |
| `POST /employees/{id}:rehire` | `people:write` | people | Rehire a terminated employee |
| `POST /employees/{id}:terminate` | `people:write` | people | Terminate an employee |
| `POST /employees/{id}:transfer` | `people:write` | people | Transfer an employee (location, department, position or manager) |
| `GET /payslips` | `payroll:read` | employee_area.payslips | List payslips |
| `POST /payslips` | `payroll:write` | employee_area.payslips | Send payslips from your payroll provider |
| `GET /skills` | `people:read` | people.skills | List skills |
| `POST /skills` | `people:write` | people.skills | Create a skill |
| `GET /skills/external/{externalId}` | `people:read` | people.skills | Get a skill by external ID |
| `PUT /skills/external/{externalId}` | `people:write` | people.skills | Create or replace a skill by external ID |
| `GET /skills/{id}` | `people:read` | people.skills | Get a skill |
| `PATCH /skills/{id}` | `people:write` | people.skills | Update a skill (partial) |
| `DELETE /skills/{id}` | `people:write` | people.skills | Archive a skill |

## Time & Attendance

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /labor-rule-sets` | `time:read` | time | List labor rule sets |
| `POST /labor-rule-sets` | `time:write` | time | Create a labor rule set |
| `GET /labor-rule-sets/{id}` | `time:read` | time | Get a labor rule set |
| `PATCH /labor-rule-sets/{id}` | `time:write` | time | Update a labor rule set (partial) |
| `GET /pay-periods` | `payroll:read` | time | List pay periods (newest first) |
| `POST /pay-periods` | `payroll:write` | time | Create a pay period |
| `GET /pay-periods/{id}` | `payroll:read` | time | Get a pay period |
| `GET /pay-periods/{id}/export` | `payroll:read` | time.payroll_export | Export hours for payroll |
| `POST /pay-periods/{id}:lock` | `payroll:write` | time | Lock a pay period (final payroll approval) |
| `POST /time-off/adjustments` | `time:write` | time.time_off | Add or deduct time-off hours (accruals, corrections) |
| `GET /time-off/balances` | `time:read` | time.time_off | Time-off balances per employee and type |
| `GET /time-off/requests` | `time:read` | time.time_off | List time-off requests |
| `POST /time-off/requests` | `time:write` | time.time_off | Request time off |
| `POST /time-off/requests/{id}:approve` | `time:write` | time.time_off | Approve a time-off request (deducts the balance) |
| `POST /time-off/requests/{id}:cancel` | `time:write` | time.time_off | Cancel a time-off request (restores the balance if it was approved) |
| `POST /time-off/requests/{id}:reject` | `time:write` | time.time_off | Reject a time-off request |
| `GET /time-off/types` | `time:read` | time.time_off | List time off types |
| `POST /time-off/types` | `time:write` | time.time_off | Create a time off type |
| `GET /time-off/types/external/{externalId}` | `time:read` | time.time_off | Get a time off type by external ID |
| `PUT /time-off/types/external/{externalId}` | `time:write` | time.time_off | Create or replace a time off type by external ID |
| `GET /time-off/types/{id}` | `time:read` | time.time_off | Get a time off type |
| `PATCH /time-off/types/{id}` | `time:write` | time.time_off | Update a time off type (partial) |
| `DELETE /time-off/types/{id}` | `time:write` | time.time_off | Archive a time off type |
| `GET /time/punches` | `time:read` | time | List punches |
| `POST /time/punches:batch` | `time:write` | time | Send punches in bulk |
| `GET /timesheets` | `time:read` | time | List timesheets |
| `GET /timesheets/{id}` | `time:read` | time | Get a timesheet with daily detail and exceptions |
| `POST /timesheets/{id}:approve` | `time:write` | time | Approve a timesheet (manager step) |
| `POST /timesheets/{id}:reject` | `time:write` | time | Reject a timesheet |
| `POST /timesheets:build` | `time:write` | time | Build or refresh timesheets from punches |

## Scheduling & Forecasting

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /availability` | `scheduling:read` | scheduling | List availabilitys |
| `POST /availability` | `scheduling:write` | scheduling | Create an availability |
| `GET /availability/{id}` | `scheduling:read` | scheduling | Get an availability |
| `PATCH /availability/{id}` | `scheduling:write` | scheduling | Update an availability (partial) |
| `DELETE /availability/{id}` | `scheduling:write` | scheduling | Archive an availability |
| `POST /demand-drivers` | `scheduling:write` | scheduling | Send demand drivers (appointments, foot traffic, orders…) |
| `GET /forecasts` | `scheduling:read` | scheduling.forecasting | Forecast a demand driver |
| `POST /forecasts/adjustments` | `scheduling:write` | scheduling.forecasting | Adjust a day's forecast by a percentage |
| `GET /schedules` | `scheduling:read` | scheduling | A location's schedule with hours and estimated cost |
| `POST /schedules:publish` | `scheduling:write` | scheduling | Publish draft shifts for a location and period |
| `GET /shift-swaps` | `scheduling:read` | scheduling.shift_swaps | List shift swap requests |
| `POST /shift-swaps` | `scheduling:write` | scheduling.shift_swaps | Request a shift swap |
| `POST /shift-swaps/{id}:approve` | `scheduling:write` | scheduling.shift_swaps | Approve a swap (reassigns the shift) |
| `POST /shift-swaps/{id}:reject` | `scheduling:write` | scheduling.shift_swaps | Reject a swap |
| `GET /shifts` | `scheduling:read` | scheduling | List shifts |
| `POST /shifts` | `scheduling:write` | scheduling | Create a shift |
| `GET /shifts/external/{externalId}` | `scheduling:read` | scheduling | Get a shift by external ID |
| `PUT /shifts/external/{externalId}` | `scheduling:write` | scheduling | Create or replace a shift by external ID |
| `GET /shifts/{id}` | `scheduling:read` | scheduling | Get a shift |
| `PATCH /shifts/{id}` | `scheduling:write` | scheduling | Update a shift (partial) |
| `POST /shifts/{id}:claim` | `scheduling:write` | scheduling.open_shifts | Assign an open shift to an employee |
| `GET /staffing-needs` | `scheduling:read` | scheduling | Staff needed vs scheduled, hour by hour |
| `GET /staffing-rules` | `scheduling:read` | scheduling | List staffing rules |
| `POST /staffing-rules` | `scheduling:write` | scheduling | Create a staffing rule |
| `GET /staffing-rules/{id}` | `scheduling:read` | scheduling | Get a staffing rule |
| `PATCH /staffing-rules/{id}` | `scheduling:write` | scheduling | Update a staffing rule (partial) |
| `DELETE /staffing-rules/{id}` | `scheduling:write` | scheduling | Archive a staffing rule |

## Cash Management

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `POST /cash/bank-transactions` | `cash:write` | cash | Send bank transactions for deposit verification |
| `GET /cash/business-days` | `cash:read` | cash | Daily cash summary for a location |
| `POST /cash/business-days/{locationId}/{date}:close` | `cash:write` | cash | Close a business day |
| `GET /cash/counts` | `cash:read` | cash | List counts |
| `POST /cash/counts` | `cash:write` | cash | Record a drawer, skim or safe count |
| `GET /cash/deposits` | `cash:read` | cash | List deposits |
| `POST /cash/deposits` | `cash:write` | cash | Record a bank deposit |
| `POST /cash/settlements` | `cash:write` | cash | Send card processor and platform payouts |
| `POST /cash/tenders` | `cash:write` | cash | Send tender totals per drawer or shift from the POS |

## Inventory

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `POST /inventory/adjustments` | `inventory:write` | inventory | Adjust stock |
| `POST /inventory/waste` | `inventory:write` | inventory.waste | Record waste or shrink |
| `GET /items` | `inventory:read` | inventory | List items |
| `POST /items` | `inventory:write` | inventory | Create an item |
| `GET /items/external/{externalId}` | `inventory:read` | inventory | Get an item by external ID |
| `PUT /items/external/{externalId}` | `inventory:write` | inventory | Create or replace an item by external ID |
| `GET /items/{id}` | `inventory:read` | inventory | Get an item |
| `PATCH /items/{id}` | `inventory:write` | inventory | Update an item (partial) |
| `GET /stock-counts` | `inventory:read` | inventory | List stock counts |
| `POST /stock-counts` | `inventory:write` | inventory | Enter a stock count |
| `GET /stock-counts/{id}` | `inventory:read` | inventory | Get a stock count with variances |
| `POST /stock-counts/{id}:cancel` | `inventory:write` | inventory | Cancel an open count |
| `POST /stock-counts/{id}:post` | `inventory:write` | inventory | Post a count to the stock ledger |
| `GET /stock-levels` | `inventory:read` | inventory | Stock on hand, reserved and available |
| `POST /stock-levels:configure` | `inventory:write` | inventory | Set par level and reorder point for an item at a location |
| `GET /stock-movements` | `inventory:read` | inventory | The stock ledger |
| `GET /transfers` | `inventory:read` | inventory.transfers | List transfers |
| `POST /transfers` | `inventory:write` | inventory.transfers | Send stock to another location |
| `GET /transfers/{id}` | `inventory:read` | inventory.transfers | Get a transfer |
| `POST /transfers/{id}:receive` | `inventory:write` | inventory.transfers | Confirm what arrived |
| `GET /usage-recipes/{itemId}` | `inventory:read` | inventory.usage_recipes | What one unit of a sold item uses |
| `PUT /usage-recipes/{itemId}` | `inventory:write` | inventory.usage_recipes | Replace a usage recipe |

## Purchasing & Ordering

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /purchase-orders` | `purchasing:read` | purchasing | List purchase orders |
| `POST /purchase-orders` | `purchasing:write` | purchasing | Create a purchase order |
| `GET /purchase-orders/{id}` | `purchasing:read` | purchasing | Get a purchase order |
| `POST /purchase-orders/{id}:approve` | `purchasing:write` | purchasing | Approve a purchase order |
| `POST /purchase-orders/{id}:cancel` | `purchasing:write` | purchasing | Cancel a purchase order |
| `POST /purchase-orders/{id}:receive` | `purchasing:write` | purchasing | Receive a delivery |
| `POST /purchase-orders/{id}:send` | `purchasing:write` | purchasing | Mark a purchase order as sent to the supplier |
| `GET /suggested-orders` | `purchasing:read` | purchasing.suggested_orders | Suggested order for a supplier and location |
| `GET /supplier-invoices` | `purchasing:read` | purchasing | List supplier invoices |
| `POST /supplier-invoices` | `purchasing:write` | purchasing | Record a supplier invoice and match it |
| `POST /supplier-invoices/{id}:approve` | `purchasing:write` | purchasing.invoice_matching | Approve a supplier invoice (accept any differences) |
| `POST /supplier-invoices/{id}:dispute` | `purchasing:write` | purchasing.invoice_matching | Dispute a supplier invoice |
| `GET /suppliers` | `purchasing:read` | purchasing | List suppliers |
| `POST /suppliers` | `purchasing:write` | purchasing | Create a supplier |
| `GET /suppliers/external/{externalId}` | `purchasing:read` | purchasing | Get a supplier by external ID |
| `PUT /suppliers/external/{externalId}` | `purchasing:write` | purchasing | Create or replace a supplier by external ID |
| `GET /suppliers/{id}` | `purchasing:read` | purchasing | Get a supplier |
| `PATCH /suppliers/{id}` | `purchasing:write` | purchasing | Update a supplier (partial) |
| `DELETE /suppliers/{id}` | `purchasing:write` | purchasing | Archive a supplier |
| `GET /suppliers/{id}/catalog` | `purchasing:read` | purchasing | A supplier's catalog (order guide) |
| `PUT /suppliers/{id}/catalog` | `purchasing:write` | purchasing | Replace a supplier's catalog |

## Sales

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /customers` | `sales:read` | sales.orders | List customers |
| `POST /customers` | `sales:write` | sales.orders | Create a customer |
| `GET /customers/external/{externalId}` | `sales:read` | sales.orders | Get a customer by external ID |
| `PUT /customers/external/{externalId}` | `sales:write` | sales.orders | Create or replace a customer by external ID |
| `GET /customers/{id}` | `sales:read` | sales.orders | Get a customer |
| `PATCH /customers/{id}` | `sales:write` | sales.orders | Update a customer (partial) |
| `DELETE /customers/{id}` | `sales:write` | sales.orders | Archive a customer |
| `GET /invoices` | `sales:read` | sales.invoicing | List invoices |
| `GET /invoices/{id}` | `sales:read` | sales.invoicing | Get an invoice |
| `GET /invoices/{id}/pdf` | `sales:read` | sales.invoicing | Download an invoice as PDF |
| `POST /invoices/{id}:pay` | `sales:write` | sales.invoicing | Record payment of an invoice |
| `POST /invoices/{id}:void` | `sales:write` | sales.invoicing | Void an invoice |
| `GET /sales-orders` | `sales:read` | sales.orders | List sales orders |
| `POST /sales-orders` | `sales:write` | sales.orders | Create a sales order (reserves stock) |
| `PUT /sales-orders/external/{externalId}` | `sales:write` | sales.orders | Create or update an order from another system (e.g. an online store) |
| `GET /sales-orders/{id}` | `sales:read` | sales.orders | Get a sales order |
| `POST /sales-orders/{id}:cancel` | `sales:write` | sales.orders | Cancel an order (releases reserved stock) |
| `POST /sales-orders/{id}:invoice` | `sales:write` | sales.invoicing | Issue an invoice for an order |
| `POST /sales-orders/{id}:ship` | `sales:write` | sales.orders | Ship an order (deducts stock) |
| `GET /sales-summaries` | `sales:read` | sales.feeds | List sales summaries |
| `POST /sales-summaries` | `sales:write` | sales.feeds | Send hourly or daily sales totals |
| `GET /sales/transactions` | `sales:read` | sales.feeds | List sales transactions |
| `POST /sales/transactions:batch` | `sales:write` | sales.feeds | Send sales transactions from a POS or online store |
| `GET /sales/unmapped-items` | `sales:read` | sales.feeds | Items in sales feeds that aren't linked to a PurrOS item |
| `POST /sales/unmapped-items:map` | `sales:write` | sales.feeds | Link an unmapped item reference to a PurrOS item |

## Forms, Checklists & Compliance

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /audits` | `operations:read` | operations.audits | List scored audits |
| `GET /corrective-actions` | `operations:read` | operations.corrective_actions | List corrective actions |
| `POST /corrective-actions` | `operations:write` | operations.corrective_actions | Create a corrective action |
| `GET /corrective-actions/{id}` | `operations:read` | operations.corrective_actions | Get a corrective action |
| `PATCH /corrective-actions/{id}` | `operations:write` | operations.corrective_actions | Update a corrective action (partial) |
| `POST /corrective-actions/{id}:close` | `operations:write` | operations.corrective_actions | Close a corrective action with its resolution |
| `GET /form-submissions` | `operations:read` | operations | List submissions |
| `POST /form-submissions` | `operations:write` | operations | Submit a completed form or checklist |
| `GET /form-submissions/{id}` | `operations:read` | operations | Get a submission with per-question results |
| `GET /forms` | `operations:read` | operations | List forms |
| `POST /forms` | `operations:write` | operations | Create a form |
| `GET /forms/external/{externalId}` | `operations:read` | operations | Get a form by external ID |
| `PUT /forms/external/{externalId}` | `operations:write` | operations | Create or replace a form by external ID |
| `GET /forms/{id}` | `operations:read` | operations | Get a form |
| `PATCH /forms/{id}` | `operations:write` | operations | Update a form (partial) |
| `DELETE /forms/{id}` | `operations:write` | operations | Archive a form |
| `POST /sensor-readings` | `operations:write` | operations.sensors | Send sensor readings |
| `GET /sensors` | `operations:read` | operations.sensors | List sensors |
| `POST /sensors` | `operations:write` | operations.sensors | Create a sensor |
| `GET /sensors/external/{externalId}` | `operations:read` | operations.sensors | Get a sensor by external ID |
| `PUT /sensors/external/{externalId}` | `operations:write` | operations.sensors | Create or replace a sensor by external ID |
| `GET /sensors/{id}` | `operations:read` | operations.sensors | Get a sensor |
| `PATCH /sensors/{id}` | `operations:write` | operations.sensors | Update a sensor (partial) |
| `DELETE /sensors/{id}` | `operations:write` | operations.sensors | Archive a sensor |
| `GET /sensors/{id}/readings` | `operations:read` | operations.sensors | Recent readings for a sensor (newest first, up to 1,000) |

## Equipment & Assets

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /assets` | `equipment:read` | equipment | List assets |
| `POST /assets` | `equipment:write` | equipment | Create an asset |
| `GET /assets/external/{externalId}` | `equipment:read` | equipment | Get an asset by external ID |
| `PUT /assets/external/{externalId}` | `equipment:write` | equipment | Create or replace an asset by external ID |
| `GET /assets/{id}` | `equipment:read` | equipment | Get an asset |
| `PATCH /assets/{id}` | `equipment:write` | equipment | Update an asset (partial) |
| `DELETE /assets/{id}` | `equipment:write` | equipment | Archive an asset |
| `POST /assets/{id}/meter-readings` | `equipment:write` | equipment | Record a meter reading (hours, km…) |
| `POST /assets/{id}:maintained` | `equipment:write` | equipment.maintenance | Record that maintenance was done |
| `GET /maintenance/due` | `equipment:read` | equipment.maintenance | Assets due for maintenance by date or meter |
| `GET /work-orders` | `equipment:read` | equipment.work_orders | List work orders |
| `POST /work-orders` | `equipment:write` | equipment.work_orders | Create a work order |
| `GET /work-orders/external/{externalId}` | `equipment:read` | equipment.work_orders | Get a work order by external ID |
| `PUT /work-orders/external/{externalId}` | `equipment:write` | equipment.work_orders | Create or replace a work order by external ID |
| `GET /work-orders/{id}` | `equipment:read` | equipment.work_orders | Get a work order |
| `PATCH /work-orders/{id}` | `equipment:write` | equipment.work_orders | Update a work order (partial) |

## Communication

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /announcements` | `communication:read` | communication.announcements | List announcements |
| `POST /announcements` | `communication:write` | communication.announcements | Create an announcement |
| `GET /announcements/{id}` | `communication:read` | communication.announcements | Get an announcement |
| `PATCH /announcements/{id}` | `communication:write` | communication.announcements | Update an announcement (partial) |
| `DELETE /announcements/{id}` | `communication:write` | communication.announcements | Archive an announcement |
| `GET /announcements/{id}/acknowledgments` | `communication:read` | communication.announcements | Who has acknowledged an announcement |
| `POST /announcements/{id}:acknowledge` | `communication:write` | communication.announcements | Record that an employee read an announcement |
| `GET /calendar-events` | `communication:read` | communication.calendar | List calendar events |
| `POST /calendar-events` | `communication:write` | communication.calendar | Create a calendar event |
| `GET /calendar-events/external/{externalId}` | `communication:read` | communication.calendar | Get a calendar event by external ID |
| `PUT /calendar-events/external/{externalId}` | `communication:write` | communication.calendar | Create or replace a calendar event by external ID |
| `GET /calendar-events/{id}` | `communication:read` | communication.calendar | Get a calendar event |
| `PATCH /calendar-events/{id}` | `communication:write` | communication.calendar | Update a calendar event (partial) |
| `DELETE /calendar-events/{id}` | `communication:write` | communication.calendar | Archive a calendar event |

## Team Displays

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /display-metrics` | `reports:read` | displays | Live display metrics |
| `POST /display-metrics` | `reports:write` | displays | Push live numbers to team displays |
| `GET /recognitions` | `communication:read` | displays.gamification | Recent shout-outs (newest first) |
| `POST /recognitions` | `communication:write` | displays.gamification | Post a shout-out |

## Reports & Insights

| Method & path | Scope | Feature | Description |
|---|---|---|---|
| `GET /alert-rules` | `reports:read` | insights | List alert rules |
| `POST /alert-rules` | `reports:write` | insights | Create an alert rule |
| `GET /alert-rules/{id}` | `reports:read` | insights | Get an alert rule |
| `PATCH /alert-rules/{id}` | `reports:write` | insights | Update an alert rule (partial) |
| `DELETE /alert-rules/{id}` | `reports:write` | insights | Archive an alert rule |
| `GET /kpis` | `reports:read` | insights | Headline KPIs for a date range |
| `GET /recommendations` | `reports:read` | insights.recommendations | Recommended actions |
| `GET /reports` | `reports:read` | insights | List the available reports |
| `GET /reports/{key}` | `reports:read` | insights | Run a report |

## Webhooks

Webhook endpoints are managed in the UI (**Settings → Webhooks**) or declared in an integration's manifest. See [Webhooks](webhooks.md).
