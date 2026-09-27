# CLI reference

The `purros` binary is both the API server and the command-line tool for setup and maintenance. In Docker, run it inside the `api` container:

```bash
docker compose exec api purros <command> [options]
```

In local development: `go run ./cmd/purros <command>` in `api/`. `purros help` lists every command.

> **Available today:** `serve`, `worker`, `migrate`, `doctor`, `setup`, `locations create|list`, `integrations register|list`, `api-keys revoke`, `features list|enable|disable` and `version`. Commands further down this page are planned.

## Server commands

### `serve`

Runs the REST API. Pending migrations are applied first (safe with several instances starting at once), and the background worker runs in the same process unless `PURROS_RUN_WORKER=false`.

```bash
purros serve [--no-migrate]
```

### `worker`

Runs only the background worker (webhook delivery and other jobs). Use it to scale job processing separately from the API.

### `migrate`

Applies pending database migrations and exits.

### `doctor`

Checks the installation: configuration (including `PURROS_SECRET` strength), database connectivity, pending migrations, company setup, events waiting in the outbox (is the worker running?) and webhook endpoints disabled after repeated failures. Exits non-zero if a check fails.

```bash
purros doctor
```

## Setup commands

### `setup`

Creates the company, the system roles (Owner and Employee) and the first Owner, and prints a one-time link (valid 7 days) for the Owner to set their password. Only works once.

```bash
purros setup --company "Acme Coffee" --owner-email owner@example.com \
  [--owner-name "Alex Kim"] [--currency USD] [--timezone America/Chicago]
```

### `users sign-in-link`

Prints a one-time link for an account: an invitation for someone who never signed in, otherwise a password reset (valid 1 hour). Use it when email isn't set up, or to recover access when every Owner is locked out. `--reset-mfa` also removes their authenticator app and recovery codes. The action is recorded in the audit log.

```bash
purros users sign-in-link --email owner@example.com [--reset-mfa]
```

### `email test`

Sends a test email through the configured SMTP server and prints the error if it fails.

```bash
purros email test --to you@example.com
```

### `locations`

Until the web app ships, locations are created from the CLI. Set `--external-id` to the ID your POS or other systems use for the location, so integrations can send `locationExternalId`.

```bash
purros locations create --name "Store 101" --external-id 101 \
  --timezone America/Chicago --currency USD --cutoff 04:00
purros locations list
```

`--cutoff` is the time the business day ends. Sales before it count toward the previous day.

## Integration commands

### `integrations register`

Registers an integration from its [manifest](../integrations/README.md#the-manifest), validates its scopes and webhook events against the enabled features, and prints its API key and webhook secret **once**.

```bash
purros integrations register --manifest purros-integration.json \
  --config posToken=abc123 [--config other=value] [--approve-sensitive]
```

`--approve-sensitive` is the Owner's approval for the `people:sensitive` scope.

### `integrations list`

Shows each integration's status, health, last heartbeat, active keys and scopes.

### `api-keys revoke`

```bash
purros api-keys revoke <keyId>
```

Revokes a key immediately. Key IDs are shown by `integrations list`.

## Feature commands

### `features`

```bash
purros features list
purros features enable <key>     # also switches on what it needs
purros features disable <key>    # dependents are off until it's switched back on
```

Changes take effect immediately in every running API and worker, and are recorded in the audit log. See [Feature switches](../admin/feature-switches.md).

## Planned commands

### `features purge`

```bash
purros features purge <key> --confirm <key>
```

Permanently deletes a **disabled** feature's data after checking that an export was taken.

### `export`

Exports all company data (or one feature's data) as JSON and CSV files in a ZIP, for migration or archiving.

```bash
purros export --out /data/exports/full-2026-09-27.zip [--feature inventory]
```

### `recalculate`

Rebuilds derived data from raw records, for example after changing usage recipes or fixing unmapped items in bulk.

```bash
purros recalculate usage --location loc_01H… --from 2026-09-01 --to 2026-09-27
purros recalculate kpis --from 2026-09-01
```

### `storage`

```bash
purros storage test                 # write, read, sign and delete a test file
purros storage init                 # create the bucket if it doesn't exist
purros storage migrate --to s3      # copy all files from local disk to S3 (resumable)
purros storage verify               # check every file record has a matching file and checksum
```

See [Email & file storage](../getting-started/email-and-storage.md).

### `backup`

```bash
purros backup run                           # back up the database to the backup bucket now
purros backup list
purros backup restore --from s3 --date 2026-09-27
```

Needs the `BACKUP_S3_*` settings. `restore` stops if the app and worker are still running, and asks for the encryption passphrase if backups are encrypted.

### `generate-vapid`

Generates the key pair for web push notifications. Put the output in `.env`.

### `api-keys list`

Lists every API key with its owner, last use and expiry (`api-keys revoke` is available today).

### `anonymize`

Anonymizes a former employee after your retention period (see [Security](security.md#privacy-and-personal-data)).

```bash
purros anonymize --employee emp_01H… --confirm
```

All commands that change data are recorded in the audit log as the `system` actor, with the command used.
