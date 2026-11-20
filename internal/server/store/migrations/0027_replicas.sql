-- +goose Up
ALTER TABLE services ADD COLUMN replicas INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE services DROP COLUMN replicas;
