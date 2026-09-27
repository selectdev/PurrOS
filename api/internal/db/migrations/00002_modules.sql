-- +goose Up

-- ---------------------------------------------------------------------------
-- Company settings used by modules
-- ---------------------------------------------------------------------------
ALTER TABLE company
    ADD COLUMN po_approval_limit numeric(19,4),                -- POs above this need approval (NULL = never)
    ADD COLUMN over_short_tolerance numeric(19,4) NOT NULL DEFAULT 5,
    ADD COLUMN pay_period_length_days integer NOT NULL DEFAULT 14;

-- ---------------------------------------------------------------------------
-- People
-- ---------------------------------------------------------------------------
CREATE TABLE employee_documents (
    id            text PRIMARY KEY,
    employee_id   text NOT NULL REFERENCES employees(id),
    type          text NOT NULL,
    name          text NOT NULL,
    url           text,
    expires_on    date,
    shared_with_employee boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    archived_at   timestamptz
);
CREATE INDEX employee_documents_employee_idx ON employee_documents (employee_id);

CREATE TABLE skills (
    id            text PRIMARY KEY,
    name          text NOT NULL UNIQUE,
    category      text,
    valid_days    integer,                       -- certificates expire after this many days
    external_id   text UNIQUE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    archived_at   timestamptz
);

CREATE TABLE employee_skills (
    employee_id   text NOT NULL REFERENCES employees(id),
    skill_id      text NOT NULL REFERENCES skills(id),
    obtained_on   date,
    expires_on    date,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (employee_id, skill_id)
);

CREATE TABLE pay_rates (
    id              text PRIMARY KEY,
    employee_id     text NOT NULL REFERENCES employees(id),
    pay_type        text NOT NULL CHECK (pay_type IN ('hourly', 'salary')),
    rate            numeric(19,4) NOT NULL CHECK (rate >= 0),
    currency        char(3) NOT NULL,
    effective_from  date NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (employee_id, effective_from)
);

CREATE TABLE payslips (
    id            text PRIMARY KEY,
    source        text NOT NULL,
    external_id   text NOT NULL,
    employee_id   text NOT NULL REFERENCES employees(id),
    period_start  date NOT NULL,
    period_end    date NOT NULL,
    pay_date      date,
    gross         numeric(19,4) NOT NULL,
    net           numeric(19,4),
    currency      char(3) NOT NULL,
    url           text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);

-- ---------------------------------------------------------------------------
-- Time
-- ---------------------------------------------------------------------------
CREATE TABLE labor_rule_sets (
    id                        text PRIMARY KEY,
    name                      text NOT NULL UNIQUE,
    rounding_minutes          integer NOT NULL DEFAULT 0 CHECK (rounding_minutes BETWEEN 0 AND 60),
    daily_overtime_minutes    integer,             -- NULL = no daily overtime
    weekly_overtime_minutes   integer DEFAULT 2400,  -- 40 h
    overtime_multiplier       numeric(6,3) NOT NULL DEFAULT 1.5,
    break_required_after_minutes integer,           -- flag a missing break after this long
    min_rest_minutes          integer,
    early_clock_in_minutes    integer NOT NULL DEFAULT 10,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE locations ADD COLUMN labor_rule_set_id text REFERENCES labor_rule_sets(id);

CREATE TABLE pay_periods (
    id          text PRIMARY KEY,
    start_date  date NOT NULL,
    end_date    date NOT NULL,
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'locked')),
    locked_at   timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (start_date),
    CHECK (end_date >= start_date)
);

CREATE TABLE timesheets (
    id                text PRIMARY KEY,
    employee_id       text NOT NULL REFERENCES employees(id),
    pay_period_id     text NOT NULL REFERENCES pay_periods(id),
    status            text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'approved', 'rejected', 'locked')),
    worked_minutes    integer NOT NULL DEFAULT 0,
    regular_minutes   integer NOT NULL DEFAULT 0,
    overtime_minutes  integer NOT NULL DEFAULT 0,
    break_minutes     integer NOT NULL DEFAULT 0,
    days              jsonb NOT NULL DEFAULT '[]',
    exceptions        jsonb NOT NULL DEFAULT '[]',
    note              text,
    approved_at       timestamptz,
    version           integer NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (employee_id, pay_period_id)
);

