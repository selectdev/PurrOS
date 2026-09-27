# Backups & upgrades

## What to back up

| Data | Where | Back up? |
|---|---|---|
| Database | PostgreSQL (`db` service or managed Postgres) | **Yes, daily at least** |
| Attachments | The `files` Docker volume, or your S3 bucket | **Yes** |
| `.env` (especially `PURROS_SECRET`) | Your server | **Yes, stored separately and securely** |
| Redis | `redis` service | No. It only holds queues and cache, which are rebuilt from the database. |

Without `PURROS_SECRET`, encrypted fields in a restored database (integration secrets, sensitive employee data, webhook secrets) can't be read.

## Database backups

A nightly logical backup with `pg_dump`:

```bash
#!/usr/bin/env bash
# /opt/purros/backup.sh — run nightly from cron: 15 2 * * * /opt/purros/backup.sh
set -euo pipefail
cd /opt/purros/PurrOS
STAMP=$(date +%F)
docker compose exec -T db pg_dump -U purros -Fc purros > /backups/purros-$STAMP.dump
find /backups -name 'purros-*.dump' -mtime +30 -delete
```

Copy backups off the server (another region, object storage, or a backup service). For large installs or point-in-time recovery, use WAL archiving (for example pgBackRest or WAL-G) or your managed database's snapshots.

## Attachment backups

- **Local volume:** back up the volume directory with your usual tools (restic, borg, rsync).
- **S3-compatible storage:** turn on bucket versioning and replication.

## Restoring

```bash
docker compose stop app worker
docker compose exec -T db pg_restore -U purros -d purros --clean --if-exists < /backups/purros-2026-09-27.dump
# restore the attachments volume or bucket to the same point in time
docker compose start app worker
docker compose exec app npm run purros -- doctor
```

`purros doctor` checks connectivity, migrations and stock ledger integrity after a restore.

**Test a restore** on a spare machine at least every few months.

## Upgrading

PurrOS uses semantic versioning:

- **Patch releases** (1.2.3 → 1.2.4): fixes only.
- **Minor releases** (1.2 → 1.3): new features, no breaking API changes. Migrations run with the app online.
- **Major releases** (1.x → 2.0): may change the API or need downtime. Read the upgrade notes first.

Steps:

```bash
# 1. Back up (see above)
# 2. Read the release notes for anything marked "Action required"
git fetch --tags
git checkout <new release tag>
docker compose pull
docker compose up -d
docker compose exec app npx prisma migrate deploy
docker compose exec app npm run purros -- doctor
```

- Migrations are forward-only and designed to run while PurrOS is live. Any exception is flagged in the release notes.
- **Don't skip major versions.** Upgrade 1.x → 2.x → 3.x in order.
- Downgrading means restoring the backup taken before the upgrade.

## Before an upgrade, check

- [ ] Backup finished and copied off the server
- [ ] Release notes read
- [ ] Integrations don't use anything listed as deprecated or removed
- [ ] A quiet time chosen (few users, and after the nightly jobs)
