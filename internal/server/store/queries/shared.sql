-- name: SharedVarsFor :many
SELECT v.*, s.agent_pubkey AS current_key FROM shared_vars v JOIN servers s ON s.id = v.server_id
WHERE v.server_id = ? ORDER BY CASE v.scope WHEN 'team' THEN 0 ELSE 1 END, v.key;
-- name: ListSharedVars :many
SELECT v.*, s.name AS server, s.agent_pubkey AS current_key FROM shared_vars v JOIN servers s ON s.id = v.server_id ORDER BY v.scope DESC, v.key, s.name;
-- name: UpsertSharedVar :exec
INSERT INTO shared_vars (id, scope, server_id, key, agent_box, recovery_box, build, sealed_to, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (scope, server_id, key) DO UPDATE SET agent_box = excluded.agent_box, recovery_box = excluded.recovery_box, build = excluded.build, sealed_to = excluded.sealed_to, updated_at = excluded.updated_at;
-- name: DeleteSharedVar :exec
DELETE FROM shared_vars WHERE scope = ? AND key = ? AND (scope = 'team' OR server_id = ?);
-- name: EnvironmentServer :one
SELECT server_id FROM environments WHERE id = ?;
-- name: ServiceVolumesOnServer :many
SELECT s.name, s.kind, s.volumes_json, s.volumes_kept, p.name AS project, e.name AS env
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE e.server_id = ?;
-- name: ServiceOwnVars :many
SELECT * FROM env_vars WHERE service_id = ? ORDER BY key;
-- name: SourcesForEnv :many
SELECT * FROM secret_sources WHERE environment_id = ? ORDER BY provider;
-- name: UpsertSource :exec
INSERT INTO secret_sources (id, environment_id, provider, url, path, workspace, env, agent_box, recovery_box, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (environment_id, provider) DO UPDATE SET url = excluded.url, path = excluded.path, workspace = excluded.workspace, env = excluded.env,
  agent_box = excluded.agent_box, recovery_box = excluded.recovery_box, updated_at = excluded.updated_at;
-- name: DeleteSource :execrows
DELETE FROM secret_sources WHERE environment_id = ? AND provider = ?;
-- name: SetSourceAgentBox :execrows
UPDATE secret_sources SET agent_box = ?, updated_at = ? WHERE id = ? AND environment_id = ?;
