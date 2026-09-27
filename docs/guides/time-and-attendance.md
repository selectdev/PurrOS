# Time & attendance

Feature key: `time`. Needs People & HR.

Record hours accurately, apply your labor rules, get timesheets approved, and hand clean hours to payroll.

## Where punches come from

| Source | How |
|---|---|
| **Kiosk timeclock** | The built-in browser timeclock on a shared tablet or PC (`time.kiosk`) |
| **Timeclock hardware or apps** | Through an [integration](../integrations/recipes.md#timeclocks) using `POST /api/v1/time/punches:batch` |
| **POS clock-ins** | The POS integration sends them the same way |
| **Manual entry** | A manager adds a punch with a reason (`punches.correct`) |

Each punch records its source and device. Punches are **never edited**: corrections are separate records with a reason, and both stay visible.

### Punch corrections

Employees ask for a correction from the [Employee Area](employee-area.md) (a missed punch, or a wrong one to replace) with a reason. A manager with `punches.correct` for that employee approves or rejects it. Approving adds the corrected punch and marks the replaced one **void**: it stays on record (`includeVoided=true` shows it) but no longer counts. Corrections into a locked pay period are refused. Rebuild timesheets after approving to update their totals.

## Kiosk timeclock

1. Go to **Settings → Kiosks** (`kiosk.manage`) and choose **Add kiosk** for a location.
2. Open the pairing URL on the tablet or PC and enter the pairing code.
3. Staff clock in with a **badge** (barcode or QR), a **QR code** from their Employee Area, or a personal **clock code**.

Options:

- **Photo on punch** (`time.kiosk_photo`) to discourage buddy punching. Photos are kept for a configurable period and then deleted.
- **Job or department selection** when someone works in several areas.
- **Offline mode**: punches are stored on the device and sent when the connection returns, keeping the original times.

A clock code **only records punches**. It can't be used to sign in or see anyone's data.

## Schedule-aware punching

With Scheduling on (`scheduling.schedule_enforcement`), the kiosk and API check each punch against the schedule:

| Situation | Behavior (configurable per location) |
|---|---|
| Clocking in more than *N* minutes early | Blocked until the shift start, or allowed with manager approval |
| Clocking in without a scheduled shift | Blocked, or flagged for approval |
| Clocking out more than *N* minutes late | Flagged, with overtime risk highlighted |
| Missing break | Reminder at the kiosk, and an exception on the timesheet |

## Labor rules

A labor rule set (`labor_rules.manage`) is assigned to each location or inherited from above:

- rounding (e.g. to 5 minutes)
- paid and unpaid breaks and meal periods, with required timing
- daily and weekly overtime thresholds and multipliers, and holiday pay
- minimum rest between shifts, and maximum consecutive days
- rules for minors (latest end time, maximum hours on school days)
- predictive scheduling: notice periods and premium pay for late changes
- break **attestation** (`time.break_attestation`): at clock-out, the employee confirms they took their breaks or explains why not

PurrOS applies the rules you configure. Check them against your local law. PurrOS doesn't provide legal advice.

## Timesheets

Timesheets are built automatically from punches for each pay period and show:

- worked, break, overtime, holiday and premium hours per day
- **exceptions**: missed punches, missed breaks, early or late punches, unscheduled shifts, overtime
- the comparison with the schedule

### Approval

1. **Manager approval** (`timesheets.approve`), within the manager's reach. Exceptions must be resolved or accepted first.
2. **Payroll approval and lock** (`pay_periods.lock`). A locked pay period can't change. Late corrections are carried into the next period as adjustments.

Employees can raise **punch correction requests** from their Employee Area. The manager approves or rejects them, and every decision is audited.

## Time off

`time.time_off` manages leave types (vacation, sick, personal, unpaid…), accrual rules and balances. Employees request leave in their Employee Area, managers approve it (`time_off.approve`), and approved time off appears on schedules and timesheets.

## Payroll export

`time.payroll_export` exports approved, locked hours:

- **CSV templates**: choose columns, codes and formats to match your payroll provider's import, and save them as named templates.
- **Integration**: a payroll integration listens for `pay_period.locked` and fetches the hours through the API.

Hours are broken down by employee, earning code (regular, overtime, holiday, premium), location, department and cost center.

## API & events

| Endpoint | Scope |
|---|---|
| `POST /api/v1/time/punches:batch` | `time:write` |
| `GET /api/v1/time/punches` | `time:read` |
| `GET /api/v1/timesheets`, `GET /api/v1/timesheets/{id}` | `time:read` |
| `GET /api/v1/pay-periods`, `GET /api/v1/pay-periods/{id}/export` | `payroll:read` |
| `GET/POST /api/v1/time-off/requests`, `GET /api/v1/time-off/balances` | `time:read` / `time:write` |
| `GET /api/v1/punch-corrections`, `POST /api/v1/punch-corrections/{id}:approve`, `:reject` | `time:read` / `time:write` |

Events: `punch.received`, `punch.corrected`, `punch.exception`, `timesheet.approved`, `timesheet.rejected`, `pay_period.locked`, `time_off.requested`, `time_off.approved`, `time_off.rejected`.
