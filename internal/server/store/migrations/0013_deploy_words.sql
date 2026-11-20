-- +goose Up
ALTER TABLE deploys ADD COLUMN subject TEXT NOT NULL DEFAULT '';
ALTER TABLE deploys ADD COLUMN actor TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deploys DROP COLUMN actor;
ALTER TABLE deploys DROP COLUMN subject;
