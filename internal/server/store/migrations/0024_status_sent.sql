-- +goose Up
ALTER TABLE deploys ADD COLUMN status_sent TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deploys DROP COLUMN status_sent;
