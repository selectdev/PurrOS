# Integration recipes

Patterns for connecting common kinds of systems. PurrOS doesn't ship these integrations. Each recipe describes what to build, which scopes, endpoints and events to use, and the pitfalls to watch for. Start from the [tutorial](building-an-integration.md).

## Point-of-sale (POS)

**Goal:** sales, tenders and clock-ins flow into PurrOS within minutes.

| Direction | Data | PurrOS side |
|---|---|---|
| POS → PurrOS | Transactions (or hourly totals if that's all the POS offers) | `POST /sales/transactions:batch` or `POST /sales-summaries` |
| POS → PurrOS | Tender totals per drawer or shift, at shift change and close | `POST /cash/tenders` |
| POS → PurrOS | Clock-ins done on the POS | `POST /time/punches:batch` |
| POS → PurrOS | Product catalog | `PUT /items/external/{externalId}` |
| PurrOS → POS | New or terminated staff | Webhooks `employee.created`, `employee.terminated` |

**Scopes:** `organization:read`, `sales:write`, `cash:write`, `time:write`, `inventory:write` (for the catalog), `people:read`.

**How to collect:** use the POS's own webhooks if it has them, otherwise poll its API every 1–5 minutes, or at least import its end-of-day export.

**Pitfalls:**
- Map POS store IDs to PurrOS locations by setting each location's `externalId` to the POS store ID.
- Include refunds and voids (see [corrections](../api/data-ingestion.md#5-send-corrections-as-they-happen)).
- Use one `source` per POS system or store, and don't also send summaries for the same data.

## Online store / e-commerce

**Goal:** online sales count toward sales and stock, orders are fulfilled from PurrOS stock, and the store shows accurate availability.

| Direction | Data | PurrOS side |
|---|---|---|
| Store → PurrOS | Paid orders | `PUT /sales-orders/external/{orderId}` (if PurrOS handles fulfilment) or `POST /sales/transactions:batch` (if the store fulfils) |
| Store → PurrOS | Cancellations and refunds | Re-send the order with `status`, or a refund transaction |
| Store → PurrOS | Payouts from the payment provider | `POST /cash/settlements` |
| PurrOS → Store | Stock levels | Webhook `stock.below_reorder_point`, plus periodic `GET /stock-levels?updatedSince=…` |
| PurrOS → Store | Shipped with tracking | Webhook `sales_order.shipped` |

**Scopes:** `sales:write`, `inventory:read`, `cash:write`.

**Pitfalls:** decide which system owns the product catalog. Usually the store owns descriptions and prices, and PurrOS owns stock. Push available quantity (on hand minus reserved) rather than on hand.

## Delivery platforms and marketplaces

Treat each platform as its own source (`delivery:platform-x`). Send orders as transactions with `channel: "delivery"` and tender type `platform`, and send the platform's payouts as settlements so [tender reconciliation](../guides/cash-management.md#card-and-digital-payments) can compare them.

## Timeclocks

**Goal:** punches from hardware terminals or apps become timesheets.

- Send punches with `POST /time/punches:batch`, identifying the employee by `employeeExternalId` or a badge number stored in `integrationData`.
- Include `deviceId` and the original punch time, even if sent late.
- Listen for `employee.created`, `employee.updated` and `employee.terminated` to enroll and remove people on the device.
- Punches are de-duplicated by employee, type, time and device, so re-sending is safe.

**Scopes:** `people:read`, `time:write`.

## HR and employment software

**Goal:** one place to enter new hires, reflected everywhere.

- **HR system as the source of truth:** poll it or receive its webhooks, then upsert with `PUT /employees/external/{hrId}`. Terminations call `POST /employees/{id}:terminate`.
- **PurrOS as the source of truth:** listen for `employee.*` events and push changes to the HR system.
- Pay rates can sync with `POST /employees/{id}/pay-rates` (`payroll:write`).
- Pick **one direction per field** to avoid loops, e.g. HR owns names and pay, and PurrOS owns schedules and locations.

**Scopes:** `people:read`, `people:write`, and `payroll:write` if syncing pay.

## Payroll

**Goal:** approved hours reach payroll without re-keying, and payslips come back.

1. Listen for `pay_period.locked`.
2. Fetch hours with `GET /pay-periods/{id}/export?template=<name>` (or the JSON form) and submit them to the payroll provider.
3. After payroll runs, send payslips back with `POST /payslips` (a PDF plus totals) so employees see them in their [Employee Area](../guides/employee-area.md).

**Scopes:** `payroll:read`, `payroll:write`, `people:read`.

## Accounting

**Goal:** sales, invoices, supplier bills and cash end up in the books.

- Listen for `invoice.issued`, `invoice.paid`, `purchase_order.received` and `cash.business_day_closed`.
- Post a daily sales journal from `GET /sales-summaries`, and supplier bills from matched supplier invoices.
- Keep the accounting system's IDs in `integrationData` to avoid duplicates.

**Scopes:** `sales:read`, `purchasing:read`, `cash:read`.

## Banks

Import bank transactions (from an open-banking provider or statement files) with `POST /cash/bank-transactions` so PurrOS can [verify deposits](../guides/cash-management.md#bank-deposits). **Scope:** `cash:write`.

## Suppliers

Listen for `purchase_order.sent` and submit orders to the supplier's portal or EDI. Import their catalog and price updates with `PUT /suppliers/{id}/catalog`, and invoices with `POST /supplier-invoices`. **Scopes:** `purchasing:read`, `purchasing:write`.

## Booking and appointment systems

Send booked appointments per hour as demand drivers (`POST /demand-drivers`, `driver: "appointments"`) so the scheduler staffs to demand. **Scope:** `scheduling:write`.

## Sensors and IoT

Collect readings from temperature, humidity, door or meter sensors (often through the sensor vendor's cloud API or a local gateway) and send them to `POST /sensor-readings` every few minutes. PurrOS handles the thresholds and alerts. **Scope:** `operations:write`.

## Notifications: SMS, chat

Subscribe to `notification.requested` (needs the `notifications:deliver` scope). Each event contains the recipient's preferred channel and address and the message, which your integration sends via an SMS gateway or chat platform. You can also forward selected events (e.g. `form.answer_failed`, `cash.deposit_mismatch`, `alert.triggered`) to a team chat channel.

## Data warehouse / BI

Subscribe to the events you care about and write them to your warehouse, or use `GET …?updatedSince=` endpoints for periodic incremental loads. **Scopes:** the `:read` scopes you need.
