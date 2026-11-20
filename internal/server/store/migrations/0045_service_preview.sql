-- +goose Up
ALTER TABLE services ADD COLUMN no_preview INTEGER NOT NULL DEFAULT 0;
ALTER TABLE previews ADD COLUMN author TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE previews DROP COLUMN author;
ALTER TABLE services DROP COLUMN no_preview;
