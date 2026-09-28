# Security

PurrOS holds sensitive data: employee details, pay, cash, and business performance. This page covers how PurrOS protects it and what you should do as the operator.

## Built in

| Area | Protection |
|---|---|
| Sign-in | Argon2id password hashing, optional or mandatory 2FA, rate limiting and lockout (passkeys and SSO planned). See [Authentication](../admin/authentication.md). |
| Sessions | Server-side sessions, secure `httpOnly` cookies, idle and absolute timeouts, remote sign-out, origin checks against cross-site requests |
| Access control | Organization-defined roles, permissions with reach, escalation protection, checked on every API request |
| Features | Switched-off features are unreachable through the API, webhooks and jobs |
| Sensitive fields | Pay data needs its own permission (`pay.read`). *(Planned: national IDs and bank details, encrypted at the column level and shown only with `employees.sensitive.read`.)* |
| Secrets | Integration config secrets, webhook secrets and TOTP secrets are encrypted with keys derived from `PURROS_SECRET`. Backups can be encrypted with their own passphrase. |
| API keys | Stored only as hashes, shown once, scoped, can expire, and record when and from where they were last used |
| Webhooks | HMAC-SHA256 signatures with timestamps, to prevent spoofing and replay |
| Integrations | Run outside PurrOS, limited to declared scopes (including which webhook events they receive), labelled in the audit log. Keys can be rotated with a grace period, and only people can manage integrations. |
| Web | Origin checks against cross-site requests, strict Content Security Policy, security headers |
| Audit log | Append-only record of every change, sign-in, permission change and feature switch, with actor, time, IP and before/after values |

## Your responsibilities as operator

- **Serve over HTTPS only**, with a valid certificate, and redirect HTTP to HTTPS.
- **Protect `PURROS_SECRET`** and the `config/` directory. Don't commit them to version control.
- **Keep PurrOS updated.** Security fixes ship as patch releases, and each release note says whether it contains security fixes.
- **Don't expose PostgreSQL or Redis** to the internet. Keep them on a private network.
- **Restrict admin tooling** at the proxy (and `/api/metrics` once metrics ship).
- **Back up and encrypt backups.** They contain everything.
- **Review access regularly**: who has Owner and HR roles, active integrations and personal API keys. `purros users list` shows each account's role, 2FA and last sign-in; `purros integrations list` (or `GET /api/v1/integrations`), `purros api-keys list` and `GET /api/v1/webhook-endpoints` show integrations, keys and where webhooks go.
- Consider **requiring 2FA** for roles with access to pay, sensitive data or cash.

## Privacy and personal data

- Employees can **export all their data** as a ZIP from the Employee Area (`GET /api/v1/me/export`).
- PurrOS sends no data anywhere you haven't configured: there is no telemetry and no AI provider today.
- *(Planned: **anonymizing** a former employee after your retention period while keeping totals for reporting; **retention settings** for kiosk photos, messages and audit details; an optional AI assistant that only sends data to a provider you configure; opt-in anonymous telemetry.)*

PurrOS gives you tools to meet privacy obligations such as GDPR or CCPA, but you, as the data controller, are responsible for configuring and using them correctly.

## Reporting a vulnerability

Please **don't** open a public issue. Use GitHub's private vulnerability reporting on the PurrOS repository (**Security → Report a vulnerability**). We aim to acknowledge reports within 3 working days.
