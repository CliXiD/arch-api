-- Writes by a service account record source `api` ([BR-004], [SPEC-model-08]): widen the source
-- checks of nodes, edges and change_log. Expand only; existing rows keep their source.
-- +goose Up
ALTER TABLE nodes DROP CONSTRAINT nodes_source_check,
    ADD CONSTRAINT nodes_source_check CHECK (source IN ('ui', 'api', 'yaml', 'discovery', 'import'));
ALTER TABLE edges DROP CONSTRAINT edges_source_check,
    ADD CONSTRAINT edges_source_check CHECK (source IN ('ui', 'api', 'yaml', 'discovery', 'import'));
ALTER TABLE change_log DROP CONSTRAINT change_log_source_check,
    ADD CONSTRAINT change_log_source_check CHECK (source IN ('ui', 'api', 'yaml', 'discovery', 'import'));

-- +goose Down
-- Fails once any `api` row exists: change_log rows can't be changed, so there is no clean way back.
ALTER TABLE change_log DROP CONSTRAINT change_log_source_check,
    ADD CONSTRAINT change_log_source_check CHECK (source IN ('ui', 'yaml', 'discovery', 'import'));
ALTER TABLE edges DROP CONSTRAINT edges_source_check,
    ADD CONSTRAINT edges_source_check CHECK (source IN ('ui', 'yaml', 'discovery', 'import'));
ALTER TABLE nodes DROP CONSTRAINT nodes_source_check,
    ADD CONSTRAINT nodes_source_check CHECK (source IN ('ui', 'yaml', 'discovery', 'import'));
