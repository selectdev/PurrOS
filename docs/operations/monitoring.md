# Monitoring & troubleshooting

## Health checks

| Endpoint | Meaning |
|---|---|
| `GET /api/health` | Liveness: the app process is running |
| `GET /api/ready` | Readiness: the database is reachable and migrations are up to date |

Point your load balancer or uptime monitor at `/api/ready`.

## Logs

The app and worker write structured JSON logs to stdout, with `requestId` on every line:

```bash
docker compose logs -f app worker
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

## Inside PurrOS

**Settings → System** (Owner) shows version, pending migrations, queue depths, failed jobs (with retry), storage use, and the result of the nightly integrity checks.

**Settings → Integrations** shows each integration's heartbeat, batches and rejected records. An integration that hasn't sent a heartbeat for a configurable time raises an alert.

## Nightly integrity checks

The worker runs these every night and alerts Owners if anything is wrong:

- **Stock ledger:** stock on hand equals the sum of stock movements for every item and location.
- **Outbox:** no events stuck undelivered.
- **Sources:** every active data source has sent data within its expected window, e.g. a POS that sent nothing yesterday.

Run them on demand with `purros doctor`.

## Common problems

| Symptom | Likely cause | Fix |
|---|---|---|
| Sign-in links go to the wrong address | `PURROS_URL` doesn't match the public URL | Correct it and restart |
| Passkeys fail to register | Site not served over HTTPS, or `PURROS_URL` mismatch | Serve over HTTPS on the exact `PURROS_URL` host |
| No emails | SMTP settings wrong | **Settings → System → Email → Send test email** or `purros email test` shows the SMTP error, and the delivery log shows failures |
| Uploads fail, or photos don't load | S3 credentials, bucket permissions or CORS | `purros storage test` reports which step fails. See [bucket setup](../getting-started/email-and-storage.md#bucket-setup) |
| Backup failed alert | Backup bucket unreachable or full | Check **Settings → System → Backups**, then `purros backup run`, which shows the error |
| Dashboards lag behind the POS | Worker queue backed up | Check `purros_queue_waiting`, and add worker replicas |
| Sales missing for a day | Integration down or sending the wrong location | Check integration health and the ingestion log, then have the integration re-send the day (safe, because it's idempotent) |
| Many "unmapped items" | POS items not linked to PurrOS items | Resolve them in **Sales → Unmapped items**, or sync the catalog |
| Webhook endpoint disabled | Receiver failing repeatedly | Fix the receiver, re-enable it, and replay missed events |
| `404 feature_disabled` from the API | The feature is switched off | An Owner can enable it under **Settings → Features** |

## Getting help

When reporting a problem, include the PurrOS version (**Settings → System**), relevant `requestId`s, and the output of `purros doctor`. Remove personal data from logs before sharing them.
