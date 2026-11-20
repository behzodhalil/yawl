-- +goose Up
ALTER TABLE join_tokens ADD COLUMN panel INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE join_tokens DROP COLUMN panel;
