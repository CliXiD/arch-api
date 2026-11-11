# arch-api

The Architecture E2E API: a Go REST service (gin + Huma v2) that will hold the typed graph of
applications, infrastructure and network paths, answer impact queries, import and export YAML, and
keep the audit log. It owns the database schema.

The walking skeleton gave it configuration, health and readiness, Prometheus metrics, JSON request
logs, optional OpenTelemetry tracing, Keycloak token validation with default-deny roles, and
`GET /api/v1/me`. The model slice adds the typed graph: tables for node and edge types, teams,
environments, nodes and edges, the built-in vocabulary and the `dev`, `uat` and `prod`
environments (seeded by migration), read-only endpoints for viewers, and a sample graph for local
development (`seed --sample`). The write slice adds create, update and delete for nodes and edges
(editors, scoped to their teams, with optimistic locking) and for teams and environments (admins),
attribute and secret checks, the append-only `change_log` written in the same transaction as every
change, a node's history, and the split between the owner role (migrations) and the runtime role
`arch_app` (the server).

## Prerequisites

- Go 1.27. `go.mod` pins the toolchain (`toolchain go1.27.2`); with `GOTOOLCHAIN=auto` (the
  default) an older `go` downloads it.
- The hub's local dev stack (Postgres 16, Keycloak with realm `arch`): see the hub's `AGENTS.md`,
  *Local services*.

## Run it

From the hub:

```bash
dev/bin/stack up                  # Postgres and Keycloak
dev/bin/apply-env arch-api        # writes repos/arch-api/.env with dummy values
```

Then in this repo, with the `.env` variables exported (`set -a; . ./.env; set +a`):

```bash
go run ./cmd/arch-api migrate up      # apply migrations as the owner (MIGRATE_DATABASE_URL)
go run ./cmd/arch-api migrate status  # applied and pending versions
go run ./cmd/arch-api seed --sample   # optional: sample graph (7 nodes, 7 edges) in prod (DATABASE_URL)
go run ./cmd/arch-api serve           # API on HTTP_ADDR, metrics on METRICS_ADDR (DATABASE_URL)
```

`seed --sample` is for local development. It writes the teams `platform` and `core` and the sample
graph in one transaction, and refuses (exit `1`, nothing written) once the database has any node.

## Database roles

Two PostgreSQL roles (`adr/0014`):

- **Owner** (`arch` locally): owns the schema and runs `migrate` through `MIGRATE_DATABASE_URL`
  (falls back to `DATABASE_URL` when unset).
- **Runtime** `arch_app`: `serve` and `seed` connect as it through `DATABASE_URL`. Migration
  `00005_app_role_grants.sql` grants it `SELECT, INSERT, UPDATE, DELETE` on the model tables, only
  `SELECT, INSERT` on `change_log` (so the server can never rewrite history), the `change_log` id
  sequence, and `SELECT` on `goose_db_version` for `/readyz`.

The role `arch_app` must exist before `migrate up` (the hub's dev stack creates it; in other
environments the deploy does); otherwise the migration fails with `role "arch_app" does not
exist`. A server still connecting as the owner keeps working. Every future migration that adds a
table must grant it to `arch_app` in the same migration.

`serve` starts while the database is down (`/healthz` answers, `/readyz` reports it) and stops
on SIGINT/SIGTERM. Any other command prints the usage and exits `2`.

## Configuration

Read once from the environment at start-up. A missing or invalid variable stops the process with
`config: <NAME> is required` (or `is invalid`) and exit `1`; values are never printed.
`.env.example` lists every variable with dummy values.

| var | required | default | used by |
|---|---|---|---|
| `DATABASE_URL` | yes | — | `serve`, `seed` (as the runtime role `arch_app`); `migrate` when `MIGRATE_DATABASE_URL` is unset |
| `MIGRATE_DATABASE_URL` | no | `DATABASE_URL` | `migrate` (as the schema owner) |
| `HTTP_ADDR` | no | `:8080` | `serve` |
| `METRICS_ADDR` | no | `:9090` | `serve` |
| `OIDC_ISSUER` | yes for `serve` | — | token `iss` check, discovery |
| `OIDC_AUDIENCE` | yes for `serve` | — | token `aud` check |
| `OIDC_JWKS_URL` | no | `jwks_uri` from `<OIDC_ISSUER>/.well-known/openid-configuration` | token signatures |
| `LOG_LEVEL` | no | `info` (`debug`, `info`, `warn`, `error`) | logging |
| `CORS_ORIGINS` | no | empty (no CORS headers) | `serve` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | no | unset = tracing off | tracing |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | no | `grpc` (`grpc` or `http/protobuf`) | tracing |

