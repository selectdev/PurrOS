# Equipment & assets

Feature key: `equipment`.

> **Status:** The API covers the asset register, meter readings, preventive maintenance that opens work orders, and repair work orders. QR labels and the screens are planned. Screens described below arrive with the web app; until then, use the endpoints listed at the end of this page.

Keep track of your equipment, maintain it before it breaks, and get repairs done quickly.

## Asset register

Each asset (`equipment.manage`) records:

- name, category, location and area
- make, model, serial number
- purchase date and cost, supplier, warranty end date
- service provider and contact
- manuals, photos and documents
- a **QR code label**: print and stick it on the equipment, and scanning it opens the asset's page

Examples: refrigeration units, ovens, POS terminals, forklifts, vehicles, HVAC, treatment chairs, gym machines, laptops.

## Preventive maintenance

With `equipment.maintenance`, create maintenance plans per asset or category:

- **By time:** clean coils every 3 months, annual inspection
- **By usage:** every 250 hours or 10,000 km, using readings entered by hand or sent through the API

Plans create **maintenance tasks** automatically when due, assigned to a person, role or outside provider, optionally with a checklist from [Forms](forms-and-checklists.md).

## Repair tickets (work orders)

With `equipment.work_orders`:

1. Anyone with `work_orders.create` scans the QR code or picks the asset, describes the problem, and adds photos.
2. A manager (`work_orders.manage`) sets the priority and assigns it to a technician or service provider.
3. The ticket moves through open → assigned → in progress → waiting for parts → resolved → closed, with notes, costs and invoices attached.
4. **Downtime** is tracked from report to resolution.

Failed checklist answers and out-of-range sensors can create work orders automatically.

## History and lifecycle

Each asset's page shows its full history: maintenance, repairs, downtime and **total cost of ownership**. Reports highlight assets that cost more to keep than to replace, warranties about to expire, and overdue maintenance.

## API & events

| Endpoint | Scope |
|---|---|
| `GET/POST /api/v1/assets`, `PUT /api/v1/assets/external/{externalId}` | `equipment:read` / `equipment:write` |
| `POST /api/v1/assets/{id}/meter-readings` | `equipment:write` |
| `GET/POST /api/v1/work-orders`, `PATCH /api/v1/work-orders/{id}` | `equipment:read` / `equipment:write` |

Events: `maintenance.due`, `work_order.created`, `work_order.updated`, `work_order.closed`.

A service-provider integration can listen for `work_order.created`, create the job in the provider's system, and send status updates back with `PATCH /api/v1/work-orders/{id}`.
