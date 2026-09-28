-- +goose Up
-- Managing integrations and webhook endpoints through the API: standalone
-- endpoints get a description, and delivery logs are listed per endpoint.
ALTER TABLE webhook_endpoints
    ADD COLUMN description   text NOT NULL DEFAULT '',
    ADD COLUMN disabled_at   timestamptz;

CREATE INDEX webhook_deliveries_endpoint_idx ON webhook_deliveries (endpoint_id, id);
CREATE INDEX ingest_batches_integration_idx ON ingest_batches (integration_id, id);
CREATE INDEX org_units_parent_idx ON org_units (parent_id);
CREATE INDEX locations_org_unit_idx ON locations (org_unit_id);

-- +goose Down
DROP INDEX locations_org_unit_idx;
DROP INDEX org_units_parent_idx;
DROP INDEX ingest_batches_integration_idx;
DROP INDEX webhook_deliveries_endpoint_idx;
ALTER TABLE webhook_endpoints DROP COLUMN description, DROP COLUMN disabled_at;
