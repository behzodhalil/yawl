-- name: CreateImport :exec
INSERT INTO imports (id, service_id, source, ref, file, token, created_at) VALUES (?, ?, ?, ?, ?, ?, ?);
-- name: GetImport :one
SELECT * FROM imports WHERE id = ?;
-- name: CreateImportDeploy :exec
INSERT INTO deploys (id, service_id, commit_sha, state, import_id, created_at, updated_at) VALUES (?, ?, '', 'queued', ?, ?, ?);
-- name: EndedUploads :many
SELECT i.id, i.file FROM imports i JOIN deploys d ON d.import_id = i.id
WHERE i.file != '' AND d.state IN ('live', 'failed', 'rolled_back', 'superseded');
-- name: ClearImportFile :exec
UPDATE imports SET file = '' WHERE id = ?;
-- name: ImportFiles :many
SELECT file FROM imports WHERE file != '';
