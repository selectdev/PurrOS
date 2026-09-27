# Scheduling & forecasting

Feature key: `scheduling`. Needs People & HR and Time & Attendance.

Build schedules that match expected demand, respect everyone's availability and your labor rules, and stay on budget.

## How it fits together

```
History (sales, transactions, orders, appointments…)
        │
        ▼
  Demand forecast ──► Staffing rules ──► Needed staff per hour and area
                                              │
  Availability, skills, labor rules ──────────┤
                                              ▼
                                   Schedule builder ──► Publish ──► Employees
```

## Demand drivers

A demand driver is a number that decides how many people you need. Common examples:

| Business | Drivers |
|---|---|
| Retail | Sales, transactions, foot traffic |
| Restaurant / café | Sales, transactions, covers, delivery orders |
| Clinic / salon | Booked appointments |
| Warehouse | Orders to pick, units shipped, trucks to unload |
| Call center / service desk | Tickets, calls |

Drivers come from the [Sales](sales.md) feature (sales and transactions from your POS or online store), or from any system through `POST /api/v1/demand-drivers` (e.g. appointments from a booking system, foot traffic from a door counter).

## Forecasting

`scheduling.forecasting` predicts each driver per location, day and 15-minute interval using:

- your history (at least 8 weeks recommended), with weekly and yearly patterns
- holidays and special days from the location profile
- recent trend

Managers can **adjust** a forecast (`forecasts.manage`), for example "+20% Saturday for the street fair", and the change is kept with a note. Reports compare forecast with actual so you can see how accurate it is.

## Staffing rules

Staffing rules turn the forecast into **needed staff** for each work area:

- **Ratio:** "1 cashier per 40 transactions per hour", "1 picker per 60 order lines per hour"
- **Fixed minimum:** "At least 2 people on the floor while open", "1 key holder at open and close"
- **Tasks:** "1 person for 1 hour before opening for setup"

Rules can be set per location or inherited from a district or region.

## Building a schedule

1. Open **Scheduling**, pick a location and week.
2. Choose **Generate** to have `scheduling.auto_builder` create shifts that cover needed staff, then assign people based on:
   - availability and approved time off
   - required skills and certifications
   - target and maximum hours, and overtime cost
   - labor rules (breaks, rest between shifts, minor restrictions)
   - fairness (sharing weekends and closing shifts)
3. Adjust by drag and drop. The **coverage graph** shows scheduled vs needed staff for each hour, with gaps in orange and overstaffing in blue. Choose a gap to see who could fill it.
4. Watch the **cost panel**: projected labor cost, labor as a percentage of forecast sales, and overtime.
5. **Publish** (`schedules.publish`). Employees are notified, and changes after publishing are highlighted and notified too.

Schedules can also be copied from a previous week or built from templates.

## Availability, swaps and open shifts

- Employees set **availability** and preferences in their [Employee Area](employee-area.md). Managers can require approval of availability changes.
- **Shift swaps** (`scheduling.shift_swaps`): an employee offers a shift, a qualified co-worker accepts, and a manager approves (`shift_swaps.approve`), unless auto-approval is on for swaps that break no rules.
- **Open shifts** (`scheduling.open_shifts`): unfilled shifts are posted to qualified employees, who can request them.

## Labor rules while scheduling

The scheduler warns about, or blocks, anything that breaks the location's [labor rule set](time-and-attendance.md#labor-rules), such as missing breaks, too few hours between shifts, a minor scheduled too late, or overtime. For predictive-scheduling rules, changes made inside the notice period are flagged along with any premium pay they cause.

## Reports

Scheduled vs actual hours, labor % of sales, sales per labor hour, overtime, forecast accuracy, schedule changes after publishing, and **skill gaps** (which additional skills would let more people cover hard-to-fill shifts).

## API & events

| Endpoint | Scope |
|---|---|
| `POST /api/v1/demand-drivers` (batch) | `scheduling:write` |
| `GET /api/v1/forecasts?locationId=…&from=…&to=…` | `scheduling:read` |
| `GET /api/v1/schedules`, `GET /api/v1/shifts` | `scheduling:read` |
| `POST /api/v1/shifts`, `PATCH /api/v1/shifts/{id}` | `scheduling:write` |
| `GET /api/v1/availability` | `scheduling:read` |

Events: `forecast.updated`, `schedule.published`, `shift.changed`, `shift.swap_requested`, `shift.swap_approved`, `open_shift.posted`.
