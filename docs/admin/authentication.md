# Authentication

PurrOS has **one account system for everyone**: owners, managers and employees all sign in the same way, and their [role](users-and-roles.md) decides what they can do. Software (integrations and scripts) uses [API keys](#api-keys).

Configure everything here under **Settings → Authentication** (Owner, or `settings.manage`).

## Sign-in methods

Turn on any combination of methods:

| Method | How it works | Good for |
|---|---|---|
| **Email + password** | Invite link, then the user sets a password. Hashed with Argon2id, with optional checks against breached-password lists. | Any business |
| **Magic link** | A single-use sign-in link emailed on request, valid for 15 minutes | Staff who sign in rarely |
| **Passkeys** | Face ID, Touch ID, Windows Hello, Android or a security key (WebAuthn) | Fast, phishing-resistant sign-in on phones |
| **Single sign-on** | OpenID Connect or SAML 2.0 with Google Workspace, Microsoft Entra ID, Okta, Keycloak, Authentik and others | Companies with an identity provider |

People without email (for example some hourly staff) can still be added as employees and clock in at a [kiosk timeclock](../guides/time-and-attendance.md#kiosk-timeclock). Kiosk clock codes only record punches and never sign anyone in.

## Single sign-on

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

- Any user can turn it on from their profile with an **authenticator app** (TOTP) or a **passkey** as the second factor.
- On setup, users get **one-time recovery codes**.
- An Owner can make 2FA **mandatory** for chosen roles or for the whole company. Affected users are asked to enrol at their next sign-in.
- Sign-ins through SSO rely on the identity provider's own 2FA.

## Sessions

| Setting | Default |
|---|---|
| Idle timeout | 12 hours |
| Absolute timeout | 30 days |
| Shared-device mode timeout | 15 minutes idle |

- Sessions are stored on the server and linked by a secure, `httpOnly`, `SameSite=Lax` cookie.
- Changing a password signs out all other sessions.
- Users see their active sessions and devices under **Profile → Security** and can sign any of them out. Admins with `users.manage` can sign out any user.
- **Shared-device mode**: mark a tablet or PC as shared (e.g. a back-office PC), and sessions on it time out quickly and are never remembered.

## Protection against attacks

- Sign-in attempts are rate-limited per account and per IP address, and repeated failures lock the account for a short time.
- Magic links and invite links are single-use and expire.
- All sign-ins, failures, lockouts, password resets and 2FA changes are recorded in the audit log.

## API keys

| Type | Created by | Access |
|---|---|---|
| **Integration keys** | Registering an [integration](../integrations/README.md) | Only the scopes declared in its manifest |
| **Personal keys** | Users whose role has `api_keys.personal` | The user's own role and reach, never more |

- Keys are sent as `Authorization: Bearer <key>` and look like `pk_live_…`.
- Only a hash is stored. A key is shown once, when it's created.
- Keys can have an expiry date, and can be rotated or revoked at any time.
- The admin UI shows each key's last-used time and IP address.
- A personal key stops working when its user is deactivated.

See the [API overview](../api/README.md#authentication) for details.
