-- name: DockerNames :many
SELECT p.name AS project, e.name AS env, COALESCE(s.name, '') AS service
FROM environments e JOIN projects p ON p.id = e.project_id LEFT JOIN services s ON s.environment_id = e.id;
