-- name: MarkServiceDeleted :execrows
UPDATE services SET deleted_at = ?, purge_at = ?, deleted_by = ? WHERE id = ? AND deleted_at IS NULL;
-- name: MarkEnvServicesDeleted :exec
UPDATE services SET deleted_at = ?, purge_at = ?, deleted_by = ? WHERE environment_id = ? AND deleted_at IS NULL;
-- name: MarkEnvironmentDeleted :execrows
UPDATE environments SET deleted_at = ?, purge_at = ?, deleted_by = ? WHERE id = ? AND deleted_at IS NULL;
-- name: MarkProjectEnvsDeleted :exec
UPDATE environments SET deleted_at = ?, purge_at = ?, deleted_by = ? WHERE project_id = ? AND deleted_at IS NULL;
-- name: MarkProjectDeleted :execrows
UPDATE projects SET deleted_at = ?, purge_at = ?, deleted_by = ? WHERE id = ? AND deleted_at IS NULL;
-- name: ServicesMarkedWith :many
SELECT s.id, s.current_deploy_id, s.kind, s.user_stopped FROM services s JOIN environments e ON e.id = s.environment_id
WHERE s.deleted_at = sqlc.arg(at) AND (s.id = sqlc.arg(id) OR e.id = sqlc.arg(id) OR e.project_id = sqlc.arg(id));
-- name: UnmarkService :exec
UPDATE services SET deleted_at = NULL, purge_at = NULL, deleted_by = '', stopped = 0 WHERE id = ?;
-- name: UnmarkEnvironments :exec
UPDATE environments SET deleted_at = NULL, purge_at = NULL, deleted_by = '' WHERE deleted_at = sqlc.arg(at) AND (id = sqlc.arg(id) OR project_id = sqlc.arg(id));
-- name: UnmarkProject :exec
UPDATE projects SET deleted_at = NULL, purge_at = NULL, deleted_by = '' WHERE id = ?;
-- name: DeletedAtOf :one
SELECT CAST(COALESCE(CASE CAST(sqlc.arg(kind) AS TEXT) WHEN 'service' THEN (SELECT deleted_at FROM services WHERE services.id = sqlc.arg(id))
  WHEN 'environment' THEN (SELECT deleted_at FROM environments WHERE environments.id = sqlc.arg(id))
  WHEN 'project' THEN (SELECT deleted_at FROM projects WHERE projects.id = sqlc.arg(id)) END, 0) AS BIGINT) AS at;
-- name: TrashedProjects :many
SELECT p.id, p.name, p.deleted_at, p.purge_at, p.deleted_by,
  CAST(COALESCE((SELECT srv.name FROM environments e JOIN servers srv ON srv.id = e.server_id WHERE e.project_id = p.id AND srv.offline = 1
    AND EXISTS (SELECT 1 FROM services s WHERE s.environment_id = e.id) ORDER BY srv.name LIMIT 1), '') AS TEXT) AS offline_server
FROM projects p WHERE p.deleted_at IS NOT NULL ORDER BY p.deleted_at DESC;
-- name: TrashedEnvironments :many
SELECT e.id, p.name AS project, e.name, e.deleted_at, e.purge_at, e.deleted_by,
  CAST(CASE WHEN srv.offline = 1 AND EXISTS (SELECT 1 FROM services s WHERE s.environment_id = e.id) THEN srv.name ELSE '' END AS TEXT) AS offline_server
