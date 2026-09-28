# Monitoring & troubleshooting

## Health checks

| Endpoint | Meaning |
|---|---|
| `GET /api/health` | Liveness: the app process is running |
| `GET /api/ready` | Readiness: the database is reachable and migrations are up to date |

Point your load balancer or uptime monitor at `/api/ready`.

## Logs

The API and its worker write structured JSON logs to stdout, with `requestId` on every request line:

```bash
docker compose logs -f api
```

Set `LOG_LEVEL=debug` temporarily when investigating a problem. API errors return the same `requestId` in the `X-Request-Id` header and the error body, so you can find the matching log lines.

## Metrics

*(Planned.)* With `METRICS_ENABLED=true`, Prometheus metrics are served at `/api/metrics`. Restrict access to this path at your proxy. Useful ones:

| Metric | Watch for |
|---|---|
| `purros_http_request_duration_seconds` | p95 above 1 s |
| `purros_queue_waiting{queue}` | Growing queues: the worker is behind, or add more workers |
| `purros_queue_failed_total{queue}` | Spikes in failed jobs |
| `purros_webhook_delivery_failures_total` | Receivers that are down |
| `purros_ingest_records_total{source,status}` | A source that stops sending, or many rejected records |
| `purros_ledger_drift_total` | Anything above 0 (see below) |

With `OTEL_EXPORTER_OTLP_ENDPOINT`, traces and metrics are also sent to an OpenTelemetry collector.

## Status and checks

`purros status` gives an overview: version, schema, accounts, queues (outbox, webhook deliveries, email), the backup schedule and the last backup.

`purros doctor` checks the installation and exits with code 1 when something is wrong, so you can run it from cron or a monitoring agent:

- configuration, database connection, migrations and company setup
- the Owner account and the encryption secret
- email (SMTP), the event outbox, webhook endpoints and email delivery
- the **stock ledger**: stock on hand equals the sum of stock movements for every item and location
- location time zones, file storage and backups

`purros integrations list` shows each integration's health message and last heartbeat, and `purros webhooks list` each webhook endpoint's status with its pending and failed deliveries.

*(Planned: a **Settings → System** page in the web app, nightly integrity checks that alert Owners, and alerts when a data source stops sending.)*

## Common problems

| Symptom | Likely cause | Fix |
|---|---|---|
| Sign-in links go to the wrong address | `PURROS_URL` doesn't match the public URL | Correct it and restart |
| No emails | SMTP settings wrong | `purros email test` shows the SMTP error, and `purros email log` shows failed deliveries |
| Uploads fail, or photos don't load | S3 credentials, bucket permissions or CORS | `purros storage test` reports which step fails. See [bucket setup](../getting-started/email-and-storage.md#bucket-setup) |
| Backups failing | Backup directory full or not writable, or the worker isn't running | `purros backup list` shows recent runs and their errors; `purros backup create` shows the error directly |
| Reports lag behind the POS | Worker backed up | Check the queues in `purros status`, and add `purros worker` replicas |
| Sales missing for a day | Integration down or sending the wrong location | Check `purros integrations list` and `GET /api/v1/sales/transactions?source=…`, then have the integration re-send the day (safe, because it's idempotent) |
| Many "unmapped items" | POS items not linked to PurrOS items | List them with `GET /api/v1/sales/unmapped-items` and link them with `POST /api/v1/sales/unmapped-items:map`, or sync the catalog |
| Webhook endpoint disabled | Receiver failed 25 times in a row | Fix the receiver, check it with `POST /webhook-endpoints/{id}:ping`, then re-enable it (`purros webhooks enable <id>`, or `PATCH /webhook-endpoints/{id}` with `"status": "active"`). Deliveries queued while it was disabled are sent; `purros webhooks retry-failed <id>` also replays those that ran out of retries |
| An integration's data stops arriving | Integration down, paused, or its key rotated or expired | `GET /integrations/{id}` shows its status, last heartbeat and keys; `GET /integrations/{id}/batches?rejectedOnly=true` and `GET /integrations/{id}/logs?level=error` show what went wrong |
| `404 feature_disabled` from the API | The feature is switched off | `purros features enable <key>` |

## Getting help

When reporting a problem, include the PurrOS version (`purros version`), relevant `requestId`s, and the output of `purros doctor`. Remove personal data from logs before sharing them.
