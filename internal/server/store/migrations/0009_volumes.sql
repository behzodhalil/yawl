-- +goose Up
ALTER TABLE services ADD COLUMN volumes_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE services ADD COLUMN volumes_kept TEXT NOT NULL DEFAULT '[]';
ALTER TABLE backups ADD COLUMN volume TEXT NOT NULL DEFAULT '';
ALTER TABLE deploys ADD COLUMN volume TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deploys DROP COLUMN volume;
ALTER TABLE backups DROP COLUMN volume;
ALTER TABLE services DROP COLUMN volumes_kept;
ALTER TABLE services DROP COLUMN volumes_json;