CREATE TABLE time_off_types (
    id          text PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    paid        boolean NOT NULL DEFAULT true,
    external_id text UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);

CREATE TABLE time_off_requests (
    id            text PRIMARY KEY,
    employee_id   text NOT NULL REFERENCES employees(id),
    type_id       text NOT NULL REFERENCES time_off_types(id),
    start_date    date NOT NULL,
    end_date      date NOT NULL,
    hours         numeric(10,2) NOT NULL CHECK (hours > 0),
    status        text NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'approved', 'rejected', 'cancelled')),
    note          text,
    decision_note text,
    decided_at    timestamptz,
    version       integer NOT NULL DEFAULT 1,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (end_date >= start_date)
);

-- Balance = sum of entries (accruals positive, approved requests negative).
CREATE TABLE time_off_ledger (
    id            text PRIMARY KEY,
    employee_id   text NOT NULL REFERENCES employees(id),
    type_id       text NOT NULL REFERENCES time_off_types(id),
    hours         numeric(10,2) NOT NULL,
    reason        text NOT NULL,
    request_id    text REFERENCES time_off_requests(id),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX time_off_ledger_emp_idx ON time_off_ledger (employee_id, type_id);

-- ---------------------------------------------------------------------------
-- Scheduling
-- ---------------------------------------------------------------------------
CREATE TABLE demand_drivers (
    id            text PRIMARY KEY,
    source        text NOT NULL,
    external_id   text NOT NULL,
    location_id   text NOT NULL REFERENCES locations(id),
    driver        text NOT NULL,
    period_start  timestamptz NOT NULL,
    period_end    timestamptz NOT NULL,
    value         numeric(19,4) NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id),
    CHECK (period_end > period_start)
);
CREATE INDEX demand_drivers_loc_idx ON demand_drivers (location_id, driver, period_start);

CREATE TABLE forecast_adjustments (
    id           text PRIMARY KEY,
    location_id  text NOT NULL REFERENCES locations(id),
    driver       text NOT NULL,
    date         date NOT NULL,
    percent      numeric(7,2) NOT NULL,
    note         text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (location_id, driver, date)
);

CREATE TABLE staffing_rules (
    id             text PRIMARY KEY,
    location_id    text NOT NULL REFERENCES locations(id),
    department_id  text REFERENCES departments(id),
    driver         text,                           -- NULL for fixed minimums
    units_per_staff numeric(19,4),                 -- e.g. 40 transactions per staff-hour
    min_staff      integer NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    archived_at    timestamptz
);

CREATE TABLE shifts (
    id             text PRIMARY KEY,
    location_id    text NOT NULL REFERENCES locations(id),
    department_id  text REFERENCES departments(id),
    employee_id    text REFERENCES employees(id),   -- NULL = open shift
    position       text,
    starts_at      timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    break_minutes  integer NOT NULL DEFAULT 0,
    status         text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'cancelled')),
    notes          text,
    external_id    text UNIQUE,
    published_at   timestamptz,
    version        integer NOT NULL DEFAULT 1,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at)
);
CREATE INDEX shifts_loc_time_idx ON shifts (location_id, starts_at);
CREATE INDEX shifts_emp_time_idx ON shifts (employee_id, starts_at);

CREATE TABLE availability (
    id            text PRIMARY KEY,
    employee_id   text NOT NULL REFERENCES employees(id),
    weekday       integer NOT NULL CHECK (weekday BETWEEN 0 AND 6),   -- 0 = Sunday
    start_time    time NOT NULL,
    end_time      time NOT NULL,
    kind          text NOT NULL DEFAULT 'available' CHECK (kind IN ('available', 'unavailable', 'preferred')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    archived_at   timestamptz,
    CHECK (end_time > start_time)
);

CREATE TABLE shift_swap_requests (
    id               text PRIMARY KEY,
    shift_id         text NOT NULL REFERENCES shifts(id),
    from_employee_id text REFERENCES employees(id),
    to_employee_id   text NOT NULL REFERENCES employees(id),
    status           text NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'approved', 'rejected')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    decided_at       timestamptz
);

