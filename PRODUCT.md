# PurrOS: Product

This document explains what PurrOS is, who it is for, what it does and does not do, and how we decide what gets built. When a feature request comes in, this is the document to check it against.

---

## 1. Vision

**Give growing businesses an ERP they can own, understand, and connect to anything.**

A company of 20–500 people usually runs on an HR platform, a timeclock, an inventory tool, a spreadsheet for purchasing, and someone who copies data between them. PurrOS becomes the system of record in the middle: one database for people, time, stock and orders, with an API that other tools can connect to without a consultant.

## 2. Problem

| Pain | Today | With PurrOS |
|---|---|---|
| Data lives in 4–6 disconnected tools | Manual CSV exports, re-keying, errors | One system of record, synced through the API and webhooks |
| Enterprise ERPs are too heavy | 6–12 month rollouts, per-seat licensing, consultants | Deployed in a day with Docker, no per-seat cost |
| Timeclock data doesn't reach payroll cleanly | Punches exported and fixed up by hand every pay period | Punches flow in live, timesheets get approved, payroll export takes one click |
| Stock levels are never quite right | Inventory updated after the fact, if at all | Receipts, sales and adjustments update stock in real time |
| Vendor lock-in and data residency | Data sits in someone else's cloud | Self-hosted, open source, with the full database under your control |

## 3. Target users

**Primary market:** small and mid-sized businesses with **20–500 employees** that handle physical goods and hourly staff, for example light manufacturing, distribution, wholesale, multi-location retail, and field services.

### Personas

**Operations Manager (primary buyer)**
Owns "how the business runs." Wants one place to see who is working, what is in stock, and what has been ordered. Judges success by fewer errors and less time spent reconciling.

**HR / Payroll Administrator**
Keeps employee records, reviews timesheets, and runs payroll through an external provider. Needs clean, approved hours exported on time.

**Warehouse / Inventory Lead**
Receives goods, moves stock, and runs cycle counts. Needs fast screens that work on a tablet or scanner, and accurate numbers.

**Line Manager**
Approves their team's timesheets and purchase requests. Uses PurrOS a few minutes a day and needs things to be obvious.

**Employee**
Hourly or salaried staff, often on a phone rather than a desk computer. Wants to check that their hours are right, see what they should be paid, and request time off without chasing a manager or HR.

**Integrator / IT Generalist (key adopter)**
Often a single in-house developer or an outside contractor. Deploys PurrOS and connects it to the existing HR system, timeclock hardware, and e-commerce or scanner apps. Whether the API is pleasant to use decides whether PurrOS gets adopted.

## 4. Product principles

1. **The API comes first.** Every feature is built as an API endpoint before it gets a UI. If the API can't do it, it isn't done.
2. **Integrate, don't replace.** PurrOS does not try to be the best HRIS, payroll engine or e-commerce platform. It connects to them and stays the source of truth for the data in the middle.
3. **Boring and correct over clever.** Stock quantities, hours and money must always be right. Prefer explicit ledgers, immutable history and audit logs.
4. **Self-hosting is a first-class path.** Installs, upgrades and backups have to work for one IT person with Docker. There is no hosted-only feature.
5. **Mid-range means saying no.** A feature that would help 5% of users and complicate things for the other 95% belongs in an integration, not in core.
6. **Calm, professional UI.** Dense where power users need it and plain where occasional users need it. No gamification and no clutter.

## 5. Scope: v1

### 5.1 People (Employee Management)
- Employee profiles: personal info, contact, employment type (full-time, part-time, contractor), status (active, on leave, terminated), start and end dates
- Organisation structure: departments, positions, reporting lines, locations
- Custom fields per company
- Document attachments (contracts, certifications) with expiry reminders
- **Sync from external HR/employment software** by `externalId`, both one-way and bidirectional

### 5.2 Time & Attendance
- Punch ingestion from any timeclock (API, CSV import, or a custom integration)
- Shifts and schedules (basic weekly roster, not full workforce optimisation)
- Automatic timesheet generation from punches, with detection of missed punches and exceptions
- Configurable rules: rounding, breaks, daily and weekly overtime thresholds
- Two-step approval: manager, then payroll admin
- Lock pay periods, then export to payroll (CSV templates plus API)
- Paid time off tracking (balances and requests, simple accrual)

### 5.3 Inventory & Warehouse
- Items and SKUs, variants, units of measure with conversions, barcodes
- Multiple warehouses with bin locations
- Stock ledger: every movement (receipt, shipment, transfer, adjustment, count) is an immutable entry, and on-hand quantity is derived from the ledger
- Reserved and available quantities (reserved by sales orders)
- Reorder points and low-stock alerts
- Cycle counts and full stock takes
- Valuation: weighted average cost in v1

### 5.4 Purchasing
- Suppliers and supplier price lists
- Purchase requests, then purchase orders, with approval thresholds
- Goods receipts (partial and full) that post to the stock ledger
- PO status tracking and supplier performance basics

### 5.5 Sales
- Customers and price lists
- Quotes, then sales orders, with stock reservation
- Pick, pack and ship, which posts to the stock ledger
- Basic invoicing (PDF) and payment status, exportable to accounting software

