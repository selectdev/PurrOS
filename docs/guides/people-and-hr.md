# People & HR

Feature key: `people`. Most other features build on it.

People & HR is the source of truth for **who works for you**: their details, role in the business, pay rates, skills and documents.

## Employee records

Each employee has:

| Section | Contents |
|---|---|
| Profile | Name, preferred name, photo, contact details, emergency contacts |
| Employment | Employee number, type (full-time, part-time, casual, contractor), status (active, on leave, terminated), start and end dates |
| Placement | Home location, department, position, manager, other locations they can work at |
| Pay | Pay type (hourly, salaried), rate, currency, with effective-dated history. Needs `pay.read` to view. |
| Sensitive | National ID, bank details, date of birth. Encrypted and masked, and needs `employees.sensitive.read`. |
| Skills & certifications | E.g. forklift licence, first aid, barista, cash handling, with expiry dates |
| Documents | Contracts, IDs, certificates, with expiry dates and an option to share with the employee |
| Custom fields | Any extra fields your company defines (text, number, date, choice, yes/no) |
| Activity | Every change, who made it, and when |

An employee doesn't need an account. Invite them from their profile to give them access to the [Employee Area](employee-area.md).

## Common tasks

### Add employees
- **One by one:** People → New employee.
- **In bulk:** People → Import. Upload a CSV, map the columns, fix any rows PurrOS flags, then confirm.
- **From your HR system:** through an [integration](../integrations/recipes.md#hr-and-employment-software), matched by `externalId`.

### Change pay
Add a new rate with an **effective date** rather than overwriting the old one. Past timesheets keep the rate that applied at the time. Changes need `pay.write` and are audited.

### Transfer
Changing home location, department, position or manager is a **transfer** with an effective date. History, reports and reporting lines follow the date.

### Terminate and rehire
Terminating sets an end date and reason, deactivates any account on that date, and removes the person from future schedules. Their history stays. Rehiring re-activates the same record, so their history continues.

## Skills & certifications

Define skills under **People → Skills** (`skills.manage`). You can mark a skill as:

- **Required for a position or shift type**, in which case the scheduler only assigns people who have it, and
- **Expiring**, which sends reminders to the employee and their manager 30, 14 and 1 days before expiry (configurable).

## Onboarding

Onboarding checklists (`people.onboarding`) collect everything a new hire needs: documents to upload, policies to acknowledge, forms to fill in, training to complete. The new hire sees them in their Employee Area, and HR sees progress per person.

Recruiting and applicant tracking aren't part of PurrOS. Connect your recruiting tool through an integration so hired candidates arrive as new employees.

## Settings

- **Custom fields** and required fields
- **Employment types**, termination reasons and positions
- **Document types** and default expiry reminders
- Which fields employees may edit directly in their Employee Area, and which need HR approval

## API & events

| Endpoint | Scope |
|---|---|
| `GET/POST /api/v1/employees`, `GET/PATCH /api/v1/employees/{id}` | `people:read` / `people:write` |
| `PUT /api/v1/employees/external/{externalId}` (upsert) | `people:write` |
| `POST /api/v1/employees/{id}:terminate`, `:rehire`, `:transfer` | `people:write` |
| `GET/POST /api/v1/employees/{id}/pay-rates` | `payroll:read` / `payroll:write` |
| `GET/POST /api/v1/employees/{id}/documents` | `people:read` / `people:write` |
| `GET /api/v1/skills`, `POST /api/v1/employees/{id}/skills` | `people:read` / `people:write` |

Events: `employee.created`, `employee.updated`, `employee.transferred`, `employee.terminated`, `employee.archived`, `pay_rate.changed`, `document.expiring`, `certification.expiring`. See [Webhooks](../api/webhooks.md).
