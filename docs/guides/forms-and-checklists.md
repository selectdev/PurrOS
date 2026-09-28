# Forms, checklists & compliance

Feature key: `operations`.

> **Status:** The API covers forms with pass/fail rules, submissions, scored audits, corrective actions, and sensors with out-of-range events. Scheduled checklists, overdue alerts and the template library are planned. Screens described below arrive with the web app; until then, use the endpoints listed at the end of this page.

Replace paper checklists, logs and inspection sheets with digital forms that get done on time, alert people when something is wrong, and prove it was handled.

## Form builder

Build forms under **Operations → Forms** (`forms.manage`). Question types:

| Type | Example |
|---|---|
| Yes / no / N/A | "Fire exits clear?" |
| Choice (single or multiple) | "Condition: good / worn / damaged" |
| Number with allowed range | "Cooler temperature (°C)", allowed 0–5 |
| Text | "Notes" |
| Photo | "Photo of the display" (required on failure) |
| Signature | Sign-off by the person responsible |
| Date / time | "Delivery arrival time" |
| Item, asset or employee picker | Links the answer to a PurrOS record |
| Section and instructions | Grouping and guidance text or images |

Questions can be **conditional** (only shown when an earlier answer calls for it) and can be marked **critical**, meaning a failed answer fails the whole form or audit.

Start from the **template library**: opening and closing routines, cleaning logs, food safety (HACCP) temperature logs, workplace safety walks, vehicle inspections, cash office checks, incident reports, customer complaints and employee evaluations.

## Scheduled checklists

Assign a form (`checklists.manage`) to a location, role or person with a schedule:

- **Fixed times:** open at 07:00, close at 22:00
- **Recurring:** every 2 hours while open, every shift, weekly on Monday, monthly
- **Triggered:** after a delivery is received, after an incident, when a sensor reading goes out of range

Staff see what's due on their phone or tablet and in their [Employee Area](employee-area.md), and complete it there (`checklists.complete`).

## Alerts

- **Overdue:** a checklist not finished within its window alerts the location manager, and can escalate to the district manager after a set delay.
- **Failed answers:** a critical or out-of-range answer alerts the right people immediately.

## Corrective actions

A failed answer can automatically create a **corrective action** (`operations.corrective_actions`) with an owner, due date and required evidence (e.g. a photo of the repaired seal). Open actions show on dashboards until closed (`corrective_actions.manage`), and their history is kept with the original form.

## Audits and inspections

`operations.audits` adds **scored audits** for district manager visits, brand standards, safety audits and internal inspections (`audits.conduct`):

- weighted sections and questions, with critical items
- score and pass/fail, with photos and comments
- follow-up corrective actions
- comparison across locations and over time (`audits.read`)

## Sensors

With `operations.sensors`, temperature, humidity, door or meter sensors send readings through `POST /api/v1/sensor-readings`. PurrOS can:

- fill in log entries automatically (e.g. hourly fridge temperatures)
- alert when readings are out of range for a set time
- start a checklist or corrective action when that happens

Register sensors and their allowed ranges with `sensors.manage`.

## Reports

Completion rate (on time, late, missed) by location and form, failed items, open corrective actions, audit scores and trends.

## API & events

| Endpoint | Scope |
|---|---|
| `GET /api/v1/forms`, `GET /api/v1/form-submissions` | `operations:read` |
| `POST /api/v1/form-submissions` (e.g. from another app) | `operations:write` |
| `POST /api/v1/sensor-readings` (batch) | `operations:write` |
| `GET /api/v1/corrective-actions`, `GET /api/v1/audits` | `operations:read` |

Events: `form.submitted`, `form.answer_failed`, `checklist.overdue` *(planned)*, `corrective_action.created`, `corrective_action.closed`, `audit.completed`, `sensor.out_of_range`.
