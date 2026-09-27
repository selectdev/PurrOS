# CLI reference

PurrOS includes a command-line tool for setup and maintenance. Run it inside the `app` container:

```bash
docker compose exec app npm run purros -- <command> [options]
```

In local development: `pnpm purros <command>`.

## Commands

### `setup`

Creates the first Owner account and prints a one-time sign-in link. Only works when no Owner exists.

```bash
npm run purros -- setup --email owner@example.com
```

### `doctor`

Checks the installation and prints a report: database and Redis connectivity, pending migrations, `PURROS_SECRET` strength, email configuration, queue health, stock ledger integrity, and data sources that have gone quiet.

```bash
npm run purros -- doctor [--fix-ledger-projection]
```

`--fix-ledger-projection` rebuilds stock levels from the stock ledger if drift was found. The ledger itself is never changed.

### `owner:reset`

Recovery when every Owner is locked out. It prints a one-time sign-in link for an existing Owner and removes their 2FA and passkeys, and the action is recorded in the audit log.

```bash
npm run purros -- owner:reset --email owner@example.com
```

### `features`

```bash
npm run purros -- features list
npm run purros -- features enable <key>
npm run purros -- features disable <key>
npm run purros -- features purge <key> --confirm <key>
```

`purge` permanently deletes a **disabled** feature's data after checking that an export was taken. See [Feature switches](../admin/feature-switches.md).

### `export`

Exports all company data (or one feature's data) as JSON and CSV files in a ZIP, for migration or archiving.

```bash
npm run purros -- export --out /data/exports/full-2026-09-27.zip [--feature inventory]
```

### `recalculate`

Rebuilds derived data from raw records, for example after changing usage recipes or fixing unmapped items in bulk.

```bash
npm run purros -- recalculate usage --location loc_01H… --from 2026-09-01 --to 2026-09-27
npm run purros -- recalculate kpis --from 2026-09-01
```

### `generate-vapid`

Generates the key pair for web push notifications. Put the output in `.env`.

### `api-keys`

```bash
npm run purros -- api-keys list
npm run purros -- api-keys revoke <keyId>
```

Emergency revocation without the UI.

### `anonymize`

Anonymizes a former employee after your retention period (see [Security](security.md#privacy-and-personal-data)).

```bash
npm run purros -- anonymize --employee emp_01H… --confirm
```

All commands that change data are recorded in the audit log as the `system` actor, with the command used.
