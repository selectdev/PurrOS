# Installation

PurrOS runs as four containers: the **app** (web UI and API), a **worker** (background jobs), **PostgreSQL** and **Redis**. The supported way to run them is Docker Compose.

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
cp .env.example .env
```

Always install from a release tag rather than the default branch.

## 2. Configure

Edit `.env`. These values are required:

```dotenv
PURROS_URL=https://erp.example.com
PURROS_SECRET=            # openssl rand -base64 32
DATABASE_URL=postgresql://purros:CHANGE_ME@db:5432/purros
REDIS_URL=redis://redis:6379
POSTGRES_PASSWORD=CHANGE_ME
```

`PURROS_SECRET` encrypts stored secrets and signs sessions. **Keep a copy of it somewhere safe.** If you lose it, encrypted data such as integration secrets and sensitive employee fields cannot be recovered.

To send invitations and password-reset links, you'll also need email (SMTP) settings. See [Configuration](configuration.md) for every option.

## 3. Start the services

```bash
docker compose up -d
docker compose exec app npx prisma migrate deploy
docker compose exec app npm run purros -- setup
```

The `setup` command creates the first **Owner** account and prints a one-time sign-in link.

## 4. Put a reverse proxy in front

The `app` container listens on port `3000` over plain HTTP. Put a reverse proxy in front of it to handle TLS. For example, with Caddy:

```caddyfile
erp.example.com {
    reverse_proxy app:3000
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
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
}
```

`PURROS_URL` must match the public address exactly (scheme and host), or sign-in links, passkeys and SSO callbacks will fail.

## 5. Sign in and run first-run setup

Open the link printed by `setup`, set up your sign-in method, and follow the [first-run setup](first-run-setup.md) wizard.

## Services

| Service | Purpose | Persistent data |
|---|---|---|
| `app` | Next.js web UI, Employee Area, kiosk timeclock, team displays and `/api/v1` | None |
| `worker` | Webhooks, data ingestion processing, imports and exports, forecasts, scheduled jobs | None |
| `db` | PostgreSQL 16 | **Yes: back this up** |
| `redis` | Redis 7: queues, cache, rate limits | No (can be rebuilt) |

Attachments (documents, photos, receipts) are stored in a Docker volume by default, or in S3-compatible storage. See [Configuration → File storage](configuration.md#file-storage).

## Scaling

- **More users:** run several `app` containers behind the proxy. They are stateless.
- **Heavy data feeds** (many POS terminals or online orders): run several `worker` containers. Jobs are distributed through Redis.
- **Managed services:** you can use a managed PostgreSQL (16+) and Redis (7+) by pointing `DATABASE_URL` and `REDIS_URL` at them and removing `db` and `redis` from the Compose file.

## Next steps

- [Configuration](configuration.md)
- [First-run setup](first-run-setup.md)
- [Backups & upgrades](../operations/backups-and-upgrades.md). Set up backups before going live.
