# Authentication

PurrOS has **one account system for everyone**: owners, managers and employees all sign in the same way, and their [role](users-and-roles.md) decides what they can do. Software (integrations and scripts) uses [API keys](#api-keys).

Company-wide settings are managed with `GET/PATCH /api/v1/settings/authentication` (Owner, or `settings.manage`), and under **Settings → Authentication** once the web app ships.

## Sign-in methods

Turn on any combination of methods:

| Method | How it works | Good for |
|---|---|---|
| **Email + password** | Invite link, then the user sets a password (at least 10 characters, not their email or a very common password). Hashed with Argon2id. | Any business |
| **Magic link** | A single-use sign-in link emailed on request, valid for 15 minutes. Needs [email](../getting-started/email-and-storage.md). | Staff who sign in rarely |
| **Passkeys** *(planned)* | Face ID, Touch ID, Windows Hello, Android or a security key (WebAuthn) | Fast, phishing-resistant sign-in on phones |
| **Single sign-on** *(planned)* | OpenID Connect or SAML 2.0 with Google Workspace, Microsoft Entra ID, Okta, Keycloak, Authentik and others | Companies with an identity provider |

> **Status:** email + password, magic links, authenticator-app 2FA with recovery codes, sessions, invitations, password reset and personal API keys work today. Passkeys, single sign-on, SCIM and breached-password checks are planned.

People without email (for example some hourly staff) can still be added as employees and have their punches sent by a timeclock integration, or use the [kiosk timeclock](../guides/time-and-attendance.md#kiosk-timeclock) once it ships. Kiosk clock codes only record punches and never sign anyone in.

## Single sign-on *(planned)*

This section describes how SSO will work.

### OpenID Connect

1. In your identity provider, create an application with the redirect URL `PURROS_URL/api/auth/callback/oidc`.
2. In PurrOS, add a provider with the issuer URL, client ID and client secret.
3. Choose how accounts are matched: by **email** (default) or by a claim such as the employee number.
4. Optionally **require SSO**. Other methods are then turned off for everyone except Owners, who keep a fallback so you can't be locked out if the identity provider fails.

### SAML 2.0

Add a SAML connection by uploading your identity provider's metadata XML or entering its metadata URL. PurrOS shows its own entity ID and ACS URL to register with the provider.

### Automatic provisioning (SCIM)

With SCIM enabled, your identity provider can create, update and deactivate PurrOS accounts automatically. You can map identity-provider groups to PurrOS roles and assignments. Deactivating someone in the identity provider signs them out of PurrOS immediately.

## Two-factor authentication

2FA is **optional** and off by default.

- Any user can turn it on from their profile with an **authenticator app** (TOTP), or a **passkey** as the second factor (planned).
- On setup, users get **10 one-time recovery codes**. Each code works once, and a used authenticator code can't be replayed.
- An admin with `settings.manage` can make 2FA **mandatory** for the whole company, and `roles.manage` can require it for a role. Affected users can only set up 2FA until they have done so.
- Sign-ins through SSO rely on the identity provider's own 2FA.

## Sessions

| Setting | Default |
|---|---|
| Idle timeout | 12 hours |
| Absolute timeout | 30 days |
| Shared-device mode timeout | 15 minutes idle |

- Sessions are stored on the server and linked by a secure, `httpOnly`, `SameSite=Lax` cookie.
- Changing a password signs out all other sessions.
- Users see their active sessions and devices (`GET /api/v1/auth/sessions`) and can sign any of them out. Admins can sign out any user with `purros users sign-out <email>`.
- **Shared-device mode**: mark a tablet or PC as shared (e.g. a back-office PC), and sessions on it time out quickly and are never remembered.

## Protection against attacks

- Sign-in attempts are rate-limited per account and per IP address. 10 wrong passwords or 2FA codes in a row lock the account for 15 minutes.
- Signing in doesn't reveal whether an account exists: unknown emails take as long as wrong passwords, and link requests always answer the same way.
- State-changing requests made with the session cookie must come from PurrOS's own address (`PURROS_URL`), so other websites can't act on a signed-in user's behalf.
- Magic links and invite links are single-use and expire.
- All sign-ins, failures, lockouts, password resets and 2FA changes are recorded in the audit log.

## API keys

| Type | Created by | Access |
|---|---|---|
| **Integration keys** | Registering an [integration](../integrations/README.md) | Only the scopes declared in its manifest |
| **Personal keys** | Users whose role has `api_keys.personal` | The user's own role and reach, never more |

- Keys are sent as `Authorization: Bearer <key>` and look like `pk_live_…`.
- Only a hash is stored. A key is shown once, when it's created.
- Keys can have an expiry date and can be revoked at any time (`DELETE /api/v1/auth/api-keys/{id}`, or `purros api-keys revoke`).
- Each key's last-used time and IP address are recorded (`purros api-keys list`).
- A personal key stops working when its user is deactivated.

See the [API overview](../api/README.md#authentication) for details.

## Recovering access

If every Owner is locked out, run on the server:

```bash
purros users sign-in-link owner@example.com [--reset-mfa]
# or, when you can't sign in with any Owner at all:
purros recover owner
```

It prints a one-time link to set a new password (or accept the invitation, for an account that never signed in), optionally removes the authenticator app and recovery codes, and records the action in the audit log. `purros setup` prints the same kind of link for the first Owner.

## API

| Endpoint | Purpose |
|---|---|
| `POST /api/v1/auth/sign-in` | Email and password. Sets the session cookie; returns `mfaRequired` when a second step is due |
| `POST /api/v1/auth/sign-in/mfa` | Authenticator or recovery code |
| `POST /api/v1/auth/sign-out` | End this session |
| `GET /api/v1/auth/session` | The signed-in person: role, permissions with reach, assignments, enabled features, 2FA state |
| `POST /api/v1/auth/magic-link`, `…/magic-link:redeem` | Request and use a magic link |
| `POST /api/v1/auth/password-reset`, `…/password-reset:complete` | Request and use a password reset link |
| `POST /api/v1/auth/invitations:accept` | Set a password from an invitation |
| `POST /api/v1/auth/password` | Change your password (signs out other sessions) |
| `GET /api/v1/auth/sessions`, `DELETE /api/v1/auth/sessions/{id}` | Your sessions and devices |
| `POST /api/v1/auth/mfa/totp:setup`, `:confirm`, `:disable`, `POST /api/v1/auth/mfa/recovery-codes:regenerate` | Authenticator app and recovery codes |
| `GET/POST /api/v1/auth/api-keys`, `DELETE /api/v1/auth/api-keys/{id}` | Your personal API keys |
| `GET/PATCH /api/v1/settings/authentication` | Magic links on or off, and company-wide 2FA (`settings.manage`) |

Links in emails point to the web app (`PURROS_URL/invitation`, `/sign-in/link`, `/reset-password`), which passes the token to these endpoints.
