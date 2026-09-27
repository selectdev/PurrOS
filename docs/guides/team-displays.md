# Team displays

Feature key: `displays`.

Turn any screen in the workplace into a live team board that shows goals and progress, recognizes good work and reminds people about shifts.

## What you need

Any screen with a web browser: a TV with a streaming stick, a smart TV browser, a tablet on the wall, or a small PC. The display is a read-only web page, so nothing needs installing.

## Pairing a screen

1. On the screen, open `PURROS_URL/display`. It shows a pairing code.
2. In PurrOS, go to **Settings → Team displays** (`displays.manage`) and choose **Add display**.
3. Enter the code, then choose the location and a **display profile**.

The screen stays signed in as a display only. It can't be used to reach any other part of PurrOS, and it can be unpaired remotely.

## Display profiles

A profile decides **what** a screen shows and **how** it rotates. Each tile can be switched on or off:

| Tile | Shows |
|---|---|
| Goals & KPIs | Today's targets and live progress, e.g. sales vs target, transactions, service times, orders shipped, checklist completion |
| Leaderboards | Rankings by team, shift or location for chosen KPIs (`displays.gamification`) |
| Recognition | Shout-outs posted by managers or peers (`recognition.give`) |
| Challenges | Team goals and fundraising drives with progress bars (`displays.gamification`) |
| Announcements | Current announcements for this location |
| Who's on | Staff currently on shift (first names only, optional) |
| Upcoming shifts | Shift reminders for the next few hours |
| Celebrations | Work anniversaries and birthdays of people who opted in (`displays.celebrations`) |
| Custom | An image, a message or an allowed web page |

## Privacy

- Displays never show pay, personal contact details or anything sensitive.
- Individual rankings can be turned off in favor of team-only rankings.
- Employees can opt out of appearing on displays in their Employee Area.
- Celebrations only show people who opted in.

## Gamification is optional

If you don't want leaderboards, challenges or recognition, switch off `displays.gamification`. Displays then show only goals, announcements and shift information. Gamification never appears in PurrOS's working screens.

## API & events

| Endpoint | Scope |
|---|---|
| `POST /api/v1/recognitions` | `communication:write` |
| `POST /api/v1/display-metrics` (custom live numbers from other systems, e.g. average service time) | `reports:write` |

Events: `recognition.posted`.
