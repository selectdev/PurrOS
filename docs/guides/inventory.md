# Inventory

Feature key: `inventory`.

Know what you have, where it is, what it's worth, and where you're losing money, in real time.

## Items

Each item (`items.manage`) has:

- SKU, name, category, barcodes, variants (size, color…)
- a **base unit** and other units with conversions, e.g. 1 case = 6 bottles = 4.5 L
- cost (weighted average, updated on each receipt)
- par levels and reorder points per location
- optional **batch and expiry tracking** (`inventory.batches`)
- `externalId` for matching with your POS or online store catalog

## How stock changes

Every change is a permanent entry in the **stock ledger**. Stock on hand is always the sum of the ledger, and nothing is overwritten.

| Movement | Created by |
|---|---|
| Receipt | [Purchasing](purchasing-and-ordering.md) goods receipts |
| Sale / usage | Sales transactions from your POS or online store, via [usage recipes](#usage-recipes) or direct item sales |
| Shipment | [Sales](sales.md) orders |
| Transfer out / in | [Transfers](#transfers-between-locations) |
| Waste | [Waste entries](#waste-and-shrink) |
| Count adjustment | Posted [counts](#counts) |
| Adjustment | Manual adjustments with a reason (`inventory.adjust`) |

Because every movement updates stock immediately, you can see **stock on hand and cost of goods sold at any moment**, not just after a month-end count.

## Counts

1. Set up **storage areas** per location (e.g. Stockroom, Cooler, Shelf A) and order items the way people walk.
2. Schedule counts: daily for high-value items, weekly or monthly for everything, or spot counts.
3. Staff count on a phone or tablet (`stock_counts.count`), scanning barcodes and entering quantities in whatever units are easiest (cases, boxes, singles).
4. A manager reviews differences and **posts** the count (`stock_counts.manage`), which creates count adjustments in the ledger.

Sales that happen during a count are handled automatically using the count's timestamp.

## Waste and shrink

Record waste, spoilage, damage, theft, samples or staff meals (`waste.record`) with a reason and an optional photo. Waste reports show cost by reason, item and location.

## Transfers between locations

1. The sending location creates a transfer and ships it (`transfers.manage`).
2. The receiving location confirms what actually arrived.
3. Differences are flagged to both sides, and stock is only moved for what was received.

## Usage recipes

For businesses that sell something made from stock, like meals, drinks, services or repairs, `inventory.usage_recipes` (needs Sales) defines what one unit sold uses:

| Sold product / service | Uses |
|---|---|
| Large latte | 18 g espresso beans, 300 ml milk, 1 large cup, 1 lid |
| Haircut & color | 60 ml color, 1 pair of gloves |
| Oil change | 5 L oil, 1 oil filter |

When sales arrive, PurrOS deducts the ingredients and calculates **expected (theoretical) usage**.

## Gain/loss and variance

Comparing **actual usage** (opening stock + received − closing stock) with **expected usage** shows:

- variance per item and location, valued at cost
- the **biggest opportunities**: items, locations and periods where you lose the most money
- trends after changes such as new portion sizes or staff training

## Reports

Stock on hand and value, movements, cost of goods sold and COGS %, variance and gain/loss, waste, counts completed on time, slow-moving and expiring stock.

## API & events

| Endpoint | Scope |
|---|---|
| `GET/POST /api/v1/items`, `PUT /api/v1/items/external/{externalId}` | `inventory:read` / `inventory:write` |
| `GET /api/v1/stock-levels?locationId=…` | `inventory:read` |
| `GET /api/v1/stock-movements` | `inventory:read` |
| `POST /api/v1/inventory/adjustments` | `inventory:write` |
| `POST /api/v1/inventory/waste` | `inventory:write` |
| `GET/POST /api/v1/stock-counts` | `inventory:read` / `inventory:write` |
| `GET/POST /api/v1/transfers` | `inventory:read` / `inventory:write` |
| `GET/PUT /api/v1/usage-recipes/{itemId}` | `inventory:read` / `inventory:write` |

Events: `item.created`, `item.updated`, `stock.level_changed`, `stock.below_reorder_point`, `stock_count.posted`, `waste.recorded`, `transfer.sent`, `transfer.received`, `transfer.discrepancy`.
