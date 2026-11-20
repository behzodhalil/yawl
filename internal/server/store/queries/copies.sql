-- name: ListStorageCopies :many
SELECT * FROM storage_copies ORDER BY name;
-- name: GetStorageCopy :one
SELECT * FROM storage_copies WHERE name = ?;
-- name: UpsertStorageCopy :exec
INSERT INTO storage_copies (name, endpoint, bucket, region, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET endpoint = excluded.endpoint, bucket = excluded.bucket, region = excluded.region, updated_at = excluded.updated_at;
-- name: DeleteStorageCopy :exec
DELETE FROM storage_copies WHERE name = ?;
-- name: SetBackupCopy :exec
INSERT INTO backup_copies (backup_id, copy, state, cause, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (backup_id, copy) DO UPDATE SET state = excluded.state, cause = excluded.cause, updated_at = excluded.updated_at;
-- name: CopiesOfService :many
SELECT c.* FROM backup_copies c JOIN backups b ON b.id = c.backup_id WHERE b.service_id = ? ORDER BY c.backup_id, c.copy;
-- name: PurgeServiceBackupCopies :exec
DELETE FROM backup_copies WHERE backup_id IN (SELECT id FROM backups WHERE service_id = ?);
-- name: SetRestoreStorage :exec
UPDATE deploys SET restore_storage = ? WHERE id = ?;
-- name: GoodCopy :one
SELECT COUNT(*) FROM backup_copies c JOIN storage_copies s ON s.name = c.copy WHERE c.backup_id = ? AND c.copy = ? AND c.state = 'ok';
-- name: ForgetCopy :exec
DELETE FROM backup_copies WHERE copy = ?;
-- name: CopyLeftovers :many
SELECT b.id, b.object_key FROM backup_copies c JOIN backups b ON b.id = c.backup_id WHERE c.copy = ? AND b.service_id = ? AND b.state = 'deleted';
-- name: DropBackupCopy :exec
DELETE FROM backup_copies WHERE backup_id = ? AND copy = ?;
-- name: BackupsMissingCopy :many
SELECT b.* FROM backups b JOIN services s ON s.id = b.service_id
WHERE b.state = 'ok' AND b.kind <> 'base' AND s.deleted_at IS NULL
  AND b.created_at = (SELECT MAX(x.created_at) FROM backups x WHERE x.service_id = b.service_id AND x.volume = b.volume AND x.state = 'ok' AND x.kind <> 'base')
  AND EXISTS (SELECT 1 FROM storage_copies c WHERE NOT EXISTS (SELECT 1 FROM backup_copies k WHERE k.backup_id = b.id AND k.copy = c.name));
-- name: CopyIncidents :many
SELECT DISTINCT b.service_id, b.volume, p.name AS project, e.name AS env, s.name AS service FROM backup_copies c JOIN backups b ON b.id = c.backup_id
JOIN services s ON s.id = b.service_id JOIN environments e ON e.id = s.environment_id JOIN projects p ON p.id = e.project_id
WHERE c.copy = ? AND c.state = 'failed';