## Endpoints

| method | path | auth | response |
|---|---|---|---|
| GET | `/healthz` | public | `200 {"status":"ok"}` |
| GET | `/readyz` | public | `200 {"status":"ok"}`, or `503 {"status":"unavailable","failing":["database"]}` / `["migrations"]` |
| GET | `/api/v1/me` | bearer, role `viewer` | `200` subject, name, email, roles, teams, actor_kind |
| GET | `/api/v1/types` | bearer, role `viewer` | `200 {node_types, edge_types}`, each ordered by id |
| GET | `/api/v1/teams` | bearer, role `viewer` | `200 {items}`, ordered by id |
| GET | `/api/v1/environments` | bearer, role `viewer` | `200 {items}`, ordered by sort |
| GET | `/api/v1/nodes` | bearer, role `viewer` | `200 {items, next_cursor}`; filters `environment`, `kind`, `type`, `owner`, `q`; `limit`, `cursor` |
| GET | `/api/v1/nodes/{environment}/{id}` | bearer, role `viewer` | `200` node with `ETag: "<version>"`, or `404` |
| GET | `/api/v1/edges` | bearer, role `viewer` | `200 {items, next_cursor}`; filters `environment`, `type`, `from`, `to`; `limit`, `cursor` |
| GET | `/api/v1/graph?environment=` | bearer, role `viewer` | `200 {environment, nodes, edges}`, compact; gzip when accepted |
| POST | `/api/v1/nodes` | bearer, role `editor`, owner team | `201` node, `ETag: "1"`; `409`, `422` |
| PATCH | `/api/v1/nodes/{environment}/{id}` | bearer, role `editor`, owner team, `If-Match` | `200` node with the new `ETag`; `404`, `412`, `422`, `428` |
| DELETE | `/api/v1/nodes/{environment}/{id}` | bearer, role `editor`, owner team, `If-Match` | `204`, with every edge from or to the node; `404`, `412`, `428` |
| POST | `/api/v1/edges` | bearer, role `editor`, team of the `from` node | `201` edge, `ETag: "1"`; `409`, `422` |
| PATCH | `/api/v1/edges/{uid}` | bearer, role `editor`, team of the `from` node, `If-Match` | `200` edge with the new `ETag`; `404`, `412`, `422`, `428` |
| DELETE | `/api/v1/edges/{uid}` | bearer, role `editor`, team of the `from` node, `If-Match` | `204`; `404`, `412`, `428` |
| POST | `/api/v1/teams` | bearer, role `admin` | `201` team; `409`, `422` |
| PATCH | `/api/v1/teams/{id}` | bearer, role `admin` | `200` team; `404` |
| DELETE | `/api/v1/teams/{id}` | bearer, role `admin` | `204`; `409 /problems/in-use` while it owns nodes |
| POST | `/api/v1/environments` | bearer, role `admin` | `201` environment; `409` (id or `sort` taken), `422` |
| PATCH | `/api/v1/environments/{id}` | bearer, role `admin` | `200` environment; `404`, `409` |
| DELETE | `/api/v1/environments/{id}` | bearer, role `admin` | `204`; `409 /problems/in-use` while it holds nodes |
| POST | `/api/v1/admin/types/node-types` | bearer, role `admin` | `201` node type (`builtin: false`); `409`, `422` |
| PATCH | `/api/v1/admin/types/node-types/{id}` | bearer, role `admin` | `200` node type; `404`, `409` (built-in loses an attribute, or existing nodes would fail, with `count`), `422` |
| DELETE | `/api/v1/admin/types/node-types/{id}` | bearer, role `admin` | `204`; `404`, `409` (built-in), `409 /problems/in-use` while nodes use it |
| POST | `/api/v1/admin/types/edge-types` | bearer, role `admin` | `201` edge type (`builtin: false`); `409`, `422` |
| PATCH | `/api/v1/admin/types/edge-types/{id}` | bearer, role `admin` | `200` edge type; `404`, `409` (built-in loses an attribute, or existing edges would fail, with `count`), `422` |
| DELETE | `/api/v1/admin/types/edge-types/{id}` | bearer, role `admin` | `204`; `404`, `409` (built-in), `409 /problems/in-use` while edges use it |
| GET | `/api/v1/history?entity=node&environment=&id=` | bearer, role `viewer` | `200 {items, next_cursor}`: the node's and its edges' change_log rows, newest first; `limit` (default `50`), `cursor` |
| GET | `/metrics` | public, on `METRICS_ADDR` only | Prometheus text |

