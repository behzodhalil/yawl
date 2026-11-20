-- +goose Up
CREATE TABLE env_moves (
  id TEXT PRIMARY KEY, environment_id TEXT NOT NULL REFERENCES environments(id),
  from_server_id TEXT NOT NULL REFERENCES servers(id), to_server_id TEXT NOT NULL REFERENCES servers(id),
  step TEXT NOT NULL, state TEXT NOT NULL, cause TEXT NOT NULL DEFAULT '',
  boxes_json TEXT NOT NULL DEFAULT '{}', stopped_json TEXT NOT NULL DEFAULT '{}',
  backups_json TEXT NOT NULL DEFAULT '{}', deploys_json TEXT NOT NULL DEFAULT '{}', old_json TEXT NOT NULL DEFAULT '{}',
  cleaned INTEGER NOT NULL DEFAULT 0, lines_json TEXT NOT NULL DEFAULT '[]',
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, finished_at BIGINT);
CREATE INDEX env_moves_env ON env_moves(environment_id, created_at);
ALTER TABLE deploys ADD COLUMN move_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deploys DROP COLUMN move_id;
DROP TABLE env_moves;
