-- +goose Up
ALTER TABLE services ADD COLUMN steps_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN steps_json;
