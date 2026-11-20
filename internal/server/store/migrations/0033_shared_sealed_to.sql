-- +goose Up
ALTER TABLE shared_vars ADD COLUMN sealed_to TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE shared_vars DROP COLUMN sealed_to;
