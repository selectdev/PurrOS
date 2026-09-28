# CLI reference

The `purros` binary is the API server, the background worker and the admin tool for setting up, recovering and backing up an install. In Docker, run it inside the `api` container:

```bash
docker compose exec api purros <command> [options]
```

Outside Docker, configuration comes from the environment and from `config/purros.env` (or `$PURROS_CONFIG_DIR/purros.env`) when that file exists. Use `--env-file <path>` (or `PURROS_ENV_FILE`) to load a different file. In local development: `go run ./cmd/purros <command>` in `api/`.

`purros help` lists every command, and `purros <command> --help` explains one with examples. Shell completion: `purros completion bash|zsh|fish|powershell`.

## Global options

| Option | Effect |
|---|---|
| `--env-file <path>` | Load environment variables from this file instead of `$PURROS_CONFIG_DIR/purros.env`. Variables already set in the environment win. |
| `--json` | Machine-readable output, for scripts and monitoring |
| `-y`, `--yes` | Answer yes to confirmations (for scripts) |
| `--no-input` | Never prompt; fail when something is missing |

Commands prompt only when run in a terminal. Destructive commands ask for confirmation. Restoring over existing data asks you to type the company name.

Every command that changes data is recorded in the audit log as the `system` actor named `cli`.

## Server

| Command | What it does |
|---|---|
| `serve [--no-migrate]` | Runs the API, and the background worker unless `PURROS_RUN_WORKER=false`. Applies migrations on start. |
| `worker` | Runs only the worker: webhooks, email, KPI alerts and scheduled backups. Run several for heavy loads. |
| `migrate [--dry-run]` | Applies pending migrations (or lists them with `--dry-run`). |
| `migrate status` | Every migration and when it was applied |
| `status` | An overview: version, schema, accounts, queues, email, backup schedule and the last backup |
| `doctor` | Checks the installation and exits with code 1 when something is wrong (see below) |
| `version` | Prints the version |

### `doctor`

`doctor` runs these checks:

| Check | What it tests |
|---|---|
| Configuration | Required values are present |
| Database | Connection and migrations |
| Setup | The company exists, and an Owner can sign in |
| Encryption secret | `PURROS_SECRET` decrypts the stored secrets |
| Email | The SMTP server is reachable and offers STARTTLS |
| Queues | Stuck events, disabled webhook endpoints, failed emails |
| Stock ledger | Every stock level matches the sum of its movements |
| Time zones | Every location has a valid time zone |
| Backups | A backup succeeded recently, and scheduled backups are on |

Warnings (`!`) don't fail the run.

## Setup & configuration

### `init`

Writes `purros.env` (with a new `PURROS_SECRET`) and `postgres.env` (with a new database password) into the configuration directory, `./config` by default (`$PURROS_CONFIG_DIR`). Both are readable only by you. `--force` rewrites existing files, but always keeps their `PURROS_SECRET` and passwords.

```bash
purros init --url https://erp.example.com [--dir /opt/purros]
```

### `setup`

Creates the company, the system roles, the first Owner and, optionally, the first location. It then prints a one-time link (valid 7 days) for the Owner to set a password. It only works once.

In a terminal it asks for everything, with suggestions such as the server's time zone. For scripted installs, pass every value as flags:

```bash
purros setup --company "Acme Coffee" --owner-email owner@example.com --owner-name "Alex Kim" \
  --timezone America/Chicago --currency USD \
  --location-name "Store 101" --location-external-id 101 --location-cutoff 04:00
```

### `config`

```bash
purros config show      # the effective configuration, secrets masked
purros config check     # validate it
```

### `secret`

```bash
purros secret generate                          # a new random secret
purros secret check                             # does PURROS_SECRET decrypt the stored data?
purros secret rotate --new-secret-file new      # re-encrypt everything with a new secret
```

Use `rotate` if `PURROS_SECRET` may have leaked:

1. Stop PurrOS.
2. Run the rotation. It re-encrypts integration settings, webhook secrets and authenticator secrets in one transaction, and aborts without changes if any value can't be decrypted.
3. Put the new secret in `config/purros.env` and start PurrOS again.

Keep the old secret as long as you keep backups made before the rotation.

### `storage`

```bash
purros storage test [--backups]            # write, read and delete a test file (--backups: the backup bucket)
purros storage init [--backups]            # create the S3 bucket if it doesn't exist
purros storage verify [--checksums]        # every attachment's file is in storage (and matches, with --checksums)
purros storage migrate --to s3|local       # copy all files to the other storage; resumable
```

### `email`

```bash
purros email test --to you@example.com     # send a test email, showing the SMTP error if it fails
purros email log [--limit 30]              # recent emails: recipient, kind, status and errors
```

## Accounts & recovery

All account commands take the email address as an argument (or `--email`).

