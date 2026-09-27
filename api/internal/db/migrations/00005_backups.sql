-- +goose Up
-- Backups made by `purros backup create` and the scheduled backup job.
CREATE TABLE backup_runs (
    id            text PRIMARY KEY,
    kind          text NOT NULL CHECK (kind IN ('manual', 'scheduled', 'pre_restore')),
    status        text NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    file          text,
    size_bytes    bigint,
    encrypted     boolean NOT NULL DEFAULT false,
    error         text,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);
CREATE INDEX backup_runs_started_idx ON backup_runs (started_at DESC);

-- +goose Down
DROP TABLE backup_runs;
