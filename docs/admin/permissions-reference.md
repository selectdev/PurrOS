# Permissions reference

Permissions are defined by PurrOS and combined into roles by your organization (see [Users, roles & permissions](users-and-roles.md)). Each permission in a role has a **reach**: own team, assigned locations, assigned departments or everyone.

Permissions of a switched-off feature are hidden and have no effect. The live catalog, filtered to your enabled features, is available at `GET /api/v1/permissions`.

Every account linked to an employee record can see and manage its **own** data in the Employee Area without any of these permissions.

The permission each API endpoint needs is listed in the [endpoint index](../api/endpoints.md). Location names, announcements, the calendar, time-off types and display metrics are readable by anyone signed in.

## Platform (always on)

| Permission | Allows |
|---|---|
| `users.read` | View accounts, their roles and assignments |
| `users.manage` | Invite, deactivate and change the role or assignments of accounts (within the escalation rules) |
| `roles.manage` | Create, edit and delete roles. **Only an Owner can grant this.** |
| `organization.manage` | Create, edit and archive org units, locations and departments. With a reach narrower than Everyone, only the profiles of locations within reach. |
| `settings.manage` | Change company settings (other than features, which are Owner-only) |
| `integrations.manage` | Register, update, pause, rotate keys of and remove integrations; see their config, logs and ingestion batches. Needs reach Everyone. Approving `people:sensitive` is Owner-only. |
| `webhooks.manage` | Add, change, disable and delete webhook endpoints, rotate their secrets, send test events, view deliveries and replay them. Needs reach Everyone. |
| `api_keys.personal` | Create personal API keys, which act with the user's own role and reach |
| `audit.read` | View the audit log |
| `attachments.read` | View uploaded files (proof, photos, receipts…) for employees and locations within reach |
| `attachments.manage` | Delete uploaded files within reach |

Switching features on and off is **Owner-only** and isn't a grantable permission.

## People & HR

| Permission | Allows |
|---|---|
| `employees.read` | View employee profiles (not sensitive fields or pay) |
| `employees.write` | Create and edit employees, transfers and terminations |
| `employees.sensitive.read` | View national IDs, bank details, date of birth and other sensitive fields |
| `employees.sensitive.write` | Edit sensitive fields and approve employees' requested changes to them |
| `pay.read` | View pay rates and history, and estimated pay |
| `pay.write` | Change pay rates |
| `documents.read` | View employee documents |
| `documents.manage` | Upload, share and delete employee documents |
| `onboarding.manage` | Create onboarding checklists and track new hires |
| `skills.manage` | Define skills and certifications and assign them |

## Time & Attendance

| Permission | Allows |
|---|---|
| `punches.read` | View punches |
| `punches.correct` | Add or approve punch corrections |
| `timesheets.read` | View timesheets |
| `timesheets.approve` | Approve or reject timesheets (manager step) |
| `pay_periods.lock` | Final payroll approval and locking of pay periods |
| `payroll.export` | Export hours to payroll |
| `time_off.read` | View time-off requests and balances |
| `time_off.approve` | Approve or reject time off |
| `labor_rules.manage` | Edit labor rule sets |
| `kiosk.manage` | Set up kiosk timeclocks and issue clock codes and badges |

## Scheduling & Forecasting

| Permission | Allows |
|---|---|
| `schedules.read` | View schedules |
| `schedules.manage` | Build and edit schedules |
| `schedules.publish` | Publish schedules to employees |
| `shift_swaps.approve` | Approve swaps and open-shift pick-ups |
| `forecasts.read` | View forecasts |
| `forecasts.manage` | Adjust forecasts |
| `staffing_rules.manage` | Edit staffing rules and minimum coverage |

## Cash Management

| Permission | Allows |
|---|---|
| `cash.read` | View counts, deposits, over/short and reconciliation |
| `cash.count` | Perform drawer counts, skims and safe drops |
| `cash.manage` | Safe counts, change orders, closing the business day |
| `cash.deposits` | Prepare and record bank deposits |
| `cash.reconcile` | Match deposits and non-cash settlements, and resolve differences |
| `cash.paid_outs.approve` | Approve paid-outs and petty cash above the limit |

## Inventory

| Permission | Allows |
|---|---|
| `inventory.read` | View stock, movements, usage and variance |
| `items.manage` | Create and edit items, units and barcodes |
| `usage_recipes.manage` | Edit usage recipes |
| `stock_counts.count` | Enter counts |
| `stock_counts.manage` | Schedule, approve and post counts |
| `inventory.adjust` | Post stock adjustments |
| `waste.record` | Record waste and shrink |
| `transfers.manage` | Send and receive transfers |

## Purchasing & Ordering

| Permission | Allows |
|---|---|
| `purchasing.read` | View suppliers, catalogs, purchase orders and supplier invoices |
| `suppliers.manage` | Manage suppliers, catalogs and order schedules |
| `orders.create` | Create orders from suggestions or from scratch |
| `purchase_orders.approve` | Approve purchase orders above the location's limit |
| `goods_receipts.create` | Receive deliveries |
| `invoices.match` | Match supplier invoices and approve price differences |

## Sales

| Permission | Allows |
|---|---|
| `sales.read` | View sales data and feeds |
| `sales.unmapped.resolve` | Match unknown items from sales feeds to PurrOS items |
| `customers.manage` | Manage customers and price lists |
| `sales_orders.manage` | Create, fulfil and cancel sales orders |
| `invoices.issue` | Issue customer invoices and record payments |

## Forms, Checklists & Compliance

| Permission | Allows |
|---|---|
| `checklists.complete` | Fill in assigned checklists and forms |
| `checklists.manage` | Assign and schedule checklists |
| `forms.manage` | Build and edit form templates |
| `audits.conduct` | Carry out scored audits and inspections |
| `audits.read` | View audit results |
| `corrective_actions.manage` | Assign and close corrective actions |
| `sensors.manage` | Register sensors and set allowed ranges |

## Equipment & Assets

| Permission | Allows |
|---|---|
| `equipment.read` | View assets and maintenance history |
| `equipment.manage` | Manage the asset register and maintenance plans |
| `work_orders.create` | Report problems (repair tickets) |
| `work_orders.manage` | Assign, update and close work orders |

## Communication

| Permission | Allows |
|---|---|
| `announcements.send` | Send announcements within reach |
| `messages.group.manage` | Create and manage group chats |
| `calendar.manage` | Add and edit company calendar events |
| `files.manage` | Manage the shared files and links library |

Taking part in conversations you belong to needs no permission.

## Team Displays

| Permission | Allows |
|---|---|
| `displays.manage` | Pair screens and edit display profiles |
| `recognition.give` | Post shout-outs and run challenges |

## Reports & Insights

| Permission | Allows |
|---|---|
| `reports.read` | View dashboards and reports (limited to reach) |
| `reports.build` | Create custom reports |
| `reports.schedule` | Schedule emailed reports |
| `alerts.manage` | Create KPI alert rules |
| `recommendations.read` | See recommended actions |
| `ai_assistant.use` | Use the optional AI assistant |
