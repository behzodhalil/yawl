-- +goose Up
ALTER TABLE servers ADD COLUMN os_name TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN os_id TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN kernel TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN cores INTEGER NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN booted_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE metric_minutes ADD COLUMN net_rx BIGINT;
ALTER TABLE metric_minutes ADD COLUMN net_tx BIGINT;
ALTER TABLE metric_hours ADD COLUMN net_rx BIGINT;
ALTER TABLE metric_hours ADD COLUMN net_tx BIGINT;

-- +goose Down
ALTER TABLE metric_hours DROP COLUMN net_tx;
ALTER TABLE metric_hours DROP COLUMN net_rx;
ALTER TABLE metric_minutes DROP COLUMN net_tx;
ALTER TABLE metric_minutes DROP COLUMN net_rx;
ALTER TABLE servers DROP COLUMN booted_at;
ALTER TABLE servers DROP COLUMN cores;
ALTER TABLE servers DROP COLUMN kernel;
ALTER TABLE servers DROP COLUMN os_id;
ALTER TABLE servers DROP COLUMN os_name;
