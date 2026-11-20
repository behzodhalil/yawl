-- +goose Up
ALTER TABLE owner ADD COLUMN sso_subject TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE owner DROP COLUMN sso_subject;
