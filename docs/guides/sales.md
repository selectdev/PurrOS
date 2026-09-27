# Sales

Feature key: `sales`.

Sales has two parts. You can use either one, or both.

1. **Sales feeds** (`sales.feeds`): sales from your POS systems, online stores and delivery platforms come in through the API. They drive forecasting, cash, inventory usage and reports.
2. **Sales orders** (`sales.orders`, `sales.invoicing`): quotes, orders, fulfilment and invoices that you manage in PurrOS itself, typically for B2B, wholesale or orders taken by phone.

PurrOS isn't a point-of-sale system. It receives sales from yours.

## Sales feeds

An [integration](../integrations/recipes.md#point-of-sale-pos) sends data at the level of detail your POS can provide:

| Level | Endpoint | Enables |
|---|---|---|
| Daily or hourly totals | `POST /api/v1/sales-summaries` | Dashboards, forecasting, labor % of sales |
| Tender totals per drawer or shift | `POST /api/v1/cash/tenders` | [Cash management](cash-management.md) |
| Individual transactions with items | `POST /api/v1/sales/transactions:batch` | Everything above, plus [inventory usage](inventory.md#usage-recipes) and item-level reports |

Each record has a **source** (e.g. `pos:front-counter`, `web-store`, `delivery:platform-x`) and an `externalId`, so data can be re-sent safely and traced back to where it came from. See [Data ingestion](../api/data-ingestion.md).

### Mapping POS items

Items in sales feeds are matched to PurrOS items by `externalId`, SKU or barcode. Anything that can't be matched goes to the **Unmapped items** queue (`sales.unmapped.resolve`), where you link it once and PurrOS remembers it. Past sales are then recalculated.

### Business days

Sales are grouped into each location's business day (see [business day cut-off](../admin/organization-and-locations.md#locations)). Late data such as refunds recalculates the affected days. If the day is already closed in Cash Management, the change is flagged for review rather than silently changing closed figures.

## Sales orders

- **Customers** and **price lists** (`customers.manage`), with customer-specific prices and payment terms.
- **Quotes → sales orders** (`sales_orders.manage`). Confirming an order **reserves stock**.
- **Online orders** can be created by an integration (`PUT /api/v1/sales-orders/external/{externalId}`) so PurrOS handles picking and stock.
- **Fulfilment**: pick lists, packing, shipping with tracking numbers. Shipping deducts stock.
- **Invoicing** (`sales.invoicing`, `invoices.issue`): PDF invoices, payment status, credit notes, and export to accounting.

## Reports

Sales by location, hour, channel (source), item and category, average transaction value, comparisons with last week, last year and forecast, and top and bottom items.

## API & events

| Endpoint | Scope |
|---|---|
| `POST /api/v1/sales/transactions:batch` | `sales:write` |
| `POST /api/v1/sales-summaries` | `sales:write` |
| `GET /api/v1/sales/transactions`, `GET /api/v1/sales-summaries` | `sales:read` |
| `GET/POST /api/v1/customers` | `sales:read` / `sales:write` |
| `GET/POST /api/v1/sales-orders`, `PUT /api/v1/sales-orders/external/{externalId}` | `sales:read` / `sales:write` |
| `POST /api/v1/sales-orders/{id}:ship`, `:cancel` | `sales:write` |
| `GET /api/v1/invoices` | `sales:read` |

Events: `sales.unmapped_item`, `sales_order.created`, `sales_order.shipped`, `sales_order.cancelled`, `invoice.issued`, `invoice.paid`.
