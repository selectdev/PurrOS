# Installation

PurrOS runs as two containers: **api** (the `purros` Go binary, which serves the REST API and runs the background worker) and **PostgreSQL**. Redis is optional, and the Next.js web app will be a third container once it's released. The supported way to run them is Docker Compose.

## Requirements

| | Minimum | Recommended (up to ~500 employees, several locations) |
|---|---|---|
| CPU | 2 vCPU | 4 vCPU |
| Memory | 4 GB | 8 GB |
| Disk | 20 GB SSD | 50 GB SSD plus attachment storage |
| Software | Docker 24+, Docker Compose v2 | Same |
| Network | A domain name and TLS certificate (a reverse proxy can obtain one automatically) | Same |

Any Linux server or VM works. PurrOS also runs on macOS and Windows with Docker Desktop for evaluation, but that isn't recommended for production.

## 1. Get the code

```bash
git clone https://github.com/selectdev/PurrOS.git
cd PurrOS
git checkout <latest release tag>
cp config/purros.env.example config/purros.env
cp config/postgres.env.example config/postgres.env
chmod 600 config/*.env
```

Always install from a release tag rather than the default branch.

## 2. Configure

Configuration lives in the `config/` directory, which is never committed:

| File | Read by | Holds |
|---|---|---|
| `config/purros.env` | the `api` container | every PurrOS setting |
| `config/postgres.env` | the `db` container | the database user and password |

If you already have the `purros` binary, `purros init --url https://erp.example.com` writes both files with generated secrets. Otherwise edit the copies. These values are required:

```dotenv
# config/purros.env
PURROS_URL=https://erp.example.com
PURROS_SECRET=            # openssl rand -base64 32, or: purros secret generate
DATABASE_URL=postgres://purros:CHANGE_ME@db:5432/purros?sslmode=disable

# config/postgres.env
POSTGRES_PASSWORD=CHANGE_ME   # the same password as in DATABASE_URL
```

`PURROS_SECRET` encrypts stored secrets and signs sessions. **Keep a copy of it somewhere safe.** If you lose it, encrypted data such as integration secrets and sensitive employee fields cannot be recovered.

Recommended for production:

```dotenv
# Email (SMTP)
SMTP_HOST=smtp.your-provider.example
SMTP_PORT=587
SMTP_USER=...
SMTP_PASSWORD=...
SMTP_FROM="Acme Operations <ops@acme.example>"

# File storage (S3-compatible)
STORAGE_DRIVER=s3
STORAGE_S3_BUCKET=acme-purros-files
STORAGE_S3_REGION=eu-central-1
STORAGE_S3_ACCESS_KEY_ID=...
STORAGE_S3_SECRET_ACCESS_KEY=...
```

See [Email & file storage](email-and-storage.md) for setup and testing, and [Configuration](configuration.md) for every option.

## 3. Start the services

```bash
docker compose up -d                  # builds the API image; migrations run automatically on start
docker compose exec api purros setup  # guided: company, first Owner and first location
docker compose exec api purros doctor
```

`setup` asks for the company, the first **Owner** and, optionally, the first location. It then prints a one-time link for the Owner to set a password. For scripted installs, pass the values as flags (see the [CLI reference](../operations/cli.md#setup)). Until the web app ships, locations and integrations are managed with the [CLI](../operations/cli.md).

Everything PurrOS writes (uploaded files and nightly backups) lives in `/var/lib/purros` inside the container, on the `state` volume. Backups go to `/var/lib/purros/backups` every night. See [Backups & upgrades](../operations/backups-and-upgrades.md) to copy backups off the server, encrypt them and restore them.

## 4. Put a reverse proxy in front

The `api` container listens on port `8080` over plain HTTP (bound to `127.0.0.1` in the Compose file). Put a reverse proxy in front of it to handle TLS. For example, with Caddy:

```caddyfile
erp.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Or nginx:

```nginx
server {
    listen 443 ssl http2;
    server_name erp.example.com;
    # ssl_certificate / ssl_certificate_key ...

    client_max_body_size 50m;          # attachments and imports

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
}
```

`PURROS_URL` must match the public address exactly (scheme and host), or sign-in links, passkeys and SSO callbacks will fail.

## 5. Connect your systems

Register an integration for your POS, online store or timeclock with `purros integrations register` (see [Integrations](../integrations/README.md)), and browse the API at `PURROS_URL/api/v1/openapi.json`. When the web app is released, the Owner will sign in and continue with the [first-run setup](first-run-setup.md) wizard.

## Services

| Service | Purpose | Persistent data |
|---|---|---|
| `api` | The `purros` binary: `/api/v1`, health checks, and the background worker (webhooks, email, KPI alerts, nightly backups) | **Yes**: the `state` volume (`/var/lib/purros`) holds uploaded files and backups |
| `db` | PostgreSQL 16: all data and the job queue | **Yes: back this up** |
| `redis` | Optional (`docker compose --profile redis up -d`): shared rate limits across several `api` containers | No |
| `web` | Next.js web UI, Employee Area, kiosk timeclock, team displays (planned) | None |

Uploaded files are stored in the `state` volume by default, or in S3-compatible storage, which is recommended for production and required when running more than one `api` container. See [Email & file storage](email-and-storage.md#file-storage-s3).

## Scaling

- **More traffic:** run several `api` containers behind the proxy. They are stateless; set `REDIS_URL` so they share rate limits.
- **Heavy data feeds** (many POS terminals or online orders): set `PURROS_RUN_WORKER=false` on the `api` containers and run one or more dedicated `purros worker` containers. Workers share the PostgreSQL job queue safely.
- **Managed services:** point `DATABASE_URL` (and optionally `REDIS_URL`) at a managed PostgreSQL 16+ and remove `db` from the Compose file.

## Next steps

- [Configuration](configuration.md)
- [Email & file storage](email-and-storage.md)
- [First-run setup](first-run-setup.md)
- [Backups & upgrades](../operations/backups-and-upgrades.md). Set up backups before going live.