Every `/api/v1` route answers, as RFC 9457 `application/problem+json`:

- `401` (`/problems/unauthenticated`) without a valid token;
- `403` (`/problems/forbidden`) when the role is too low;
- `503` (`/problems/auth-unavailable`, `Retry-After: 5`) when a bearer token arrives while the API
  has never loaded the realm's signing keys (the fetch failed, or is held back for 5 s after a
  failure);
- `413` (`/problems/too-large`) when the request body is over 1 MB. This limit applies to every
  route of the API server and is checked before authentication.

Every operation is registered with a minimum role; a unit test fails when one is missing.

A token is a service account (`actor_kind: "service"`, subject and name = its client id) only when
its `client_id` claim equals `azp`; every other token is a user. CI clients therefore need the
`client_id` protocol mapper (user session note `clientId`) in Keycloak.

Both servers use a 10 s read-header timeout, 30 s read and write timeouts and a 120 s idle timeout.

### Writes

Every successful write runs in one transaction that also appends one `change_log` row per changed
entity: who (`actor` = token subject, `actor_name` = username, `actor_kind` `user` or `service`;
a service account is its client id), `source` (`ui` for a user token, `api` for a service
account; the node or edge written gets the same `source`), `entity` (`node`, `edge`, `team`,
`environment`, `type`), `entity_key` (`<environment>/<id>`, the edge uid, `node/<id>` or
`edge/<id>` for a node or edge type, or the id), `action` and the
entity's JSON `before` and `after`. A rejected write leaves no row. A trigger rejects `UPDATE`,
`DELETE` and `TRUNCATE` on `change_log` for every role, the owner included.

- **Optimistic locking.** Node and edge `GET`, `POST` and `PATCH` return `ETag: "<version>"`.
  `PATCH` and `DELETE` of a node or an edge need `If-Match` with that value: without it `428`
  (`/problems/precondition-required`); with any other value, `*` or a weak tag included, `412`
  (`/problems/stale`). A `PATCH` that changes nothing returns `200` with the same version and
  writes no row.
- **Permissions.** Admins may write anything. An editor writes a node only when its `owner` is one
  of the token's `teams` (an owner change needs both the old and the new team), and an edge only
  when it may write its `from` node. Otherwise `403` (`/problems/forbidden`), `detail`
  `node is owned by team <team>`. Deleting a node deletes every edge from or to it, whoever owns
  the other end. Teams, environments and types are admin-only. An `owner` that is not an existing
  team gives `422` at `body.owner` (`team does not exist`) before any team-membership check.
- **Patches.** Each field present replaces the stored value; `tags` and `attributes` are replaced
  whole. `id`, `type`, `environment`, `from` and `to` cannot change: `422` at `body.<field>`.
- **Attributes** are checked against the type's `attributes_schema` (`type`, `enum`, `minimum`,
  `maximum`, `required`, `additionalProperties: false`). Undeclared attributes are allowed, but
  every value must be a string, number, boolean or array of strings. Edges also need both nodes in
  their `environment` and a (from kind, to kind) pair their type allows: `422` at `body.from` when
  the source node's kind starts no allowed pair of the type, otherwise at `body.to`.
- **Secrets.** Attribute names holding `password`, `passwd`, `secret`, `api_key`, `private_key` or
  `credential` anywhere, or `token` as a whole word or the end of one (`access_token`, `apiToken`;
  not `max_tokens`), and string values holding a URL with a password (whatever characters the
  password holds), a PEM header or an AWS access key id, give `422` (`/problems/secret-detected`)
  with one `errors[]` entry per attribute (`body.attributes.<name>`, `message`
  `looks like a secret`). Values are never echoed or logged.
- **Types.** A type's `attributes_schema` must use exactly the subset above: `type: "object"`,
  `properties` (may be `{}`), `required` (declared names, each once) and `additionalProperties`
  (boolean); per property a `type` of `string`, `number`, `boolean` or `array`, with `enum`
  (string or number, non-empty), `minimum`/`maximum` (number, minimum not above maximum) and
  `items: {"type": "string"}` (array, required). Anything else, an unknown keyword included, gives
  `422` at `body.attributes_schema`, `body.attributes_schema.properties.<name>` or
  `body.attributes_schema.required`. Property names follow the secret name rules
  (`422 /problems/secret-detected` at `body.attributes_schema.properties.<name>`). A changed schema
  or `allowed` is checked against every existing node or edge of the type: when any would fail,
  `409` (`/problems/conflict`) with `count`, the number of failing nodes or edges. `id` and `kind`
  cannot change. Built-in types can be renamed and gain attributes, but can't lose one or be
  deleted. A type change waits for node and edge writes of that type, and the reverse.

