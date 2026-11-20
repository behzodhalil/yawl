-- +goose Up
ALTER TABLE wal_batches ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE wal_batches DROP COLUMN sha256;
