# Deploying on Dokploy

[Dokploy](https://dokploy.com) is a self-hosted platform that builds and runs Docker Compose apps behind its own Traefik proxy, with automatic HTTPS. PurrOS includes a Compose file made for it: [`docker-compose.dokploy.yml`](../../docker-compose.dokploy.yml).

It differs from the standard [`docker-compose.yml`](installation.md) in three ways:

- Settings come from Dokploy's **Environment** tab instead of the `config/` directory, which isn't in the repository.
- The API isn't published on a host port. Dokploy's Traefik reaches it on the container network.
- There's no Redis service. Add one only if you run several `api` containers.

## 1. Create the app

1. In Dokploy, open a project and choose **Create Service → Compose**.
2. Under **General → Provider**, pick your Git provider (or **Git** with the repository URL) and the branch or tag to deploy. Deploy a release tag for production.
3. Set **Compose Path** to `./docker-compose.dokploy.yml`.
4. Under **Advanced**, turn on **Isolated Deployment**, so the `db` service name can't clash with other apps on the server.

## 2. Set the environment

In the **Environment** tab, add at least:

```dotenv
PURROS_URL=https://erp.example.com
PURROS_SECRET=           # openssl rand -base64 32
POSTGRES_PASSWORD=       # openssl rand -base64 24 | tr -d '/+='
```

- `PURROS_URL` must be the exact public address, with `https://`.
- **Keep a copy of `PURROS_SECRET` somewhere outside Dokploy.** Without it, encrypted data in the database and in backups can't be read.
- The database password is used by both containers: the Compose file builds `DATABASE_URL` from it, so don't set `DATABASE_URL` yourself. Avoid `/`, `+` and `=` in it, because it's placed in a URL.

Optional settings go in the same tab. Every variable in [Configuration](configuration.md) works, for example:

```dotenv
# Email (invitations, sign-in links, password resets)
SMTP_HOST=smtp.your-provider.example
SMTP_PORT=587
SMTP_USER=apikey
SMTP_PASSWORD=...
SMTP_FROM="Acme Operations <ops@acme.example>"

# Encrypt the nightly backups, and copy them off the server
PURROS_BACKUP_PASSPHRASE=...
PURROS_BACKUP_S3_ENABLED=true
PURROS_BACKUP_S3_BUCKET=acme-purros-backups
PURROS_BACKUP_S3_REGION=eu-central-1
PURROS_BACKUP_S3_ACCESS_KEY_ID=...
PURROS_BACKUP_S3_SECRET_ACCESS_KEY=...
```

Nightly backups go to `/var/lib/purros/backups` on the `state` volume by default. Set `PURROS_BACKUP_DIR=` (empty) to turn them off.

## 3. Add the domain

In the **Domains** tab, add a domain:

| Field | Value |
|---|---|
| Service name | `api` |
| Host | `erp.example.com` (the host of `PURROS_URL`) |
| Path | `/` |
| Container port | `8080` |
| HTTPS | on, with **Let's Encrypt** |

Point the domain's DNS `A` (or `AAAA`) record at the Dokploy server before deploying, so the certificate can be issued.

## 4. Deploy

Click **Deploy**. Dokploy builds the API image from `api/` and starts PostgreSQL and the API; migrations run automatically on start. Check the **Logs** for the `api` service, then open `https://erp.example.com/api/ready`, which should answer `{"status":"ready",…}`.

## 5. Run first-time setup

Open the `api` service's **Terminal** in Dokploy and pick **sh** (the image has a minimal BusyBox shell, not bash), then run `purros setup` and `purros doctor`. Or run them over SSH on the Dokploy server:

```bash
docker ps --filter name=api --format '{{.Names}}'     # find the container, e.g. purros-abc123-api-1
docker exec -it purros-abc123-api-1 purros setup     # company, first Owner and first location
docker exec -it purros-abc123-api-1 purros doctor
```

`setup` prints a one-time link for the Owner to set a password. Every other [CLI command](../operations/cli.md) runs the same way, for example `docker exec -it <container> purros backup create`.

From there, continue with [First-run setup](first-run-setup.md): build the organization through the API and register your [integrations](../integrations/README.md).

## Backups

- **PurrOS backups** (recommended): nightly, consistent while running, optionally encrypted, and uploaded to S3 when `PURROS_BACKUP_S3_ENABLED=true`. See [Backups & upgrades](../operations/backups-and-upgrades.md).
- **Dokploy volume backups** can copy the `state` volume (uploaded files and PurrOS's backup files) to S3 too. Don't rely on a raw copy of the `db-data` volume while PostgreSQL is running; use the PurrOS backup files instead.

To restore, stop the API container (not the database), then run the restore in a one-off container that uses the API's image, volumes and network:

```bash
API=purros-abc123-api-1                                   # from docker ps
IMAGE=$(docker inspect -f '{{.Config.Image}}' $API)
NET=$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' $API | tr ' ' '\n' | grep -v -e '^dokploy-network$' -e '^$' | head -1)   # the app's own network, where "db" resolves
docker stop $API
docker run --rm -it --volumes-from $API --network $NET \
  -e DATABASE_URL="postgres://purros:<POSTGRES_PASSWORD>@db:5432/purros?sslmode=disable" \
  -e PURROS_SECRET="<PURROS_SECRET>" -e PURROS_STATE_DIR=/var/lib/purros \
  $IMAGE backup restore /var/lib/purros/backups/<file>.purros-backup --replace
docker start $API
```

Add `-e PURROS_BACKUP_PASSPHRASE=…` for encrypted backups. See [Backups & upgrades](../operations/backups-and-upgrades.md#restoring) for what `restore` checks and does.

## Upgrading

Change the branch or tag in **General** (or push to the tracked branch with auto-deploy on), then **Deploy**. Take a backup first (`docker exec -it <container> purros backup create`). Migrations run when the new container starts; check `purros doctor` afterwards.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Deploy fails with `set POSTGRES_PASSWORD in the Environment tab` | Add `POSTGRES_PASSWORD` in **Environment** and deploy again |
| Deploy fails with `env file .env not found` | Save the variables in the **Environment** tab, then deploy again |
| The API restarts with `PURROS_SECRET must be at least 32 characters` | Set a real `PURROS_SECRET` |
| `404` or a certificate error on the domain | Check the domain's service (`api`) and port (`8080`), and that DNS points at the server |
| Sign-in links point to the wrong address | `PURROS_URL` must match the domain exactly |
| `password authentication failed` after changing `POSTGRES_PASSWORD` | The database keeps the password it was created with. Change it back, or change it inside PostgreSQL too (`ALTER USER purros PASSWORD '…'`) |
