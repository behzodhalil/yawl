-- +goose Up
ALTER TABLE owner ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE owner ADD COLUMN last_sign_in_at BIGINT;
ALTER TABLE api_tokens ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';
UPDATE api_tokens SET owner_id = (SELECT id FROM owner ORDER BY created_at, id LIMIT 1) WHERE owner_id = '' AND EXISTS (SELECT 1 FROM owner);
CREATE TABLE invites (
  id TEXT PRIMARY KEY, hash TEXT NOT NULL UNIQUE, role TEXT NOT NULL, created_by TEXT NOT NULL,
  expires_at BIGINT NOT NULL, used_at BIGINT, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);

-- +goose Down
DROP TABLE invites;
ALTER TABLE api_tokens DROP COLUMN owner_id;
ALTER TABLE owner DROP COLUMN last_sign_in_at;
ALTER TABLE owner DROP COLUMN role;
