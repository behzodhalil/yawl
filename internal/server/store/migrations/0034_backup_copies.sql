-- +goose Up
CREATE TABLE storage_copies (
  name TEXT PRIMARY KEY, endpoint TEXT NOT NULL, bucket TEXT NOT NULL, region TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE backup_copies (
  backup_id TEXT NOT NULL, copy TEXT NOT NULL, state TEXT NOT NULL, cause TEXT NOT NULL DEFAULT '', updated_at BIGINT NOT NULL,
  PRIMARY KEY (backup_id, copy));
ALTER TABLE deploys ADD COLUMN restore_storage TEXT NOT NULL DEFAULT '';


-- +goose Down
ALTER TABLE deploys DROP COLUMN restore_storage;
DROP TABLE backup_copies;
DROP TABLE storage_copies;
