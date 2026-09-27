-- +goose Up
-- Uploaded files (proof, photos, receipts, documents…). The file itself is in
-- storage (local disk or S3); this is its metadata and who it belongs to.
CREATE TABLE attachments (
    id                text PRIMARY KEY,
    name              text NOT NULL,
    content_type      text NOT NULL,
    size_bytes        bigint NOT NULL,
    sha256            text NOT NULL,
    storage_key       text NOT NULL UNIQUE,
    purpose           text NOT NULL DEFAULT 'other'
                      CHECK (purpose IN ('proof', 'photo', 'receipt', 'invoice', 'document', 'payslip', 'signature', 'other')),
    note              text,
    employee_id       text REFERENCES employees(id),
    location_id       text REFERENCES locations(id),
    uploaded_by_type  text NOT NULL,
    uploaded_by_id    text,
    uploaded_by_name  text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);
CREATE INDEX attachments_employee_idx ON attachments (employee_id) WHERE deleted_at IS NULL;
CREATE INDEX attachments_location_idx ON attachments (location_id) WHERE deleted_at IS NULL;
CREATE INDEX attachments_uploader_idx ON attachments (uploaded_by_id) WHERE deleted_at IS NULL;

-- Backups uploaded to S3.
ALTER TABLE backup_runs
    ADD COLUMN remote_key   text,
    ADD COLUMN uploaded_at  timestamptz,
    ADD COLUMN upload_error text;

-- +goose Down
ALTER TABLE backup_runs DROP COLUMN remote_key, DROP COLUMN uploaded_at, DROP COLUMN upload_error;
DROP TABLE attachments;
