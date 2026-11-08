-- name: AnyNodeExists :one
SELECT EXISTS (SELECT 1 FROM nodes);

-- name: InsertTeam :exec
INSERT INTO teams (id, name)
VALUES ($1, $2)
ON CONFLICT (id) DO NOTHING;

-- name: InsertNode :exec
INSERT INTO nodes (environment, id, type, name, description, owner, source)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: InsertEdge :exec
INSERT INTO edges (environment, type, from_id, to_id, attributes, source)
VALUES ($1, $2, $3, $4, $5, $6);
