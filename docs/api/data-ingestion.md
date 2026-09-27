# Data ingestion

Most API traffic is data flowing **into** PurrOS from the systems a business already uses: point-of-sale systems, online stores, delivery platforms, timeclocks, booking systems, sensors and banks. This page explains how to send it reliably.

## What you can send

| Data | Endpoint | Scope | Used by |
|---|---|---|---|
| Sales transactions (lines, discounts, tax, tenders, refunds, voids) | `POST /sales/transactions:batch` | `sales:write` | Inventory usage, item reports, all sales KPIs |
| Sales totals per day or hour | `POST /sales-summaries` | `sales:write` | Dashboards, forecasting, labor % |
| Tender totals per drawer or shift | `POST /cash/tenders` | `cash:write` | Expected cash, card reconciliation |
| Processor and platform payouts | `POST /cash/settlements` | `cash:write` | Tender reconciliation |
| Bank transactions | `POST /cash/bank-transactions` | `cash:write` | Deposit verification |
| Online orders to fulfil | `PUT /sales-orders/external/{externalId}` | `sales:write` | Stock reservation, fulfilment |
| Product catalog | `PUT /items/external/{externalId}` | `inventory:write` | Item matching |
| Punches | `POST /time/punches:batch` | `time:write` | Timesheets |
| Demand drivers (appointments, foot traffic, orders…) | `POST /demand-drivers` | `scheduling:write` | Forecasting |
| Sensor readings | `POST /sensor-readings` | `operations:write` | Logs and alerts |
| Employees from HR | `PUT /employees/external/{externalId}` | `people:write` | Everything people-related |

Send the most detailed data your source system can provide. If you send transactions, PurrOS builds the summaries itself, so don't send both for the same location and source.

## The rules

### 1. Label the source and use external IDs

Every batch has a `source`, and every record has an `externalId` from the source system:

```json
{ "source": "pos:front-counter", "transactions": [ { "externalId": "txn-88213", … } ] }
```

Records are unique on **(source, externalId)**. Sending a record again **updates** it instead of creating a duplicate, so you can safely:

- retry after a timeout
- re-send a whole day to be sure nothing was missed
- send a corrected version later

Use a stable source label per system and location, e.g. `pos:store-101`, `web-store`, `delivery:platform-x`.

### 2. Map locations and items

- **Locations:** use `locationId`, or `locationExternalId` if you've set an `externalId` on the location (e.g. the POS store number).
- **Items:** reference them by `itemSku`, `itemBarcode` or `itemExternalId`. Lines whose item can't be matched are **still accepted**, stored, and listed in the *Unmapped items* queue for someone to link. After linking, past sales are recalculated.

### 3. Batch it

Batches accept up to 1,000 records (configurable) and count as one request against rate limits. For live feeds, send a batch every 1–5 minutes, or when 100 records are waiting, whichever comes first.

### 4. Read the per-record results

Batch endpoints return `202 Accepted` with a result for every record:

```json
{
  "batchId": "bat_01J8Z…",
  "results": [
    { "externalId": "txn-88213", "status": "created", "id": "stx_01J8Z…" },
    { "externalId": "txn-88214", "status": "updated", "id": "stx_01J8Z…" },
    { "externalId": "txn-88215", "status": "rejected",
      "errors": [{ "path": "tenders", "message": "Tender total does not match transaction total" }] }
  ]
}
```

`202` means the records are **safely stored**. Stock, cash expectations and reports update a few seconds later in the background, so a busy till never waits. Rejected records aren't stored, so fix and re-send them.

### 5. Send corrections as they happen

Refunds, voids and end-of-day corrections are normal:

- A **refund** is a new transaction with negative quantities and `"type": "refund"`, and `refundOf` pointing to the original's `externalId` if known.
- A **void** of an earlier transaction: re-send it with `"status": "voided"`.
- **Backdated** data is accepted. It recalculates the affected days. If a business day is already closed in Cash Management, or a pay period is locked, the change is **flagged for review** rather than silently changing locked figures.

### 6. Use the time zone correctly

Send `occurredAt` in UTC. PurrOS assigns the **business day** using the location's time zone and business day cut-off, so a sale at 01:30 in a bar that closes at 04:00 counts toward the previous day.

## Example: sales transactions

```http
POST /api/v1/sales/transactions:batch
Authorization: Bearer pk_live_…
Content-Type: application/json
```

```json
{
  "source": "pos:store-101",
  "transactions": [
    {
      "externalId": "txn-88213",
      "locationExternalId": "101",
      "occurredAt": "2026-09-27T12:41:07Z",
      "type": "sale",
      "channel": "in_store",
      "registerId": "drawer-2",
      "employeeExternalId": "hris-1042",
      "lines": [
        { "itemSku": "LATTE-12", "quantity": "2", "unitPrice": "4.50", "discount": "0.00", "tax": "0.72" },
        { "itemExternalId": "pos-item-775", "name": "Blueberry muffin", "quantity": "1", "unitPrice": "3.25" }
      ],
      "tenders": [
        { "type": "card", "amount": "9.00" },
        { "type": "cash", "amount": "4.00", "change": "0.03" }
      ],
      "subtotal": "12.25",
      "tax": "0.72",
      "total": "12.97",
      "currency": "USD"
    }
  ]
}
```

Tender types: `cash`, `card`, `digital_wallet`, `gift_card`, `voucher`, `account`, `platform` (with `platform` name), and `other`.

## Example: hourly sales summaries

For systems that only provide totals:

```json
{
  "source": "pos:store-101",
  "summaries": [
    {
      "externalId": "2026-09-27T12",
      "locationExternalId": "101",
      "periodStart": "2026-09-27T12:00:00Z",
      "periodEnd": "2026-09-27T13:00:00Z",
      "netSales": "1840.25",
      "grossSales": "1912.00",
      "discounts": "71.75",
      "tax": "147.22",
      "transactions": 212,
      "guests": 240,
      "byChannel": [
        { "channel": "in_store", "netSales": "1502.10", "transactions": 180 },
        { "channel": "delivery", "netSales": "338.15", "transactions": 32 }
      ]
    }
  ]
}
```

## Example: online order

```http
PUT /api/v1/sales-orders/external/web-100482
```

```json
{
  "source": "web-store",
  "customer": { "externalId": "cust-5521", "name": "Alex Kim", "email": "alex@example.com" },
  "fulfilmentLocationExternalId": "wh-main",
  "lines": [ { "itemExternalId": "web-sku-991", "quantity": "3", "unitPrice": "19.99" } ],
  "shipping": { "method": "standard", "amount": "4.99", "address": { "line1": "12 High St", "city": "Springfield", "postalCode": "12345", "country": "US" } },
  "paymentStatus": "paid",
  "total": "64.96",
  "currency": "USD"
}
```

Sending the same order again with changes (e.g. `"status": "cancelled"`) updates it.

## Checking what arrived

- `GET /sales/transactions?source=pos:store-101&updatedSince=…` lists what PurrOS has stored.
- **Settings → Integrations → *your integration*** shows batches received, rejected records, unmapped items and the integration's health messages.
- `POST /integrations/self/logs` lets your integration add its own notes, e.g. "Backfilled 2026-09-01 to 2026-09-26: 18,204 transactions".

## Backfilling history

Forecasting works best with at least 8 weeks of history, and a year is better. To load history:

1. Send older data in date order, one location at a time.
2. Use the same `source` and `externalId`s you'll use for live data, so there's no overlap.
3. Stay within rate limits. A backfill of a year of transactions for one store usually takes minutes.

See [Integration recipes](../integrations/recipes.md) for complete POS and online store examples.
