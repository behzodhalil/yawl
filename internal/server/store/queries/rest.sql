-- name: InsertEvent :exec
INSERT INTO events (id, kind, subject_id, actor, body_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?);
-- name: CountEvents :one
SELECT COUNT(*) FROM events;
-- name: InsertOutbox :exec
INSERT INTO outbox (id, event_id, channel, body, message_json, next_attempt_at, incident, action, quiet, label, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
-- name: DueOutbox :many
SELECT * FROM outbox WHERE sent_at IS NULL AND failed_at IS NULL AND next_attempt_at <= ? ORDER BY created_at LIMIT 50;
-- name: ListOutbox :many
SELECT * FROM outbox ORDER BY created_at;
-- name: MarkOutboxSent :exec
UPDATE outbox SET sent_at = ?, updated_at = ? WHERE id = ?;
-- name: MarkOutboxRetry :exec
UPDATE outbox SET attempts = ?, next_attempt_at = ?, last_error = ?, updated_at = ? WHERE id = ?;
-- name: MarkOutboxFailed :exec
UPDATE outbox SET failed_at = ?, last_error = ?, updated_at = ? WHERE id = ?;

-- name: ReleaseLease :exec
DELETE FROM leases WHERE name = ? AND holder = ?;

-- name: UpsertProject :one
INSERT INTO projects (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET updated_at = excluded.updated_at RETURNING id;
-- name: UpsertEnvironment :one
INSERT INTO environments (id, project_id, name, server_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, name) DO UPDATE SET server_id = excluded.server_id, updated_at = excluded.updated_at RETURNING id;
-- name: UpsertService :one
INSERT INTO services (id, environment_id, name, kind, repo, branch, port, health_path, health_timeout_ms, drain_timeout_ms, auto_deploy, version, backup_at,
  release_json, release_timeout_ms, outside_port, allow_json, schedule, timezone, command_json, timeout_ms, volumes_json, cpu, memory, image, backup, pitr, builder, root, noindex, www, watch_json, replicas, tags_json, keep_json, no_preview, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (environment_id, name) DO UPDATE SET kind = excluded.kind, repo = excluded.repo, branch = excluded.branch,
  port = excluded.port, health_path = excluded.health_path, health_timeout_ms = excluded.health_timeout_ms,
  drain_timeout_ms = excluded.drain_timeout_ms, auto_deploy = excluded.auto_deploy, version = excluded.version, backup_at = excluded.backup_at,
  release_json = excluded.release_json, release_timeout_ms = excluded.release_timeout_ms, outside_port = excluded.outside_port, allow_json = excluded.allow_json,
  schedule = excluded.schedule, timezone = excluded.timezone, command_json = excluded.command_json, timeout_ms = excluded.timeout_ms, volumes_json = excluded.volumes_json,
  cpu = excluded.cpu, memory = excluded.memory, image = excluded.image, backup = excluded.backup, pitr = excluded.pitr, builder = excluded.builder, root = excluded.root, noindex = excluded.noindex, www = excluded.www, watch_json = excluded.watch_json, replicas = excluded.replicas, tags_json = excluded.tags_json, keep_json = excluded.keep_json, no_preview = excluded.no_preview, updated_at = excluded.updated_at
RETURNING id;
-- name: SettingsWithPrefix :many
SELECT key, value FROM settings WHERE substr(key, 1, length(sqlc.arg(prefix))) = sqlc.arg(prefix) ORDER BY key;
-- name: ServerMemoryLimits :one
SELECT CAST(COALESCE(SUM(s.memory), 0) AS BIGINT) FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE e.server_id = sqlc.arg(server_id) AND NOT (p.name = sqlc.arg(project) AND e.name = sqlc.arg(env)) AND s.deleted_at IS NULL;
-- name: SetCurrentDeploy :exec
UPDATE services SET current_deploy_id = ?, updated_at = ? WHERE id = ?;
-- name: GetService :one
SELECT s.*, p.name AS project, e.name AS env, e.server_id, e.preview_of
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id WHERE s.id = ?;
-- name: GetServiceByName :one
SELECT s.*, p.name AS project, e.name AS env, e.server_id
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE p.name = sqlc.arg(project) AND e.name = sqlc.arg(env) AND s.name = sqlc.arg(service) AND s.deleted_at IS NULL;
-- name: ListServices :many
SELECT s.*, p.name AS project, e.name AS env, e.server_id
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE s.deleted_at IS NULL
ORDER BY p.name, e.name, s.name;

-- name: CreateJoinToken :exec
INSERT INTO join_tokens (id, hash, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?);
-- name: GetJoinTokenByHash :one
SELECT * FROM join_tokens WHERE hash = ?;
-- name: UseJoinToken :execrows
UPDATE join_tokens SET used_at = ?, updated_at = ? WHERE id = ? AND used_at IS NULL;
-- name: UpsertServer :one
INSERT INTO servers (id, name, public_ip, agent_pubkey, cert_serial, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET public_ip = excluded.public_ip, agent_pubkey = excluded.agent_pubkey,
  cert_serial = excluded.cert_serial, updated_at = excluded.updated_at RETURNING id;
-- name: GetServer :one
SELECT * FROM servers WHERE id = ?;
-- name: GetServerByName :one
SELECT * FROM servers WHERE name = ?;
-- name: TouchServer :exec
UPDATE servers SET last_heartbeat = ?, agent_version = ?, disk_used_pct = ?, os_name = ?, os_id = ?, kernel = ?, cores = ?, booted_at = ?, reboot_required = ?, updated_at = ? WHERE id = ?;

-- name: CreateAPIToken :exec
INSERT INTO api_tokens (id, name, hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?);
-- name: GetAPITokenByHash :one
SELECT * FROM api_tokens WHERE hash = ?;

-- name: SetSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value;
-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;
-- name: DeleteSetting :execrows
DELETE FROM settings WHERE key = ? AND value = ?;

-- name: GetEnvironmentByName :one
SELECT e.id, e.server_id, s.name AS server_name, e.deleted_at, e.purge_at, e.preview, e.preview_of, e.preview_domain
FROM environments e JOIN projects p ON p.id = e.project_id JOIN servers s ON s.id = e.server_id
WHERE p.name = sqlc.arg(project) AND e.name = sqlc.arg(env);
-- name: ListServicesInEnv :many
SELECT * FROM services WHERE environment_id = ? AND deleted_at IS NULL ORDER BY name;
-- name: SetServerCert :exec
UPDATE servers SET cert_serial = ?, next_cert_serial = '', updated_at = ? WHERE id = ?;
-- name: SetServerNextCert :execrows
UPDATE servers SET next_cert_serial = ?, updated_at = ? WHERE id = ? AND cert_serial = ?;
-- name: PromoteServerCert :execrows
UPDATE servers SET cert_serial = next_cert_serial, next_cert_serial = '', updated_at = ? WHERE id = ? AND next_cert_serial = ? AND next_cert_serial <> '';
-- name: UpsertEnvVar :exec
INSERT INTO env_vars (id, environment_id, service_id, key, agent_box, recovery_box, build, ref, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (environment_id, service_id, key) DO UPDATE SET agent_box = excluded.agent_box,
  recovery_box = excluded.recovery_box, build = excluded.build, ref = excluded.ref, updated_at = excluded.updated_at;
-- name: DeleteEnvVar :execrows
DELETE FROM env_vars WHERE environment_id = ? AND service_id = ? AND key = ?;
-- name: EnvVarsFor :many
SELECT * FROM env_vars WHERE environment_id = sqlc.arg(environment_id) AND (service_id = '' OR service_id = sqlc.arg(service_id))
ORDER BY CASE WHEN service_id = '' THEN 0 ELSE 1 END, key;
-- name: EnvVarsInEnv :many
SELECT * FROM env_vars WHERE environment_id = ? ORDER BY service_id, key;
-- name: ListAutoDeployServices :many
SELECT id, repo, branch, watch_json FROM services WHERE auto_deploy = 1 AND user_stopped = 0 AND deleted_at IS NULL ORDER BY id;
-- name: DeliverySeen :one
SELECT COUNT(*) FROM events WHERE kind IN ('deploy.queued', 'push.skipped') AND json_extract(body_json, '$.delivery') = sqlc.arg(delivery);
-- name: LatestDeployCommit :one
SELECT commit_sha FROM deploys WHERE service_id = ? AND state <> 'superseded' ORDER BY created_at DESC, id DESC LIMIT 1;
-- name: GetServiceInEnv :one
SELECT * FROM services WHERE environment_id = ? AND name = ?;
-- name: ListBackupCandidates :many
SELECT s.id, s.name, s.kind, s.version, s.backup_at, s.created_at, s.environment_id, s.pitr, s.backup, p.name AS project, e.name AS env, e.server_id
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE s.deleted_at IS NULL AND e.preview_of = '' AND s.user_stopped = 0 AND NOT EXISTS (SELECT 1 FROM env_moves m WHERE m.environment_id = e.id AND (m.state = 'running' OR (m.state = 'failed' AND m.step NOT IN ('stop', 'backup', 'switch_server')))) ORDER BY s.id;
-- name: InsertBackup :exec
INSERT INTO backups (id, service_id, object_key, volume, state, created_at, updated_at) VALUES (?, ?, ?, ?, 'queued', ?, ?);
-- name: InsertBaseBackup :exec
INSERT INTO backups (id, service_id, object_key, kind, state, created_at, updated_at) VALUES (?, ?, ?, 'base', 'queued', ?, ?);
-- name: StartBackup :exec
UPDATE backups SET state = 'running', started_at = ?, updated_at = ? WHERE id = ?;
-- name: FinishBackup :exec
UPDATE backups SET state = ?, size = ?, sha256 = ?, cause = ?, finished_at = ?, updated_at = ? WHERE id = ?;
-- name: GetBackup :one
SELECT * FROM backups WHERE id = ?;
-- name: ListBackups :many
SELECT * FROM backups WHERE service_id = ? ORDER BY created_at DESC, id DESC;
-- name: LatestOKBackup :one
SELECT * FROM backups WHERE service_id = ? AND state = 'ok' AND kind = 'dump' ORDER BY created_at DESC, id DESC LIMIT 1;
-- name: BackupsSince :one
SELECT COUNT(*) FROM backups WHERE service_id = ? AND volume = ? AND created_at >= ?;
-- name: SetVolumesKept :exec
UPDATE services SET volumes_kept = ?, updated_at = ? WHERE id = ?;
-- name: LatestOKVolumeBackup :one
SELECT * FROM backups WHERE service_id = ? AND volume = ? AND state = 'ok' ORDER BY created_at DESC, id DESC LIMIT 1;
-- name: ListVolumeServices :many
SELECT s.id, s.name, s.backup_at, s.volumes_json, s.created_at, p.name AS project, e.name AS env FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE s.kind IN ('web', 'worker') AND s.volumes_json <> '[]' AND s.current_deploy_id IS NOT NULL AND s.deleted_at IS NULL AND e.preview_of = ''
  AND NOT EXISTS (SELECT 1 FROM env_moves m WHERE m.environment_id = e.id AND (m.state = 'running' OR (m.state = 'failed' AND m.step NOT IN ('stop', 'backup', 'switch_server')))) ORDER BY s.id;
-- name: QueuedBackups :many
SELECT * FROM backups WHERE state = 'queued' ORDER BY created_at;
-- name: RunningBackups :many
SELECT * FROM backups WHERE state = 'running';
-- name: CountEventsFor :one
SELECT COUNT(*) FROM events WHERE kind = ? AND subject_id = ? AND created_at > ?;
-- name: MarkBackupDeleted :exec
UPDATE backups SET state = 'deleted', updated_at = ? WHERE id = ? AND state = 'ok';

-- name: ListDomains :many
SELECT * FROM domains WHERE service_id = ? ORDER BY host;
-- name: DomainOwner :one
SELECT d.service_id, p.name AS project, e.name AS env, s.name AS service FROM domains d JOIN services s ON s.id = d.service_id
JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id WHERE d.host = ?;
-- name: DomainsOnHost :many
-- The host and every host/path on it. LIKE is safe:
-- a host has no % or _.
SELECT d.host, p.name AS project, e.name AS env, s.name AS service, e.server_id, srv.name AS server
FROM domains d JOIN services s ON s.id = d.service_id JOIN environments e ON e.id = s.environment_id
JOIN projects p ON p.id = e.project_id JOIN servers srv ON srv.id = e.server_id
WHERE d.host = sqlc.arg(host) OR d.host LIKE sqlc.arg(host) || '/%';
-- name: StatusPagesOnHost :one
SELECT count(*) FROM status_pages WHERE domain = ?;
-- name: InsertDomain :exec
INSERT INTO domains (id, service_id, host, created_at, updated_at) VALUES (?, ?, ?, ?, ?);
-- name: DeleteDomain :exec
DELETE FROM domains WHERE service_id = ? AND host = ?;
-- name: SetDomainState :exec
UPDATE domains SET dns_state = ?, dns_detail = ?, cert_state = ?, updated_at = ? WHERE id = ?;
-- name: PendingDomains :many
SELECT d.id, d.host, d.dns_state, d.cert_state, d.service_id, s.port, dep.container, e.server_id, srv.public_ip
FROM domains d JOIN services s ON s.id = d.service_id JOIN environments e ON e.id = s.environment_id
JOIN servers srv ON srv.id = e.server_id JOIN deploys dep ON dep.id = s.current_deploy_id
WHERE dep.state = 'live' AND s.deleted_at IS NULL AND (d.dns_state <> 'ok' OR d.cert_state <> 'issued') AND NOT EXISTS (SELECT 1 FROM env_moves m WHERE m.environment_id = e.id AND (m.state = 'running' OR (m.state = 'failed' AND m.step NOT IN ('stop', 'backup', 'switch_server')))) ORDER BY d.host;
-- name: LiveServices :many
SELECT s.id, s.name, s.kind, s.port, s.health_path, s.health, p.name AS project, e.name AS env, e.server_id,
  dep.id AS deploy_id, dep.container, dep.finished_at, dep.prev_deploy_id, dep.auto_rolled_back, dep.rollback
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
JOIN deploys dep ON dep.id = s.current_deploy_id WHERE dep.state = 'live' AND s.deleted_at IS NULL AND s.user_stopped = 0 AND NOT EXISTS (SELECT 1 FROM env_moves m WHERE m.environment_id = e.id AND (m.state = 'running' OR (m.state = 'failed' AND m.step NOT IN ('stop', 'backup', 'switch_server')))) ORDER BY s.id;
-- name: SetServiceHealth :exec
UPDATE services SET health = ?, updated_at = ? WHERE id = ?;
-- name: ListServers :many
SELECT * FROM servers ORDER BY name;
-- name: SetServerAlert :exec
UPDATE servers SET disk_alert = ?, offline = ?, updated_at = ? WHERE id = ?;
-- name: OutsidePortUsers :many
SELECT p.name AS project, e.name AS env, s.name AS service FROM services s JOIN environments e ON e.id = s.environment_id
JOIN projects p ON p.id = e.project_id WHERE e.server_id = ? AND s.outside_port = ?;
-- name: CreatePanelJoinToken :exec
INSERT INTO join_tokens (id, hash, expires_at, panel, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?);
-- name: AuditEvents :many
-- 15-audit: instr, not LIKE, so a % or _ in the search is itself (sqlc's parser takes no ESCAPE).
SELECT * FROM events
WHERE (sqlc.arg(actor) = '' OR actor = sqlc.arg(actor))
  AND (sqlc.arg(q) = '' OR instr(lower(body_json), lower(sqlc.arg(q))) > 0 OR instr(lower(kind), lower(sqlc.arg(q))) > 0)
  AND created_at >= sqlc.arg(from_at) AND created_at < sqlc.arg(to_at)
  AND (CAST(sqlc.arg(admin) AS INTEGER) = 1 OR subject_id NOT IN ('POST /api/services/{project}/{env}/{name}/data/query', 'POST /api/services/{project}/{env}/{name}/data/write'))
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
-- name: DeleteOldOutbox :exec
DELETE FROM outbox WHERE (outbox.sent_at IS NOT NULL OR outbox.failed_at IS NOT NULL)
  AND outbox.event_id IN (SELECT e.id FROM events e WHERE e.created_at < ?);
-- name: DeleteEventsBefore :exec
DELETE FROM events WHERE events.created_at < ? AND events.id NOT IN (SELECT o.event_id FROM outbox o WHERE o.sent_at IS NULL AND o.failed_at IS NULL);
-- name: SetServerWG :exec
UPDATE servers SET wg_pubkey = ?, private_ip = ?, updated_at = ? WHERE id = ?;
-- name: UsedPrivateIPs :many
SELECT private_ip FROM servers WHERE private_ip <> '';
-- name: ServerPeers :many
SELECT * FROM server_peers;
-- name: UpsertServerPeer :exec
INSERT INTO server_peers (server_id, peer_id, last_handshake, since, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (server_id, peer_id) DO UPDATE SET last_handshake = excluded.last_handshake, updated_at = excluded.updated_at;
-- name: DeleteServerPeers :exec
DELETE FROM server_peers WHERE server_id = ?;
-- name: DeletePeersOf :exec
DELETE FROM server_peers WHERE server_id = sqlc.arg(id) OR peer_id = sqlc.arg(id);
-- name: SetAutoDeploy :exec
UPDATE services SET auto_deploy = ?, updated_at = ? WHERE id = ?;
-- name: EnvProject :one
SELECT e.project_id, e.server_id, e.name, e.preview_domain, p.name AS project FROM environments e JOIN projects p ON p.id = e.project_id WHERE e.id = ?;
-- name: EnvInProject :one
SELECT id, server_id FROM environments WHERE project_id = ? AND name = ? AND deleted_at IS NULL;
-- name: GetShare :one
SELECT url_box FROM db_shares WHERE service_id = ? AND server_id = ?;
-- name: UpsertShare :exec
INSERT INTO db_shares (service_id, server_id, url_box, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (service_id, server_id) DO UPDATE SET url_box = excluded.url_box, updated_at = excluded.updated_at;
-- name: SetPrivatePort :exec
UPDATE services SET private_port = ?, updated_at = ? WHERE id = ?;
-- name: ReferencingServers :many
SELECT DISTINCT e.server_id, srv.agent_pubkey FROM env_vars v JOIN environments e ON e.id = v.environment_id JOIN servers srv ON srv.id = e.server_id
WHERE e.project_id = sqlc.arg(project_id) AND v.ref = sqlc.arg(ref) AND e.deleted_at IS NULL ORDER BY e.server_id;
-- name: DeleteShares :exec
DELETE FROM db_shares WHERE service_id = ?;
-- name: ReferencingApps :many
SELECT DISTINCT s.id, s.name, e.name AS env, s.current_deploy_id FROM env_vars v JOIN environments e ON e.id = v.environment_id
JOIN services s ON s.environment_id = e.id AND (v.service_id = '' OR v.service_id = s.id)
WHERE e.project_id = sqlc.arg(project_id) AND v.ref = sqlc.arg(ref) AND s.kind NOT IN ('postgres', 'valkey', 'mysql', 'mongodb', 'mariadb', 'clickhouse') AND s.current_deploy_id IS NOT NULL
  AND s.deleted_at IS NULL AND e.deleted_at IS NULL ORDER BY e.name, s.name;
-- name: WebServicesOnServer :many
SELECT s.name, e.name AS env, p.name AS project FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE e.server_id = ? AND s.kind = 'web' AND s.deleted_at IS NULL AND e.deleted_at IS NULL AND p.deleted_at IS NULL ORDER BY p.name, e.name, s.name;
-- name: DatabaseUsers :many
SELECT DISTINCT e.name AS env, s.name FROM env_vars v JOIN environments e ON e.id = v.environment_id
JOIN services s ON s.environment_id = e.id AND (v.service_id = '' OR v.service_id = s.id)
WHERE e.project_id = (SELECT o.project_id FROM environments o WHERE o.id = sqlc.arg(environment_id))
  AND ((e.id = sqlc.arg(environment_id) AND v.ref = sqlc.arg(own_ref)) OR v.ref = sqlc.arg(cross_ref))
  AND s.kind NOT IN ('postgres', 'valkey', 'mysql', 'mongodb', 'mariadb', 'clickhouse') AND s.deleted_at IS NULL AND e.deleted_at IS NULL ORDER BY e.name, s.name;
-- name: PortsOnServer :many
SELECT private_port, outside_port FROM services s JOIN environments e ON e.id = s.environment_id WHERE e.server_id = ?;
-- name: PrivatePortUsers :many
SELECT p.name AS project, s.name AS service FROM services s JOIN environments e ON e.id = s.environment_id
JOIN projects p ON p.id = e.project_id WHERE e.server_id = ? AND s.private_port = ?;
-- name: DeleteServerShares :exec
DELETE FROM db_shares WHERE server_id = ?;
-- name: SetServiceNoPreview :exec
UPDATE services SET no_preview = ?, updated_at = ? WHERE id = ?;
-- name: SetEnvironmentPreview :exec
UPDATE environments SET preview = ?, preview_domain = ?, updated_at = ? WHERE id = ?;
-- name: PreviewEnvInProject :one
SELECT e.id, e.name FROM environments e JOIN projects p ON p.id = e.project_id WHERE p.name = ? AND e.preview = 1 AND e.deleted_at IS NULL LIMIT 1;
-- name: PreviewSources :many
SELECT e.id, e.project_id, s.repo FROM environments e JOIN services s ON s.environment_id = e.id
WHERE e.preview = 1 AND e.deleted_at IS NULL AND s.deleted_at IS NULL AND s.kind NOT IN ('postgres', 'valkey', 'mysql', 'mongodb', 'mariadb', 'clickhouse');
-- name: UpsertPreview :exec
INSERT INTO previews (id, project_id, source_env_id, number, repo, title, branch, head_sha, state, author, base, last_push_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, repo, number) DO UPDATE SET title = excluded.title, branch = excluded.branch,
  author = CASE WHEN previews.author = '' THEN excluded.author ELSE previews.author END, base = CASE WHEN excluded.base = '' THEN previews.base ELSE excluded.base END,
  head_sha = CASE WHEN excluded.state = 'held' THEN previews.head_sha ELSE excluded.head_sha END,

  last_push_at = excluded.last_push_at, updated_at = excluded.updated_at,
  state = CASE WHEN previews.state IN ('closing', 'closed', 'held') OR excluded.state IN ('fork', 'held') THEN excluded.state ELSE previews.state END;
-- name: PreviewOfPR :one
SELECT id, env_id FROM previews WHERE project_id = ? AND repo = ? AND number = ?;
-- name: ClosePreview :exec
UPDATE previews SET state = 'closing', updated_at = ? WHERE project_id = ? AND repo = ? AND number = ? AND state NOT IN ('closed', 'fork');
-- name: PreviewsOnBranch :many
SELECT id, project_id, env_id FROM previews WHERE repo = ? AND branch = ? AND state IN ('waiting', 'making', 'live', 'failed', 'held');
-- name: RenewPreview :exec
UPDATE previews SET last_push_at = sqlc.arg(now), head_sha = sqlc.arg(sha), updated_at = sqlc.arg(now),
  state = CASE WHEN state = 'failed' THEN 'making' WHEN state = 'held' THEN 'waiting' ELSE state END

WHERE id = sqlc.arg(id);
-- name: HoldPreview :exec
UPDATE previews SET state = 'held', updated_at = ? WHERE id = ?;
-- name: OpenPreviews :many
SELECT * FROM previews WHERE state IN ('waiting', 'making', 'live', 'failed', 'closing') ORDER BY created_at, id;
-- name: SetPreviewState :exec
UPDATE previews SET state = ?, env_id = ?, updated_at = ? WHERE id = ?;
-- name: SetPreviewOf :exec
UPDATE environments SET preview_of = ?, updated_at = ? WHERE id = ?;
-- name: EnvVarsAll :many
SELECT * FROM env_vars WHERE environment_id = ? ORDER BY service_id, key;
-- name: MarkPreviewEnvGone :exec
UPDATE environments SET deleted_at = sqlc.arg(now), purge_at = sqlc.arg(now), deleted_by = 'preview', updated_at = sqlc.arg(now) WHERE id = sqlc.arg(id) AND deleted_at IS NULL;
-- name: MarkPreviewServicesGone :exec
UPDATE services SET deleted_at = sqlc.arg(now), purge_at = sqlc.arg(now), deleted_by = 'preview', updated_at = sqlc.arg(now) WHERE environment_id = sqlc.arg(env_id) AND deleted_at IS NULL;
-- name: CommentablePreviews :many
SELECT * FROM previews WHERE state IN ('waiting', 'live', 'failed', 'fork', 'held') OR (state = 'closed' AND comment_id <> 0 AND comment_done = 0) ORDER BY created_at, id;
-- name: SetPreviewCommentDone :exec
UPDATE previews SET comment_done = 1 WHERE id = ?;
-- name: SetPreviewsMerged :exec
UPDATE previews SET base = ?, merge_sha = ?, merged_at = ?, updated_at = ? WHERE repo = ? AND number = ?;
-- name: MergeTargets :many
SELECT s.id, s.name, s.kind, s.repo, e.name AS env, srv.public_ip FROM services s JOIN environments e ON e.id = s.environment_id JOIN servers srv ON srv.id = e.server_id
WHERE e.project_id = ? AND e.preview_of = '' AND e.deleted_at IS NULL AND s.deleted_at IS NULL AND s.branch = ? AND s.auto_deploy = 1 AND s.kind IN ('web', 'worker')
ORDER BY e.name, s.name;
-- name: DeployOfCommit :one
SELECT * FROM deploys WHERE service_id = ? AND commit_sha = ? ORDER BY created_at DESC LIMIT 1;
-- name: PushSkipped :one
SELECT COUNT(*) FROM events WHERE kind = 'push.skipped' AND subject_id IN (sqlc.arg(service_id), sqlc.arg(repo)) AND json_extract(body_json, '$.commit') = sqlc.arg(commit);
-- name: SetPreviewComment :exec
UPDATE previews SET comment_id = ?, comment = ?, updated_at = ? WHERE id = ?;
-- name: RunningPreviews :one
SELECT COUNT(*) FROM previews WHERE project_id = ? AND state IN ('making', 'live');
-- name: ProjectPreviews :many
SELECT pv.*, e.name AS source FROM previews pv JOIN environments e ON e.id = pv.source_env_id JOIN projects p ON p.id = pv.project_id
WHERE p.name = sqlc.arg(project) AND (pv.state <> 'closed' OR (pv.merge_sha <> '' AND pv.env_id <> '' AND pv.merged_at > sqlc.arg(merged_since))) ORDER BY pv.number DESC;
-- name: ClosePreviewByID :execrows
UPDATE previews SET state = 'closing', updated_at = ? WHERE id = ? AND state IN ('waiting', 'making', 'live', 'failed');
-- name: DeletePreviewsOfEnv :exec
DELETE FROM previews WHERE source_env_id = ?;
-- name: DeletePreviewsOfProject :exec
DELETE FROM previews WHERE project_id = ?;
-- name: PreviewSourceOK :one
SELECT COUNT(*) FROM environments WHERE id = ? AND preview = 1 AND deleted_at IS NULL;
-- name: ClosePreviewsOfPR :exec
UPDATE previews SET state = 'closing', updated_at = ? WHERE repo = ? AND number = ? AND state IN ('waiting', 'making', 'live', 'failed', 'held');
-- name: ClosedPreviewOfPR :exec
UPDATE previews SET state = 'closed', updated_at = ? WHERE repo = ? AND number = ? AND state = 'fork';
-- name: RetitlePreviews :exec
UPDATE previews SET title = ?, updated_at = ? WHERE repo = ? AND number = ?;
-- name: SetPreviewFailure :exec
UPDATE previews SET state = 'failed', cause = ?, updated_at = ? WHERE id = ?;
-- name: SetPreviewMade :exec
UPDATE previews SET state = 'making', env_id = ?, last_push_at = ?, updated_at = ? WHERE id = ?;
-- name: TouchSignIn :exec
UPDATE owner SET last_sign_in_at = ?, updated_at = ? WHERE id = ?;
-- name: FirstAdmin :one
SELECT * FROM owner WHERE role = 'admin' ORDER BY created_at, id LIMIT 1;
-- name: GetAPITokenOwner :one
SELECT owner_id FROM api_tokens WHERE hash = ?;
-- name: OutsidePortsOnServer :many
SELECT s.outside_port, s.allow_json FROM services s JOIN environments e ON e.id = s.environment_id
WHERE e.server_id = ? AND s.kind = 'postgres' AND s.outside_port > 0 AND s.deleted_at IS NULL AND e.deleted_at IS NULL ORDER BY s.outside_port;
-- name: SetOutboxChannels :exec
UPDATE outbox SET sent_channels = ?, updated_at = ? WHERE id = ?;
-- name: ListPITRServices :many
SELECT s.id, s.name, p.name AS project, e.name AS env, e.server_id
FROM services s JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE s.kind = 'postgres' AND s.pitr = 1 AND s.user_stopped = 0 AND s.deleted_at IS NULL AND e.deleted_at IS NULL AND e.preview_of = '' ORDER BY s.id;
-- name: InsertWALBatch :exec
INSERT INTO wal_batches (id, service_id, object_key, first_wal, last_wal, size, files, sha256, backlog, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
-- name: ClearWALBatchFiles :exec
UPDATE wal_batches SET files = '' WHERE id = ?;
-- name: ListWALBatches :many
SELECT * FROM wal_batches WHERE service_id = ? ORDER BY created_at;
-- name: PurgeServiceWALBatches :exec
DELETE FROM wal_batches WHERE service_id = ?;
-- name: DeleteWALBatch :exec
DELETE FROM wal_batches WHERE id = ?;

-- name: SetUserStopped :exec
UPDATE services SET user_stopped = ?, stopped = 0, updated_at = ? WHERE id = ?;
-- name: ClearStopDone :exec
UPDATE services SET stopped = 0 WHERE id = ? AND deleted_at IS NULL;
-- name: ClearUserStopped :exec
UPDATE services SET user_stopped = 0, updated_at = ? WHERE id = ?;
-- name: SetMaintenance :exec
UPDATE services SET maintenance = ?, updated_at = ? WHERE id = ?;
-- name: SetBasicAuth :exec
UPDATE services SET basic_auth = ?, updated_at = ? WHERE id = ?;
-- name: CountLiveServices :one
SELECT COUNT(*) FROM services WHERE deleted_at IS NULL;
-- name: CountServers :one
SELECT COUNT(*) FROM servers;
