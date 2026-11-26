-- +goose Up
ALTER TABLE services ADD COLUMN settings_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE services DROP COLUMN settings_at;
