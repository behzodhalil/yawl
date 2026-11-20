-- +goose Up
CREATE TABLE shared_vars (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,

  server_id TEXT NOT NULL REFERENCES servers(id),
  key TEXT NOT NULL, agent_box TEXT NOT NULL, recovery_box TEXT NOT NULL, build INTEGER NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
  UNIQUE (scope, server_id, key));

-- +goose Down
DROP TABLE shared_vars;
