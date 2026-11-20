-- +goose Up
ALTER TABLE services ADD COLUMN root TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN root;
