-- The typed graph: vocabulary (node and edge types), teams, environments, nodes and edges.
-- Foreign keys do not cascade; deletes belong to the write slice.
-- +goose Up
CREATE TABLE environments (
    id   text PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    name text NOT NULL,
    sort integer NOT NULL UNIQUE
);

CREATE TABLE teams (
    id         text PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE node_types (
    id                text PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    kind              text NOT NULL CHECK (kind IN ('application', 'infrastructure', 'network', 'external', 'team')),
    name              text NOT NULL,
    attributes_schema jsonb NOT NULL DEFAULT '{"type":"object","properties":{}}',
    builtin           boolean NOT NULL DEFAULT false
);

CREATE TABLE edge_types (
    id                text PRIMARY KEY CHECK (id ~ '^[a-z][a-z0-9_]{1,62}$'),
    name              text NOT NULL,
    attributes_schema jsonb NOT NULL DEFAULT '{"type":"object","properties":{}}',
    allowed           jsonb NOT NULL DEFAULT '[]',
    builtin           boolean NOT NULL DEFAULT false
);

CREATE TABLE nodes (
    environment text NOT NULL REFERENCES environments,
    id          text NOT NULL CHECK (id ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    type        text NOT NULL REFERENCES node_types,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    owner       text NOT NULL REFERENCES teams,
    attributes  jsonb NOT NULL DEFAULT '{}',
    tags        text[] NOT NULL DEFAULT '{}',
    source      text NOT NULL CHECK (source IN ('ui', 'yaml', 'discovery', 'import')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    version     integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    PRIMARY KEY (environment, id)
);

CREATE INDEX nodes_environment_type_idx ON nodes (environment, type);
CREATE INDEX nodes_environment_owner_idx ON nodes (environment, owner);

CREATE TABLE edges (
    uid         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    environment text NOT NULL REFERENCES environments,
    type        text NOT NULL REFERENCES edge_types,
    from_id     text NOT NULL,
    to_id       text NOT NULL,
    attributes  jsonb NOT NULL DEFAULT '{}',
    source      text NOT NULL CHECK (source IN ('ui', 'yaml', 'discovery', 'import')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    version     integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    UNIQUE (environment, type, from_id, to_id),
    FOREIGN KEY (environment, from_id) REFERENCES nodes (environment, id),
    FOREIGN KEY (environment, to_id) REFERENCES nodes (environment, id)
);

CREATE INDEX edges_environment_from_idx ON edges (environment, from_id);
CREATE INDEX edges_environment_to_idx ON edges (environment, to_id);

-- +goose Down
DROP TABLE edges;
DROP TABLE nodes;
DROP TABLE edge_types;
DROP TABLE node_types;
DROP TABLE teams;
DROP TABLE environments;
