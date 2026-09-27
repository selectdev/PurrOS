# PurrOS Documentation

> **Status:** PurrOS is in early development. These docs describe the planned behavior of v1 and 1.x, and details may change before the first tagged release. See the [roadmap](../PRODUCT.md#9-roadmap) for what gets built when.

PurrOS is an open-source, self-hosted operations platform (ERP) for businesses with 20–500 people. It covers people, scheduling, time, cash, inventory, ordering, sales, checklists, equipment, communication and reporting across one location or many. Every feature is optional, and other systems connect through a REST API.

## Start here

| I want to… | Read |
|---|---|
| Install PurrOS on my own server | [Installation](getting-started/installation.md) |
| Configure email, SSO, storage | [Configuration](getting-started/configuration.md) |
| Set up my company after installing | [First-run setup](getting-started/first-run-setup.md) |
| Understand the words PurrOS uses | [Key concepts](getting-started/concepts.md) |
| Connect my POS, online store or timeclock | [Integrations](integrations/README.md) |
| Use the API | [API overview](api/README.md) |

## Getting started

- [Installation](getting-started/installation.md): requirements, Docker Compose, reverse proxy, first admin
- [Configuration](getting-started/configuration.md): every environment variable
- [First-run setup](getting-started/first-run-setup.md): business type, features, locations, roles, inviting people
- [Key concepts](getting-started/concepts.md): the terms used throughout PurrOS

## Administration

- [Organization & locations](admin/organization-and-locations.md): hierarchy, location profiles, inherited settings
- [Users, roles & permissions](admin/users-and-roles.md): accounts, custom roles, reach, safeguards
- [Permissions reference](admin/permissions-reference.md): every permission, grouped by feature
- [Authentication](admin/authentication.md): sign-in methods, SSO, 2FA, sessions
- [Feature switches](admin/feature-switches.md): turning features on and off, dependencies, data retention

## Feature guides

| Guide | For |
|---|---|
| [People & HR](guides/people-and-hr.md) | HR, managers |
| [Scheduling & forecasting](guides/scheduling-and-forecasting.md) | Managers, schedulers |
| [Time & attendance](guides/time-and-attendance.md) | Managers, payroll |
| [Cash management](guides/cash-management.md) | Managers, finance |
| [Inventory](guides/inventory.md) | Inventory and warehouse staff, managers |
| [Purchasing & ordering](guides/purchasing-and-ordering.md) | Purchasing, managers |
| [Sales](guides/sales.md) | Sales, fulfilment |
| [Forms, checklists & compliance](guides/forms-and-checklists.md) | Managers, auditors, all staff |
| [Equipment & assets](guides/equipment-and-assets.md) | Managers, maintenance |
| [Communication](guides/communication.md) | Everyone |
| [Team displays](guides/team-displays.md) | Managers |
| [Reports & insights](guides/reports-and-insights.md) | Managers, owners |
| [Employee Area](guides/employee-area.md) | Every employee |

## API

- [API overview](api/README.md): authentication, scopes, conventions, errors, pagination, rate limits, versioning
- [Endpoint index](api/endpoints.md): every resource, grouped by feature
- [Data ingestion](api/data-ingestion.md): sending data from POS systems, online stores and other services
- [Webhooks](api/webhooks.md): event catalog, payloads, signature verification, retries

## Integrations

- [Integrations overview](integrations/README.md): how integrations work, the manifest, registration
- [Build an integration](integrations/building-an-integration.md): step-by-step tutorial with the SDK
- [Integration recipes](integrations/recipes.md): POS, online store, timeclock, HR, payroll, accounting, sensors

## Operations

- [Backups & upgrades](operations/backups-and-upgrades.md)
- [Monitoring & troubleshooting](operations/monitoring.md)
- [Security](operations/security.md)
- [CLI reference](operations/cli.md)

## Contributing

- [Development guide](development/README.md): local setup, repository layout, adding a feature module, testing

## Project documents

- [PRODUCT.md](../PRODUCT.md): vision, users, scope, non-goals, roadmap
- [DESIGN.md](../DESIGN.md): architecture, data model, API design, UI system
