-- +goose Up
ALTER TABLE services ADD COLUMN pitr INTEGER NOT NULL DEFAULT 0;
ALTER TABLE backups ADD COLUMN kind TEXT NOT NULL DEFAULT 'dump';

CREATE TABLE wal_batches (
  id TEXT PRIMARY KEY, service_id TEXT NOT NULL REFERENCES services(id), object_key TEXT NOT NULL,
  first_wal TEXT NOT NULL, last_wal TEXT NOT NULL, size BIGINT NOT NULL, created_at BIGINT NOT NULL);
CREATE INDEX wal_batches_service ON wal_batches (service_id, created_at);

-- +goose Down
DROP TABLE wal_batches;
ALTER TABLE backups DROP COLUMN kind;
ALTER TABLE services DROP COLUMN pitr;
