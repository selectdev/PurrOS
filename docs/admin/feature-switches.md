# Feature switches

Every PurrOS feature is optional. The **Owner** decides what's switched on, today with `purros features enable|disable <key>` and later under **Settings → Features** in the web app. Use only what your business needs, and the rest never gets in the way.

## What "off" means

A switched-off feature is removed completely:

| Place | Effect |
|---|---|
| Interface *(web app, planned)* | Gone from navigation, search, the command palette, dashboards, reports, settings, the Employee Area and team displays |
| Roles | Its permissions disappear from the role editor, and existing grants are ignored |
| API | Its endpoints return `404` with `code: "feature_disabled"`, and it's removed from the OpenAPI spec |
| Webhooks | Its events stop being sent and can't be subscribed to |
| Integrations | Its scopes can't be requested, and existing keys lose them while it's off |
| Background work | Its jobs, alerts, reminders and scheduled reports stop |
| Other features | Figures that depend on it are left out rather than shown as zero. For example, the labor-% KPI disappears if Sales is off. |

**Always on (platform core):** sign-in and accounts, roles, the organization and locations, settings, the audit log, and the API and webhook infrastructure.

## Feature list

Features are switched as whole areas, and many have smaller features inside them that can be switched separately.

† The switch exists so the catalog of features, permissions and scopes is stable, but the feature itself isn't built yet. `people.custom_fields` currently only controls the free-form `customFields` object on employees.

| Feature (key) | Needs | Smaller features that can be switched separately |
|---|---|---|
| **People & HR** (`people`) | | `people.documents`, `people.onboarding`†, `people.skills`, `people.custom_fields`† |
| **Time & Attendance** (`time`) | People | `time.kiosk`†, `time.kiosk_photo`†, `time.break_attestation`†, `time.time_off`, `time.payroll_export` |
| **Scheduling & Forecasting** (`scheduling`) | People, Time | `scheduling.forecasting`, `scheduling.auto_builder`†, `scheduling.shift_swaps`, `scheduling.open_shifts`, `scheduling.schedule_enforcement`† (blocks early and unscheduled punches) |
| **Cash Management** (`cash`) | Sales | `cash.deposit_verification`, `cash.tender_reconciliation`, `cash.petty_cash`† |
| **Inventory** (`inventory`) | | `inventory.waste`, `inventory.transfers`, `inventory.usage_recipes` (needs Sales), `inventory.batches`† |
| **Purchasing & Ordering** (`purchasing`) | Inventory | `purchasing.suggested_orders`, `purchasing.invoice_matching` |
| **Sales** (`sales`) | | `sales.feeds`, `sales.orders`, `sales.invoicing` |
| **Forms, Checklists & Compliance** (`operations`) | | `operations.audits`, `operations.corrective_actions`, `operations.sensors` |
| **Equipment & Assets** (`equipment`) | | `equipment.maintenance`, `equipment.work_orders` |
| **Communication** (`communication`) | | `communication.announcements`, `communication.messaging`†, `communication.direct_messages`†, `communication.calendar`, `communication.files`† |
| **Team Displays** (`displays`) | | `displays.gamification`, `displays.celebrations`† |
| **Reports & Insights** (`insights`) | | `insights.custom_reports`†, `insights.scheduled_reports`†, `insights.recommendations`, `insights.ai_assistant`† |
| **Employee Area** (`employee_area`) | People | `employee_area.estimated_pay`, `employee_area.payslips`, `employee_area.profile_edit`, `employee_area.data_export` |

Forecasting uses sales data when Sales is on. Without Sales, you can forecast from other demand drivers sent through the API (see [Scheduling & forecasting](../guides/scheduling-and-forecasting.md#demand-drivers)).

## Dependencies

- **Turning on** a feature that needs another one offers to turn both on together.
- **Turning off** a feature that others depend on lists everything that will also be switched off, and asks you to confirm.

For example, switching off *Time & Attendance* also switches off *Scheduling & Forecasting*.

## Your data is kept

Switching a feature off **never deletes data**. Turn it back on and everything is as you left it. Anything that happened while it was off, like sales arriving for usage calculations, is picked up when the feature is back on.

To remove a feature's data permanently:

*(Planned.)* Switch the feature off, download an export, then confirm deletion. Deletion will run in the background, be recorded in the audit log, and also be available as `purros features purge <key>`.

## Audit

Every switch on and switch off is recorded in the audit log, with who did it and when.

## API

`GET /api/v1/features` returns the enabled features and smaller features, so integrations can adapt. Changing features is only possible for an Owner, with `purros features enable|disable` (and in the web app, once it ships).
