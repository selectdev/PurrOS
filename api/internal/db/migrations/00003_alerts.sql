-- +goose Up
ALTER TABLE alert_rules ADD COLUMN last_triggered_on date;
ALTER TABLE alert_rules ADD COLUMN name text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE alert_rules DROP COLUMN last_triggered_on, DROP COLUMN name;
