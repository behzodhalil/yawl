-- +goose Up
ALTER TABLE services ADD COLUMN schedule TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN timezone TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN command_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE services ADD COLUMN timeout_ms BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE services DROP COLUMN timeout_ms;
ALTER TABLE services DROP COLUMN command_json;
ALTER TABLE services DROP COLUMN timezone;
ALTER TABLE services DROP COLUMN schedule;