| Command | What it does |
|---|---|
| `users list [--status …] [--role …]` | Accounts with role, status, 2FA, lockout and last sign-in |
| `users show <email>` | One account, with its sessions and personal keys |
| `users invite <email> [--name] [--role] [--employee-id]` | Creates an account and prints its invitation link |
| `users sign-in-link <email> [--reset-mfa]` | Prints a one-time link: an invitation, or a password reset. Also clears a lockout. |
| `users unlock <email>` | Clears a lockout after failed sign-ins |
| `users reset-mfa <email>` | Removes their authenticator app and recovery codes, and signs them out |
| `users sign-out <email>` | Ends all their sessions |
| `users set-role <email> --role <name or ID>` | Changes their role. `--role owner` makes them an Owner. |
| `users deactivate <email>`, `users reactivate <email>` | Deactivating signs them out and stops their personal keys |
| `roles list` | Roles with members and permissions |

The last Owner can't be demoted or deactivated.

### `recover owner`

This is break-glass access for when nobody can sign in. It makes the account an Owner, activates it, clears its lockout, removes its 2FA (unless `--keep-mfa`) and sessions, and prints a sign-in link.

```bash
purros recover owner owner@example.com
purros recover owner new-admin@example.com --create --name "Pat Lee"
```

## Backups

Backups are complete and consistent, and taken while PurrOS keeps running. PurrOS makes them itself, so no `pg_dump` is needed. Each is a single `.purros-backup` file, optionally encrypted, and optionally including uploaded files. With `PURROS_BACKUP_S3_ENABLED`, backups are also uploaded to an S3 bucket. See [Backups & upgrades](backups-and-upgrades.md) for how they work and how to schedule them.

```bash
purros backup create [--dir DIR | --out FILE] [--encrypt] [--passphrase-file F] [--keep N]
                     [--files | --no-files] [--no-upload]
purros backup list [--dir DIR | --remote]          # files (or the S3 bucket), and recent backup runs
purros backup download <name> [--out PATH]         # copy a backup from the S3 bucket
purros backup verify <file | s3:name>              # complete and undamaged?
purros backup inspect <file | s3:name>             # what it contains, and whether PURROS_SECRET matches
purros backup restore <file | s3:name> [--replace] [--no-safety-backup]
purros backup prune [--keep N] [--older-than 30d] [--remote] [--dry-run]
```

- **Where backups go:** the default directory is `PURROS_BACKUP_DIR`, or `$PURROS_STATE_DIR/backups` when S3 uploads are off. With S3 on and no directory, backups go only to the bucket.
- **Remote backups:** refer to a backup in the bucket as `s3:<name>`.
- **Passphrase:** it comes from `--passphrase-file`, then `PURROS_BACKUP_PASSPHRASE`, then a prompt.

## Management

Integrations, webhook endpoints and the organization can also be managed through the API; see [Integrations](../integrations/README.md#managing-integrations), [Webhooks](../api/webhooks.md) and [Organization & locations](../admin/organization-and-locations.md#api).

| Command | What it does |
|---|---|
| `locations create --name … [--code] [--external-id] [--timezone] [--currency] [--cutoff]` | Adds a location (and sends `location.created`). `--cutoff` is when its business day ends; time zone and currency default to the company's. Org units and departments are managed with the [API](../admin/organization-and-locations.md#api). |
| `locations list` | Lists locations |
| `integrations register --manifest … [--config k=v] [--approve-sensitive]` | Registers an integration and prints its API key and webhook secret **once**. `--config` values are converted to each field's type. |
| `integrations update <name> --manifest … [--config k=v] [--approve-sensitive]` | Installs a new version of its manifest (scopes, config fields, webhooks). Prints a webhook secret only if the update adds an endpoint. |
| `integrations list` | Status, health, last heartbeat, keys and scopes |
| `integrations pause <name>`, `integrations resume <name>` | Pausing makes its keys stop working and holds its webhooks; resuming sends them |
| `integrations rotate-key <name> [--grace 1h]` | Issues a new API key (printed once). The old keys keep working for `--grace` (`0` revokes them now, at most `168h`). |
| `integrations remove <name>` | Removes it with its keys, config, logs and webhook endpoint (asks to confirm) |
| `webhooks list` | Every webhook endpoint with its integration, status, failure count, and pending and failed deliveries |
| `webhooks enable <endpointId>`, `webhooks disable <endpointId>` | Re-enabling resets the failure count and sends waiting deliveries |
| `webhooks retry-failed <endpointId>` | Queues every failed delivery again, e.g. after a long outage |
| `api-keys list`, `api-keys revoke <keyId>` | Active integration and personal keys |
| `features list`, `features enable <key>`, `features disable <key>` | Switches features on or off, together with what they need or what depends on them |

## Planned commands

| Command | Purpose |
|---|---|
| `features purge <key>` | Permanently delete a disabled feature's data |
| `export` | Export all company data as JSON and CSV |
| `recalculate` | Rebuild derived data, e.g. after changing usage recipes |
| `generate-vapid` | Keys for web push notifications |
| `anonymize --employee …` | Anonymize a former employee after the retention period |
