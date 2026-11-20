-- +goose Up
ALTER TABLE services ADD COLUMN image TEXT NOT NULL DEFAULT '';
ALTER TABLE deploys ADD COLUMN digest TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deploys DROP COLUMN digest;
ALTER TABLE services DROP COLUMN image;
