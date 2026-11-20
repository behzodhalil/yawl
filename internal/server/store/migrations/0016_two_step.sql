-- +goose Up
ALTER TABLE owner ADD COLUMN totp_secret TEXT;
ALTER TABLE owner ADD COLUMN totp_pending TEXT;
ALTER TABLE owner ADD COLUMN totp_last_step BIGINT NOT NULL DEFAULT 0;
CREATE TABLE recovery_codes (
  id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owner(id), hash TEXT NOT NULL UNIQUE, used_at BIGINT);

-- +goose Down
DROP TABLE recovery_codes;
ALTER TABLE owner DROP COLUMN totp_last_step;
ALTER TABLE owner DROP COLUMN totp_pending;
ALTER TABLE owner DROP COLUMN totp_secret;
