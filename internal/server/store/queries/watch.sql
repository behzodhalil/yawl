-- name: WatchedWebServices :many
SELECT s.id, s.name, s.public, s.health_path, p.name AS project, e.name AS env, e.server_id, srv.public_ip
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
JOIN servers srv ON srv.id = e.server_id JOIN deploys dep ON dep.id = s.current_deploy_id
WHERE dep.state = 'live' AND s.kind = 'web' AND s.deleted_at IS NULL AND s.user_stopped = 0 AND e.preview_of = '' ORDER BY s.id;

-- name: ListHeartbeats :many
SELECT * FROM heartbeats ORDER BY name;
-- name: InsertHeartbeat :exec
INSERT INTO heartbeats (id, name, period_s, grace_s, token, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?);
-- name: DeleteHeartbeat :execrows
DELETE FROM heartbeats WHERE id = ?;
-- name: SetServicePublic :exec
UPDATE services SET public = ?, updated_at = ? WHERE id = ?;
-- name: MarkOutboxOwed :exec
UPDATE outbox SET owed = ?, next_attempt_at = ?, updated_at = ? WHERE id = ?;
-- name: LiveCronServices :many
SELECT s.id, s.name, s.schedule, s.timezone, s.timeout_ms, p.name AS project, e.name AS env, e.server_id, dep.finished_at
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
JOIN deploys dep ON dep.id = s.current_deploy_id WHERE dep.state = 'live' AND s.kind = 'cron' AND s.deleted_at IS NULL AND s.user_stopped = 0 ORDER BY s.id;
-- name: IncidentOpenedAt :one
SELECT created_at FROM outbox WHERE incident = ? AND action = 'open' ORDER BY created_at DESC LIMIT 1;
-- name: CountServicesOnServer :one
SELECT count(*) FROM services s JOIN environments e ON e.id = s.environment_id WHERE e.server_id = ? AND s.deleted_at IS NULL;
