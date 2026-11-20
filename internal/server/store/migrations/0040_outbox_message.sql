-- +goose Up
ALTER TABLE outbox ADD COLUMN message_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE outbox DROP COLUMN message_json;
