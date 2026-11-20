-- +goose Up
ALTER TABLE services ADD COLUMN deleted_at BIGINT;
ALTER TABLE services ADD COLUMN purge_at BIGINT;
ALTER TABLE services ADD COLUMN deleted_by TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN stopped INTEGER NOT NULL DEFAULT 0;
ALTER TABLE environments ADD COLUMN deleted_at BIGINT;
ALTER TABLE environments ADD COLUMN purge_at BIGINT;
ALTER TABLE environments ADD COLUMN deleted_by TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN deleted_at BIGINT;
ALTER TABLE projects ADD COLUMN purge_at BIGINT;
ALTER TABLE projects ADD COLUMN deleted_by TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE projects DROP COLUMN deleted_by;
ALTER TABLE projects DROP COLUMN purge_at;
ALTER TABLE projects DROP COLUMN deleted_at;
ALTER TABLE environments DROP COLUMN deleted_by;
ALTER TABLE environments DROP COLUMN purge_at;
ALTER TABLE environments DROP COLUMN deleted_at;
ALTER TABLE services DROP COLUMN stopped;
ALTER TABLE services DROP COLUMN deleted_by;
ALTER TABLE services DROP COLUMN purge_at;
ALTER TABLE services DROP COLUMN deleted_at;
