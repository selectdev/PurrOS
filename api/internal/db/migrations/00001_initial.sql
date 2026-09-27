-- +goose Up

-- ---------------------------------------------------------------------------
-- Platform
-- ---------------------------------------------------------------------------

CREATE TABLE company (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    currency    char(3) NOT NULL DEFAULT 'USD',
    timezone    text NOT NULL DEFAULT 'UTC',
    locale      text NOT NULL DEFAULT 'en',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE feature_settings (
    key         text PRIMARY KEY,
    enabled     boolean NOT NULL,
    changed_by  text,
    changed_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE roles (
    id          text PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    system_key  text UNIQUE,              -- 'owner' | 'employee' for system roles
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE role_permissions (
    role_id     text NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission  text NOT NULL,
    reach       text NOT NULL CHECK (reach IN ('own_team', 'assigned_locations', 'assigned_departments', 'everyone')),
    PRIMARY KEY (role_id, permission)
);

CREATE TABLE integrations (
    id                text PRIMARY KEY,
    name              text NOT NULL UNIQUE,
    display_name      text NOT NULL,
    version           text NOT NULL DEFAULT '',
    description       text NOT NULL DEFAULT '',
    homepage          text NOT NULL DEFAULT '',
    scopes            text[] NOT NULL DEFAULT '{}',
    manifest          jsonb NOT NULL,
    config_encrypted  bytea,
    status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused')),
    health_status     text,
    health_message    text,
    last_heartbeat_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE integration_logs (
    id              text PRIMARY KEY,
    integration_id  text NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    level           text NOT NULL DEFAULT 'info',
    message         text NOT NULL,
    data            jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX integration_logs_integration_idx ON integration_logs (integration_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Organization
-- ---------------------------------------------------------------------------

CREATE TABLE org_units (
    id          text PRIMARY KEY,
    parent_id   text REFERENCES org_units(id),
    level_name  text NOT NULL,             -- e.g. 'Region', 'District'
    name        text NOT NULL,
    external_id text UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);

CREATE TABLE locations (
    id                   text PRIMARY KEY,
    org_unit_id          text REFERENCES org_units(id),
    name                 text NOT NULL,
    code                 text,
    external_id          text UNIQUE,
    timezone             text NOT NULL DEFAULT 'UTC',
    currency             char(3) NOT NULL DEFAULT 'USD',
    business_day_cutoff  interval NOT NULL DEFAULT '0 hours',
    address              jsonb,
    status               text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'temporarily_closed', 'closed')),
    version              integer NOT NULL DEFAULT 1,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    archived_at          timestamptz
);

CREATE TABLE departments (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    external_id text UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);

-- ---------------------------------------------------------------------------
-- People
-- ---------------------------------------------------------------------------

CREATE TABLE employees (
    id                text PRIMARY KEY,
    external_id       text UNIQUE,
    employee_number   text UNIQUE,
    first_name        text NOT NULL,
    last_name         text NOT NULL,
    preferred_name    text,
    email             text,
    phone             text,
    employment_type   text NOT NULL DEFAULT 'full_time'
                      CHECK (employment_type IN ('full_time', 'part_time', 'casual', 'contractor')),
    status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'on_leave', 'terminated')),
    start_date        date,
    end_date          date,
    termination_reason text,
    home_location_id  text REFERENCES locations(id),
    department_id     text REFERENCES departments(id),
    position          text,
    manager_id        text REFERENCES employees(id),
    custom_fields     jsonb NOT NULL DEFAULT '{}',
    integration_data  jsonb NOT NULL DEFAULT '{}',
    version           integer NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    archived_at       timestamptz
);
CREATE INDEX employees_updated_idx ON employees (updated_at, id);
CREATE INDEX employees_location_idx ON employees (home_location_id);

CREATE TABLE users (
    id           text PRIMARY KEY,
    email        text NOT NULL UNIQUE,
    name         text NOT NULL,
    role_id      text NOT NULL REFERENCES roles(id),
    employee_id  text UNIQUE REFERENCES employees(id),
    status       text NOT NULL DEFAULT 'active' CHECK (status IN ('invited', 'active', 'deactivated')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_location_assignments (
    user_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    location_id  text REFERENCES locations(id),
    org_unit_id  text REFERENCES org_units(id),
    CHECK ((location_id IS NULL) <> (org_unit_id IS NULL))
);

CREATE TABLE user_department_assignments (
    user_id        text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    department_id  text NOT NULL REFERENCES departments(id),
    PRIMARY KEY (user_id, department_id)
);

CREATE TABLE api_keys (
    id              text PRIMARY KEY,
    kind            text NOT NULL CHECK (kind IN ('integration', 'personal')),
    display_prefix  text NOT NULL,
    hash            bytea NOT NULL UNIQUE,
    integration_id  text REFERENCES integrations(id) ON DELETE CASCADE,
    user_id         text REFERENCES users(id) ON DELETE CASCADE,
    name            text NOT NULL DEFAULT '',
    expires_at      timestamptz,
    revoked_at      timestamptz,
    last_used_at    timestamptz,
    last_used_ip    text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'integration' AND integration_id IS NOT NULL) OR (kind = 'personal' AND user_id IS NOT NULL))
);

-- ---------------------------------------------------------------------------
-- Cross-cutting: audit, idempotency, outbox, webhooks, ingestion
-- ---------------------------------------------------------------------------

CREATE TABLE audit_log (
    id           text PRIMARY KEY,
    actor_type   text NOT NULL,           -- user | integration | api_key | system | employee
    actor_id     text,
    actor_name   text,
    action       text NOT NULL,           -- e.g. employee.create
    entity_type  text,
    entity_id    text,
    before       jsonb,
    after        jsonb,
    ip           text,
    request_id   text,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_entity_idx ON audit_log (entity_type, entity_id, created_at DESC);
CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);

CREATE TABLE idempotency_records (
    api_key_id     text NOT NULL,
    key            text NOT NULL,
    method         text NOT NULL,
    path           text NOT NULL,
    request_hash   bytea NOT NULL,
    status_code    integer NOT NULL,
    response_body  bytea NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (api_key_id, key)
);
CREATE INDEX idempotency_created_idx ON idempotency_records (created_at);

CREATE TABLE outbox_events (
    id             text PRIMARY KEY,
    type           text NOT NULL,
    feature        text NOT NULL,
    location_id    text,
    ordering_key   text NOT NULL DEFAULT '',   -- events with the same key are delivered in order
    actor          jsonb NOT NULL,
    payload        jsonb NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    dispatched_at  timestamptz
);
CREATE INDEX outbox_pending_idx ON outbox_events (created_at) WHERE dispatched_at IS NULL;

CREATE TABLE webhook_endpoints (
    id                text PRIMARY KEY,
    integration_id    text REFERENCES integrations(id) ON DELETE CASCADE,
    url               text NOT NULL,
    secret_encrypted  bytea NOT NULL,
    events            text[] NOT NULL,
    status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    consecutive_failures integer NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE webhook_deliveries (
    id                text PRIMARY KEY,
    endpoint_id       text NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    event_id          text NOT NULL REFERENCES outbox_events(id),
    ordering_key      text NOT NULL DEFAULT '',
    status            text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed')),
    attempts          integer NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    last_status_code  integer,
    last_error        text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    delivered_at      timestamptz,
    UNIQUE (endpoint_id, event_id)
);
CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX webhook_deliveries_order_idx ON webhook_deliveries (endpoint_id, ordering_key, event_id) WHERE status = 'pending';

CREATE TABLE ingest_batches (
    id          text PRIMARY KEY,
    api_key_id  text,
    integration_id text,
    kind        text NOT NULL,
    source      text NOT NULL,
    received    integer NOT NULL,
    created     integer NOT NULL,
    updated     integer NOT NULL,
    rejected    integer NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Time
-- ---------------------------------------------------------------------------

CREATE TABLE punches (
    id           text PRIMARY KEY,
    employee_id  text NOT NULL REFERENCES employees(id),
    type         text NOT NULL CHECK (type IN ('in', 'out', 'break_start', 'break_end')),
    at           timestamptz NOT NULL,
    location_id  text REFERENCES locations(id),
    device_id    text NOT NULL DEFAULT '',
    source       text NOT NULL,
    external_id  text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (employee_id, type, at, device_id)
);
CREATE INDEX punches_employee_at_idx ON punches (employee_id, at);
CREATE INDEX punches_created_idx ON punches (created_at, id);

-- ---------------------------------------------------------------------------
-- Inventory (items, used for sales mapping in this release)
-- ---------------------------------------------------------------------------

CREATE TABLE items (
    id           text PRIMARY KEY,
    sku          text NOT NULL UNIQUE,
    name         text NOT NULL,
    barcode      text UNIQUE,
    category     text,
    base_unit    text NOT NULL DEFAULT 'each',
    external_id  text UNIQUE,
    version      integer NOT NULL DEFAULT 1,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    archived_at  timestamptz
);
CREATE INDEX items_updated_idx ON items (updated_at, id);

-- Links an item reference seen in a sales feed to a PurrOS item.
CREATE TABLE item_mappings (
    source        text NOT NULL,
    external_ref  text NOT NULL,
    item_id       text NOT NULL REFERENCES items(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source, external_ref)
);

-- ---------------------------------------------------------------------------
-- Sales feeds
-- ---------------------------------------------------------------------------

CREATE TABLE sales_transactions (
    id             text PRIMARY KEY,
    source         text NOT NULL,
    external_id    text NOT NULL,
    location_id    text NOT NULL REFERENCES locations(id),
    business_date  date NOT NULL,
    occurred_at    timestamptz NOT NULL,
    type           text NOT NULL CHECK (type IN ('sale', 'refund')),
    status         text NOT NULL DEFAULT 'completed' CHECK (status IN ('completed', 'voided')),
    channel        text,
    register_id    text,
    employee_id    text REFERENCES employees(id),
    refund_of      text,
    subtotal       numeric(19,4),
    tax            numeric(19,4) NOT NULL DEFAULT 0,
    total          numeric(19,4) NOT NULL,
    currency       char(3) NOT NULL,
    tenders        jsonb NOT NULL DEFAULT '[]',
    batch_id       text,
    version        integer NOT NULL DEFAULT 1,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);
CREATE INDEX sales_transactions_loc_date_idx ON sales_transactions (location_id, business_date);
CREATE INDEX sales_transactions_updated_idx ON sales_transactions (updated_at, id);

CREATE TABLE sales_transaction_lines (
    transaction_id    text NOT NULL REFERENCES sales_transactions(id) ON DELETE CASCADE,
    line_no           integer NOT NULL,
    item_id           text REFERENCES items(id),
    item_sku          text,
    item_barcode      text,
    item_external_id  text,
    name              text,
    quantity          numeric(18,6) NOT NULL,
    unit_price        numeric(19,4) NOT NULL,
    discount          numeric(19,4) NOT NULL DEFAULT 0,
    tax               numeric(19,4) NOT NULL DEFAULT 0,
    PRIMARY KEY (transaction_id, line_no)
);
CREATE INDEX sales_lines_unmapped_idx ON sales_transaction_lines (transaction_id) WHERE item_id IS NULL;

CREATE TABLE sales_summaries (
    id             text PRIMARY KEY,
    source         text NOT NULL,
    external_id    text NOT NULL,
    location_id    text NOT NULL REFERENCES locations(id),
    business_date  date NOT NULL,
    period_start   timestamptz NOT NULL,
    period_end     timestamptz NOT NULL,
    net_sales      numeric(19,4) NOT NULL,
    gross_sales    numeric(19,4),
    discounts      numeric(19,4),
    tax            numeric(19,4),
    transactions   integer,
    guests         integer,
    by_channel     jsonb NOT NULL DEFAULT '[]',
    currency       char(3) NOT NULL,
    batch_id       text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id),
    CHECK (period_end > period_start)
);
CREATE INDEX sales_summaries_loc_date_idx ON sales_summaries (location_id, business_date);

-- +goose Down
DROP TABLE sales_summaries, sales_transaction_lines, sales_transactions, item_mappings, items, punches,
    ingest_batches, webhook_deliveries, webhook_endpoints, outbox_events, idempotency_records, audit_log,
    api_keys, user_department_assignments, user_location_assignments, users, employees, departments,
    locations, org_units, integration_logs, integrations, role_permissions, roles, feature_settings, company;