### 5.6 Employee Area (self-service)
- Every employee signs in to see all data PurrOS holds about them: punches, timesheets, pay rate and history, estimated gross pay per period, payslips (when a payroll integration provides them), time-off balances, profile, documents and change history
- Request punch corrections and time off, with approvals and audit
- Update own contact details directly. Sensitive fields go to HR for approval
- Export all personal data (JSON/CSV)
- Works on phones as well as desktops. Employees see only their own data

### 5.7 Platform
- Organization-defined roles (e.g. HR, District Manager, Supervisor): each account has one role, and each role has its own set of permissions, each limited to own team, assigned locations, assigned departments or everyone
- API keys with scopes, and signed webhooks with retries
- Full audit log (who changed what, when, and from UI or API)
- CSV import and export for every core entity
- Single company per install in v1, multi-currency display, and configurable timezone and locale
- One account system for everyone: email + password, magic link, passkeys, and SSO via OIDC or SAML (Google Workspace, Microsoft Entra ID, Okta, Keycloak, Authentik, etc.)
- Optional 2FA (TOTP or passkey) that an Owner can make mandatory

## 6. Non-goals (v1)

These are left out on purpose. Integrate with a dedicated tool instead.

- **Payroll calculation and tax filing.** We export approved hours to payroll providers.
- **Full general ledger / accounting.** We export invoices and bills to accounting software.
- **Recruiting / ATS, performance reviews, learning management**
- **Manufacturing (BOMs, MRP, work orders).** Candidate for v2.
- **CRM and marketing automation**
- **E-commerce storefront.** We integrate with storefronts instead.
- **Multi-company / multi-tenant hosting in one install**
- **Native mobile apps.** The web UI is responsive and works on tablets and scanners.

## 7. Integrations

The integration story is what sets PurrOS apart. **PurrOS ships no built-in integrations with third-party products.** Every integration is **custom code**, written by the business, its integrator or the community, that talks to PurrOS only through the public API.

What PurrOS provides:

1. **REST API + webhooks**: covers the whole product, with a published OpenAPI spec. This is the only way integrations talk to PurrOS.
2. **Integration registration**: an admin registers an integration with a manifest that declares its name, the API scopes it needs and the webhook events it subscribes to. PurrOS issues the integration its own scoped API key and webhook signing secret, and attributes its changes to it in the audit log.
3. **TypeScript SDK and integration template** (`@purros/sdk`): a typed API client generated from the OpenAPI spec, webhook signature verification, retry and pagination helpers, and a starter repository.
4. **CSV import/export**: for one-off loads and for systems with no API at all.

What PurrOS does **not** provide: official integrations for specific vendors. Because PurrOS doesn't have to track changes in dozens of vendor APIs, the core team can focus on a stable, well-documented API.

Integrations run as **separate processes** (a container, a serverless function, a cron script), outside PurrOS. An integration can crash, hang or be badly written without affecting the ERP, and it can use any language. TypeScript is only the path with the best tooling.

Typical integrations someone might build:

| Category | Example integration |
|---|---|
| Employment / HR software | Sync new hires and terminations from an HR platform into People |
| Timeclocks | Forward punches from a timeclock device or app to `/time/punches:batch` |
| Inventory / commerce | Push stock levels to an online store and create sales orders from its orders |
| Payroll / accounting | Listen for `pay_period.locked` and send approved hours to a payroll provider |

A community-maintained **integration directory** (a list of links in the docs, not code in this repository) helps people find and share integrations.

## 8. Success metrics

| Metric | Target for v1 |
|---|---|
| Time from `git clone` to first login | < 15 minutes |
| Time for a developer to push their first punch or employee via the API | < 30 minutes, using only the docs |
| API coverage of UI actions | 100% |
| Stock ledger correctness | Zero unexplained discrepancies (on-hand always equals the ledger sum) |
| p95 API latency (single-record reads, 500-employee dataset) | < 200 ms |
| Upgrade success without manual DB intervention | 100% of minor releases |

Community metrics (tracked, not targeted): GitHub stars, active installs (anonymous, opt-in telemetry only), external contributors, and community-built integrations listed in the directory.

## 9. Roadmap

| Phase | Focus |
|---|---|
| **0.1 — Foundation** | Auth, RBAC, audit log, API keys, webhooks, People module |
| **0.2 — Time** | Punch ingestion, timesheets, approvals, payroll CSV export |
| **0.3 — Inventory** | Items, locations, stock ledger, adjustments, transfers, counts |
| **0.4 — Purchasing & Sales** | Suppliers, POs, receipts, customers, SOs, fulfilment, invoices |
| **0.5 — Integrations** | Integration registration and manifests, `@purros/sdk`, integration starter template, example integrations, CSV import templates |
| **1.0 — Stable** | API v1 frozen, upgrade guarantees, documentation complete |
| **Post-1.0** | Manufacturing (BOM/work orders), UI extension points for integrations, multi-company, advanced scheduling |

## 10. Open questions

- Should integrations be able to add UI (settings pages, dashboard widgets) inside PurrOS, or stay API-only?
- Which example integrations should ship with the SDK to show the common patterns (one-way sync, webhook listener, scheduled export)?
- Is opt-in anonymous telemetry acceptable to the community for measuring adoption?
- Do we offer a commercially supported / managed hosting option later, and how does that interact with AGPL?
