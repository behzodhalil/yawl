-- +goose Up
CREATE TABLE password_resets (
  id TEXT PRIMARY KEY, hash TEXT NOT NULL UNIQUE, owner_id TEXT NOT NULL,
  expires_at BIGINT NOT NULL, used_at BIGINT, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);

-- +goose Down
DROP TABLE password_resets;
