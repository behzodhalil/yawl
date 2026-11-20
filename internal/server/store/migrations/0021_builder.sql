-- +goose Up
ALTER TABLE services ADD COLUMN builder TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN builder;
