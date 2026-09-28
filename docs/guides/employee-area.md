# Employee Area

Feature key: `employee_area`. Needs People & HR.

> **Status:** Everything under `/api/v1/me` works today: profile, punches and corrections, timesheets, shifts, open shifts, swaps, availability, time off, pay and estimates, payslips, documents, announcements, activity and the data export. Assigned tasks, messages and the screens themselves arrive with the web app. Screens described below arrive with the web app; until then, use the endpoints listed at the end of this page.

The Employee Area is every employee's own space in PurrOS. It shows everything the company holds about them and lets them handle everyday requests themselves. It's written for employees, so you can share this page with your staff.

## Getting in

You'll get an invitation email from your company. Open the link and set up how you sign in: a password, a passkey (Face ID or fingerprint), or your company's single sign-on. On a phone, choose **Add to Home Screen** to use PurrOS like an app and get notifications.

You only ever see **your own** information. Co-workers can't see it, and managers can only see what their role allows.

## What you can see

Your company decides which sections are available.

| Section | What's there |
|---|---|
| **My schedule** | Your upcoming shifts with location and role, open shifts you can pick up, and your swap requests |
| **My time** | Every clock-in and clock-out with where it came from, your breaks, daily and weekly totals, overtime, and whether your timesheet is pending, approved or locked |
| **My pay** | Your pay rate and its history, an **estimate** of your gross pay for each pay period, and your **payslips** if your company's payroll sends them to PurrOS |
| **Time off** | Your balances, how they build up, and your past and upcoming requests |
| **My tasks** | Checklists and forms assigned to you, onboarding steps, and policies to read and acknowledge |
| **Messages** | Announcements, team chats and direct messages |
| **My profile** | Your personal and contact details, emergency contacts, position, department, manager and location |
| **My documents** | Contracts, certificates and other files shared with you, with expiry dates |
| **Activity** | Every change made to your record: who changed what, and when |

Estimated pay is **before taxes and deductions** and is only a guide. Your payslip from payroll is the final figure.

## What you can do

- **Fix a punch.** Spotted a missed or wrong clock-in? Choose the day, then **Request correction**, and say what happened. Your manager approves or rejects it. Your original punch is always kept.
- **Request time off.** Pick the dates and type. You'll see your balance before and after, and get notified of the decision.
- **Set your availability.** Tell your manager when you can and can't work. The scheduler takes it into account.
- **Pick up or swap shifts.** Request an open shift, or offer one of yours to qualified co-workers. A manager may need to approve it.
- **Complete tasks.** Fill in checklists, report broken equipment with a photo, and acknowledge policies.
- **Update your details.** Change your phone, address and emergency contacts yourself. Some fields, like your legal name or bank details, go to HR for approval.
- **Clock-in QR code.** If your workplace uses the kiosk timeclock with QR codes, your personal code is under **My time**.
- **Download your data.** **Profile → Export my data** gives you a ZIP of everything PurrOS stores about you (profile, punches, timesheets, pay, time off, documents and history) in JSON and CSV.
- **Manage your sign-in.** Add a passkey, turn on two-factor authentication, and see and sign out other devices under **Profile → Security**.
- **Privacy on team displays.** Choose whether you appear on workplace screens and whether your work anniversary or birthday is celebrated.

## For administrators

- Sections follow [feature switches](../admin/feature-switches.md). For example, turn off `employee_area.estimated_pay` to hide pay estimates, or `employee_area.payslips` if payroll doesn't send payslips.
- Choose which profile fields employees may edit directly and which need approval under **Settings → People**.
- Payslips arrive from a payroll integration through `POST /api/v1/payslips` (`payroll:write`). See [recipes](../integrations/recipes.md#payroll).
- The Employee Area needs no role permission. Any account linked to an employee record can use it for its own data.

## API

The Employee Area is served under `/api/v1/me`, for sessions and personal keys of accounts linked to an employee record. Every endpoint only ever touches the caller's own data. The full list is in the [endpoint index](../api/endpoints.md#employee-area).

| Endpoint | Feature |
|---|---|
| `GET/PATCH /me/profile` | `employee_area`, editing needs `employee_area.profile_edit` |
| `GET /me/punches`, `GET /me/timesheets` | `time` |
| `GET/POST /me/punch-corrections`, `POST /me/punch-corrections/{id}:cancel` | `time` |
| `GET /me/shifts`, `GET /me/open-shifts`, `POST /me/shifts/{id}:claim` | `scheduling`, `scheduling.open_shifts` |
| `GET/POST /me/shift-swaps` | `scheduling.shift_swaps` |
| `GET/POST /me/availability`, `DELETE /me/availability/{id}` | `scheduling` |
| `GET /me/time-off/balances`, `GET/POST /me/time-off/requests`, `POST /me/time-off/requests/{id}:cancel` | `time.time_off` |
| `GET /me/pay` | `employee_area.estimated_pay` |
| `GET /me/payslips` | `employee_area.payslips` |
| `GET /me/documents` | `people.documents` (only documents shared with the employee) |
| `GET /me/announcements`, `POST /me/announcements/{id}:acknowledge` | `communication.announcements` |
| `GET /me/activity` | `employee_area` |
| `GET /me/export` | `employee_area.data_export` (a ZIP of JSON and CSV files) |

Managers decide punch corrections at `POST /api/v1/punch-corrections/{id}:approve` or `:reject` (`punches.correct`, within their reach). Approving adds the corrected punch and keeps the original, marked void.
