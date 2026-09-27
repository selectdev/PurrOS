# Security

PurrOS holds sensitive data: employee details, pay, cash, and business performance. This page covers how PurrOS protects it and what you should do as the operator.

## Built in

| Area | Protection |
|---|---|
| Sign-in | Argon2id password hashing, optional or mandatory 2FA, rate limiting and lockout (passkeys and SSO planned). See [Authentication](../admin/authentication.md). |
| Sessions | Server-side sessions, secure `httpOnly` cookies, idle and absolute timeouts, remote sign-out, origin checks against cross-site requests |
| Access control | Organization-defined roles, permissions with reach, escalation protection, checked in the service layer (not just the UI) |
| Features | Switched-off features are unreachable through the UI, API, webhooks and jobs |
| Sensitive fields | National IDs, bank details and similar fields are encrypted at the column level and masked unless the viewer has explicit permission |
| Secrets | Integration config secrets, webhook secrets and SSO credentials are encrypted with keys derived from `PURROS_SECRET` |
| API keys | Stored only as hashes, shown once, scoped, can expire, and show when they were last used |
| Webhooks | HMAC-SHA256 signatures with timestamps, to prevent spoofing and replay |
| Integrations | Run outside PurrOS, limited to declared scopes, labelled in the audit log |
| Web | CSRF protection, strict Content Security Policy, security headers |
| Audit log | Append-only record of every change, sign-in, permission change and feature switch, with actor, time, IP and before/after values |

## Your responsibilities as operator

- **Serve over HTTPS only**, with a valid certificate, and redirect HTTP to HTTPS.
- **Protect `PURROS_SECRET`** and the `.env` file. Don't commit them to version control.
- **Keep PurrOS updated.** Security fixes ship as patch releases, and each release note says whether it contains security fixes.
- **Don't expose PostgreSQL or Redis** to the internet. Keep them on a private network.
- **Restrict `/api/metrics`** and any admin tooling at the proxy.
- **Back up and encrypt backups.** They contain everything.
- **Review access regularly**: who has Owner and HR roles, active integrations and personal API keys. **Settings → Users** can list accounts that haven't signed in for 90 days.
- Consider **requiring 2FA** for roles with access to pay, sensitive data or cash.

## Privacy and personal data

- Employees can **export all their data** from the Employee Area.
- Admins can **anonymize** a former employee after your retention period. This removes personal details while keeping totals intact for reporting and accounting.
- **Retention settings** control how long kiosk photos, messages and audit details are kept.
- The optional AI assistant is off by default and only sends data to a provider you configure. You can choose a self-hosted model.
- Anonymous telemetry is **opt-in** (`PURROS_TELEMETRY`).

PurrOS gives you tools to meet privacy obligations such as GDPR or CCPA, but you, as the data controller, are responsible for configuring and using them correctly.

## Reporting a vulnerability

Please **don't** open a public issue. Use GitHub's private vulnerability reporting on the PurrOS repository (**Security → Report a vulnerability**). We aim to acknowledge reports within 3 working days.
