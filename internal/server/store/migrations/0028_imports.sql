-- +goose Up
CREATE TABLE imports (
  id         TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id),
  source     TEXT NOT NULL,

  ref        TEXT NOT NULL,

  file       TEXT NOT NULL DEFAULT '',

  token      TEXT NOT NULL,

  created_at INTEGER NOT NULL
);
ALTER TABLE deploys ADD COLUMN import_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deploys DROP COLUMN import_id;
DROP TABLE imports;
