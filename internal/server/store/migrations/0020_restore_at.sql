-- +goose Up
ALTER TABLE deploys ADD COLUMN restore_at BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE deploys DROP COLUMN restore_at;
