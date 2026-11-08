-- name: InsertChangeLog :exec
INSERT INTO change_log (actor, actor_name, actor_kind, source, entity, entity_key, action, before, after)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: NodeHistory :many
-- A node's rows and its edges' rows, newest first. Each branch matches one change_log index.
SELECT id, at, actor, actor_name, actor_kind, source, entity, entity_key, action, before, after
FROM change_log
WHERE ((entity = 'node' AND entity_key = sqlc.arg('environment')::text || '/' || sqlc.arg('node_id')::text)
       OR (entity = 'edge' AND coalesce(after, before) ->> 'environment' = sqlc.arg('environment')::text
           AND coalesce(after, before) ->> 'from' = sqlc.arg('node_id')::text)
       OR (entity = 'edge' AND coalesce(after, before) ->> 'environment' = sqlc.arg('environment')::text
           AND coalesce(after, before) ->> 'to' = sqlc.arg('node_id')::text))
  AND (NOT sqlc.arg('paged')::boolean
       OR (at, id) < (sqlc.arg('cursor_at')::timestamptz, sqlc.arg('cursor_id')::bigint))
ORDER BY at DESC, id DESC
LIMIT sqlc.arg('row_limit');
