-- +goose Up
ALTER TABLE services ADD COLUMN watch_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE deploys ADD COLUMN cancel_requested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE deploys ADD COLUMN no_cache INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE deploys DROP COLUMN no_cache;
ALTER TABLE deploys DROP COLUMN cancel_requested;
ALTER TABLE services DROP COLUMN watch_json;
