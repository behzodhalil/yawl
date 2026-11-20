-- name: AddAnalyticsHour :exec
INSERT INTO analytics_hours (service_id, at, s2, s3, s4, s5, bytes, views) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (service_id, at) DO UPDATE SET s2 = s2 + excluded.s2, s3 = s3 + excluded.s3, s4 = s4 + excluded.s4,
  s5 = s5 + excluded.s5, bytes = bytes + excluded.bytes, views = views + excluded.views;
-- name: SetAnalyticsVisitors :exec
INSERT INTO analytics_days (service_id, day, visitors) VALUES (?, ?, ?)
ON CONFLICT (service_id, day) DO UPDATE SET visitors = max(visitors, excluded.visitors);
-- name: AddAnalyticsTop :exec
INSERT INTO analytics_top (service_id, at, kind, value, n) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (service_id, at, kind, value) DO UPDATE SET n = n + excluded.n;
-- name: AnalyticsHours :many
SELECT * FROM analytics_hours WHERE service_id = ? AND at >= ? ORDER BY at;
-- name: AnalyticsDays :many
SELECT * FROM analytics_days WHERE service_id = ? AND day >= ? ORDER BY day;
-- name: AnalyticsTop :many
SELECT kind, value, CAST(SUM(n) AS INTEGER) AS n FROM analytics_top WHERE service_id = ? AND at >= ? GROUP BY kind, value ORDER BY n DESC, value;
-- name: DeleteAnalyticsHoursBefore :exec
DELETE FROM analytics_hours WHERE at < ?;
-- name: DeleteAnalyticsTopBefore :exec
DELETE FROM analytics_top WHERE at < ?;
-- name: DeleteAnalyticsDaysBefore :exec
DELETE FROM analytics_days WHERE day < ?;
