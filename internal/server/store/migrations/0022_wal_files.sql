-- +goose Up
ALTER TABLE wal_batches ADD COLUMN files TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE wal_batches DROP COLUMN files;
