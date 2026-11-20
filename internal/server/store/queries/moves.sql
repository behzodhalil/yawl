-- name: CreateMove :exec
INSERT INTO env_moves (id, environment_id, from_server_id, to_server_id, step, state, boxes_json, lines_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'running', ?, ?, ?, ?);
-- name: GetMove :one
SELECT * FROM env_moves WHERE id = ?;
-- name: RunningMoves :many
SELECT * FROM env_moves WHERE state = 'running' OR (state = 'done' AND cleaned = 0) ORDER BY created_at, id;
-- name: ActiveMove :one
SELECT * FROM env_moves WHERE environment_id = ?
  AND (state = 'running' OR (state = 'failed' AND step NOT IN ('stop', 'backup', 'switch_server')))
ORDER BY created_at DESC, id DESC LIMIT 1;
-- name: LatestMove :one
SELECT * FROM env_moves WHERE environment_id = ? ORDER BY created_at DESC, id DESC LIMIT 1;
-- name: LatestMoveStart :one
SELECT CAST(COALESCE(MAX(created_at), 0) AS BIGINT) FROM env_moves WHERE environment_id = ? AND step NOT IN ('stop', 'backup', 'switch_server');
-- name: SetMoveStep :exec
UPDATE env_moves SET step = ?, state = ?, cause = ?, stopped_json = ?, backups_json = ?, deploys_json = ?, old_json = ?, cleaned = ?,
  finished_at = ?, updated_at = ? WHERE id = ?;
-- name: UnfinishedDeploysInEnv :one
SELECT COUNT(*) FROM deploys d JOIN services s ON s.id = d.service_id
WHERE s.environment_id = ? AND d.state NOT IN ('live', 'failed', 'rolled_back', 'superseded');
-- name: SealedVarsInEnv :many
SELECT v.id, v.key, CAST(COALESCE(s.name, '') AS TEXT) AS service, v.recovery_box, v.updated_at
FROM env_vars v LEFT JOIN services s ON s.id = v.service_id
WHERE v.environment_id = ? AND v.ref = '' ORDER BY v.service_id, v.key;
-- name: SetEnvironmentServer :exec
UPDATE environments SET server_id = ?, updated_at = ? WHERE id = ?;
-- name: SetEnvVarAgentBox :execrows
UPDATE env_vars SET agent_box = ?, updated_at = ? WHERE id = ? AND environment_id = ?;
-- name: ResetEnvDomains :exec
UPDATE domains SET dns_state = 'pending', dns_detail = '', cert_state = 'pending', updated_at = ?
WHERE service_id IN (SELECT id FROM services WHERE environment_id = ?);
-- name: GetEnvironmentNames :one
SELECT e.name, p.name AS project FROM environments e JOIN projects p ON p.id = e.project_id WHERE e.id = ?;
-- name: RetryMoveState :exec
UPDATE env_moves SET state = 'running', cause = '', finished_at = NULL, updated_at = ? WHERE id = ?;
