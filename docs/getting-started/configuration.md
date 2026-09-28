# Configuration

Server-level configuration lives in environment variables, usually in `config/purros.env`. Everything that belongs to the business (features, roles, locations, sign-in methods, labor rules and so on) is stored in the database and managed through the API and the `purros` CLI (and under **Settings** once the web app ships), not here.

Restart the `api` container (and any `worker` containers) after changing `config/purros.env`:

```bash
docker compose up -d
```

## Core

| Variable | Required | Default | Description |
|---|---|---|---|
| `PURROS_URL` | Yes | | Public URL, e.g. `https://erp.example.com`. Used in links, emails and the API spec. |
| `PURROS_SECRET` | Yes | | At least 32 random bytes (`openssl rand -base64 32`). Encrypts stored secrets and signs sessions. PurrOS refuses to start with a missing or example value. |
| `DATABASE_URL` | Yes | | PostgreSQL connection string. |
| `REDIS_URL` | No | | Optional Redis, used to share rate limits across several API containers. |
| `PURROS_LISTEN` | No | `:8080` | Address the API listens on. |
| `PURROS_RUN_WORKER` | No | `true` | Run the background worker inside `purros serve`. Set to `false` when you run separate `purros worker` containers. |
| `LOG_LEVEL` | No | `info` | `debug`, `info`, `warn` or `error`. |
| `PURROS_TRUST_PROXY` | No | `true` | Trust `X-Forwarded-*` headers from the reverse proxy. |
| `PURROS_ENV_FILE` | No | | Env file for the `purros` command to load instead of `$PURROS_CONFIG_DIR/purros.env` (same as `--env-file`). |

## Directories

PurrOS keeps configuration and runtime state apart:

| Variable | Default | Docker image | What's in it |
|---|---|---|---|
| `PURROS_CONFIG_DIR` | `./config` | *(unused: Compose passes `config/purros.env` as the environment)* | `purros.env` (PurrOS settings) and `postgres.env` (database container). The `purros` command loads `purros.env` from here when it exists. |
| `PURROS_STATE_DIR` | `./state` | `/var/lib/purros`, on the `state` volume | `files/` (uploaded files with `local` storage) and `backups/` |

Back up both: `config/` holds `PURROS_SECRET`, and `state/` holds uploaded files. See [Backups & upgrades](../operations/backups-and-upgrades.md).

## Email

PurrOS sends email through any SMTP server. Today that's invitations, sign-in links and password resets; notifications, scheduled reports, purchase orders and invoices by email are planned.

| Variable | Default | Description |
|---|---|---|
| `SMTP_HOST`, `SMTP_PORT` | `587` | SMTP server and port |
| `SMTP_SECURE` | `false` | `true` for implicit TLS (port 465) |
| `SMTP_USER`, `SMTP_PASSWORD` | | Credentials |
| `SMTP_FROM`, `SMTP_REPLY_TO` | | Sender and reply-to address |
| `SMTP_REQUIRE_TLS`, `SMTP_TLS_REJECT_UNAUTHORIZED` | `true` | TLS requirements |
| `SMTP_RATE_PER_SECOND` | `10` | Sending rate |

