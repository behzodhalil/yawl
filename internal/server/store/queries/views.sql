-- name: ListEnvironmentTree :many
SELECT p.name AS project, e.name AS env, s.name AS server, e.preview, e.preview_of
FROM environments e JOIN projects p ON p.id = e.project_id JOIN servers s ON s.id = e.server_id
WHERE e.deleted_at IS NULL AND p.deleted_at IS NULL
ORDER BY p.name, CASE e.name WHEN 'production' THEN 0 ELSE 1 END, e.name;
-- name: DeployWarnings :many
SELECT body_json FROM events WHERE subject_id = ? AND kind = 'deploy.warning' ORDER BY created_at, id;
-- name: DeleteSettingKey :exec
DELETE FROM settings WHERE key = ?;
-- name: ListAPITokens :many
SELECT t.id, t.name, t.last_used_at, t.created_at, t.owner_id, CAST(COALESCE(o.email, '') AS TEXT) AS member
FROM api_tokens t LEFT JOIN owner o ON o.id = t.owner_id ORDER BY t.created_at, t.id;
-- name: DeleteAPIToken :execrows
DELETE FROM api_tokens WHERE id = ?;
-- name: DeleteOwnAPIToken :execrows
DELETE FROM api_tokens WHERE id = ? AND owner_id = ?;
