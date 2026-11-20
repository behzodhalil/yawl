-- +goose Up
CREATE TABLE metric_minutes (
  server_id TEXT NOT NULL, service_id TEXT NOT NULL, at BIGINT NOT NULL,
  cpu REAL NOT NULL DEFAULT 0, mem BIGINT NOT NULL DEFAULT 0, mem_total BIGINT NOT NULL DEFAULT 0,
  disk BIGINT NOT NULL DEFAULT 0, disk_total BIGINT NOT NULL DEFAULT 0, load REAL NOT NULL DEFAULT 0,
  req BIGINT, err5 BIGINT, p95_ms BIGINT,

  PRIMARY KEY (server_id, service_id, at));
CREATE INDEX metric_minutes_service ON metric_minutes(service_id, at);
CREATE INDEX metric_minutes_at ON metric_minutes(at);
CREATE TABLE metric_hours (
  server_id TEXT NOT NULL, service_id TEXT NOT NULL, at BIGINT NOT NULL,
  cpu REAL NOT NULL DEFAULT 0, mem BIGINT NOT NULL DEFAULT 0, mem_total BIGINT NOT NULL DEFAULT 0,
  disk BIGINT NOT NULL DEFAULT 0, disk_total BIGINT NOT NULL DEFAULT 0, load REAL NOT NULL DEFAULT 0,
  req BIGINT, err5 BIGINT, p95_ms BIGINT,

  PRIMARY KEY (server_id, service_id, at));
CREATE INDEX metric_hours_service ON metric_hours(service_id, at);

-- +goose Down
DROP TABLE metric_hours;
DROP TABLE metric_minutes;
