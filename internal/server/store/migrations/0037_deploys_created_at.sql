-- +goose Up
CREATE INDEX deploys_created_at ON deploys(created_at);

-- +goose Down
DROP INDEX deploys_created_at;
