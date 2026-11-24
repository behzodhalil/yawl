-- +goose Up
ALTER TABLE wal_batches ADD COLUMN backlog INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE wal_batches DROP COLUMN backlog;
