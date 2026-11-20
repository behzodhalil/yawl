-- +goose Up
ALTER TABLE outbox ADD COLUMN sent_channels TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE outbox DROP COLUMN sent_channels;
