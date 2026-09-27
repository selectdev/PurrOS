# Key concepts

The terms used throughout PurrOS and its documentation.

## Organization

| Term | Meaning |
|---|---|
| **Company** | The business using PurrOS. One install serves one company. |
| **Hierarchy** | How the company is organized, e.g. company → region → district → location. You choose the levels and their names. |
| **Org unit** | Any node in the hierarchy above a location, such as a region or a district. |
| **Location** | A place where work happens: a store, branch, site, clinic, warehouse or office. You can rename the term. |
| **Department** | A work area within locations, e.g. Kitchen, Front desk, Warehouse, Service. |
| **Inherited setting** | A setting defined at company or org-unit level that locations use unless they override it. |

## People and access

| Term | Meaning |
|---|---|
| **Employee** | A person who works for the company. Employees don't need an account. |
| **Account (user)** | A sign-in. An account is usually linked to an employee record, but not always (e.g. an external accountant). |
| **Role** | A named set of permissions defined by your company, such as HR, District Manager or Supervisor. Each account has exactly one role. |
| **Permission** | A single thing a role allows, like `timesheets.approve`. PurrOS defines the list. |
| **Reach** | How far a permission extends: *own team*, *assigned locations*, *assigned departments* or *everyone*. |
| **Owner** | The system role with every permission. Only Owners can switch features on or off. |
| **Employee Area** | The self-service portal every employee has for their own data. |

## Features

| Term | Meaning |
|---|---|
| **Feature** | A part of PurrOS that can be switched on or off, such as Cash Management or the kiosk timeclock. |
| **Platform core** | The always-on parts: accounts, roles, locations, settings, audit log, API. |

## Time and scheduling

| Term | Meaning |
|---|---|
| **Punch** | A single clock-in, clock-out or break event. Punches are never edited, only corrected with a new record. |
| **Timesheet** | An employee's hours for a pay period, built from punches and approved by a manager, then by payroll. |
| **Pay period** | The span of dates paid together. Once *locked*, its timesheets can't change. |
| **Demand driver** | A number that decides how many staff you need, like sales, transactions, orders, appointments or foot traffic. |
| **Forecast** | Predicted demand drivers for each hour or day. |
| **Staffing rule** | Turns forecast demand into staff needed, e.g. "1 cashier per 40 transactions an hour". |
| **Labor rule set** | Break, overtime, rest and minor rules for a location or jurisdiction. |

## Money and stock

| Term | Meaning |
|---|---|
| **Business day** | A location's trading day, which can end after midnight. Sales and cash are grouped by business day. |
| **Tender** | A way of paying: cash, card, gift card, voucher, a third-party platform… |
| **Over/short** | The difference between the cash counted and the cash expected. |
| **Stock ledger** | The permanent list of every stock movement. Stock on hand is always the sum of the ledger. |
| **Usage recipe** | What one sold product or service consumes, e.g. a latte uses 18 g coffee, 250 ml milk and 1 cup. |
| **Expected (theoretical) usage** | What should have been used according to sales and usage recipes. |
| **Actual usage** | What was really used according to counts: opening stock + received − closing stock. |
| **Variance / gain-loss** | Actual minus expected usage, valued at cost. |
| **Par level** | The stock level a location wants on hand after a delivery. |

## Integration and API

| Term | Meaning |
|---|---|
| **Integration** | A separate program you run that connects another system (POS, online store, HR, payroll…) to PurrOS through the API. |
| **Scope** | What an integration's API key is allowed to do, e.g. `sales:write`. |
| **External ID** | The ID a record has in another system. PurrOS stores it so integrations can find and update records without keeping their own mapping. |
| **Source** | A label for where ingested data came from, e.g. `pos:front-counter` or `web-store`. |
| **Webhook** | A message PurrOS sends to another system when something happens. |
