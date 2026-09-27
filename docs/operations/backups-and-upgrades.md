# Backups & upgrades

## What to back up

| Data | Where | Back up? |
|---|---|---|
| Database | PostgreSQL (`db` service or managed Postgres) | **Yes, daily at least.** PurrOS does this itself (see below). |
| Attachments | The `files` Docker volume, or your S3 bucket | **Yes.** With local storage, PurrOS backups include them. |
| `.env`, especially `PURROS_SECRET` | Your server | **Yes, stored separately and securely** |
| Redis | `redis` service | No. It only holds rate-limit counters. |

Without `PURROS_SECRET`, encrypted values in a restored database can't be read. These are integration settings, webhook secrets and authenticator-app secrets. `purros backup inspect` shows whether a backup matches the current secret.

## Built-in backups

PurrOS makes its own database backups, so neither `pg_dump` nor an external tool is needed.

- **Consistent while running.** Every table is read in a single snapshot, so the backup matches one moment even while people keep working.
- **One file.** Each backup is a compressed `.purros-backup` file. It contains every table and a manifest with the PurrOS and schema version, row counts and a SHA-256 checksum per table.
- **Uploaded files included.** With local file storage, backups also contain every [attachment](../api/attachments.md) (`PURROS_BACKUP_FILES=auto`; set `true` to include them from S3 storage too, or `false` to leave them out). Restoring puts them back into the configured storage.
- **Off-site copies.** With `PURROS_BACKUP_S3_ENABLED`, every backup is also uploaded to an S3-compatible bucket, keeping `PURROS_BACKUP_S3_KEEP` there. See [Database backups to S3](../getting-started/email-and-storage.md#database-backups-to-s3-optional).
- **Optional encryption.** Backups can be encrypted with a passphrase. The key is derived with Argon2id and the data is sealed with AES-256-GCM, so a damaged, edited or cut-off file is detected rather than restored.
- **Files are private.** They are written with permissions `600`, and appear only once complete.

### On demand

```bash
docker compose exec api purros backup create
docker compose exec api purros backup create --encrypt          # asks for a passphrase
docker compose exec api purros backup list
```

### Scheduled

Set `PURROS_BACKUP_DIR`, `PURROS_BACKUP_S3_ENABLED` or both, and the worker makes one backup a day and deletes old ones:

| Variable | Default | Description |
|---|---|---|
| `PURROS_BACKUP_DIR` | *(off)*; `/backups` in `docker-compose.yml` | Where backups are written |
| `PURROS_BACKUP_HOUR` | `2` | Hour of the day (UTC) after which the daily backup runs |
| `PURROS_BACKUP_KEEP` | `14` | How many backups to keep. The newest is never deleted. |
| `PURROS_BACKUP_PASSPHRASE` | | Encrypt backups with this passphrase. **Store it with `PURROS_SECRET`.** |
| `PURROS_BACKUP_FILES` | `auto` | Include uploaded files: `auto` (with local storage), `true`, `false` |
| `PURROS_BACKUP_S3_ENABLED`, `PURROS_BACKUP_S3_*` | `false` | Also upload to an S3 bucket ([settings](../getting-started/email-and-storage.md#database-backups-to-s3-optional)). Without `PURROS_BACKUP_DIR`, backups are only kept in the bucket. |
| `PURROS_BACKUP_S3_KEEP` | `PURROS_BACKUP_KEEP` | How many backups to keep in the bucket |

When several workers run, only one makes the backup. `purros status` shows the schedule and the last backup, and `purros doctor` fails if scheduled backups haven't succeeded for 36 hours.

The Compose file keeps backups in the `backups` volume. **Keep a copy off the server**: turn on S3 uploads, or copy the volume with your usual tools, for example:

```bash
docker compose cp api:/backups ./purros-backups
```

For very large installs or point-in-time recovery, add WAL archiving (for example pgBackRest or WAL-G) or your managed database's snapshots.

## Attachment backups

- **Local volume:** back up the volume directory with your usual tools (restic, borg, rsync).
- **S3-compatible storage:** turn on bucket versioning, and replicate to a second region or provider.

## Restoring

```bash
docker compose stop api
docker compose run --rm api backup verify /backups/purros-20260927-020012-scheduled.purros-backup
docker compose run --rm api backup restore /backups/purros-20260927-020012-scheduled.purros-backup --replace
# or straight from the S3 backup bucket:
# docker compose run --rm api backup list --remote
# docker compose run --rm api backup restore s3:purros-20260927-020012-scheduled.purros-backup --replace
docker compose start api
docker compose exec api purros doctor
```

What `restore` does:

1. It verifies the **whole** backup (every checksum) before changing anything.
2. It refuses a backup from a newer PurrOS: update PurrOS first.
3. It warns if the backup was made with a different `PURROS_SECRET`, or if a PurrOS server or worker is still connected.
4. When the database already has data, it needs `--replace` and asks you to type the company name. It then saves a **safety backup** of the current data first (skip with `--no-safety-backup`).
5. It rebuilds the schema at the backup's version, and loads every table with all references re-checked.
6. It migrates to the current version. This means a backup from an older PurrOS can be restored into a newer one.

Backups that include uploaded files put them back into the configured storage. Otherwise, restore the files volume or bucket to the same point in time.

**Test a restore** on a spare machine at least every few months: `purros backup restore` into an empty database, then `purros doctor`.

## Upgrading

Upgrades are done by hand. PurrOS doesn't download or install new versions itself. It uses semantic versioning:

- **Patch releases** (1.2.3 → 1.2.4): fixes only.
- **Minor releases** (1.2 → 1.3): new features, no breaking API changes. Migrations run with the app online.
- **Major releases** (1.x → 2.0): may change the API or need downtime. Read the upgrade notes first.

Steps:

```bash
# 1. Back up, and copy the backup off the server
docker compose exec api purros backup create
# 2. Read the release notes for anything marked "Action required"
# 3. Get the new version and start it (migrations run automatically on start)
docker compose up -d --build
# 4. Check
docker compose exec api purros migrate status
docker compose exec api purros doctor
```

Without Docker: take a backup, replace the `purros` binary, run `purros migrate`, restart the service, then run `purros doctor`.

- Migrations are forward-only and designed to run while PurrOS is live. Any exception is flagged in the release notes.
- **Don't skip major versions.** Upgrade 1.x → 2.x → 3.x in order.
- **Downgrading** means going back to the previous version and restoring the backup taken before the upgrade.

## Before an upgrade, check

- [ ] Backup finished and copied off the server
- [ ] Release notes read
- [ ] Integrations don't use anything listed as deprecated or removed
- [ ] A quiet time chosen (few users, and after the nightly jobs)
