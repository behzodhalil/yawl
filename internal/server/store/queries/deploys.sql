-- name: CreateDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, actor, state, created_at, updated_at) VALUES (?, ?, ?, ?, 'queued', ?, ?);
-- name: GetDeploy :one
SELECT * FROM deploys WHERE id = ?;
-- name: SetDeployState :exec
UPDATE deploys SET state = ?, cause = ?, finished_at = ?, updated_at = ? WHERE id = ?;
-- name: StartDeploy :execrows
UPDATE deploys SET state = ?, started_at = ?, prev_deploy_id = ?, updated_at = ? WHERE id = ? AND state = 'queued';
-- name: AdvanceDeploy :execrows
UPDATE deploys SET state = ?, cause = '', finished_at = NULL, updated_at = ? WHERE id = ? AND cancel_requested = 0;
-- name: SetDeployImage :exec
UPDATE deploys SET image = ?, updated_at = ? WHERE id = ?;
-- name: SetDeployContainer :exec
UPDATE deploys SET container = ?, updated_at = ? WHERE id = ?;
-- name: SupersedeQueued :exec
UPDATE deploys SET state = 'superseded', updated_at = ? WHERE service_id = ? AND state = 'queued' AND move_id = '' AND import_id = '' AND restore_from = '';

-- name: SupersedeQueuedOfDeleted :exec
UPDATE deploys SET state = 'superseded', updated_at = sqlc.arg(at)
WHERE state = 'queued' AND service_id IN (SELECT s.id FROM services s WHERE s.deleted_at = sqlc.arg(at));
-- name: RunnableDeploys :many
SELECT * FROM deploys d WHERE d.state NOT IN ('live', 'failed', 'rolled_back', 'superseded')
  AND d.created_at = (SELECT MIN(created_at) FROM deploys x WHERE x.service_id = d.service_id
    AND x.state NOT IN ('live', 'failed', 'rolled_back', 'superseded'))
  AND NOT EXISTS (SELECT 1 FROM env_moves m JOIN services s ON s.environment_id = m.environment_id
    WHERE s.id = d.service_id AND (m.state = 'running' OR (m.state = 'failed' AND m.step NOT IN ('stop', 'backup', 'switch_server'))) AND m.id <> d.move_id)
  AND (d.state <> 'queued' OR EXISTS (SELECT 1 FROM services sv WHERE sv.id = d.service_id AND sv.deleted_at IS NULL))
ORDER BY d.created_at;
-- name: ListDeploysForService :many
SELECT * FROM deploys WHERE service_id = ? ORDER BY created_at DESC LIMIT ?;
-- name: AddDeployStep :exec
INSERT INTO deploy_steps (id, deploy_id, step, state, started_at, created_at, updated_at) VALUES (?, ?, ?, 'running', ?, ?, ?)
ON CONFLICT (deploy_id, step) DO UPDATE SET state = 'running', started_at = excluded.started_at, updated_at = excluded.updated_at;
-- name: FinishDeployStep :exec
UPDATE deploy_steps SET state = ?, output = ?, finished_at = ?, updated_at = ? WHERE deploy_id = ? AND step = ?;
-- name: DeploySteps :many
SELECT * FROM deploy_steps WHERE deploy_id = ? ORDER BY started_at, created_at;
-- name: CreateRestoreDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, state, restore_from, created_at, updated_at) VALUES (?, ?, '', 'queued', ?, ?, ?);
-- name: CreateRecoverDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, state, restore_from, restore_at, created_at, updated_at) VALUES (?, ?, '', 'queued', ?, ?, ?, ?);
-- name: CreateVolumeRestoreDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, subject, actor, image, rollback, restore_from, volume, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, 'queued', ?, ?);

