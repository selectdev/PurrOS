# Build an integration

This tutorial builds a small integration in TypeScript that:

1. pulls new sales from a POS API every 2 minutes and sends them to PurrOS, and
2. listens for `employee.created` webhooks and creates the person in the POS.

The same pattern works for online stores, timeclocks and most other systems. For other languages, generate a client from `/api/v1/openapi.json` and follow the same steps.

## 1. Set up the project

> The `@purros/sdk` package and the integration template are **planned**. Until they ship, this tutorial uses a small `fetch`-based client (step 4) with the same method names, so the code carries over when the SDK arrives.

```bash
mkdir pos-bridge && cd pos-bridge
npm init -y && npm install express && npm install -D typescript tsx @types/express @types/node
```

Lay it out like the planned template:

```
src/
  index.ts          # starts the sync loop and the webhook server
  purros.ts         # PurrOS API client
  sync.ts           # your sync logic
  webhooks.ts       # your webhook handlers
purros-integration.json
Dockerfile
.env.example
```

## 2. Write the manifest

`purros-integration.json`:

```json
{
  "name": "pos-bridge",
  "displayName": "POS bridge",
  "version": "0.1.0",
  "scopes": ["organization:read", "sales:write", "people:read"],
  "webhooks": { "url": "https://integrations.example.com/pos-bridge/webhooks", "events": ["employee.created"] },
  "config": [
    { "key": "posBaseUrl", "type": "string", "required": true },
    { "key": "posApiToken", "type": "secret", "required": true }
  ]
}
```

## 3. Register it in PurrOS

Register it with the API as someone with `integrations.manage` (for example with a personal API key):

```bash
curl -X POST https://erp.example.com/api/v1/integrations \
  -H "Authorization: Bearer $PURROS_PERSONAL_KEY" -H "Content-Type: application/json" \
  -d "{\"manifest\": $(cat purros-integration.json), \"config\": {\"posBaseUrl\": \"https://pos.example.com\", \"posApiToken\": \"…\"}}"
```

or with the CLI on the PurrOS server:

```bash
docker compose exec api purros integrations register --manifest /path/to/purros-integration.json \
  --config posBaseUrl=https://pos.example.com --config posApiToken=…
```

Either way, the API key and webhook secret are returned **once**. Put them in the integration's `.env`:

```dotenv
PURROS_URL=https://erp.example.com
PURROS_INTEGRATION_KEY=pk_live_…
PURROS_WEBHOOK_SECRET=whsec_…
```

## 4. Create the client

`src/purros.ts`:

```ts
import { createHmac, randomUUID, timingSafeEqual } from "node:crypto";

const baseUrl = process.env.PURROS_URL!.replace(/\/$/, "") + "/api/v1";
const apiKey = process.env.PURROS_INTEGRATION_KEY!;

// Calls the API with an idempotency key, retrying 429 and 5xx with backoff.
async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const key = randomUUID();
  for (let attempt = 0; ; attempt++) {
    const res = await fetch(baseUrl + path, {
      method,
      headers: { Authorization: `Bearer ${apiKey}`, "Content-Type": "application/json", "Idempotency-Key": key },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (res.ok) return res.status === 204 ? (undefined as T) : res.json();
    if ((res.status === 429 || res.status >= 500) && attempt < 5) {
      const wait = Number(res.headers.get("Retry-After")) || 2 ** attempt;
      await new Promise((r) => setTimeout(r, wait * 1000));
      continue;
    }
    throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
  }
}

export const purros = {
  sales: { transactions: { batch: (b: unknown) => call<{ results: { status: string }[] }>("POST", "/sales/transactions:batch", b) } },
  integrations: {
    self: {
      config: () => call<Record<string, string>>("GET", "/integrations/self/config"),
      health: (b: { status: "ok" | "warning" | "error"; message?: string }) => call("POST", "/integrations/self/health", b),
      log: (b: { message: string }) => call("POST", "/integrations/self/logs", b),
    },
  },
};

// Checks PurrOS-Signature: t=<unix>,v1=<hex HMAC-SHA256(secret, t + "." + body)>.
export function verifyWebhook(body: Buffer, header: string | undefined, secret: string) {
  const parts = Object.fromEntries((header ?? "").split(",").map((p) => p.split("=", 2)));
  const expected = createHmac("sha256", secret).update(`${parts.t}.`).update(body).digest("hex");
  const ok = parts.v1?.length === expected.length && timingSafeEqual(Buffer.from(parts.v1), Buffer.from(expected));
  if (!ok || Math.abs(Date.now() / 1000 - Number(parts.t)) > 300) throw new Error("bad signature");
  return JSON.parse(body.toString("utf8"));
}

export const config = await purros.integrations.self.config();
// → { posBaseUrl: "...", posApiToken: "..." }
```