See [Email & file storage](email-and-storage.md#email-smtp) for what's sent, deliverability (SPF, DKIM, DMARC), testing and the delivery log.

## File storage

Uploaded files (documents, photos, receipts…) are stored on local disk or in any S3-compatible object storage.

| Variable | Default | Description |
|---|---|---|
| `STORAGE_DRIVER` | `local` | `local` (Docker volume) or `s3` |
| `STORAGE_LOCAL_PATH` | `$PURROS_STATE_DIR/files` | Where `local` storage keeps files |
| `STORAGE_S3_BUCKET`, `STORAGE_S3_REGION` | | Bucket and region |
| `STORAGE_S3_ENDPOINT` | | For non-AWS services (MinIO, R2, Backblaze, Wasabi…) |
| `STORAGE_S3_ACCESS_KEY_ID`, `STORAGE_S3_SECRET_ACCESS_KEY` | | Credentials (or an IAM role on AWS) |
| `STORAGE_S3_FORCE_PATH_STYLE`, `STORAGE_S3_PREFIX` | `false`, | Path-style addressing, and a folder prefix inside the bucket |
| `STORAGE_S3_SSE`, `STORAGE_S3_KMS_KEY_ID` | | Server-side encryption |
| `STORAGE_SIGNED_URL_TTL` | `300` | Seconds a download link stays valid |
| `STORAGE_MAX_UPLOAD_MB` | `25` | Largest single upload |

See [Email & file storage](email-and-storage.md#file-storage-s3) for bucket setup, IAM policy, CORS, a MinIO example and migrating from local disk to S3.

## Backups

| Variable | Default | Description |
|---|---|---|
| `PURROS_BACKUP_DIR` | *(off)*; `/var/lib/purros/backups` in the config written by `purros init` | Turn on daily backups into this directory |
| `PURROS_BACKUP_HOUR` | `2` | Hour of the day (UTC) after which the daily backup runs |
| `PURROS_BACKUP_KEEP` | `14` | How many backups to keep |
| `PURROS_BACKUP_PASSPHRASE` | | Encrypt backups with this passphrase |
| `PURROS_BACKUP_FILES` | `auto` | Include uploaded files: `auto` (only with local storage), `true` or `false` |
| `PURROS_BACKUP_S3_ENABLED` | `false` | Also upload backups to an S3 bucket |
| `PURROS_BACKUP_S3_BUCKET`, `_REGION`, `_ENDPOINT`, `_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY`, `_FORCE_PATH_STYLE`, `_SSE`, `_KMS_KEY_ID` | | The backup bucket, with the same meaning as the `STORAGE_S3_*` settings. See [Database backups to S3](email-and-storage.md#database-backups-to-s3-optional). |
| `PURROS_BACKUP_S3_PREFIX` | `purros-backups/` | Folder inside the bucket |
| `PURROS_BACKUP_S3_KEEP` | `PURROS_BACKUP_KEEP` | Backups to keep in the bucket |

See [Backups & upgrades](../operations/backups-and-upgrades.md).

## API and ingestion limits

| Variable | Default | Description |
|---|---|---|
| `API_RATE_LIMIT_PER_MIN` | `600` | Default requests per minute per API key. |
| `API_INGEST_RATE_LIMIT_PER_MIN` | `3000` | Limit for keys that send data feeds (POS, online store). |
| `API_MAX_BATCH_SIZE` | `1000` | Maximum records per batch request. |

## Planned settings

These aren't read by PurrOS yet. They're listed so you know what's coming; setting them today has no effect.

| Variable | For |
|---|---|
| `PURROS_DEFAULT_TIMEZONE`, `PURROS_DEFAULT_LOCALE` | Timezone before the first location exists; default language and formats |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_DISPLAY_NAME` | Pre-configuring a single sign-on provider (redirect URL `PURROS_URL/api/auth/callback/oidc`). See [Authentication](../admin/authentication.md). |
| `WEB_PUSH_VAPID_PUBLIC_KEY`, `WEB_PUSH_VAPID_PRIVATE_KEY` | Browser push notifications (`purros generate-vapid`) |
| `AI_PROVIDER_BASE_URL`, `AI_PROVIDER_API_KEY`, `AI_MODEL` | The optional [AI assistant](../guides/reports-and-insights.md#ai-assistant-optional). No data will be sent anywhere unless these are set and the feature is on. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Traces and metrics to an OpenTelemetry collector |
| `METRICS_ENABLED` | Prometheus metrics at `/api/metrics` |
| `PURROS_TELEMETRY` | Opt-in anonymous usage statistics (off by default) |

SMS and chat notifications will be sent through an [integration](../integrations/recipes.md#notifications-sms-chat) rather than configured here.