-- ---------------------------------------------------------------------------
-- Inventory
-- ---------------------------------------------------------------------------
ALTER TABLE items ADD COLUMN default_cost numeric(19,4);
ALTER TABLE items ADD COLUMN track_stock boolean NOT NULL DEFAULT true;

-- Append-only ledger. Stock on hand is always the sum of movements.
CREATE TABLE stock_movements (
    id           text PRIMARY KEY,
    item_id      text NOT NULL REFERENCES items(id),
    location_id  text NOT NULL REFERENCES locations(id),
    quantity     numeric(18,6) NOT NULL,
    type         text NOT NULL CHECK (type IN ('receipt', 'sale', 'shipment', 'transfer_out', 'transfer_in',
                                               'waste', 'count', 'adjustment', 'reversal')),
    unit_cost    numeric(19,4),
    reason       text,
    note         text,
    source_type  text,
    source_id    text,
    occurred_at  timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX stock_movements_item_loc_idx ON stock_movements (item_id, location_id, occurred_at);
CREATE INDEX stock_movements_source_idx ON stock_movements (source_type, source_id);

CREATE TABLE stock_levels (
    item_id        text NOT NULL REFERENCES items(id),
    location_id    text NOT NULL REFERENCES locations(id),
    on_hand        numeric(18,6) NOT NULL DEFAULT 0,
    reserved       numeric(18,6) NOT NULL DEFAULT 0,
    avg_cost       numeric(19,4),
    par_level      numeric(18,6),
    reorder_point  numeric(18,6),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (item_id, location_id)
);

CREATE TABLE usage_recipes (
    item_id            text NOT NULL REFERENCES items(id),      -- what is sold
    component_item_id  text NOT NULL REFERENCES items(id),      -- what it uses
    quantity           numeric(18,6) NOT NULL CHECK (quantity > 0),
    PRIMARY KEY (item_id, component_item_id),
    CHECK (item_id <> component_item_id)
);

CREATE TABLE stock_counts (
    id           text PRIMARY KEY,
    location_id  text NOT NULL REFERENCES locations(id),
    status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'posted', 'cancelled')),
    counted_at   timestamptz NOT NULL DEFAULT now(),
    posted_at    timestamptz,
    note         text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE stock_count_lines (
    count_id   text NOT NULL REFERENCES stock_counts(id) ON DELETE CASCADE,
    item_id    text NOT NULL REFERENCES items(id),
    counted    numeric(18,6) NOT NULL CHECK (counted >= 0),
    expected   numeric(18,6),
    PRIMARY KEY (count_id, item_id)
);

CREATE TABLE transfers (
    id                text PRIMARY KEY,
    from_location_id  text NOT NULL REFERENCES locations(id),
    to_location_id    text NOT NULL REFERENCES locations(id),
    status            text NOT NULL DEFAULT 'sent' CHECK (status IN ('sent', 'received', 'cancelled')),
    note              text,
    sent_at           timestamptz NOT NULL DEFAULT now(),
    received_at       timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (from_location_id <> to_location_id)
);

CREATE TABLE transfer_lines (
    transfer_id   text NOT NULL REFERENCES transfers(id) ON DELETE CASCADE,
    item_id       text NOT NULL REFERENCES items(id),
    sent_qty      numeric(18,6) NOT NULL CHECK (sent_qty > 0),
    received_qty  numeric(18,6),
    PRIMARY KEY (transfer_id, item_id)
);

