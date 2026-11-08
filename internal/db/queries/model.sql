-- name: ListNodeTypes :many
SELECT id, kind, name, attributes_schema, builtin
FROM node_types
ORDER BY id;

-- name: ListEdgeTypes :many
SELECT id, name, attributes_schema, allowed, builtin
FROM edge_types
ORDER BY id;

-- name: ListTeams :many
SELECT id, name, created_at
FROM teams
ORDER BY id;

-- name: ListEnvironments :many
SELECT id, name, sort
FROM environments
ORDER BY sort;

-- name: EnvironmentExists :one
SELECT EXISTS (SELECT 1 FROM environments WHERE id = $1);

-- name: ListNodes :many
SELECT n.environment, n.id, t.kind, n.type, n.name, n.description, n.owner, n.attributes, n.tags,
       n.source, n.created_at, n.updated_at, n.version
FROM nodes n
JOIN node_types t ON t.id = n.type
WHERE (sqlc.narg('environment')::text IS NULL OR n.environment = sqlc.narg('environment'))
  AND (sqlc.narg('kind')::text IS NULL OR t.kind = sqlc.narg('kind'))
  AND (sqlc.narg('type')::text IS NULL OR n.type = sqlc.narg('type'))
  AND (sqlc.narg('owner')::text IS NULL OR n.owner = sqlc.narg('owner'))
  AND (sqlc.narg('pattern')::text IS NULL OR n.id ILIKE sqlc.narg('pattern') OR n.name ILIKE sqlc.narg('pattern'))
  AND (NOT sqlc.arg('after')::boolean
       OR (n.environment, n.id) > (sqlc.arg('after_environment')::text, sqlc.arg('after_id')::text))
ORDER BY n.environment, n.id
LIMIT sqlc.arg('row_limit');

-- name: GetNode :one
SELECT n.environment, n.id, t.kind, n.type, n.name, n.description, n.owner, n.attributes, n.tags,
       n.source, n.created_at, n.updated_at, n.version
FROM nodes n
JOIN node_types t ON t.id = n.type
WHERE n.environment = $1 AND n.id = $2;

-- name: ListEdges :many
SELECT uid, environment, type, from_id, to_id, attributes, source, created_at, updated_at, version
FROM edges
WHERE (sqlc.narg('environment')::text IS NULL OR environment = sqlc.narg('environment'))
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type'))
  AND (sqlc.narg('from_id')::text IS NULL OR from_id = sqlc.narg('from_id'))
  AND (sqlc.narg('to_id')::text IS NULL OR to_id = sqlc.narg('to_id'))
  AND (NOT sqlc.arg('after')::boolean
       OR (environment, type, from_id, to_id) > (sqlc.arg('after_environment')::text, sqlc.arg('after_type')::text,
                                                 sqlc.arg('after_from_id')::text, sqlc.arg('after_to_id')::text))
ORDER BY environment, type, from_id, to_id
LIMIT sqlc.arg('row_limit');

-- name: GraphNodes :many
SELECT n.id, t.kind, n.type, n.name, n.owner, n.tags
FROM nodes n
JOIN node_types t ON t.id = n.type
WHERE n.environment = $1
ORDER BY n.id;

-- name: GraphEdges :many
SELECT uid, type, from_id, to_id, attributes
FROM edges
WHERE environment = $1
ORDER BY type, from_id, to_id;
