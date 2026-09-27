# Development guide

How to work on PurrOS itself. For the architecture and the reasons behind it, read [DESIGN.md](../../DESIGN.md) first.

## Local setup

Requirements: Node.js 22 LTS, pnpm 9, Docker.

```bash
git clone https://github.com/selectdev/PurrOS.git
cd PurrOS
pnpm install
docker compose -f docker-compose.dev.yml up -d   # PostgreSQL + Redis only
cp .env.example .env
pnpm prisma migrate dev
pnpm db:seed          # demo company: 3 locations, employees, items, a week of sales
pnpm dev              # http://localhost:3000
pnpm worker:dev       # background worker, in a second terminal
```

The seed prints demo sign-in links for an Owner, a district manager, a store manager and an employee, so you can see each view.

## Scripts

| Command | What it does |
|---|---|
| `pnpm dev` / `pnpm worker:dev` | Run the app and worker with reload |
| `pnpm lint` | ESLint + Prettier check |
| `pnpm typecheck` | `tsc --noEmit` |
| `pnpm test` | Unit and service tests (Vitest, with a real Postgres from Docker) |
| `pnpm test:e2e` | End-to-end tests (Playwright) |
| `pnpm openapi:check` | Fails if the API changed in a breaking way |
| `pnpm prisma studio` | Browse the database |
| `pnpm purros <cmd>` | The [CLI](../operations/cli.md) |

Before opening a pull request: `pnpm lint && pnpm typecheck && pnpm test && pnpm openapi:check`.

## Repository layout

```
prisma/                 schema, migrations, seed
src/
  app/                  Next.js routes: (dashboard), (employee), (kiosk), (display), (auth), api/v1
  modules/<feature>/    domain logic, one folder per feature
  lib/                  db, auth, api helpers, redis, queue
  components/ui/        design-system components
  worker/               background worker entrypoint and jobs
packages/
  sdk/                  @purros/sdk
  integration-template/ starter for integrations
examples/integrations/  small reference integrations
docs/                   this documentation
```

## Rules to follow

1. **Only domain services write to the database.** Routes and Server Actions validate input, check auth and call a service.
2. **Every feature is optional.** New code belongs to a feature in the registry, and every entry point checks it. Never assume another feature is on. Check `isEnabled()` and degrade gracefully.
3. **API first.** Anything the UI can do must be possible through `/api/v1`, with Zod schemas that generate the OpenAPI spec.
4. **No breaking API changes** in v1. Add fields, don't rename or remove them.
5. **Side effects go through the outbox.** Never call a webhook or external service inside a request.
6. **Money and quantities are decimals**, never floats. Stock changes are ledger entries, never updates.
7. **Permission checks in services**, with `can()` and `scopeWhere()`, not only in the UI.
8. **Nothing vendor-specific in core.** Connections to specific products belong in integrations.

## Adding a feature module

1. Create `src/modules/<feature>/` with:
   - `<feature>.feature.ts`: `defineFeature({ key, parent, dependsOn, nav, permissions, routes, events, jobs, kpis, widgets, employeeAreaSections, displayTiles, scopes })`
   - `<feature>.schemas.ts`: Zod input and output schemas
   - `<feature>.service.ts`: domain logic, starting each public method with `ctx.requireFeature(key)`
   - `<feature>.events.ts`: event types and payload schemas
   - `<feature>.test.ts`
2. Add Prisma models and a migration. Follow the conventions: prefixed IDs, `externalId` where syncable, `createdAt`/`updatedAt`, `Decimal` for money and quantities.
3. Add API routes under `src/app/api/v1/…`, wrapped with `withFeature(key)`.
4. Add UI routes under `src/app/(dashboard)/<feature>/`, using the design-system components.
5. Register permissions with clear descriptions. They appear in the role editor automatically.
6. Document it in `docs/guides/`, and add endpoints to `docs/api/endpoints.md` and events to `docs/api/webhooks.md`.
7. Add tests that show the feature is **unreachable when disabled** (UI, API, events, jobs).

## Tests

| Kind | Where | Notes |
|---|---|---|
| Unit | Next to the code | Pure logic: labor rules, forecasting, costing, unit conversion |
| Service | `*.test.ts` | Real Postgres in Docker, with each test in a transaction that's rolled back |
| API contract | `tests/api/` | Responses validated against the OpenAPI spec |
| E2E | `tests/e2e/` | Playwright: onboarding → punches → approval → export; PO → receive → stock; POS sale → usage → variance |

## Documentation

Docs live in `docs/` as Markdown. Keep them in the same pull request as the change. Write for the reader of each section (employees, managers, admins, integrators), use plain language, and don't document behavior that doesn't exist yet without marking it as planned.

## Commit and PR conventions

- Small, focused pull requests with a clear description of what and why.
- Link the issue being fixed.
- Include screenshots for UI changes.
- Contributions are accepted under the project's AGPL-3.0 license.
