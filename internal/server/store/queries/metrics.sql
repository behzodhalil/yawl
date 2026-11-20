-- name: InsertMetricMinute :exec
INSERT INTO metric_minutes (server_id, service_id, at, cpu, mem, mem_total, disk, disk_total, load, req, err5, p95_ms, net_rx, net_tx)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (server_id, service_id, at) DO NOTHING;
-- name: FoldMetricHour :exec
INSERT INTO metric_hours (server_id, service_id, at, cpu, mem, mem_total, disk, disk_total, load, req, err5, p95_ms, net_rx, net_tx)
SELECT m.server_id, m.service_id, CAST(sqlc.arg(hour) AS BIGINT), MAX(m.cpu), MAX(m.mem), MAX(m.mem_total), MAX(m.disk), MAX(m.disk_total), AVG(m.load), SUM(m.req), SUM(m.err5), MAX(m.p95_ms), MAX(m.net_rx), MAX(m.net_tx)
FROM metric_minutes m WHERE m.at >= sqlc.arg(hour) AND m.at < sqlc.arg(hour_end) GROUP BY m.server_id, m.service_id
ON CONFLICT (server_id, service_id, at) DO UPDATE SET cpu = excluded.cpu, mem = excluded.mem, mem_total = excluded.mem_total,
  disk = excluded.disk, disk_total = excluded.disk_total, load = excluded.load, req = excluded.req, err5 = excluded.err5, p95_ms = excluded.p95_ms,
  net_rx = excluded.net_rx, net_tx = excluded.net_tx;
-- name: DeleteMetricMinutesBefore :exec
DELETE FROM metric_minutes WHERE at < ?;
-- name: DeleteMetricHoursBefore :exec
DELETE FROM metric_hours WHERE at < ?;
-- name: MetricMinutes :many
SELECT * FROM metric_minutes WHERE server_id = ? AND service_id = ? AND at >= ? ORDER BY at;
-- name: MetricHours :many
SELECT * FROM metric_hours WHERE server_id = ? AND service_id = ? AND at >= ? ORDER BY at;
-- name: ServiceMetricMinutes :many
SELECT * FROM metric_minutes WHERE service_id = ? AND at >= ? ORDER BY at;
-- name: ServiceMetricHours :many
SELECT * FROM metric_hours WHERE service_id = ? AND at >= ? ORDER BY at;
-- name: LastMachineMinutes :many
SELECT * FROM metric_minutes WHERE server_id = ? AND service_id = '' ORDER BY at DESC LIMIT ?;
-- name: TopContainerAt :one
SELECT service_id, mem FROM metric_minutes WHERE server_id = ? AND service_id <> '' AND at = ? ORDER BY mem DESC LIMIT 1;
-- name: TopCPUContainerAt :one
SELECT service_id, cpu FROM metric_minutes WHERE server_id = ? AND service_id <> '' AND at = ? ORDER BY cpu DESC LIMIT 1;
-- name: ServerServicesNow :many
SELECT p.name AS project, e.name AS env, s.name, s.cpu AS cpu_limit, s.memory AS mem_limit, m.cpu, m.mem
FROM metric_minutes m JOIN services s ON s.id = m.service_id JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE m.server_id = sqlc.arg(server_id) AND s.deleted_at IS NULL
  AND m.at = (SELECT MAX(x.at) FROM metric_minutes x WHERE x.server_id = sqlc.arg(server_id) AND x.service_id <> '')
ORDER BY p.name, e.name, s.name;
-- name: ServiceKeys :many
SELECT s.id, s.name, p.name AS project, e.name AS env FROM services s
JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id WHERE s.deleted_at IS NULL;