## 5. Send data in

`src/sync.ts`:

```ts
import { purros, config } from "./purros";
import { fetchPosTransactions } from "./pos-client"; // your code for the POS API

let cursor = await loadCursor(); // e.g. the last POS transaction timestamp, kept in a small file

export async function syncSales() {
  const txns = await fetchPosTransactions(config.posBaseUrl, config.posApiToken, cursor);
  if (txns.length === 0) return;

  for (const chunk of chunks(txns, 500)) {
    const result = await purros.sales.transactions.batch({
      source: "pos:store-101",
      transactions: chunk.map((t) => ({
        externalId: t.id,
        locationExternalId: t.storeId,
        occurredAt: t.closedAt,
        type: t.isRefund ? "refund" : "sale",
        lines: t.items.map((i) => ({
          itemExternalId: i.productId,
          name: i.name,
          quantity: String(i.qty),
          unitPrice: i.price,
          discount: i.discount ?? "0",
        })),
        tenders: t.payments.map((p) => ({ type: mapTender(p.method), amount: p.amount })),
        total: t.total,
      })),
    });

    const rejected = result.results.filter((r) => r.status === "rejected");
    if (rejected.length) console.warn("Rejected records", rejected);
  }

  cursor = txns.at(-1)!.closedAt;
  await saveCursor(cursor);
  await purros.integrations.self.log({ message: `Sent ${txns.length} transactions` });
}
```

Because records are unique on `(source, externalId)`, it's safe to overlap: if you're unsure whether something was sent, send it again.

## 6. React to webhooks

`src/webhooks.ts`:

```ts
import express from "express";
import { createPosStaff } from "./pos-client";
import { config, verifyWebhook } from "./purros";

export const app = express();

app.post("/webhooks", express.raw({ type: "application/json" }), async (req, res) => {
  let event;
  try {
    event = verifyWebhook(req.body, req.header("PurrOS-Signature"), process.env.PURROS_WEBHOOK_SECRET!);
  } catch {
    return res.sendStatus(400);
  }

  res.sendStatus(204); // answer quickly, then do the work

  if (event.type === "employee.created") {
    const e = event.data.object;
    await createPosStaff(config.posBaseUrl, config.posApiToken, {
      externalRef: e.id,
      name: `${e.firstName} ${e.lastName}`,
    });
  }
});
```

Webhooks can arrive more than once. Check `event.id` if an action must not be repeated.

## 7. Run it

`src/index.ts`:

```ts
import { app } from "./webhooks";
import { syncSales } from "./sync";
import { purros } from "./purros";

app.listen(8080);

setInterval(async () => {
  try {
    await syncSales();
    await purros.integrations.self.health({ status: "ok" });
  } catch (err) {
    await purros.integrations.self.health({ status: "error", message: String(err) });
  }
}, 2 * 60 * 1000);
```

Run it next to PurrOS by adding it to your Compose file:

```yaml
  pos-bridge:
    build: ./pos-bridge
    env_file: ./pos-bridge/.env
    restart: unless-stopped
```

## 8. Check it in PurrOS

With `integrations.manage` (and `webhooks.manage` for deliveries):

- `GET /api/v1/integrations/{id}` shows its health message, last heartbeat, keys and webhook endpoint (or `purros integrations list`).
- `GET /api/v1/integrations/{id}/batches?rejectedOnly=true` shows batches with rejected records, and `GET /api/v1/integrations/{id}/logs` your log messages.
- `GET /api/v1/webhook-endpoints/{endpointId}/deliveries` shows every webhook sent to it, and `POST /api/v1/webhook-endpoints/{endpointId}:ping` sends a test event.
- `GET /api/v1/sales/transactions?source=pos:store-101` shows what arrived, and `GET /api/v1/sales/unmapped-items` lists POS items that still need linking.

When you release a new version, update its manifest with `PUT /api/v1/integrations/{id}/manifest` (or `purros integrations update pos-bridge`). To replace its key without downtime, use `POST /api/v1/integrations/{id}:rotate-key`.

*(A **Settings → Integrations** page in the web app is planned; it uses the same API.)*

## Checklist before going live

- [ ] Only the scopes you need
- [ ] Stable `source` labels and `externalId`s
- [ ] Batches of 100–1,000 records
- [ ] Rejected records logged and fixed
- [ ] Webhook signature verified on the raw body
- [ ] Webhook handler answers within 10 seconds and tolerates duplicates
- [ ] Health heartbeat sent regularly
- [ ] Secrets kept in environment variables, not in code
- [ ] History backfilled (see [Data ingestion → Backfilling](../api/data-ingestion.md#backfilling-history))
