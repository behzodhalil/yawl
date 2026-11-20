-- +goose Up
CREATE TABLE analytics_hours (
  service_id TEXT NOT NULL, at INTEGER NOT NULL,
  s2 INTEGER NOT NULL DEFAULT 0, s3 INTEGER NOT NULL DEFAULT 0, s4 INTEGER NOT NULL DEFAULT 0, s5 INTEGER NOT NULL DEFAULT 0,
  bytes INTEGER NOT NULL DEFAULT 0, views INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (service_id, at));
CREATE TABLE analytics_days (service_id TEXT NOT NULL, day TEXT NOT NULL, visitors INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (service_id, day));
CREATE TABLE analytics_top (
  service_id TEXT NOT NULL, at INTEGER NOT NULL, kind TEXT NOT NULL, value TEXT NOT NULL, n INTEGER NOT NULL,
  PRIMARY KEY (service_id, at, kind, value));

-- +goose Down
DROP TABLE analytics_top;
DROP TABLE analytics_days;
DROP TABLE analytics_hours;
