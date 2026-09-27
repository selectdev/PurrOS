# Build an integration

This tutorial builds a small integration in TypeScript that:

1. pulls new sales from a POS API every 2 minutes and sends them to PurrOS, and
2. listens for `employee.created` webhooks and creates the person in the POS.

The same pattern works for online stores, timeclocks and most other systems. For other languages, generate a client from `/api/v1/openapi.json` and follow the same steps.

## 1. Start from the template

```bash
npx degit selectdev/PurrOS/packages/integration-template pos-bridge
cd pos-bridge
npm install
```

The template contains:

```
src/
  index.ts          # starts the sync loop and the webhook server
  purros.ts         # PurrOS SDK client
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

Go to **Settings → Integrations → Add integration**, upload the manifest, and fill in the config. Put the key and secret you're given in `.env`:

```dotenv
PURROS_URL=https://erp.example.com
PURROS_INTEGRATION_KEY=pk_live_…
PURROS_WEBHOOK_SECRET=whsec_…
```

## 4. Create the client

`src/purros.ts`:

```ts
import { PurrOS } from "@purros/sdk";

export const purros = new PurrOS({
  baseUrl: process.env.PURROS_URL!,
  apiKey: process.env.PURROS_INTEGRATION_KEY!,
  // retries on 429 and 5xx with backoff, and sets idempotency keys, by default
});

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
import { verifyWebhook } from "@purros/sdk";
import { createPosStaff } from "./pos-client";
import { config } from "./purros";

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

**Settings → Integrations → POS bridge** shows the heartbeat, batches received, rejected records, unmapped items, your log messages and webhook deliveries.

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
