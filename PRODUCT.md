# PurrOS: Product

This document explains what PurrOS is, who it is for, what it does and does not do, and how we decide what gets built. When a feature request comes in, this is the document to check it against.

---

## 1. Vision

**Give growing businesses an ERP they can own, understand, and connect to anything.**

A company of 20–500 people, often spread across several locations, usually runs on an HR platform, a scheduling app, a timeclock, a point-of-sale, an inventory tool, paper checklists, a group chat and a spreadsheet for cash, plus someone who copies data between them. PurrOS becomes the operating system in the middle: one database for people, schedules, time, cash, stock, orders, checklists and equipment, with dashboards across every location and an API that other tools can connect to without a consultant.

## 2. Problem

| Pain | Today | With PurrOS |
|---|---|---|
| Data lives in 4–6 disconnected tools | Manual CSV exports, re-keying, errors | One system of record, synced through the API and webhooks |
| Enterprise ERPs are too heavy | 6–12 month rollouts, per-seat licensing, consultants | Deployed in a day with Docker, no per-seat cost |
| Timeclock data doesn't reach payroll cleanly | Punches exported and fixed up by hand every pay period | Punches flow in live, timesheets get approved, payroll export takes one click |
| Stock levels are never quite right | Inventory updated after the fact, if at all | Receipts, sales and adjustments update stock in real time |
| Scheduling is guesswork | Managers copy last week's rota and hope it fits | Schedules built from a forecast of demand, with labor cost visible as you go |
| Cash and checklists live on paper | Missing deposits and skipped safety checks found weeks later | Guided cash counts and digital checklists that alert in real time |
| Multi-location visibility | Area managers phone each site for numbers | Live dashboards rolled up by region and district |
| Vendor lock-in and data residency | Data sits in someone else's cloud | Self-hosted, open source, with the full database under your control |

## 3. Target users

**Primary market:** small and mid-sized businesses with **20–500 employees**, especially those with hourly staff, physical goods or cash, and more than one location. PurrOS isn't tied to an industry. Target users include retail chains, restaurants and hospitality, franchise and multi-unit operators, clinics, salons and gyms, distribution and wholesale, light manufacturing, and field service companies. Industry-specific needs are met with configurable terms, templates and integrations, not with separate editions.

### Personas

**Operations Manager (primary buyer)**
Owns "how the business runs." Wants one place to see who is working, what is in stock, and what has been ordered. Judges success by fewer errors and less time spent reconciling.

**HR / Payroll Administrator**
Keeps employee records, reviews timesheets, and runs payroll through an external provider. Needs clean, approved hours exported on time.

**Warehouse / Inventory Lead**
Receives goods, moves stock, and runs cycle counts. Needs fast screens that work on a tablet or scanner, and accurate numbers.

**Location / Site Manager**
Runs one store, branch or site day to day: builds schedules, counts cash, places orders, and makes sure checklists get done. Needs guided workflows on a tablet and a clear list of what's overdue.

**District / Area Manager**
Oversees several locations. Needs rolled-up numbers, location comparisons, audits for site visits, and a ranked list of problems to act on.

**Supervisor / Line Manager**
Approves their team's timesheets and requests, and completes shift checklists. Uses PurrOS a few minutes a day and needs things to be obvious.

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
6. **Calm, professional UI.** Dense where power users need it and plain where occasional users need it, with no clutter. Gamification and recognition belong only on the opt-in Team Displays, never in the working screens.
7. **Industry-neutral core.** Features are described in general terms (locations, demand drivers, usage recipes, checklists), and industry-specific behavior comes from configuration and templates.
8. **Everything is optional.** Every feature area, and many smaller features inside them, can be switched off by the Owner. A disabled feature disappears from the UI, roles, API, webhooks and background jobs, so a business only ever sees what it uses. Only the platform core (accounts, roles, locations, settings, audit log, API infrastructure) is always on.

## 5. Scope

The roadmap (§9) sets the order. The API already covers most of every section below; [api/README.md](api/README.md) lists what's built and what's still planned.

### 5.1 People (Employee Management)
- Employee profiles: personal info, contact, employment type (full-time, part-time, contractor), status (active, on leave, terminated), start and end dates
- Organisation structure: departments, positions, reporting lines, locations
- Custom fields per company
- Organization hierarchy (company → regions → districts → locations, configurable levels) with location profiles and inherited settings
- Skills and certifications with expiry, used by the scheduler
- Onboarding checklists (documents, acknowledgments, training)
- Document attachments (contracts, certifications) with expiry reminders
- **Sync from external HR/employment software** by `externalId`, both one-way and bidirectional

