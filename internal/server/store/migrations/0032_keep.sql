-- +goose Up
ALTER TABLE services ADD COLUMN keep_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN keep_json;