-- name: CreateRollbackDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, subject, actor, image, digest, rollback, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1, 'queued', ?, ?);
-- name: CreateRestartDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, subject, actor, image, digest, rollback, restart, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, 'queued', ?, ?);
-- name: MarkAutoRolledBack :exec
UPDATE deploys SET auto_rolled_back = 1, updated_at = ? WHERE id = ?;
-- name: RollbackTarget :one
SELECT * FROM deploys WHERE service_id = sqlc.arg(service_id) AND state = 'live' AND auto_rolled_back = 0 AND image <> ''
  AND image <> sqlc.arg(current_image) AND created_at < sqlc.arg(built_at)
  AND NOT (digest <> '' AND digest = sqlc.arg(current_digest))

  AND created_at >= sqlc.arg(since)

ORDER BY created_at DESC LIMIT 1;
-- name: ActiveDeploys :one
SELECT COUNT(*) FROM deploys WHERE service_id = ? AND state NOT IN ('live', 'failed', 'rolled_back', 'superseded');
-- name: LiveImages :many
SELECT image FROM deploys WHERE service_id = ? AND state = 'live' AND auto_rolled_back = 0 AND image <> '' ORDER BY created_at DESC LIMIT 10;
-- name: SetDeployCommit :exec
UPDATE deploys SET commit_sha = ?, updated_at = ? WHERE id = ? AND commit_sha = '';
-- name: SetDeployDigest :exec
UPDATE deploys SET digest = ?, updated_at = ? WHERE id = ?;
-- name: SetDeployLimits :exec
UPDATE deploys SET cpu = ?, memory = ?, updated_at = ? WHERE id = ?;
-- name: SetDeploySubject :exec
UPDATE deploys SET subject = ?, updated_at = ? WHERE id = ?;
-- name: InsertAdoptedDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, image, container, state, started_at, finished_at, created_at, updated_at)
VALUES (?, ?, '', ?, ?, 'live', ?, ?, ?, ?);
-- name: MarkDeployLive :exec
UPDATE deploys SET state = 'live', image = ?, container = ?, updated_at = ? WHERE id = ?;
-- name: SupersedeUnfinishedBefore :execrows
UPDATE deploys SET state = 'superseded', cause = 'The panel was restored from a backup taken while this deploy was unfinished.', updated_at = ?
WHERE created_at < ? AND state NOT IN ('live', 'failed', 'rolled_back', 'superseded');
-- name: CreateMoveDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, state, move_id, created_at, updated_at) VALUES (?, ?, ?, 'queued', ?, ?, ?);
-- name: CreateMoveRestoreDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, state, restore_from, move_id, created_at, updated_at) VALUES (?, ?, '', 'queued', ?, ?, ?, ?);
-- name: NewestQueuedDeploy :one
SELECT * FROM deploys WHERE service_id = ? AND state = 'queued' AND move_id = '' ORDER BY created_at DESC, id DESC LIMIT 1;
-- name: StatusDeploys :many
SELECT d.id, d.state, d.cause, d.commit_sha, d.status_sent, d.started_at, d.finished_at, s.name, s.repo, e.name AS env, p.name AS project
FROM deploys d JOIN services s ON s.id = d.service_id JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE d.created_at > ? AND d.commit_sha != '' AND s.repo != '' ORDER BY d.created_at;
-- name: SetStatusSent :exec
UPDATE deploys SET status_sent = ? WHERE id = ?;
-- name: CancelDeploy :execrows
UPDATE deploys SET state = 'superseded', cause = 'Canceled.', finished_at = ?, updated_at = ? WHERE id = ? AND state IN ('queued', 'building', 'pulling');
-- name: CancelQueued :execrows
UPDATE deploys SET state = 'superseded', cause = 'Canceled.', finished_at = ?, updated_at = ? WHERE id = ? AND state = 'queued';
-- name: RequestCancel :execrows
UPDATE deploys SET cancel_requested = 1, updated_at = ? WHERE id = ? AND state IN ('queued', 'building', 'pulling');
-- name: QueuedNoCache :one
SELECT COUNT(*) FROM deploys WHERE service_id = ? AND state = 'queued' AND no_cache = 1 AND move_id = '' AND import_id = '' AND restore_from = '';
-- name: SetDeployNoCache :exec
UPDATE deploys SET no_cache = 1 WHERE id = ?;
