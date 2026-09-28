# Communication

Feature key: `communication`.

> **Status:** The API handles announcements with acknowledgments and the company calendar. Messaging, direct messages, the shared files library and web push notifications are planned. Screens described below arrive with the web app; until then, use the endpoints listed at the end of this page.

Reach the right people, at the right locations, without sharing personal phone numbers or relying on outside group chats.

## Announcements

`communication.announcements` lets managers (`announcements.send`) post to:

- the whole company, a region or district, one or more locations
- a role (e.g. all supervisors) or a department

Options:

- **Read receipts**: see who has and hasn't read it.
- **Required acknowledgment**: for policies and procedures, staff must confirm they've read it, and HR can see who is outstanding.
- **Schedule** for later, and **expire** after a date.
- Attach files, images and links.

Announcements appear in the Employee Area and, if enabled, on [team displays](team-displays.md).

## Messaging

`communication.messaging` provides chat inside PurrOS:

- **Group chats** by location, team or custom group (`messages.group.manage` to create and manage them). Location groups can be kept in sync with who works there automatically.
- **Direct messages** (`communication.direct_messages`) between two people. Can be switched off if you only want group communication.
- Photos and files, mentions, and read status.
- Managers can message staff without anyone seeing personal phone numbers.

When someone leaves the company, they're removed from all conversations automatically.

## Company calendar

`communication.calendar` shows events, deadlines, deliveries, inspections, visits, promotions and holidays (`calendar.manage`), and can be filtered by location. Checklist due dates, maintenance and time off can be shown as layers.

## Shared files and links

`communication.files` is a library of manuals, policies, training material, forms and useful links (`files.manage`), organized in folders and visible according to role and location. Linked documents that need acknowledging are tracked like announcements.

## Notifications

People choose how to be notified about each kind of event, within limits set by the company:

| Channel | Notes |
|---|---|
| In-app | Always on |
| Email | Needs SMTP ([Configuration](../getting-started/configuration.md#email)) |
| Web push | Works when PurrOS is installed on the phone's home screen (PWA) |
| SMS, chat apps and others | Through an integration that handles `notification.requested` *(planned)* webhooks. See [recipes](../integrations/recipes.md#notifications-sms-chat) |

Quiet hours stop non-urgent notifications outside a person's shifts.

## API & events

| Endpoint | Scope |
|---|---|
| `GET/POST /api/v1/announcements` | `communication:read` / `communication:write` |
| `GET /api/v1/calendar-events`, `POST /api/v1/calendar-events` | `communication:read` / `communication:write` |

Events: `announcement.published`, `notification.requested` *(planned)* (for delivery integrations; needs the `notifications:deliver` scope).

Messages themselves aren't available through the API, to protect staff privacy.
