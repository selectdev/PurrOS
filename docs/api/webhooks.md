# Webhooks

Webhooks tell other systems about changes **made in PurrOS**, when they happen. They are the outgoing counterpart to the API. Typical uses:

- **Keeping systems in sync:** a new hire in PurrOS creates their account in the timeclock and door-access system.
- **Starting workflows:** a locked pay period sends hours to payroll, and an approved purchase order goes to the supplier.
- **Alerting:** a failed fridge temperature check or a deposit mismatch goes to a chat channel or an on-call phone.
- **Updating storefronts:** low stock marks a product as sold out online.
- **Analytics:** a stream of events into a data warehouse.

## Subscribing

There are two kinds of endpoint:

- **An integration's endpoint.** Declare `webhooks` in its [manifest](../integrations/README.md#the-manifest). It's created when the integration is registered, and its URL and events change only with the manifest. An integration can subscribe only to events its scopes cover: each event needs a scope of the same feature (read or write), `organization:read` for organization events, `attachments:read` for attachment events, and the `communication:*` scopes for Team Displays events.
- **A standalone endpoint**, for a data warehouse, a chat channel or any receiver that doesn't call the API. Add it with `POST /webhook-endpoints` (permission `webhooks.manage`):

```http
POST /api/v1/webhook-endpoints
```

```json
{ "url": "https://hooks.example.com/purros", "events": ["employee.created", "employee.terminated"], "description": "Door access sync" }
```

`["*"]` subscribes to every event. The response includes the endpoint's **signing secret**, which is shown only once (`POST /webhook-endpoints/{id}:rotate-secret` issues a new one).

You can only subscribe to events of enabled features. Send a test event with `POST /webhook-endpoints/{id}:ping`: a `webhook.ping` event, signed and delivered like any other, with `data.object.endpointId`.

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

With the SDK *(planned)*:

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
  const expected = crypto.createHmac("sha256", secret).update(`${t}.`).update(rawBody).digest("hex");
  if (parts.v1?.length !== expected.length || !crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(parts.v1)))
    throw new Error("bad signature");
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
- An endpoint that fails **25 times in a row** is disabled automatically. *(Notifying admins when that happens is planned.)*
- While an endpoint is **disabled**, or its integration is **paused**, new events still queue for it but aren't sent. Re-enabling (`PATCH /webhook-endpoints/{id}` with `"status": "active"`, or `purros webhooks enable`) or resuming sends them, in order.

Events are recorded in the same database transaction as the change itself, so an event is never lost and never sent for a change that didn't happen.

## Delivery log and replay

Every delivery is kept with its status (`pending`, `succeeded`, or `failed` after the retry schedule ran out), attempts, last HTTP status and error:

| To… | Use |
|---|---|
| See what an endpoint received or missed | `GET /webhook-endpoints/{id}/deliveries?status=failed&from=2026-09-27T00:00:00Z` |
| See the exact body that was sent | `GET /webhook-deliveries/{id}` |
| Send one again now (failed or not) | `POST /webhook-deliveries/{id}:retry` |
| Queue everything that failed, e.g. after a long outage | `POST /webhook-endpoints/{id}:retry-failed`, or `purros webhooks retry-failed <id>` |

Replayed deliveries restart the retry schedule and keep the original event `id`, so receivers that skip duplicates stay correct.

## Event catalog

Events belong to their feature. Events of switched-off features aren't sent.

### Organization
`location.created`, `location.updated`, `location.archived`, `org_unit.created`, `org_unit.updated`, `org_unit.archived`, `department.created`, `department.updated`, `department.archived`

Integrations need `organization:read` to receive these.

### Attachments
`attachment.uploaded`, `attachment.deleted` (needs `attachments:read`)

### People & HR
`employee.created`, `employee.updated`, `employee.transferred`, `employee.terminated`, `employee.archived`, `pay_rate.changed`, `document.expiring` *(planned)*, `certification.expiring` *(planned)*

### Time & Attendance
`punch.received`, `punch.corrected`, `punch.exception` *(planned)*, `timesheet.approved`, `timesheet.rejected`, `pay_period.locked`, `time_off.requested`, `time_off.approved`, `time_off.rejected`

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
`form.submitted`, `form.answer_failed`, `checklist.overdue` *(planned)*, `corrective_action.created`, `corrective_action.closed`, `audit.completed`, `sensor.out_of_range`

### Equipment & Assets
`maintenance.due`, `work_order.created`, `work_order.updated`, `work_order.closed`

### Communication & Team Displays
`announcement.published`, `recognition.posted`, `notification.requested` *(planned)*. Integrations receive these with any `communication:*` scope (or `notifications:deliver`).

`notification.requested` *(planned)* is for integrations that deliver notifications by SMS or chat. It needs the `notifications:deliver` scope and contains the recipient's chosen contact details and the message text.

### Reports & Insights
`alert.triggered`, `recommendation.created` *(planned)*

### Tips

- `stock.level_changed` can be very frequent for busy locations. Prefer `stock.below_reorder_point` for storefront "sold out" updates, or poll `GET /stock-levels?updatedSince=…`.
- `webhook.ping` is only sent by `POST /webhook-endpoints/{id}:ping`; you can't subscribe to it.
- Webhooks from your POS or online store aren't sent to PurrOS directly. An integration receives them and calls the PurrOS API (see [Data ingestion](data-ingestion.md)).
