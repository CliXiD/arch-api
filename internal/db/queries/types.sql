-- name: CreateNodeType :exec
INSERT INTO node_types (id, kind, name, attributes_schema, builtin)
VALUES ($1, $2, $3, $4, false);

-- name: LockNodeType :one
SELECT id, kind, name, attributes_schema, builtin
FROM node_types
WHERE id = $1
FOR UPDATE;

-- name: UpdateNodeType :exec
UPDATE node_types
SET name = $2, attributes_schema = $3
WHERE id = $1;

-- name: DeleteNodeType :exec
DELETE FROM node_types
WHERE id = $1;

-- name: CountNodesOfType :one
SELECT count(*)
FROM nodes
WHERE type = $1;

-- name: ListNodeAttributesOfType :many
SELECT attributes
FROM nodes
WHERE type = $1;

-- name: CreateEdgeType :exec
INSERT INTO edge_types (id, name, attributes_schema, allowed, builtin)
VALUES ($1, $2, $3, $4, false);

-- name: LockEdgeType :one
SELECT id, name, attributes_schema, allowed, builtin
FROM edge_types
WHERE id = $1
FOR UPDATE;

-- name: UpdateEdgeType :exec
UPDATE edge_types
SET name = $2, attributes_schema = $3, allowed = $4
WHERE id = $1;

-- name: DeleteEdgeType :exec
DELETE FROM edge_types
WHERE id = $1;

-- name: CountEdgesOfType :one
SELECT count(*)
FROM edges
WHERE type = $1;

-- name: ListEdgesOfType :many
SELECT ft.kind AS from_kind, tt.kind AS to_kind, e.attributes
FROM edges e
JOIN nodes f ON f.environment = e.environment AND f.id = e.from_id
JOIN node_types ft ON ft.id = f.type
JOIN nodes t ON t.environment = e.environment AND t.id = e.to_id
JOIN node_types tt ON tt.id = t.type
WHERE e.type = $1;