### 5.2 Time & Attendance
- Punch ingestion from any timeclock (API, CSV import, or a custom integration)
- Built-in kiosk timeclock (badge, QR or clock code, optional photo), with schedule-aware punching that blocks or flags early, late and unscheduled punches
- Break reminders and attestation at clock-out
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
- Cycle counts and full stock takes, with mobile counting by storage area and barcode scanning
- Waste and shrink entry with reason codes and photos
- Transfers between locations with sending and receiving confirmation
- Usage recipes (what one sold product or service consumes) for expected vs actual usage and gain/loss reporting
- Optional batch and expiry tracking
- Valuation: weighted average cost in v1

### 5.4 Purchasing
- Suppliers and supplier price lists
- Purchase requests, then purchase orders, with approval thresholds
- Goods receipts (partial and full) that post to the stock ledger
- PO status tracking and supplier performance basics
- Suggested orders from par levels, stock on hand, forecast and delivery schedules
- Supplier catalogs and order guides
- Invoice matching (PO, goods received, supplier invoice)

### 5.5 Sales
- Sales feeds from any POS or e-commerce system (daily totals, hourly figures or transactions), which feed forecasting, cash, usage and reports
- Customers and price lists
- Quotes, then sales orders, with stock reservation
- Pick, pack and ship, which posts to the stock ledger
- Basic invoicing (PDF) and payment status, exportable to accounting software

### 5.6 Employee Area (self-service)
- Every employee signs in to see all data PurrOS holds about them: schedule, punches, timesheets, pay rate and history, estimated gross pay per period, payslips (when a payroll integration provides them), time-off balances, profile, documents and change history
- Request punch corrections and time off, with approvals and audit
- Set availability, pick up open shifts and swap shifts
- Complete assigned checklists and forms, report equipment problems, and read messages and announcements
- Update own contact details directly. Sensitive fields go to HR for approval
- Export all personal data (JSON/CSV)
- Works on phones as well as desktops. Employees see only their own data

### 5.7 Platform
- Feature switches (Owner only): turn any feature area or smaller feature off to remove it everywhere (UI, roles, API, webhooks, jobs), with automatic dependency handling. Data is kept when disabled, and export plus permanent deletion is a separate action
- First-run setup suggests features based on the type of business
- Organization-defined roles (e.g. HR, District Manager, Supervisor): each account has one role, and each role has its own set of permissions, each limited to own team, assigned locations, assigned departments or everyone
- API keys with scopes, and signed webhooks with retries
- Full audit log (who changed what, when, and from UI or API)
- CSV import and export for every core entity
- Single company per install in v1, multi-currency display, and configurable timezone and locale
- One account system for everyone: email + password, magic link, passkeys, and SSO via OIDC or SAML (Google Workspace, Microsoft Entra ID, Okta, Keycloak, Authentik, etc.)
- Optional 2FA (TOTP or passkey) that an Owner can make mandatory

### 5.8 Scheduling & Forecasting
- Demand forecasting for any driver (sales, transactions, foot traffic, orders, appointments, units shipped) from history, seasonality and holidays, with manual adjustments
- Staffing rules that turn the forecast into needed hours, plus minimum coverage
- Automatic schedule builder (availability, skills, target hours, cost, fairness) with drag-and-drop editing
- Gap and overstaffing views, suggested fill-ins, and skill gap analysis
- Availability, shift swaps, open shifts and pick-ups
- Labor rules engine (breaks, max hours, rest periods, minors, overtime, predictive scheduling), configurable per location or jurisdiction
- Live schedule cost and labor as a percentage of sales, publishing, and shift reminders

### 5.9 Cash Management
- Register-to-bank workflow: drawer counts, skims and safe drops, safe counts, change orders, bank deposits
- Over/short per drawer, shift and employee, with alerts
- Deposit verification against bank data, and reconciliation of card and digital payments against processor settlements
- Paid-outs and petty cash with receipts, blind counts, two-person verification

### 5.10 Forms, Checklists & Compliance
- Form builder (numbers with ranges, temperatures, photos, signatures, pickers, …)
- Scheduled checklists assigned to a role, person or location, with overdue and failed-answer alerts
- Corrective actions with owner, due date and photo proof
- Scored audits and inspections compared across locations
- Sensor readings via API that fill in checks automatically
- Template library (opening/closing, cleaning, food safety/HACCP, workplace safety, vehicle checks)

### 5.11 Equipment & Assets
- Asset register with QR labels, warranty and service providers
- Preventive maintenance schedules, repair tickets with photos, and downtime and cost history

