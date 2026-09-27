-- +goose Up
-- Sign-in for people: passwords, sessions, single-use links, 2FA, and the
-- email queue that delivers invitations and sign-in links.

ALTER TABLE company
    ADD COLUMN magic_link_enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN mfa_required       boolean NOT NULL DEFAULT false;

ALTER TABLE roles ADD COLUMN mfa_required boolean NOT NULL DEFAULT false;

ALTER TABLE users
    ADD COLUMN password_hash       text,
    ADD COLUMN password_changed_at timestamptz,
    ADD COLUMN totp_secret         bytea,        -- sealed with PURROS_SECRET
    ADD COLUMN totp_enabled_at     timestamptz,
    ADD COLUMN totp_last_step      bigint,       -- blocks replaying a code
    ADD COLUMN failed_sign_ins     integer NOT NULL DEFAULT 0,
    ADD COLUMN locked_until        timestamptz,
    ADD COLUMN last_sign_in_at     timestamptz;
CREATE UNIQUE INDEX users_email_ci_idx ON users (lower(email));

CREATE TABLE sessions (
    id             text PRIMARY KEY,
    token_hash     bytea NOT NULL UNIQUE,
    user_id        text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method         text NOT NULL,                 -- password, magic_link, invitation, password_reset
    mfa_pending    boolean NOT NULL DEFAULT false,
    shared_device  boolean NOT NULL DEFAULT false,
    ip             text,
    user_agent     text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    revoked_at     timestamptz
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE auth_tokens (
    id          text PRIMARY KEY,
    token_hash  bytea NOT NULL UNIQUE,
    user_id     text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     text NOT NULL CHECK (purpose IN ('invitation', 'magic_link', 'password_reset')),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_tokens_user_idx ON auth_tokens (user_id, purpose);

CREATE TABLE recovery_codes (
    user_id    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  bytea NOT NULL,
    used_at    timestamptz,
    PRIMARY KEY (user_id, code_hash)
);

-- Outgoing email. The body is cleared once sent; the log keeps recipient,
-- subject and kind only.
CREATE TABLE emails (
    id               text PRIMARY KEY,
    to_address       text NOT NULL,
    subject          text NOT NULL,
    kind             text NOT NULL,
    body_text        text,
    status           text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'failed')),
    attempts         integer NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    last_error       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    sent_at          timestamptz
);
CREATE INDEX emails_due_idx ON emails (next_attempt_at) WHERE status = 'queued';

-- Fields employees keep up to date themselves.
ALTER TABLE employees
    ADD COLUMN address             jsonb,
    ADD COLUMN emergency_contacts  jsonb NOT NULL DEFAULT '[]';

-- Punch corrections requested by employees and decided by managers. The
-- original punch is kept, marked void, when a correction replaces it.
ALTER TABLE punches
    ADD COLUMN voided_at  timestamptz,
    ADD COLUMN voided_by  text;

CREATE TABLE punch_corrections (
    id                text PRIMARY KEY,
    employee_id       text NOT NULL REFERENCES employees(id),
    punch_id          text REFERENCES punches(id),       -- the punch to replace; NULL for a missed punch
    type              text NOT NULL CHECK (type IN ('in', 'out', 'break_start', 'break_end')),
    at                timestamptz NOT NULL,
    location_id       text REFERENCES locations(id),
    reason            text NOT NULL,
    status            text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    decided_by        text,
    decision_note     text,
    decided_at        timestamptz,
    created_punch_id  text REFERENCES punches(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX punch_corrections_employee_idx ON punch_corrections (employee_id, status);

-- +goose Down
DROP TABLE punch_corrections;
ALTER TABLE punches DROP COLUMN voided_at, DROP COLUMN voided_by;
ALTER TABLE employees DROP COLUMN address, DROP COLUMN emergency_contacts;
DROP TABLE emails, recovery_codes, auth_tokens, sessions;
DROP INDEX users_email_ci_idx;
ALTER TABLE users DROP COLUMN password_hash, DROP COLUMN password_changed_at, DROP COLUMN totp_secret,
    DROP COLUMN totp_enabled_at, DROP COLUMN totp_last_step, DROP COLUMN failed_sign_ins, DROP COLUMN locked_until,
    DROP COLUMN last_sign_in_at;
ALTER TABLE roles DROP COLUMN mfa_required;
ALTER TABLE company DROP COLUMN magic_link_enabled, DROP COLUMN mfa_required;