FROM environments e JOIN projects p ON p.id = e.project_id JOIN servers srv ON srv.id = e.server_id
WHERE e.deleted_at IS NOT NULL AND p.deleted_at IS NULL AND e.deleted_by <> 'preview' ORDER BY e.deleted_at DESC;
-- name: TrashedServices :many
SELECT s.id, p.name AS project, e.name AS env, s.name, s.deleted_at, s.purge_at, s.deleted_by FROM services s
JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE s.deleted_at IS NOT NULL AND e.deleted_at IS NULL AND s.deleted_by <> 'preview' ORDER BY s.deleted_at DESC;
-- name: TrashedServiceByName :one
SELECT s.purge_at FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE p.name = sqlc.arg(project) AND e.name = sqlc.arg(env) AND s.name = sqlc.arg(service) AND s.deleted_at IS NOT NULL;
-- name: MarkProjectServicesDeleted :exec
UPDATE services SET deleted_at = ?, purge_at = ?, deleted_by = ?
WHERE deleted_at IS NULL AND environment_id IN (SELECT e.id FROM environments e WHERE e.project_id = ?);
-- name: GetProjectName :one
SELECT name FROM projects WHERE id = ?;
-- name: TrashToStop :many
SELECT s.id, s.kind, s.name, s.volumes_json, s.volumes_kept, s.deleted_by, e.name AS env, p.name AS project, e.server_id, srv.name AS server_name, srv.public_ip, srv.offline,
  COALESCE(d.container, '') AS container
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id JOIN servers srv ON srv.id = e.server_id
LEFT JOIN deploys d ON d.id = s.current_deploy_id
WHERE s.deleted_at IS NOT NULL AND s.stopped = 0 ORDER BY s.id;
-- name: TrashDue :many
SELECT s.id, s.kind, s.name, s.volumes_json, s.volumes_kept, s.deleted_by, e.name AS env, p.name AS project, e.server_id, srv.name AS server_name, srv.public_ip, srv.offline,
  COALESCE(d.container, '') AS container
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id JOIN servers srv ON srv.id = e.server_id
LEFT JOIN deploys d ON d.id = s.current_deploy_id
WHERE s.deleted_at IS NOT NULL AND s.purge_at <= ? ORDER BY s.id;
-- name: SetServiceStopped :exec
UPDATE services SET stopped = 1 WHERE id = ?;
-- name: PurgeServiceDomains :exec
DELETE FROM domains WHERE service_id = ?;
-- name: PurgeServiceVars :exec
DELETE FROM env_vars WHERE service_id = ?;
-- name: PurgeServiceBackups :exec
DELETE FROM backups WHERE service_id = ?;
-- name: PurgeServiceSteps :exec
DELETE FROM deploy_steps WHERE deploy_id IN (SELECT d.id FROM deploys d WHERE d.service_id = ?);
-- name: ClearCurrentDeploy :exec
UPDATE services SET current_deploy_id = NULL WHERE id = ?;
-- name: PurgeServiceDeploys :exec
DELETE FROM deploys WHERE service_id = ?;
-- name: PurgeServiceMinutes :exec
DELETE FROM metric_minutes WHERE service_id = ?;
-- name: PurgeServiceHours :exec
DELETE FROM metric_hours WHERE service_id = ?;
-- name: PurgeService :exec
DELETE FROM services WHERE id = ?;
-- name: PurgeEmptyEnvironments :many
SELECT e.id FROM environments e WHERE e.deleted_at IS NOT NULL AND e.purge_at <= ?
  AND NOT EXISTS (SELECT 1 FROM services s WHERE s.environment_id = e.id);
-- name: PurgeEnvironment :exec
DELETE FROM environments WHERE id = ?;
-- name: PurgeEnvVars :exec
DELETE FROM env_vars WHERE environment_id = ?;
-- name: PurgeEnvMoves :exec
DELETE FROM env_moves WHERE environment_id = ?;
-- name: PurgeEnvSources :exec
DELETE FROM secret_sources WHERE environment_id = ?;
-- name: PurgeEmptyProjects :many
SELECT p.id, p.name FROM projects p WHERE p.deleted_at IS NOT NULL AND p.purge_at <= ?
  AND NOT EXISTS (SELECT 1 FROM environments e WHERE e.project_id = p.id);
