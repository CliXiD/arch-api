-- name: GetNodeType :one
SELECT id, kind, attributes_schema
FROM node_types
WHERE id = $1;

-- name: GetEdgeType :one
SELECT id, attributes_schema, allowed
FROM edge_types
WHERE id = $1;

-- name: TeamExists :one
SELECT EXISTS (SELECT 1 FROM teams WHERE id = $1);

-- name: NodeOwnerKind :one
SELECT n.owner, t.kind
FROM nodes n
JOIN node_types t ON t.id = n.type
WHERE n.environment = $1 AND n.id = $2;

-- name: LockNode :one
SELECT n.environment, n.id, t.kind, n.type, n.name, n.description, n.owner, n.attributes, n.tags,
       n.source, n.created_at, n.updated_at, n.version
FROM nodes n
JOIN node_types t ON t.id = n.type
WHERE n.environment = $1 AND n.id = $2
FOR UPDATE OF n;

-- name: CreateNode :one
INSERT INTO nodes (environment, id, type, name, description, owner, attributes, tags, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'ui')
RETURNING created_at, updated_at, version;

-- name: UpdateNode :one
UPDATE nodes
SET name = $3, description = $4, owner = $5, attributes = $6, tags = $7,
    source = 'ui', version = version + 1, updated_at = now()
WHERE environment = $1 AND id = $2
RETURNING updated_at, version;

-- name: DeleteNode :exec
DELETE FROM nodes
WHERE environment = $1 AND id = $2;

-- name: LockEdge :one
SELECT uid, environment, type, from_id, to_id, attributes, source, created_at, updated_at, version
FROM edges
WHERE uid = $1
FOR UPDATE;

-- name: ShareEdgeSourceOwner :one
SELECT n.owner
FROM edges e
JOIN nodes n ON n.environment = e.environment AND n.id = e.from_id
WHERE e.uid = $1
FOR SHARE OF n;

-- name: LockNodeEdges :many
SELECT uid, environment, type, from_id, to_id, attributes, source, created_at, updated_at, version
FROM edges
WHERE environment = sqlc.arg('environment') AND (from_id = sqlc.arg('node_id') OR to_id = sqlc.arg('node_id'))
ORDER BY uid
FOR UPDATE;

-- name: CreateEdge :one
INSERT INTO edges (environment, type, from_id, to_id, attributes, source)
VALUES ($1, $2, $3, $4, $5, 'ui')
RETURNING uid, created_at, updated_at, version;

-- name: UpdateEdge :one
UPDATE edges
SET attributes = $2, source = 'ui', version = version + 1, updated_at = now()
WHERE uid = $1
RETURNING updated_at, version;

-- name: DeleteEdge :exec
DELETE FROM edges
WHERE uid = $1;

-- name: LockTeam :one
SELECT id, name, created_at
FROM teams
WHERE id = $1
FOR UPDATE;

-- name: CreateTeam :one
INSERT INTO teams (id, name)
VALUES ($1, $2)
RETURNING id, name, created_at;

-- name: UpdateTeam :exec
UPDATE teams
SET name = $2
WHERE id = $1;

-- name: DeleteTeam :exec
DELETE FROM teams
WHERE id = $1;

-- name: CountTeamNodes :one
SELECT count(*)
FROM nodes
WHERE owner = $1;

-- name: LockEnvironment :one
SELECT id, name, sort
FROM environments
WHERE id = $1
FOR UPDATE;

-- name: CreateEnvironment :exec
INSERT INTO environments (id, name, sort)
VALUES ($1, $2, $3);

-- name: UpdateEnvironment :exec
UPDATE environments
SET name = $2, sort = $3
WHERE id = $1;

-- name: DeleteEnvironment :exec
DELETE FROM environments
WHERE id = $1;

-- name: CountEnvironmentNodes :one
SELECT count(*)
FROM nodes
WHERE environment = $1;
