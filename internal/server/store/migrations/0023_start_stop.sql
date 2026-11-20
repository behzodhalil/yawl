-- +goose Up
ALTER TABLE services ADD COLUMN user_stopped INTEGER NOT NULL DEFAULT 0;
ALTER TABLE deploys ADD COLUMN restart INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE deploys DROP COLUMN restart;
ALTER TABLE services DROP COLUMN user_stopped;
