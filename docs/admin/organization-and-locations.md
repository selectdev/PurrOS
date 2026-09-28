# Organization & locations

The organization structure is part of the always-on platform core. It decides three things:

1. **What people can see.** Role permissions with the *assigned locations* reach follow the hierarchy.
2. **How reports roll up.** Every KPI can be viewed per location, per district, per region or company-wide.
3. **Where settings apply** *(planned)*. Settings made at a higher level are inherited by the locations below.

> **Status:** org units, locations and departments are managed through the [API](#api) (permission `organization.manage`, scope `organization:write`), and locations also with `purros locations create`. **Settings → Organization** in the web app, opening hours, terminology, inherited settings and historical structure in reports are planned.

## The hierarchy

The top node is always the company. Below it you add **org units** (regions, districts, areas…), as many levels as you need, named however your business does. Each org unit has a `levelName` (e.g. "Region") and an optional `parentId`:

```
Company
├── Region: North
│   ├── District: North-East
│   │   ├── Location: Store 101
│   │   └── Location: Store 102
│   └── District: North-West
│       └── Location: Store 110
└── Region: South
    └── Location: Distribution Center
```

- A single-location business simply has **Company → one location**.
- Levels don't need to be even. A warehouse can sit directly under a region while stores sit under districts.
- Moving a location or an org unit to another parent keeps all its history. An org unit can't be placed under itself or one of its own units. *(Planned: reports for past periods using the structure as it was at the time.)*
- An org unit can only be **archived** once it contains no active org units or locations. Archived units are hidden from lists unless you ask for `includeArchived=true`.

Building the example above with the API:

```bash
curl -X POST https://erp.example.com/api/v1/org-units -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"levelName": "Region", "name": "North", "externalId": "north"}'
# → {"id": "org_01J…", …}
curl -X POST https://erp.example.com/api/v1/org-units -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"levelName": "District", "name": "North-East", "parentId": "org_01J…"}'
curl -X POST https://erp.example.com/api/v1/locations -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"name": "Store 101", "code": "101", "externalId": "101", "orgUnitId": "org_01J…", "timezone": "America/Chicago", "businessDayCutoff": "04:00"}'
```

## Locations

Each location has a profile:

| Field | Used for |
|---|---|
| Name, code, address | Display, reports, supplier deliveries |
| Org unit | Where it sits in the hierarchy |
| Timezone | Punches, schedules, business days and reports are shown in local time |
| Business day cut-off | E.g. a bar whose day ends at 04:00. Sales and cash after midnight count toward the previous day. |
| Currency | Defaults to the company's currency (and the time zone to the company's) |
| Status | `open`, `temporarily_closed` or `closed`. Archiving a location (`DELETE /locations/{id}`) closes it and hides it from lists; its history is kept. |
| External ID | The location's ID in your POS or other systems |
| Opening hours and special days *(planned)* | Forecasting, scheduling coverage, checklist timing |

Departments (work areas) are company-wide (`/departments`) and used by scheduling, labor reports and the *assigned departments* reach. Labor rule sets are managed in [Time & attendance](../guides/time-and-attendance.md#labor-rules).

*(Planned: renaming the word "location" to store, branch, site, clinic, restaurant… throughout the web app.)*

## Inherited settings *(planned)*

Many settings can be set at company, region, district or location level: labor rules, checklist schedules, suppliers and order days, cash thresholds, report recipients, and team display content. Each setting shows where its value comes from, for example "Inherited from Region: North". Admins can:

- **Override** a value at a lower level.
- **Reset** it to inherit again.
- **Lock** a value so lower levels can't override it, e.g. a company-wide cash over/short threshold.

## Assigning people to places

- **Employees** have a home location and department, and can be allowed to work at other locations. Those locations then appear when scheduling them.
- **Accounts** with a role that uses the *assigned locations* reach are given one or more locations **or org units**. Assigning a district gives access to every location in it, including locations added later.

See [Users, roles & permissions](users-and-roles.md).

## Who can change the structure

The `organization.manage` permission (scope `organization:write` for integration keys):

- With reach **Everyone**, it covers everything: org units, departments, and creating locations.
- With a narrower reach (e.g. *assigned locations*), it lets someone edit and archive **only their own locations' profiles**, for example a store manager keeping their store's address and status up to date.

## Events

Webhooks for changes to the structure (integrations need `organization:read`): `org_unit.created`, `org_unit.updated`, `org_unit.archived`, `location.created`, `location.updated`, `location.archived`, `department.created`, `department.updated`, `department.archived`. Locations created with `purros locations create` send `location.created` too. See [Webhooks](../api/webhooks.md).

## API

| Endpoint | Scope | Permission |
|---|---|---|
| `GET /api/v1/org-units`, `…/{id}`, `…/external/{externalId}` | `organization:read` | anyone |
| `POST /api/v1/org-units`, `PATCH …/{id}`, `PUT …/external/{externalId}`, `DELETE …/{id}` | `organization:write` | `organization.manage` (Everyone) |
| `GET /api/v1/locations` (`orgUnitId`, `status`, `includeArchived` filter), `…/{id}`, `…/external/{externalId}` | `organization:read` | anyone |
| `POST /api/v1/locations`, `PUT …/external/{externalId}` | `organization:write` | `organization.manage` (Everyone) |
| `PATCH /api/v1/locations/{id}`, `DELETE …/{id}` | `organization:write` | `organization.manage` (within reach) |
| `GET /api/v1/departments`, `…/{id}`, `…/external/{externalId}` | `organization:read` | anyone |
| `POST /api/v1/departments`, `PATCH …/{id}`, `PUT …/external/{externalId}`, `DELETE …/{id}` | `organization:write` | `organization.manage` (Everyone) |

All of them support `externalId`, so an HR system or POS integration can sync its store, region and department IDs with `PUT …/external/{externalId}`. `PATCH` is a merge patch and accepts `If-Match` with a location's `version`. With the CLI: `purros locations create --name … [--code] [--external-id] [--timezone] [--currency] [--cutoff]` and `purros locations list`.
