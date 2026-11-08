# arch-api

The Architecture E2E API: a Go REST service (gin + Huma v2) that will hold the typed graph of
applications, infrastructure and network paths, answer impact queries, import and export YAML, and
keep the audit log. It owns the database schema.

This first slice is the walking skeleton: configuration, health and readiness, Prometheus metrics,
JSON request logs, optional OpenTelemetry tracing, Keycloak token validation with default-deny
roles, and `GET /api/v1/me`.

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
go run ./cmd/arch-api migrate up      # apply migrations (MIGRATE_DATABASE_URL)
go run ./cmd/arch-api migrate status  # applied and pending versions
go run ./cmd/arch-api serve           # API on HTTP_ADDR, metrics on METRICS_ADDR
```

`serve` starts while the database is down (`/healthz` answers, `/readyz` reports it) and stops
on SIGINT/SIGTERM. Any other command prints the usage and exits `2`.

## Configuration

Read once from the environment at start-up. A missing or invalid variable stops the process with
`config: <NAME> is required` (or `is invalid`) and exit `1`; values are never printed.
`.env.example` lists every variable with dummy values.

| var | required | default | used by |
|---|---|---|---|
| `DATABASE_URL` | yes | — | `serve`; `migrate` when `MIGRATE_DATABASE_URL` is unset |
| `MIGRATE_DATABASE_URL` | no | `DATABASE_URL` | `migrate` |
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
| GET | `/metrics` | public, on `METRICS_ADDR` only | Prometheus text |

Every `/api/v1` route answers `401` (`/problems/unauthenticated`) without a valid token and `403`
(`/problems/forbidden`) when the role is too low, as RFC 9457 `application/problem+json`. Every
operation is registered with a minimum role; a unit test fails when one is missing.

## OpenAPI

`api/openapi.yaml` is the contract with `arch-web`. It is generated from the Huma types; never edit
it by hand.

```bash
make openapi        # regenerate api/openapi.yaml; commit it with the change
make openapi-check  # fail when the committed file is stale (writes only a temp file)
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

## Layout

```
cmd/arch-api         subcommand dispatch: serve, migrate up|status
cmd/openapi          prints the OpenAPI YAML (make openapi)
internal/config      environment parsing and validation
internal/auth        JWKS cache, token validation, roles, Principal
internal/httpapi     gin engine, Huma API, middleware, routes, handlers
internal/health      readiness checks
internal/observability  slog, Prometheus metrics, OpenTelemetry
internal/db          pgxpool and goose
migrations           goose SQL migrations, embedded; never edit an existing one
```

## Documents

In the hub's `docs/`: specs `specs/001_identity-access.md` and `specs/009_operations.md`; ADRs
`adr/0003` (gin + Huma), `adr/0004` (database and migrations), `adr/0008` (Keycloak),
`adr/0010` (CI), `adr/0011` (observability), `adr/0013` (runtime versions) and `adr/0014`
(database roles).