-- name: PurgeProject :exec
DELETE FROM projects WHERE id = ?;
-- name: GetLiveProjectByName :one
SELECT id FROM projects WHERE name = ? AND deleted_at IS NULL;
-- name: RefUsers :many
SELECT DISTINCT s.name FROM env_vars v JOIN services s ON s.environment_id = v.environment_id AND (v.service_id = '' OR v.service_id = s.id)
WHERE v.environment_id = ? AND v.ref LIKE ? AND s.deleted_at IS NULL AND s.kind NOT IN ('postgres', 'valkey', 'mysql', 'mongodb', 'mariadb', 'clickhouse') ORDER BY s.name;
-- name: TrashOne :one
SELECT s.id, s.kind, s.name, s.volumes_json, s.volumes_kept, s.deleted_by, e.name AS env, p.name AS project, e.server_id, srv.name AS server_name, srv.public_ip, srv.offline,
  COALESCE(d.container, '') AS container
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id JOIN servers srv ON srv.id = e.server_id
LEFT JOIN deploys d ON d.id = s.current_deploy_id
WHERE s.id = ? AND s.deleted_at IS NOT NULL AND s.purge_at <= ?;
-- name: PurgeAtOf :one
SELECT CAST(COALESCE((SELECT purge_at FROM services WHERE services.id = sqlc.arg(id)), (SELECT purge_at FROM environments WHERE environments.id = sqlc.arg(id)),
  (SELECT purge_at FROM projects WHERE projects.id = sqlc.arg(id)), 0) AS BIGINT) AS at;
-- name: ServiceParents :one
SELECT e.deleted_at AS env_deleted, p.deleted_at AS project_deleted, p.name AS project, e.name AS env
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id WHERE s.id = ?;
-- name: EnvironmentParent :one
SELECT p.deleted_at AS project_deleted, p.name AS project FROM environments e JOIN projects p ON p.id = e.project_id WHERE e.id = ?;
-- name: TrashedProjectByName :one
SELECT purge_at FROM projects WHERE name = ? AND deleted_at IS NOT NULL;
-- name: TrashedInEnv :one
SELECT name, purge_at FROM services WHERE environment_id = ? AND deleted_at IS NOT NULL ORDER BY name LIMIT 1;
-- name: ExtendTrashServices :exec
UPDATE services SET purge_at = sqlc.arg(at) WHERE deleted_at IS NOT NULL AND purge_at < sqlc.arg(at);
-- name: ExtendTrashEnvironments :exec
UPDATE environments SET purge_at = sqlc.arg(at) WHERE deleted_at IS NOT NULL AND purge_at < sqlc.arg(at);
-- name: ExtendTrashProjects :exec
UPDATE projects SET purge_at = sqlc.arg(at) WHERE deleted_at IS NOT NULL AND purge_at < sqlc.arg(at);

-- name: UserStopsToDo :many
SELECT s.id, s.kind, s.name, s.volumes_json, s.volumes_kept, s.deleted_by, e.name AS env, p.name AS project, e.server_id, srv.name AS server_name, srv.public_ip, srv.offline,
  COALESCE(d.container, '') AS container
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id JOIN servers srv ON srv.id = e.server_id
LEFT JOIN deploys d ON d.id = s.current_deploy_id
WHERE s.deleted_at IS NULL AND s.user_stopped = 1 AND s.stopped = 0 ORDER BY s.id;
-- name: ServiceImportFiles :many
SELECT file FROM imports WHERE service_id = ? AND file != '';
-- name: PurgeServiceImports :exec
DELETE FROM imports WHERE service_id = ?;
-- name: PurgeServiceAnalyticsHours :exec
DELETE FROM analytics_hours WHERE service_id = ?;
-- name: PurgeServiceAnalyticsDays :exec
DELETE FROM analytics_days WHERE service_id = ?;
-- name: PurgeServiceAnalyticsTop :exec
DELETE FROM analytics_top WHERE service_id = ?;
