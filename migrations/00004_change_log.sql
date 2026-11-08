-- The append-only change log: one row per created, updated or deleted entity, written in the same
-- transaction as the change. A trigger rejects UPDATE, DELETE and TRUNCATE, whoever runs them.
-- entity_key: node `<environment>/<id>`, edge `<uid>`, team and environment `<id>`.
-- +goose Up
CREATE TABLE change_log (
    id         bigserial PRIMARY KEY,
    at         timestamptz NOT NULL DEFAULT now(),
    actor      text NOT NULL,
    actor_name text NOT NULL,
    actor_kind text NOT NULL CHECK (actor_kind IN ('user', 'service')),
    source     text NOT NULL CHECK (source IN ('ui', 'yaml', 'discovery', 'import')),
    entity     text NOT NULL CHECK (entity IN ('node', 'edge', 'type', 'team', 'environment')),
    entity_key text NOT NULL,
    action     text NOT NULL CHECK (action IN ('create', 'update', 'delete')),
    before     jsonb NULL,
    after      jsonb NULL,
    CHECK (before IS NOT NULL OR after IS NOT NULL)
);

CREATE INDEX change_log_entity_idx ON change_log (entity, entity_key, at DESC, id DESC);

-- A node's edges, for its history. An edge's environment, from and to never change.
CREATE INDEX change_log_edge_from_idx ON change_log (
    (coalesce(after, before) ->> 'environment'), (coalesce(after, before) ->> 'from'), at DESC, id DESC
) WHERE entity = 'edge';
CREATE INDEX change_log_edge_to_idx ON change_log (
    (coalesce(after, before) ->> 'environment'), (coalesce(after, before) ->> 'to'), at DESC, id DESC
) WHERE entity = 'edge';

-- +goose StatementBegin
CREATE FUNCTION change_log_reject() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'change_log is append-only: % is not allowed', TG_OP;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER change_log_reject_update_delete
    BEFORE UPDATE OR DELETE ON change_log
    FOR EACH ROW EXECUTE FUNCTION change_log_reject();

CREATE TRIGGER change_log_reject_truncate
    BEFORE TRUNCATE ON change_log
    FOR EACH STATEMENT EXECUTE FUNCTION change_log_reject();

-- +goose Down
DROP TRIGGER change_log_reject_truncate ON change_log;
DROP TRIGGER change_log_reject_update_delete ON change_log;
DROP FUNCTION change_log_reject();
DROP TABLE change_log;
