-- +goose Up
ALTER TABLE environments ADD COLUMN preview INTEGER NOT NULL DEFAULT 0;
ALTER TABLE environments ADD COLUMN preview_of TEXT NOT NULL DEFAULT '';
CREATE TABLE previews (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), source_env_id TEXT NOT NULL REFERENCES environments(id),
  number INTEGER NOT NULL, repo TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL, head_sha TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,

  env_id TEXT NOT NULL DEFAULT '', comment_id INTEGER NOT NULL DEFAULT 0, comment TEXT NOT NULL DEFAULT '',
  cause TEXT NOT NULL DEFAULT '',

  last_push_at BIGINT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
  UNIQUE (project_id, repo, number));

-- +goose Down
DROP TABLE previews;
ALTER TABLE environments DROP COLUMN preview_of;
ALTER TABLE environments DROP COLUMN preview;
