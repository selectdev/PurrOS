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
  "scopes": ["organization:read", "sales:write", "cash:write", "time:write", "inventory:read", "people:read"],
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
| `name` | Unique, 2–63 lowercase letters, digits or dashes. It can't change after registration. |
| `displayName`, `version`, `description`, `homepage` | Shown to admins and in the audit log |
| `scopes` | The minimum the integration needs. See [scopes](../api/README.md#scopes). `people:sensitive` needs an Owner's approval. |
| `webhooks` | Optional. The URL and events to receive. Each event needs a scope of its feature (e.g. `employee.*` needs a `people:` scope, `location.*` needs `organization:read`). See [Webhooks](../api/webhooks.md#subscribing). |
| `config` | Settings the admin fills in when registering: `key`, `type`, `label`, `required`, and `options` for `select`. Types: `string`, `number`, `boolean`, `secret` (encrypted, masked when read back by admins), `json`, `location` (a location ID) and `select`. The integration reads the values with `GET /integrations/self/config`. |

## Registering an integration

Register with the API (permission `integrations.manage`) or the CLI on the server. Both validate the manifest against the scope and event catalogs and the features that are switched on.

```http
POST /api/v1/integrations
```

```json
{
  "manifest": { "name": "store-101-pos", "scopes": ["organization:read", "sales:write"], "config": [ … ] },
  "config": { "posBaseUrl": "https://pos.example.com", "posApiToken": "…" },
  "approveSensitive": false
}
```

```bash
purros integrations register --manifest purros-integration.json --config posBaseUrl=https://pos.example.com --config posApiToken=…
```

The response contains the **API key** and, when the manifest declares webhooks, the **webhook secret**. Both are shown only once: put them in the integration's environment. Values given on the command line are converted to the field's type (`number`, `boolean`, `json`). Only an Owner can approve `people:sensitive` (`approveSensitive`, or `--approve-sensitive`).

*(A **Settings → Integrations** page in the web app is planned; it will use the same API.)*

## Managing integrations

| To… | API (`integrations.manage`) | CLI |
|---|---|---|
| List them, with health and last heartbeat | `GET /integrations` | `purros integrations list` |
| See one: manifest, config (secrets masked), API keys, webhook endpoint | `GET /integrations/{id}` | |
| Change config values | `PATCH /integrations/{id}/config` with `{"values": {…}}`; `null` removes a value | |
| Install a new version | `PUT /integrations/{id}/manifest` | `purros integrations update <name> --manifest …` |
| Pause and resume | `POST /integrations/{id}:pause`, `:resume` | `purros integrations pause|resume <name>` |
| Rotate its API key | `POST /integrations/{id}:rotate-key` with `graceMinutes` | `purros integrations rotate-key <name> --grace 1h` |
| See what it logged | `GET /integrations/{id}/logs?level=error&from=…` | |
| See the batches it sent and how many records were rejected | `GET /integrations/{id}/batches?rejectedOnly=true` | |
| See its webhook deliveries | `GET /webhook-endpoints/{id}/deliveries` (`webhooks.manage`) | `purros webhooks list` |
| Remove it | `DELETE /integrations/{id}` | `purros integrations remove <name>` |

- **Pausing** makes its API keys fail with `401` and holds its webhooks. When it's resumed, the held events are delivered in order.
- **Rotating** issues a new key. The previous keys keep working for `graceMinutes` (default 60, at most 7 days; `0` revokes them at once), so you can deploy the new key without downtime.
- **Updating the manifest** keeps the name, API keys and config values of fields that still exist. Scopes and events are checked again. If the new manifest drops `webhooks`, the endpoint and its delivery log are removed; if it adds `webhooks`, the response includes the new endpoint's secret, once.
- **Removing** deletes its API keys, config, logs and webhook endpoint. Records it created stay, and the audit log keeps its name.

Every change is recorded in the audit log.

## What an integration can call about itself

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/integrations/self` | Its registration, scopes and status |
| `GET /api/v1/integrations/self/config` | Config values entered by the admin |
| `POST /api/v1/integrations/self/health` | Heartbeat, e.g. `{ "status": "ok", "message": "Last sync 12:05" }`. `status` is `ok`, `warning` or `error`. |
| `POST /api/v1/integrations/self/logs` | Notes for the integration's log, e.g. `{ "level": "info", "message": "Imported 842 transactions" }`. Levels: `debug`, `info` (default), `warning`, `error`. Admins read them at `GET /integrations/{id}/logs`. |

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
