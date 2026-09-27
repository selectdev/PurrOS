# Webhooks

Webhooks tell other systems about changes **made in PurrOS**, when they happen. They are the outgoing counterpart to the API. Typical uses:

- **Keeping systems in sync:** a new hire in PurrOS creates their account in the timeclock and door-access system.
- **Starting workflows:** a locked pay period sends hours to payroll, and an approved purchase order goes to the supplier.
- **Alerting:** a failed fridge temperature check or a deposit mismatch goes to a chat channel or an on-call phone.
- **Updating storefronts:** low stock marks a product as sold out online.
- **Analytics:** a stream of events into a data warehouse.

## Subscribing

Either:

- declare `webhooks` in an [integration manifest](../integrations/README.md#the-manifest), or
- add an endpoint under **Settings → Webhooks** (`webhooks.manage`), choosing the events you want.

Each endpoint has its own **signing secret**. You can only subscribe to events of enabled features that the integration or user is allowed to see.

## Payload

Every delivery is a `POST` with a JSON body:

```json
{
  "id": "evt_01J8ZQ7M3K2N…",
  "type": "timesheet.approved",
  "createdAt": "2026-09-27T17:04:00Z",
  "apiVersion": "v1",
  "actor": { "type": "user", "id": "usr_01H…" },
  "locationId": "loc_01H…",
  "data": {
    "object": { "id": "tsh_01J8…", "employeeId": "emp_01H…", "status": "approved", "totalHours": "38.50" },
    "previous": { "status": "submitted" }
  }
}
```

- `data.object` is the full resource as the API would return it.
- `data.previous` holds the changed fields' old values (on update events).
- `actor` is who caused it: `user`, `integration`, `system` or `employee` (Employee Area).

Headers:

| Header | Value |
|---|---|
| `PurrOS-Event-Id` | Same as `id`. Use it to ignore duplicates. |
| `PurrOS-Event-Type` | Same as `type` |
| `PurrOS-Signature` | `t=<unix seconds>,v1=<hex signature>` |
| `User-Agent` | `PurrOS-Webhooks/1` |

## Verifying signatures

The signature is `HMAC-SHA256(secret, "<t>.<raw request body>")`, hex-encoded. Always verify it, and reject deliveries whose timestamp is more than 5 minutes old.

With the SDK:

```ts
import { verifyWebhook } from "@purros/sdk";

app.post("/webhooks", express.raw({ type: "application/json" }), (req, res) => {
  const event = verifyWebhook(req.body, req.header("PurrOS-Signature"), process.env.PURROS_WEBHOOK_SECRET);
  // throws if invalid or too old
  handle(event);
  res.sendStatus(204);
});
```

Without the SDK (Node.js):

```ts
import crypto from "node:crypto";

function verify(rawBody: Buffer, header: string, secret: string) {
  const parts = Object.fromEntries(header.split(",").map((p) => p.split("=")));
  const t = Number(parts.t);
  if (Math.abs(Date.now() / 1000 - t) > 300) throw new Error("stale");
  const expected = crypto.createHmac("sha256", secret).update(`${t}.${rawBody}`).digest("hex");
  if (!crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(parts.v1))) throw new Error("bad signature");
  return JSON.parse(rawBody.toString("utf8"));
}
```

Python:

```python
import hmac, hashlib, json, time

def verify(raw_body: bytes, header: str, secret: str):
    parts = dict(p.split("=", 1) for p in header.split(","))
    t = int(parts["t"])
    if abs(time.time() - t) > 300:
        raise ValueError("stale")
    expected = hmac.new(secret.encode(), f"{t}.".encode() + raw_body, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(expected, parts["v1"]):
        raise ValueError("bad signature")
    return json.loads(raw_body)
```

Always compute the signature over the **raw** body bytes, before any JSON parsing.

## Delivery and retries

- Respond with any `2xx` within **10 seconds**. Do slow work after responding, e.g. put the event on your own queue.
- Anything else, or a timeout, is retried with exponential backoff: about 15 attempts over **3 days**.
- Delivery is **at least once**, so the same event can arrive more than once. Use `id` to skip duplicates.
- Events for the same record are sent in order, but events for different records may arrive in any order. Use `createdAt`, or fetch the latest state from the API if order matters.
- An endpoint that keeps failing is **disabled automatically** and admins are notified. Re-enable it in Settings, and missed events can be replayed.
- **Settings → Webhooks** shows every delivery, its response and timing, and lets you **resend** any event.

Events are recorded in the same database transaction as the change itself, so an event is never lost and never sent for a change that didn't happen.

## Event catalog

Events belong to their feature. Events of switched-off features aren't sent.

### Organization
`location.created`, `location.updated`

### People & HR
`employee.created`, `employee.updated`, `employee.transferred`, `employee.terminated`, `employee.archived`, `pay_rate.changed`, `document.expiring`, `certification.expiring`

### Time & Attendance
`punch.received`, `punch.corrected`, `punch.exception`, `timesheet.approved`, `timesheet.rejected`, `pay_period.locked`, `time_off.requested`, `time_off.approved`, `time_off.rejected`

### Scheduling & Forecasting
`forecast.updated`, `schedule.published`, `shift.changed`, `shift.swap_requested`, `shift.swap_approved`, `open_shift.posted`

### Cash Management
`cash.count_completed`, `cash.over_short_exceeded`, `cash.deposit_recorded`, `cash.deposit_mismatch`, `cash.settlement_mismatch`, `cash.business_day_closed`

### Inventory
`item.created`, `item.updated`, `stock.level_changed`, `stock.below_reorder_point`, `stock_count.posted`, `waste.recorded`, `transfer.sent`, `transfer.received`, `transfer.discrepancy`

### Purchasing & Ordering
`purchase_order.created`, `purchase_order.approved`, `purchase_order.sent`, `purchase_order.received`, `supplier_invoice.mismatch`

### Sales
`sales.unmapped_item`, `sales_order.created`, `sales_order.shipped`, `sales_order.cancelled`, `invoice.issued`, `invoice.paid`

### Forms, Checklists & Compliance
`form.submitted`, `form.answer_failed`, `checklist.overdue`, `corrective_action.created`, `corrective_action.closed`, `audit.completed`, `sensor.out_of_range`

### Equipment & Assets
`maintenance.due`, `work_order.created`, `work_order.updated`, `work_order.closed`

### Communication & Team Displays
`announcement.published`, `recognition.posted`, `notification.requested`

`notification.requested` is for integrations that deliver notifications by SMS or chat. It needs the `notifications:deliver` scope and contains the recipient's chosen contact details and the message text.

### Reports & Insights
`alert.triggered`, `recommendation.created`

### Tips

- `stock.level_changed` can be very frequent for busy locations. Prefer `stock.below_reorder_point` for storefront "sold out" updates, or poll `GET /stock-levels?updatedSince=…`.
- Webhooks from your POS or online store aren't sent to PurrOS directly. An integration receives them and calls the PurrOS API (see [Data ingestion](data-ingestion.md)).