### 5.12 Communication
- Announcements with read receipts and required acknowledgment
- Group and direct messaging, a company calendar, and a shared files/links library
- Email and web push notifications, and SMS through an integration

### 5.13 Team Displays
- Browser-based display mode for any screen, paired by code
- Goals, live KPIs, recognition, leaderboards, announcements and shift reminders, limited by display profile

### 5.14 Reports & Insights
- Role-based, near-real-time dashboards with rollups by hierarchy and location rankings
- Built-in KPIs (labor %, sales per labor hour, over/short, cost of goods and variance, waste, compliance, downtime, turnover)
- Threshold alerts, scheduled email reports, a custom report builder, CSV/Excel export

### 5.15 Recommended actions
- Rule-based ranking of the day's biggest issues across locations, with an explanation and a suggested owner and next step
- Optional AI assistant using an admin-chosen model provider (including self-hosted models). Off by default, and no data leaves the server unless configured
## 6. Non-goals (v1)

These are left out on purpose. Integrate with a dedicated tool instead.

- **Payroll calculation and tax filing.** We export approved hours to payroll providers.
- **Full general ledger / accounting.** We export invoices and bills to accounting software.
- **Recruiting / ATS and learning management systems.** Simple evaluation forms and onboarding checklists are included, but full performance management and course authoring are not.
- **Manufacturing (multi-level BOMs, MRP, production work orders).** Candidate for v2. Single-level usage recipes for expected usage are in scope.
- **CRM and marketing automation**
- **E-commerce storefront.** We integrate with storefronts instead.
- **Multi-company / multi-tenant hosting in one install**
- **Native mobile apps.** The web UI is responsive, installable as a progressive web app (PWA) with push notifications, and works on tablets and scanners.
- **Point-of-sale.** PurrOS takes in sales from your POS. It is not a POS itself.

## 7. Integrations

The integration story is what sets PurrOS apart. **PurrOS ships no built-in integrations with third-party products.** Every integration is **custom code**, written by the business, its integrator or the community, that talks to PurrOS only through the public API.

What PurrOS provides:

1. **REST API + webhooks**: covers the whole product, with a published OpenAPI spec. This is the only way integrations talk to PurrOS.
2. **Integration registration**: an admin registers an integration with a manifest that declares its name, the API scopes it needs and the webhook events it subscribes to. PurrOS issues the integration its own scoped API key and webhook signing secret, and attributes its changes to it in the audit log.
3. **TypeScript SDK and integration template** (`@purros/sdk`, planned): a typed API client generated from the OpenAPI spec, webhook signature verification, retry and pagination helpers, and a starter repository.
4. **CSV import/export**: for one-off loads and for systems with no API at all. Reports, payroll and pay periods export CSV today; CSV import is planned.

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

The API is being built ahead of the web app, so the whole scope below exists as endpoints before it has screens. Status as of September 2026 (unreleased, pre-alpha):

| Phase | Focus | Status |
|---|---|---|
| **API foundation** | Auth (password, magic link, TOTP), roles with reach, audit log, API keys, feature switches, webhooks, organization hierarchy, file storage, email, backups, admin CLI | Built |
| **API modules** | People, time and attendance, scheduling and forecasting, inventory, purchasing, sales, cash, forms and checklists, equipment, communication (announcements, calendar), team display data, reports, KPI alerts, recommendations, Employee Area | Built (see [api/README.md](api/README.md)) |
| **0.1 — First release** | The web app for setup, People, Time and the Employee Area; `@purros/sdk`; integration template and examples; CSV import templates | Next |
| **0.2 – 0.4** | Web app screens for inventory, purchasing, sales, cash, scheduling, operations, equipment and insights; kiosk timeclock; schedule-aware punching and break attestation | Planned |
| **1.0 — Stable** | API v1 frozen, upgrade guarantees, core dashboards, documentation complete | Planned |
| **1.x** | Passkeys and SSO (OIDC, SAML, SCIM); automatic schedule builder; messaging, shared files and web push; scheduled checklists and onboarding; batch and expiry tracking; petty cash; custom and scheduled reports; optional AI assistant; Prometheus and OpenTelemetry | Planned |
| **Later** | Manufacturing (BOM/work orders), UI extension points for integrations, multi-company | Ideas |

## 10. Open questions

- Should integrations be able to add UI (settings pages, dashboard widgets) inside PurrOS, or stay API-only?
- Which example integrations should ship with the SDK to show the common patterns (one-way sync, webhook listener, scheduled export)?
- Is opt-in anonymous telemetry acceptable to the community for measuring adoption?
- Do we offer a commercially supported / managed hosting option later, and how does that interact with AGPL?
