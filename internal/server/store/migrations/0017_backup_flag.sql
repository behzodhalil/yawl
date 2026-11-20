-- +goose Up
ALTER TABLE services ADD COLUMN backup INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE services DROP COLUMN backup;
