# Configuration

Server-level configuration lives in environment variables, usually in `.env`. Everything that belongs to the business (features, roles, locations, sign-in methods, labor rules and so on) is configured in the web UI under **Settings**, not here.

Restart the `app` and `worker` containers after changing `.env`:

```bash
docker compose up -d
```

## Core

| Variable | Required | Default | Description |
|---|---|---|---|
| `PURROS_URL` | Yes | | Public URL, e.g. `https://erp.example.com`. Used in links, emails, passkeys and SSO callbacks. |
| `PURROS_SECRET` | Yes | | At least 32 random bytes (`openssl rand -base64 32`). Encrypts stored secrets and signs sessions. PurrOS refuses to start with a missing or example value. |
| `DATABASE_URL` | Yes | | PostgreSQL connection string. |
| `REDIS_URL` | Yes | | Redis connection string. |
| `PURROS_DEFAULT_TIMEZONE` | No | `UTC` | Timezone used before the first location is created. Each location has its own timezone. |
| `PURROS_DEFAULT_LOCALE` | No | `en` | Default language and number and date formats. Users can choose their own. |
| `LOG_LEVEL` | No | `info` | `debug`, `info`, `warn` or `error`. |
| `PURROS_TRUST_PROXY` | No | `true` | Trust `X-Forwarded-*` headers from the reverse proxy. |

## Email

Email is needed for invitations, magic links, password resets, notifications and scheduled reports.

| Variable | Description |
|---|---|
| `SMTP_HOST`, `SMTP_PORT` | SMTP server and port (usually `587`). |
| `SMTP_USER`, `SMTP_PASSWORD` | Credentials. |
| `SMTP_SECURE` | `true` for implicit TLS (port 465). STARTTLS is used automatically on 587. |
| `SMTP_FROM` | Sender, e.g. `PurrOS <erp@example.com>`. |

Without SMTP, PurrOS still works, but invitation and sign-in links must be copied from the admin UI by hand.

## Single sign-on

SSO providers are added in the UI under **Settings → Authentication**. Environment variables are only needed to pre-configure one provider at install time:

| Variable | Description |
|---|---|
| `OIDC_ISSUER` | Issuer URL of your identity provider. |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Client credentials. |
| `OIDC_DISPLAY_NAME` | Button label, e.g. `Sign in with Microsoft`. |

The redirect URL to register with your identity provider is `PURROS_URL/api/auth/callback/oidc`. See [Authentication](../admin/authentication.md) for SAML and automatic account provisioning.

## File storage

| Variable | Default | Description |
|---|---|---|
| `STORAGE_DRIVER` | `local` | `local` (Docker volume) or `s3`. |
| `STORAGE_LOCAL_PATH` | `/data/files` | Path inside the container for `local`. |
| `STORAGE_S3_ENDPOINT` | | Endpoint for S3-compatible services (MinIO, Backblaze, Wasabi…). Leave empty for AWS. |
| `STORAGE_S3_REGION`, `STORAGE_S3_BUCKET` | | Region and bucket. |
| `STORAGE_S3_ACCESS_KEY_ID`, `STORAGE_S3_SECRET_ACCESS_KEY` | | Credentials. |
| `STORAGE_MAX_UPLOAD_MB` | `25` | Maximum size of a single upload. |

## Notifications

| Variable | Description |
|---|---|
| `WEB_PUSH_VAPID_PUBLIC_KEY`, `WEB_PUSH_VAPID_PRIVATE_KEY` | Keys for browser push notifications. Generate them with `npm run purros -- generate-vapid`. If unset, push is disabled. |

SMS and chat notifications are sent through an [integration](../integrations/recipes.md#notifications-sms-chat) that subscribes to notification webhooks. They aren't configured here.

## API and ingestion limits

| Variable | Default | Description |
|---|---|---|
| `API_RATE_LIMIT_PER_MIN` | `600` | Default requests per minute per API key. |
| `API_INGEST_RATE_LIMIT_PER_MIN` | `3000` | Limit for keys that send data feeds (POS, online store). |
| `API_MAX_BATCH_SIZE` | `1000` | Maximum records per batch request. |

## AI assistant (optional)

The optional AI assistant in [Reports & insights](../guides/reports-and-insights.md#ai-assistant-optional) is off unless the feature is enabled **and** a provider is configured. These can also be set in the UI.

| Variable | Description |
|---|---|
| `AI_PROVIDER_BASE_URL` | Base URL of the model API, which can be a self-hosted model server. |
| `AI_PROVIDER_API_KEY` | API key, if the provider needs one. |
| `AI_MODEL` | Model name to use. |

No data is sent anywhere unless these are set and the feature is switched on.

## Observability

| Variable | Description |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Send traces and metrics to an OpenTelemetry collector. |
| `METRICS_ENABLED` | `true` exposes Prometheus metrics at `/api/metrics` (restrict access at the proxy). |

See [Monitoring](../operations/monitoring.md).

## Telemetry

| Variable | Default | Description |
|---|---|---|
| `PURROS_TELEMETRY` | `off` | Anonymous usage statistics (version, enabled features, rough size). Opt-in only. |
