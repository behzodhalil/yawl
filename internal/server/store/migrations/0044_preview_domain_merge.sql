-- +goose Up
ALTER TABLE environments ADD COLUMN preview_domain TEXT NOT NULL DEFAULT '';
ALTER TABLE previews ADD COLUMN base TEXT NOT NULL DEFAULT '';
ALTER TABLE previews ADD COLUMN merge_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE previews ADD COLUMN merged_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE previews ADD COLUMN comment_done INTEGER NOT NULL DEFAULT 0;
UPDATE previews SET comment_done = 1 WHERE state = 'closed';

-- +goose Down
ALTER TABLE previews DROP COLUMN comment_done;
ALTER TABLE previews DROP COLUMN merged_at;
ALTER TABLE previews DROP COLUMN merge_sha;
ALTER TABLE previews DROP COLUMN base;
ALTER TABLE environments DROP COLUMN preview_domain;
