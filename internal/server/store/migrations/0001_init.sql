-- +goose Up
-- Times are BIGINT unix milliseconds: they compare correctly on SQLite and
-- Postgres alike, which TEXT timestamps with mixed precision do not.
CREATE TABLE servers (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, public_ip TEXT NOT NULL,
  agent_pubkey TEXT NOT NULL, cert_serial TEXT NOT NULL, next_cert_serial TEXT NOT NULL DEFAULT '',
  last_heartbeat BIGINT, agent_version TEXT NOT NULL DEFAULT '', disk_used_pct INTEGER NOT NULL DEFAULT 0,
  disk_alert INTEGER NOT NULL DEFAULT 0, offline INTEGER NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE join_tokens (
  id TEXT PRIMARY KEY, hash TEXT NOT NULL UNIQUE, expires_at BIGINT NOT NULL, used_at BIGINT,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE projects (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE environments (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), name TEXT NOT NULL,
  server_id TEXT NOT NULL REFERENCES servers(id),
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, UNIQUE(project_id, name));
CREATE TABLE services (
  id TEXT PRIMARY KEY, environment_id TEXT NOT NULL REFERENCES environments(id), name TEXT NOT NULL,
  kind TEXT NOT NULL, repo TEXT NOT NULL, branch TEXT NOT NULL, port INTEGER NOT NULL,
  health_path TEXT NOT NULL, health_timeout_ms BIGINT NOT NULL, drain_timeout_ms BIGINT NOT NULL,
  current_deploy_id TEXT, auto_deploy INTEGER NOT NULL DEFAULT 1,
  version INTEGER NOT NULL DEFAULT 0, backup_at TEXT NOT NULL DEFAULT '',
  release_json TEXT NOT NULL DEFAULT '[]', release_timeout_ms BIGINT NOT NULL DEFAULT 0,
  outside_port INTEGER NOT NULL DEFAULT 0, allow_json TEXT NOT NULL DEFAULT '[]', health TEXT NOT NULL DEFAULT 'ok',
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, UNIQUE(environment_id, name));
CREATE TABLE deploys (
  id TEXT PRIMARY KEY, service_id TEXT NOT NULL REFERENCES services(id), commit_sha TEXT NOT NULL,
  image TEXT NOT NULL DEFAULT '', container TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
  prev_deploy_id TEXT NOT NULL DEFAULT '', restore_from TEXT NOT NULL DEFAULT '',
  rollback INTEGER NOT NULL DEFAULT 0, auto_rolled_back INTEGER NOT NULL DEFAULT 0,
  cause TEXT NOT NULL DEFAULT '', started_at BIGINT, finished_at BIGINT,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE INDEX deploys_service_state ON deploys(service_id, state);
CREATE TABLE deploy_steps (
  id TEXT PRIMARY KEY, deploy_id TEXT NOT NULL REFERENCES deploys(id), step TEXT NOT NULL,
  state TEXT NOT NULL, output TEXT NOT NULL DEFAULT '', started_at BIGINT NOT NULL, finished_at BIGINT,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, UNIQUE(deploy_id, step));
CREATE TABLE events (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, subject_id TEXT NOT NULL, actor TEXT NOT NULL,
  body_json TEXT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE outbox (
  id TEXT PRIMARY KEY, event_id TEXT NOT NULL REFERENCES events(id), channel TEXT NOT NULL,
  body TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at BIGINT NOT NULL,
  sent_at BIGINT, failed_at BIGINT, last_error TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE leases (name TEXT PRIMARY KEY, holder TEXT NOT NULL, expires_at BIGINT NOT NULL);
CREATE TABLE api_tokens (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, hash TEXT NOT NULL UNIQUE, last_used_at BIGINT,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
-- service_id '' means shared by the whole environment (NULL would defeat the UNIQUE).
CREATE TABLE env_vars (
  id TEXT PRIMARY KEY, environment_id TEXT NOT NULL REFERENCES environments(id), service_id TEXT NOT NULL DEFAULT '',
  key TEXT NOT NULL, agent_box TEXT NOT NULL, recovery_box TEXT NOT NULL, build INTEGER NOT NULL DEFAULT 0, ref TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, UNIQUE(environment_id, service_id, key));

CREATE TABLE backups (
  id TEXT PRIMARY KEY, service_id TEXT NOT NULL REFERENCES services(id), object_key TEXT NOT NULL,
  state TEXT NOT NULL, size BIGINT NOT NULL DEFAULT 0, sha256 TEXT NOT NULL DEFAULT '', cause TEXT NOT NULL DEFAULT '',
  started_at BIGINT, finished_at BIGINT, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE INDEX backups_service ON backups(service_id, created_at);

CREATE TABLE domains (
  id TEXT PRIMARY KEY, service_id TEXT NOT NULL REFERENCES services(id), host TEXT NOT NULL UNIQUE,
  dns_state TEXT NOT NULL DEFAULT 'pending', dns_detail TEXT NOT NULL DEFAULT '', cert_state TEXT NOT NULL DEFAULT 'pending',
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);

-- +goose Down
DROP TABLE domains;
DROP TABLE backups;
DROP TABLE env_vars; DROP TABLE settings; DROP TABLE api_tokens; DROP TABLE leases; DROP TABLE outbox; DROP TABLE events;
DROP TABLE deploy_steps; DROP TABLE deploys; DROP TABLE services; DROP TABLE environments;
DROP TABLE projects; DROP TABLE join_tokens; DROP TABLE servers;
