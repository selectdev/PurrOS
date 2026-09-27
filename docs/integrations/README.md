# Integrations

PurrOS ships **no built-in connections** to specific products. Instead, every connection to another system (a POS, an online store, an HR platform, a timeclock, payroll, accounting, a supplier or sensors) is an **integration**: a small program you write or install that talks to PurrOS through its [API](../api/README.md) and [webhooks](../api/webhooks.md).

## How integrations work

```
 ┌──────────────┐     its own API / webhooks /     ┌──────────────┐    PurrOS API     ┌─────────┐
 │ POS, store,  │ ───────── export files ────────► │ Integration  │ ────────────────► │ PurrOS  │
 │ HR, payroll… │ ◄─────────────────────────────── │ (you run it) │ ◄──── webhooks ── │         │
 └──────────────┘                                  └──────────────┘                   └─────────┘
```

- An integration is a **separate program**: a container next to PurrOS, a serverless function, or a scheduled script. It can be written in any language.
- It uses **only** the public API and webhooks. It never touches the database or PurrOS internals.
- PurrOS never runs integration code, so a broken integration can't slow down or crash PurrOS. It can only affect data within the scopes it was given.
- Each integration gets **its own API key** limited to the scopes it declared, and its own webhook secret. Everything it changes is labelled with its name in the audit log.

## The manifest

An integration describes itself in a manifest (`purros-integration.json`):

```json
{
  "name": "store-101-pos",
  "displayName": "Front counter POS",
  "version": "1.0.0",
  "description": "Sends sales, tenders and clock-ins from the POS to PurrOS.",
  "homepage": "https://git.example.com/ops/pos-bridge",
  "scopes": ["organization:read", "sales:write", "cash:write", "time:write", "inventory:read"],
  "webhooks": {
    "url": "https://integrations.example.com/pos-bridge/webhooks",
    "events": ["item.updated", "employee.created", "employee.terminated"]
  },
  "config": [
    { "key": "posBaseUrl", "type": "string", "label": "POS API URL", "required": true },
    { "key": "posApiToken", "type": "secret", "label": "POS API token", "required": true },
    { "key": "locationMap", "type": "json", "label": "POS store ID → PurrOS location", "required": false }
  ]
}
```

| Field | Notes |
|---|---|
| `name` | Unique, lowercase, dashes allowed |
| `scopes` | The minimum the integration needs. See [scopes](../api/README.md#scopes). |
| `webhooks` | Optional. The URL and events to receive. |
| `config` | Settings the admin fills in when registering. Types: `string`, `number`, `boolean`, `secret`, `json`, `location`, `select`. Secrets are encrypted. |

## Registering an integration

Under **Settings → Integrations** (`integrations.manage`):

1. **Add integration** and upload the manifest, or fill in the same fields by hand.
2. Review the requested scopes. Scopes of switched-off features are refused, and the sensitive-data scope needs an Owner.
3. Fill in the config values.
4. Copy the **API key** and **webhook secret**, which are shown once, into the integration's environment.

Admins can then:

- see its **health** (last heartbeat and status message), recent API calls, batches received, rejected records and webhook deliveries
- **pause** it, which stops its key and its webhooks
- **rotate** its key or webhook secret
- **remove** it

## What an integration can call about itself

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/integrations/self` | Its registration, scopes and status |
| `GET /api/v1/integrations/self/config` | Config values entered by the admin |
| `POST /api/v1/integrations/self/health` | Heartbeat, e.g. `{ "status": "ok", "message": "Last sync 12:05" }` |
| `POST /api/v1/integrations/self/logs` | Notes shown in the admin UI, e.g. "Imported 842 transactions" |

## Matching records

Integrations match records using **external IDs**: store the other system's ID in `externalId` and use the `…/external/{externalId}` endpoints to look records up or upsert them. Most integrations don't need their own database at all.

If a record needs more than one external ID, for example a badge number in a timeclock as well as an ID in the HR system, use `integrationData`, a namespace only your integration can write:

```json
"integrationData": { "store-101-pos": { "posEmployeeId": "00417" } }
```

## Compatibility

Integrations depend only on `/api/v1`, so an integration written today keeps working across every PurrOS 1.x release. See [API versioning](../api/README.md#versioning).

## Sharing integrations

The community keeps a directory of integrations people have built and shared. It's a list of links, not code in the PurrOS repository. Integrations in the directory are maintained by their authors, so review the code and scopes before installing one.

## Next

- [Build an integration](building-an-integration.md): step-by-step tutorial
- [Integration recipes](recipes.md): POS, online store, timeclock, HR, payroll, accounting, sensors, notifications
