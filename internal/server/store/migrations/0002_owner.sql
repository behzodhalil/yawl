-- +goose Up
CREATE TABLE owner (
  id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owner(id), hash TEXT NOT NULL UNIQUE, expires_at BIGINT NOT NULL,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);

-- +goose Down
DROP TABLE sessions;
DROP TABLE owner;
