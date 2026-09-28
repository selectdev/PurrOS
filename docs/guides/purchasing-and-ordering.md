# Purchasing & ordering

Feature key: `purchasing`. Needs Inventory.

> **Status:** The API covers suppliers and catalogs, suggested orders, purchase orders (approval, send, receive, cancel) and supplier invoices with matching. Emailing orders to suppliers and the screens are planned. Screens described below arrive with the web app; until then, use the endpoints listed at the end of this page.

Order the right amount at the right time, receive it accurately, and pay only for what arrived at the agreed price.

## Suppliers

Each supplier (`suppliers.manage`) has contact details, the locations it delivers to, and:

- a **catalog / order guide**: the items you buy from them, with pack size, price, minimum order quantity and supplier item code
- an **order schedule**: order days, cut-off times and delivery days per location
- how orders are sent: email (PDF and CSV) or an integration with the supplier's ordering system

## Suggested orders

`purchasing.suggested_orders` calculates what to order for each location and supplier:

```
Suggested = Par level
          + Expected usage until the next delivery after this one (from the forecast)
          − Stock on hand
          − Already on order
          → rounded up to pack size and minimum order
```

On order day, managers see the suggestion, adjust quantities, and send it (`orders.create`).

## Purchase orders

- Create from a suggestion, a template, or from scratch.
- **Approval thresholds:** orders above a location's limit need approval (`purchase_orders.approve`), e.g. from a district manager.
- Statuses: draft → awaiting approval → sent → partially received → received → closed.

## Receiving

When a delivery arrives (`goods_receipts.create`):

1. Open the PO on a phone or tablet, scan items or tick them off.
2. Record **short, damaged or substituted** items, and batch/expiry numbers if tracked.
3. Confirm. Stock is updated immediately, and differences are noted on the PO.

## Invoice matching

With `purchasing.invoice_matching`, enter or import the supplier's invoice. PurrOS compares **PO ↔ goods received ↔ invoice**:

- quantity differences (billed for items not received)
- **price changes** compared with the catalog price
- totals and tax

Mismatches are flagged for someone with `invoices.match` to approve or dispute. Matched invoices can be exported to your accounting software.

## Reports

Spend by supplier, category and location, price changes over time, supplier fill rate and on-time delivery, and order accuracy.

## API & events

| Endpoint | Scope |
|---|---|
| `GET/POST /api/v1/suppliers`, `GET/PUT /api/v1/suppliers/{id}/catalog` | `purchasing:read` / `purchasing:write` |
| `GET /api/v1/suggested-orders?locationId=…&supplierId=…` | `purchasing:read` |
| `GET/POST /api/v1/purchase-orders`, `POST /api/v1/purchase-orders/{id}:approve` | `purchasing:read` / `purchasing:write` |
| `POST /api/v1/purchase-orders/{id}:receive` | `purchasing:write` |
| `POST /api/v1/supplier-invoices` | `purchasing:write` |

Events: `purchase_order.created`, `purchase_order.approved`, `purchase_order.sent`, `purchase_order.received`, `supplier_invoice.mismatch`.

A supplier integration can listen for `purchase_order.sent` and submit the order to the supplier's portal automatically.
