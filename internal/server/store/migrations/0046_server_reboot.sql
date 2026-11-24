-- +goose Up
ALTER TABLE servers ADD COLUMN reboot_required INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE servers DROP COLUMN reboot_required;
