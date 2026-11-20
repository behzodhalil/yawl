-- +goose Up
CREATE TABLE status_pages (
  id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, title TEXT NOT NULL,
  server_id TEXT NOT NULL REFERENCES servers(id), domain TEXT NOT NULL DEFAULT '',
  watcher INTEGER NOT NULL DEFAULT 0, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE status_rows (
  id TEXT PRIMARY KEY, page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
  position INTEGER NOT NULL, name TEXT NOT NULL, subject TEXT NOT NULL,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE status_incidents (
  id TEXT PRIMARY KEY, page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
  kind TEXT NOT NULL, title TEXT NOT NULL, rows_json TEXT NOT NULL DEFAULT '[]',
  state TEXT NOT NULL, auto INTEGER NOT NULL DEFAULT 0,
  starts_at BIGINT NOT NULL DEFAULT 0, ends_at BIGINT NOT NULL DEFAULT 0,
  resolved_at BIGINT NOT NULL DEFAULT 0, published_at BIGINT NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE INDEX status_incidents_page ON status_incidents(page_id, state);
CREATE TABLE status_updates (
  id TEXT PRIMARY KEY, incident_id TEXT NOT NULL REFERENCES status_incidents(id) ON DELETE CASCADE,
  body TEXT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL);
CREATE TABLE status_routes (
  host TEXT NOT NULL, server_id TEXT NOT NULL, page_id TEXT NOT NULL,
  dns TEXT NOT NULL DEFAULT '', dns_detail TEXT NOT NULL DEFAULT '', cert TEXT NOT NULL DEFAULT '',
  tried_at BIGINT NOT NULL DEFAULT 0, PRIMARY KEY (host, server_id));
CREATE INDEX events_kind_created ON events(kind, created_at);

-- +goose Down
DROP INDEX events_kind_created;
DROP TABLE status_routes;
DROP TABLE status_updates;
DROP TABLE status_incidents;
DROP TABLE status_rows;
DROP TABLE status_pages;