| status | type | when |
|---|---|---|
| 403 | `/problems/forbidden` | role too low, or the editor lacks the node's team |
| 404 | `/problems/not-found` | the node, edge, type, team or environment in the path does not exist (`node type not found`, `edge type not found`) |
| 409 | `/problems/conflict` | duplicate node, edge (`environment, type, from, to`), node or edge type, team or environment id or `sort` |
| 409 | `/problems/conflict` | deleting a built-in type, or a built-in type losing an attribute; no `count` |
| 409 | `/problems/conflict` | a type change existing nodes or edges would fail; `count` is the number of them |
| 409 | `/problems/in-use` | deleting a team that owns nodes, an environment that holds nodes, or a type nodes or edges use; `count` is the number of them |
| 412 | `/problems/stale` | `If-Match` is not the current version |
| 422 | `/problems/validation` | request schema, unknown reference, attribute schema, kind pair; one `errors[]` entry per field |
| 422 | `/problems/secret-detected` | an attribute looks like a secret |
| 428 | `/problems/precondition-required` | `If-Match` missing on a node or edge `PATCH` or `DELETE` |

Pages hold `limit` items (default `100`, history `50`; `1` to `500`) in a stable order: nodes by
`(environment, id)`, edges by `(environment, type, from, to)`, history by `(at, id)` descending.
Pass `next_cursor` back as `cursor` for the next page; it is `null` on the last one. A bad `limit`,
`cursor` or `kind`, an unknown `environment`, or a `q` over 200 characters gives `422`
(`/problems/validation`); an unknown node gives `404` (`/problems/not-found`). Problems never echo
the received values. History of an unknown node is an empty page; a deleted node keeps its rows.

## OpenAPI

`api/openapi.yaml` is the contract with `arch-web`. It is generated from the Huma types; never edit
it by hand.

```bash
make openapi        # regenerate api/openapi.yaml; commit it with the change
make openapi-check  # fail when the committed file is stale (writes only a temp file)
```

## Database code

Queries are SQL in `internal/db/queries/`; sqlc generates typed Go for them in `internal/db/dbq`
from those files and the schema in `migrations/`. Never edit `internal/db/dbq` by hand.

```bash
make sqlc           # go tool sqlc generate; commit the result with the query change
```

## Checks

The same steps run in CI (`.github/workflows/ci.yml`) and in the hub's factory gate
(`factory/checks/arch-api.sh`):

```bash
go vet ./...
go tool golangci-lint run ./...
go test -count=1 ./...
go build ./...
make openapi-check
```

Integration tests run against a real PostgreSQL 16 in a container (testcontainers), so they need
podman or docker. They create the role `arch_app`, migrate as the container's owner `arch`, and
run the API as `arch_app`. They sit behind the `integration` build tag and stay out of the checks
above:

```bash
go test -tags integration -count=1 ./...
```

With podman, point testcontainers at its socket, for example
`DOCKER_HOST=unix://$(podman machine inspect --format '{{.ConnectionInfo.PodmanSocket.Path}}')`
and `TESTCONTAINERS_RYUK_DISABLED=true`.

## Layout

```
cmd/arch-api         subcommand dispatch: serve, migrate up|status, seed --sample
cmd/openapi          prints the OpenAPI YAML (make openapi)
internal/config      environment parsing and validation
internal/auth        JWKS cache, token validation, roles, Principal
internal/httpapi     gin engine, Huma API, middleware, routes, handlers
internal/model       model reads (paging, cursors, graph), writes with the change_log, history,
                     attribute validation, secret detection and the sample seed
internal/health      readiness checks
internal/observability  slog, Prometheus metrics, OpenTelemetry
internal/db          pgxpool and goose
internal/db/queries  SQL queries for sqlc
internal/db/dbq      sqlc output (make sqlc); never edit by hand
migrations           goose SQL migrations, embedded; never edit an existing one
```

## Documents

In the hub's `docs/`: specs `specs/001_identity-access.md`, `specs/002_model.md`,
`specs/003_editing-history.md`, `specs/008_admin.md` and `specs/009_operations.md`; ADRs
`adr/0003` (gin + Huma), `adr/0004` (database and migrations), `adr/0005` (change log),
`adr/0008` (Keycloak),
`adr/0010` (CI), `adr/0011` (observability), `adr/0013` (runtime versions) and `adr/0014`
(database roles).
