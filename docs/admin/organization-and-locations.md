# Organization & locations

The organization structure is part of the always-on platform core. It decides three things:

1. **What people can see.** Role permissions with the *assigned locations* reach follow the hierarchy.
2. **How reports roll up.** Every KPI can be viewed per location, per district, per region or company-wide.
3. **Where settings apply.** Settings made at a higher level are inherited by the locations below.

## The hierarchy

Go to **Settings → Organization**. The top node is always the company. Below it you can add as many levels as you need, and name them however your business does:

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
- Moving a location to another district keeps all its history. Reports for past periods use the structure **as it was at the time**, unless you choose "current structure" in the report options.

## Locations

Each location has a profile:

| Field | Used for |
|---|---|
| Name, code, address | Display, reports, supplier deliveries |
| Timezone | Punches, schedules, business days and reports are shown in local time |
| Opening hours and special days (holidays, closures) | Forecasting, scheduling coverage, checklist timing |
| Business day cut-off | E.g. a bar whose day ends at 04:00. Sales and cash after midnight count toward the previous day. |
| Departments or work areas | Scheduling, labor reports, the *assigned departments* reach |
| Labor rule set | Breaks, overtime and minor rules (see [Time & attendance](../guides/time-and-attendance.md#labor-rules)) |
| Currency | Defaults to the company currency |
| Status | Open, temporarily closed, or closed (archived, with history kept) |

You can rename the word "location" under **Settings → Organization → Terminology** (store, branch, site, clinic, restaurant…). The new term is used throughout the UI.

## Inherited settings

Many settings can be set at company, region, district or location level: labor rules, checklist schedules, suppliers and order days, cash thresholds, report recipients, and team display content. Each setting shows where its value comes from, for example "Inherited from Region: North". Admins can:

- **Override** a value at a lower level.
- **Reset** it to inherit again.
- **Lock** a value so lower levels can't override it, e.g. a company-wide cash over/short threshold.

## Assigning people to places

- **Employees** have a home location and department, and can be allowed to work at other locations. Those locations then appear when scheduling them.
- **Accounts** with a role that uses the *assigned locations* reach are given one or more locations **or org units**. Assigning a district gives access to every location in it, including locations added later.

See [Users, roles & permissions](users-and-roles.md).

## API

| Endpoint | Scope |
|---|---|
| `GET /api/v1/org-units`, `GET /api/v1/locations` | `organization:read` |
| `GET /api/v1/locations/{id}` (profile, hours, business day cut-off) | `organization:read` |

Locations support `externalId`, so an integration can match a POS store ID or online store warehouse ID to a PurrOS location. The structure itself is edited in the UI.
