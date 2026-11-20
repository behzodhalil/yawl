-- +goose Up
ALTER TABLE servers ADD COLUMN wg_pubkey TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN private_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN private_port INTEGER NOT NULL DEFAULT 0;
CREATE TABLE db_shares (
  service_id TEXT NOT NULL REFERENCES services(id), server_id TEXT NOT NULL REFERENCES servers(id), url_box TEXT NOT NULL,
  updated_at BIGINT NOT NULL, PRIMARY KEY (service_id, server_id));
CREATE TABLE server_peers (
  server_id TEXT NOT NULL REFERENCES servers(id), peer_id TEXT NOT NULL REFERENCES servers(id), last_handshake BIGINT NOT NULL DEFAULT 0,
  since BIGINT NOT NULL,

  updated_at BIGINT NOT NULL, PRIMARY KEY (server_id, peer_id));

-- +goose Down
DROP TABLE server_peers;
DROP TABLE db_shares;
ALTER TABLE services DROP COLUMN private_port;
ALTER TABLE servers DROP COLUMN private_ip;
ALTER TABLE servers DROP COLUMN wg_pubkey;
