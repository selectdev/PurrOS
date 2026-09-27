# Reports & insights

Feature key: `insights`.

See how every location is doing, catch problems early, and know what to fix first.

## Dashboards

Each person sees a dashboard for their role and [reach](../admin/users-and-roles.md#reach) (`reports.read`):

- a **supervisor** sees their team and shift
- a **location manager** sees their location
- a **district manager** sees their district, with each location side by side
- an **owner** sees the whole company

Dashboards update close to real time as sales, punches, counts and checklists come in. Tiles can be rearranged, and admins can set default dashboards per role.

## Built-in KPIs

KPIs appear only when the features they need are switched on.

| Area | KPIs |
|---|---|
| Sales | Net sales, transactions, average transaction value, sales by channel and hour, vs forecast, last week and last year |
| Labor | Labor cost, labor % of sales, sales per labor hour, scheduled vs actual hours, overtime, forecast accuracy |
| Cash | Over/short, late or mismatched deposits, reconciliation differences |
| Inventory | Cost of goods and COGS %, variance (gain/loss), waste, count completion |
| Purchasing | Spend, price changes, supplier fill rate |
| Operations | Checklist completion, failed items, open corrective actions, audit scores |
| Equipment | Downtime, overdue maintenance, repair cost |
| People | Headcount, turnover, certifications expiring, time off |

## Multi-location rollups

Every report can be grouped by the [organization hierarchy](../admin/organization-and-locations.md) and compared:

- **Location rankings** for any KPI
- **Comparisons** with last week, last year, forecast and budget
- **Drill-down** from company → region → district → location → shift or employee

## Alerts

Create alert rules (`alerts.manage`) such as "labor % above 28% by 2 pm", "over/short worse than −$20", or "variance above $300 this week". Alerts go to chosen roles within reach by in-app notification, email or push, and optionally by webhook (`alert.triggered`).

## Scheduled reports

Send any report by email on a schedule (`reports.schedule`), for example the daily flash report at 07:00 or the weekly district summary on Monday, as a PDF, CSV or Excel file.

## Custom reports

The report builder (`insights.custom_reports`, `reports.build`) lets you choose data, filters, grouping, columns and charts, then save and share the report with roles. Every report can be exported to CSV and Excel, and the same data is available through the API.

## Recommended actions

`insights.recommendations` looks across your data every morning and during the day, and lists the **biggest issues first** (`recommendations.read`):

> **Store 102: overtime risk.** 3 people are projected to pass 40 hours by Friday (+$410). *Suggested:* move 2 Friday shifts to Jamie and Sam. *Owner:* Store manager.
>
> **Store 110: cash short 3 days in a row** (−$64 total), all on drawer 2, closing shift. *Suggested:* review drawer 2 counts. *Owner:* District manager.

Each recommendation explains **why** it was flagged using your own numbers, suggests a next step and an owner, and can be dismissed or marked done. This works with built-in rules and needs no AI.

## AI assistant (optional)

`insights.ai_assistant` adds a chat assistant that answers questions about your data ("Why was labor high at Store 102 last week?") and helps explain recommendations (`ai_assistant.use`).

- It's **off by default**, and needs both the feature switch and a model provider configured by an admin (see [Configuration](../getting-started/configuration.md#ai-assistant-optional)).
- You choose the provider, including **self-hosted models**, so data doesn't have to leave your infrastructure.
- It only sees data the asking user is allowed to see.
- Questions and answers are logged for audit.

## API & events

| Endpoint | Scope |
|---|---|
| `GET /api/v1/kpis?from=…&to=…&locationId=…` | `reports:read` |
| `GET /api/v1/reports` (the reports available with your features) | `reports:read` |
| `GET /api/v1/reports/{key}?from=…&to=…&locationId=…&format=csv` | `reports:read` |
| `GET /api/v1/recommendations?locationId=…` | `reports:read` |
| `GET/POST /api/v1/alert-rules`, `PATCH/DELETE /api/v1/alert-rules/{id}` | `reports:read` / `reports:write` |

**KPIs** cover business dates `from`–`to` (inclusive, at most 366 days, default today). They include net sales (less tax and refunds), transactions, average ticket, labor hours and cost (from punches and pay rates, breaks excluded), labor %, sales per labor hour, over/short, waste cost, COGS and COGS %, forms submitted and failed, and current counts of open corrective actions, open work orders and low-stock items. A figure whose feature is switched off is left out of the response.

**Built-in reports:** `sales-by-day`, `labor-by-day`, `location-ranking`, `over-short`, `waste-by-reason`, `variance` (stock count differences) and `stock-valuation` (current). A report whose feature is off returns 404.

**Alert rules** watch one KPI (`kpi`, `comparator` `gt`/`lt`, `threshold`, optional `locationId`). The worker checks them every 5 minutes against today's figures (the location's time zone, or UTC for rules across all locations) and raises `alert.triggered` at most once per rule per day.

Events: `alert.triggered`, `recommendation.created` (planned).
