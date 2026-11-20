-- +goose Up
ALTER TABLE services ADD COLUMN tags_json TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE services DROP COLUMN tags_json;
