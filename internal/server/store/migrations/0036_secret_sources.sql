-- +goose Up
CREATE TABLE secret_sources (
  id TEXT PRIMARY KEY, environment_id TEXT NOT NULL REFERENCES environments(id),
  provider TEXT NOT NULL, url TEXT NOT NULL DEFAULT '', path TEXT NOT NULL DEFAULT '',
  workspace TEXT NOT NULL DEFAULT '', env TEXT NOT NULL DEFAULT '',
  agent_box TEXT NOT NULL, recovery_box TEXT NOT NULL,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
  UNIQUE (environment_id, provider));

-- +goose Down
DROP TABLE secret_sources;
