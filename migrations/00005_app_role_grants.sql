-- The runtime role arch_app ([ADR-0014]): DML on the model tables, only SELECT and INSERT on
-- change_log, and SELECT on goose_db_version for readiness. The role must exist before this runs.
-- Every later migration that adds a table grants it to arch_app too.
-- +goose Up
GRANT USAGE ON SCHEMA public TO arch_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON environments, teams, node_types, edge_types, nodes, edges TO arch_app;
GRANT SELECT, INSERT ON change_log TO arch_app;
GRANT USAGE, SELECT ON SEQUENCE change_log_id_seq TO arch_app;
GRANT SELECT ON goose_db_version TO arch_app;

-- +goose Down
REVOKE SELECT ON goose_db_version FROM arch_app;
REVOKE USAGE, SELECT ON SEQUENCE change_log_id_seq FROM arch_app;
REVOKE SELECT, INSERT ON change_log FROM arch_app;
REVOKE SELECT, INSERT, UPDATE, DELETE ON environments, teams, node_types, edge_types, nodes, edges FROM arch_app;
REVOKE USAGE ON SCHEMA public FROM arch_app;