-- ---------------------------------------------------------------------------
-- Purchasing
-- ---------------------------------------------------------------------------
CREATE TABLE suppliers (
    id              text PRIMARY KEY,
    name            text NOT NULL,
    email           text,
    phone           text,
    contact_name    text,
    currency        char(3) NOT NULL DEFAULT 'USD',
    lead_time_days  integer NOT NULL DEFAULT 2 CHECK (lead_time_days >= 0),
    order_days      integer[] NOT NULL DEFAULT '{}',   -- weekdays orders are placed, 0 = Sunday
    min_order_value numeric(19,4),
    external_id     text UNIQUE,
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    archived_at     timestamptz
);

CREATE TABLE supplier_catalog (
    supplier_id   text NOT NULL REFERENCES suppliers(id),
    item_id       text NOT NULL REFERENCES items(id),
    supplier_sku  text,
    pack_size     numeric(18,6) NOT NULL DEFAULT 1 CHECK (pack_size > 0),
    price         numeric(19,4) NOT NULL CHECK (price >= 0),   -- per pack
    min_packs     numeric(18,6) NOT NULL DEFAULT 1,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (supplier_id, item_id)
);

CREATE SEQUENCE purchase_order_number_seq START 1000;
CREATE TABLE purchase_orders (
    id            text PRIMARY KEY,
    number        text NOT NULL UNIQUE DEFAULT ('PO-' || nextval('purchase_order_number_seq')),
    supplier_id   text NOT NULL REFERENCES suppliers(id),
    location_id   text NOT NULL REFERENCES locations(id),
    status        text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'awaiting_approval', 'approved', 'sent',
                                                                  'partially_received', 'received', 'cancelled')),
    expected_on   date,
    total         numeric(19,4) NOT NULL DEFAULT 0,
    currency      char(3) NOT NULL,
    notes         text,
    external_id   text UNIQUE,
    approved_at   timestamptz,
    sent_at       timestamptz,
    version       integer NOT NULL DEFAULT 1,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE purchase_order_lines (
    po_id         text NOT NULL REFERENCES purchase_orders(id) ON DELETE CASCADE,
    line_no       integer NOT NULL,
    item_id       text NOT NULL REFERENCES items(id),
    quantity      numeric(18,6) NOT NULL CHECK (quantity > 0),     -- in item base units
    unit_price    numeric(19,4) NOT NULL CHECK (unit_price >= 0),  -- per base unit
    received_qty  numeric(18,6) NOT NULL DEFAULT 0,
    PRIMARY KEY (po_id, line_no)
);

CREATE TABLE goods_receipts (
    id           text PRIMARY KEY,
    po_id        text NOT NULL REFERENCES purchase_orders(id),
    location_id  text NOT NULL REFERENCES locations(id),
    lines        jsonb NOT NULL,
    note         text,
    received_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE supplier_invoices (
    id              text PRIMARY KEY,
    supplier_id     text NOT NULL REFERENCES suppliers(id),
    po_id           text REFERENCES purchase_orders(id),
    invoice_number  text NOT NULL,
    invoice_date    date NOT NULL,
    total           numeric(19,4) NOT NULL,
    currency        char(3) NOT NULL,
    lines           jsonb NOT NULL DEFAULT '[]',
    status          text NOT NULL CHECK (status IN ('matched', 'mismatch', 'approved', 'disputed')),
    issues          jsonb NOT NULL DEFAULT '[]',
    external_id     text UNIQUE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (supplier_id, invoice_number)
);

-- ---------------------------------------------------------------------------
-- Sales orders
-- ---------------------------------------------------------------------------
CREATE TABLE customers (
    id                 text PRIMARY KEY,
    name               text NOT NULL,
    email              text,
    phone              text,
    billing_address    jsonb,
    shipping_address   jsonb,
    payment_terms_days integer NOT NULL DEFAULT 0,
    external_id        text UNIQUE,
    version            integer NOT NULL DEFAULT 1,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    archived_at        timestamptz
);

CREATE SEQUENCE sales_order_number_seq START 1000;
CREATE TABLE sales_orders (
    id               text PRIMARY KEY,
    number           text NOT NULL UNIQUE DEFAULT ('SO-' || nextval('sales_order_number_seq')),
    source           text NOT NULL DEFAULT 'purros',
    external_id      text,
    customer_id      text REFERENCES customers(id),
    location_id      text NOT NULL REFERENCES locations(id),
    status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'shipped', 'cancelled')),
    payment_status   text NOT NULL DEFAULT 'unpaid' CHECK (payment_status IN ('unpaid', 'paid', 'refunded')),
    shipping         jsonb,
    tracking_number  text,
    total            numeric(19,4) NOT NULL DEFAULT 0,
    currency         char(3) NOT NULL,
    shipped_at       timestamptz,
    version          integer NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);

CREATE TABLE sales_order_lines (
    order_id    text NOT NULL REFERENCES sales_orders(id) ON DELETE CASCADE,
    line_no     integer NOT NULL,
    item_id     text NOT NULL REFERENCES items(id),
    quantity    numeric(18,6) NOT NULL CHECK (quantity > 0),
    unit_price  numeric(19,4) NOT NULL CHECK (unit_price >= 0),
    PRIMARY KEY (order_id, line_no)
);

CREATE SEQUENCE invoice_number_seq START 1000;
CREATE TABLE invoices (
    id              text PRIMARY KEY,
    number          text NOT NULL UNIQUE DEFAULT ('INV-' || nextval('invoice_number_seq')),
    sales_order_id  text REFERENCES sales_orders(id),
    customer_id     text REFERENCES customers(id),
    issued_on       date NOT NULL,
    due_on          date NOT NULL,
    lines           jsonb NOT NULL,
    total           numeric(19,4) NOT NULL,
    currency        char(3) NOT NULL,
    status          text NOT NULL DEFAULT 'issued' CHECK (status IN ('issued', 'paid', 'void')),
    paid_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Cash
-- ---------------------------------------------------------------------------
CREATE TABLE cash_tenders (
    id             text PRIMARY KEY,
    source         text NOT NULL,
    external_id    text NOT NULL,
    location_id    text NOT NULL REFERENCES locations(id),
    business_date  date NOT NULL,
    register_id    text NOT NULL DEFAULT '',
    tender_type    text NOT NULL,
    platform       text,
    amount         numeric(19,4) NOT NULL,
    currency       char(3) NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);
CREATE INDEX cash_tenders_day_idx ON cash_tenders (location_id, business_date);

CREATE TABLE cash_settlements (
    id             text PRIMARY KEY,
    source         text NOT NULL,
    external_id    text NOT NULL,
    location_id    text REFERENCES locations(id),
    provider       text NOT NULL,
    tender_type    text NOT NULL,
    business_date  date NOT NULL,
    gross          numeric(19,4) NOT NULL,
    fees           numeric(19,4) NOT NULL DEFAULT 0,
    net            numeric(19,4) NOT NULL,
    currency       char(3) NOT NULL,
    paid_on        date,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);

CREATE TABLE bank_transactions (
    id            text PRIMARY KEY,
    source        text NOT NULL,
    external_id   text NOT NULL,
    account       text,
    booked_on     date NOT NULL,
    amount        numeric(19,4) NOT NULL,
    reference     text,
    currency      char(3) NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);

CREATE TABLE cash_counts (
    id             text PRIMARY KEY,
    location_id    text NOT NULL REFERENCES locations(id),
    business_date  date NOT NULL,
    register_id    text NOT NULL DEFAULT '',
    kind           text NOT NULL CHECK (kind IN ('open', 'skim', 'shift_change', 'close', 'safe')),
    counted        numeric(19,4) NOT NULL,
    expected       numeric(19,4),
    over_short     numeric(19,4),
    denominations  jsonb,
    counted_by     text,
    note           text,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cash_counts_day_idx ON cash_counts (location_id, business_date);

CREATE TABLE bank_deposits (
    id                   text PRIMARY KEY,
    location_id          text NOT NULL REFERENCES locations(id),
    business_date        date NOT NULL,
    bag_number           text,
    amount               numeric(19,4) NOT NULL CHECK (amount > 0),
    currency             char(3) NOT NULL,
    status               text NOT NULL DEFAULT 'recorded' CHECK (status IN ('recorded', 'verified', 'mismatch')),
    bank_transaction_id  text REFERENCES bank_transactions(id),
    difference           numeric(19,4),
    created_at           timestamptz NOT NULL DEFAULT now(),
    verified_at          timestamptz
);

CREATE TABLE business_days (
    location_id    text NOT NULL REFERENCES locations(id),
    business_date  date NOT NULL,
    status         text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    closed_at      timestamptz,
    PRIMARY KEY (location_id, business_date)
);

-- ---------------------------------------------------------------------------
-- Operations: forms, checklists, audits, sensors
-- ---------------------------------------------------------------------------
CREATE TABLE forms (
    id           text PRIMARY KEY,
    name         text NOT NULL,
    category     text,
    kind         text NOT NULL DEFAULT 'checklist' CHECK (kind IN ('checklist', 'audit', 'log', 'report')),
    questions    jsonb NOT NULL,
    external_id  text UNIQUE,
    version      integer NOT NULL DEFAULT 1,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    archived_at  timestamptz
);

CREATE TABLE form_submissions (
    id             text PRIMARY KEY,
    form_id        text NOT NULL REFERENCES forms(id),
    form_version   integer NOT NULL,
    location_id    text REFERENCES locations(id),
    employee_id    text REFERENCES employees(id),
    submitted_at   timestamptz NOT NULL DEFAULT now(),
    answers        jsonb NOT NULL,
    results        jsonb NOT NULL,
    failed_count   integer NOT NULL DEFAULT 0,
    score          numeric(7,2),
    max_score      numeric(7,2),
    passed         boolean,
    source         text,
    external_id    text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);
CREATE INDEX form_submissions_form_idx ON form_submissions (form_id, submitted_at);

CREATE TABLE corrective_actions (
    id              text PRIMARY KEY,
    submission_id   text REFERENCES form_submissions(id),
    question_id     text,
    location_id     text REFERENCES locations(id),
    title           text NOT NULL,
    assignee_id     text REFERENCES employees(id),
    due_on          date,
    status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    resolution      text,
    evidence_url    text,
    closed_at       timestamptz,
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sensors (
    id           text PRIMARY KEY,
    location_id  text NOT NULL REFERENCES locations(id),
    name         text NOT NULL,
    kind         text NOT NULL DEFAULT 'temperature',
    unit         text NOT NULL DEFAULT '°C',
    min_value    numeric(12,4),
    max_value    numeric(12,4),
    external_id  text UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    archived_at  timestamptz
);

CREATE TABLE sensor_readings (
    sensor_id  text NOT NULL REFERENCES sensors(id),
    at         timestamptz NOT NULL,
    value      numeric(12,4) NOT NULL,
    in_range   boolean NOT NULL,
    PRIMARY KEY (sensor_id, at)
);

-- ---------------------------------------------------------------------------
-- Equipment
-- ---------------------------------------------------------------------------
CREATE TABLE assets (
    id                text PRIMARY KEY,
    location_id       text NOT NULL REFERENCES locations(id),
    name              text NOT NULL,
    category          text,
    make              text,
    model             text,
    serial_number     text,
    purchase_date     date,
    purchase_cost     numeric(19,4),
    warranty_until    date,
    service_provider  text,
    meter_unit        text,
    meter_value       numeric(19,4),
    maintenance_every_days integer,
    maintenance_every_meter numeric(19,4),
    last_maintained_at timestamptz,
    last_maintained_meter numeric(19,4),
    external_id       text UNIQUE,
    version           integer NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    archived_at       timestamptz
);

CREATE TABLE asset_meter_readings (
    asset_id  text NOT NULL REFERENCES assets(id),
    at        timestamptz NOT NULL,
    value     numeric(19,4) NOT NULL,
    PRIMARY KEY (asset_id, at)
);

CREATE TABLE work_orders (
    id              text PRIMARY KEY,
    asset_id        text REFERENCES assets(id),
    location_id     text NOT NULL REFERENCES locations(id),
    title           text NOT NULL,
    description     text,
    kind            text NOT NULL DEFAULT 'repair' CHECK (kind IN ('repair', 'maintenance')),
    priority        text NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'assigned', 'in_progress', 'waiting_parts', 'resolved', 'closed')),
    assignee        text,
    cost            numeric(19,4),
    photos          text[] NOT NULL DEFAULT '{}',
    external_id     text UNIQUE,
    resolved_at     timestamptz,
    closed_at       timestamptz,
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX work_orders_asset_idx ON work_orders (asset_id);

-- ---------------------------------------------------------------------------
-- Communication & displays
-- ---------------------------------------------------------------------------
CREATE TABLE announcements (
    id            text PRIMARY KEY,
    title         text NOT NULL,
    body          text NOT NULL,
    location_ids  text[] NOT NULL DEFAULT '{}',   -- empty = everyone
    require_ack   boolean NOT NULL DEFAULT false,
    publish_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    archived_at   timestamptz
);

CREATE TABLE announcement_acks (
    announcement_id  text NOT NULL REFERENCES announcements(id),
    employee_id      text NOT NULL REFERENCES employees(id),
    acked_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (announcement_id, employee_id)
);

CREATE TABLE calendar_events (
    id           text PRIMARY KEY,
    title        text NOT NULL,
    description  text,
    location_id  text REFERENCES locations(id),
    kind         text NOT NULL DEFAULT 'event',
    starts_at    timestamptz NOT NULL,
    ends_at      timestamptz NOT NULL,
    all_day      boolean NOT NULL DEFAULT false,
    external_id  text UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    archived_at  timestamptz,
    CHECK (ends_at >= starts_at)
);

CREATE TABLE recognitions (
    id           text PRIMARY KEY,
    employee_id  text NOT NULL REFERENCES employees(id),
    location_id  text REFERENCES locations(id),
    message      text NOT NULL,
    given_by     text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE display_metrics (
    location_id  text NOT NULL REFERENCES locations(id),
    key          text NOT NULL,
    label        text NOT NULL,
    value        numeric(19,4) NOT NULL,
    unit         text,
    target       numeric(19,4),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (location_id, key)
);

-- ---------------------------------------------------------------------------
-- Insights
-- ---------------------------------------------------------------------------
CREATE TABLE alert_rules (
    id           text PRIMARY KEY,
    kpi          text NOT NULL,
    location_id  text REFERENCES locations(id),
    comparator   text NOT NULL CHECK (comparator IN ('gt', 'lt')),
    threshold    numeric(19,4) NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    archived_at  timestamptz
);

-- +goose Down
DROP TABLE alert_rules, display_metrics, recognitions, calendar_events, announcement_acks, announcements,
    work_orders, asset_meter_readings, assets, sensor_readings, sensors, corrective_actions, form_submissions,
    forms, business_days, bank_deposits, cash_counts, bank_transactions, cash_settlements, cash_tenders,
    invoices, sales_order_lines, sales_orders, customers, supplier_invoices, goods_receipts, purchase_order_lines,
    purchase_orders, supplier_catalog, suppliers, transfer_lines, transfers, stock_count_lines, stock_counts,
    usage_recipes, stock_levels, stock_movements, shift_swap_requests, availability, shifts, staffing_rules,
    forecast_adjustments, demand_drivers, time_off_ledger, time_off_requests, time_off_types, timesheets,
    pay_periods, payslips, pay_rates, employee_skills, skills, employee_documents;
DROP SEQUENCE purchase_order_number_seq, sales_order_number_seq, invoice_number_seq;
ALTER TABLE locations DROP COLUMN labor_rule_set_id;
DROP TABLE labor_rule_sets;
ALTER TABLE items DROP COLUMN default_cost, DROP COLUMN track_stock;
ALTER TABLE company DROP COLUMN po_approval_limit, DROP COLUMN over_short_tolerance, DROP COLUMN pay_period_length_days;
