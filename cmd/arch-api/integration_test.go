//go:build integration

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/CliXiD/arch-api/internal/auth"
	"github.com/CliXiD/arch-api/internal/auth/authtest"
	"github.com/CliXiD/arch-api/internal/db"
	"github.com/CliXiD/arch-api/internal/health"
	"github.com/CliXiD/arch-api/internal/httpapi"
	"github.com/CliXiD/arch-api/internal/model"
	"github.com/CliXiD/arch-api/internal/observability"
)

type okReady struct{}

func (okReady) Check(context.Context) health.Report { return health.Report{Status: "ok"} }

type client struct {
	engine *gin.Engine
	token  string
}

func (c client) get(t *testing.T, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	rec := httptest.NewRecorder()
	c.engine.ServeHTTP(rec, req)
	return rec
}

func (c client) json(t *testing.T, path string, out any) {
	t.Helper()
	rec := c.get(t, path, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), out))
}

// startPostgres starts PostgreSQL 16 with the owner `arch`, creates the runtime role `arch_app`
// (as the hub's init script and the deploy do), runs the migrations as the owner, and returns the
// owner's and arch_app's connection strings.
func startPostgres(t *testing.T) (owner, app string) {
	t.Helper()
	ctx := context.Background()
	owner = startEmptyPostgres(t)
	require.NoError(t, db.Migrate(ctx, owner, "up", io.Discard))

	u, err := url.Parse(owner)
	require.NoError(t, err)
	u.User = url.UserPassword("arch_app", "arch_app")
	return owner, u.String()
}

// startEmptyPostgres starts PostgreSQL 16 with the owner `arch` and the runtime role `arch_app`,
// runs no migrations, and returns the owner's connection string.
func startEmptyPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("arch"), postgres.WithUsername("arch"), postgres.WithPassword("arch"),
		postgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(t, pg)
	require.NoError(t, err)
	owner, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	conn, err := pgx.Connect(ctx, owner)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "CREATE ROLE arch_app LOGIN PASSWORD 'arch_app'")
	require.NoError(t, err)
	require.NoError(t, conn.Close(ctx))
	return owner
}

// newPool returns a pool for dsn, closed when the test ends.
func newPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := db.NewPool(dsn, false)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// newEngine returns the API over pool, as `serve` builds it, and the test realm signing its tokens.
func newEngine(t *testing.T, pool *pgxpool.Pool) (*gin.Engine, *authtest.Realm, authtest.Key) {
	t.Helper()
	key := authtest.NewKey(t, "k1")
	realm := authtest.NewRealm(t, key)
	gin.SetMode(gin.TestMode)
	svc := model.NewService(pool)
	engine := httpapi.NewEngine(httpapi.Deps{
		Auth: auth.NewVerifier(auth.Options{
			Issuer: realm.Issuer(), Audience: authtest.Audience, JWKSURL: realm.JWKSURL(),
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}),
		Ready:   okReady{},
		Model:   svc,
		Writer:  svc,
		Logger:  observability.NewLogger(io.Discard, slog.LevelInfo),
		Metrics: observability.NewMetrics(),
	})
	return engine, realm, key
}

// TestModelIntegration runs the migrations as the owner, then the seed command and the read API as
// the runtime role arch_app, against a real PostgreSQL 16. Needs podman or docker:
// go test -tags integration ./...
func TestModelIntegration(t *testing.T) {
	ctx := context.Background()
	_, dsn := startPostgres(t)
	pool := newPool(t, dsn)
	engine, realm, key := newEngine(t, pool)
	c := client{engine: engine, token: authtest.Sign(t, key, realm.Claims(time.Now(), "viewer"))}

	t.Run("seeded vocabulary", func(t *testing.T) {
		var types model.Types
		c.json(t, "/api/v1/types", &types)
		nodeTypes := map[string]string{}
		for _, nt := range types.NodeTypes {
			assert.True(t, nt.Builtin, nt.ID)
			assert.Equal(t, map[string]any{"type": "object", "properties": map[string]any{}}, nt.AttributesSchema, nt.ID)
			nodeTypes[nt.ID] = nt.Kind
		}
		assert.Equal(t, map[string]string{
			"service": "application", "k8s-cluster": "infrastructure", "database": "infrastructure",
			"queue": "infrastructure", "bucket": "infrastructure", "vpc": "network", "landing-zone": "network",
			"transit-gateway": "network", "vpn": "network", "direct-connect": "network", "firewall": "network",
			"on-prem-system": "external", "saas": "external", "team": "team",
			"client": "external", "load-balancer": "network", "container-registry": "infrastructure", "job": "application",
		}, nodeTypes)

		pair := func(from, to string) model.KindPair { return model.KindPair{From: from, To: to} }
		allowed := map[string][]model.KindPair{}
		schemas := map[string]map[string]any{}
		for _, et := range types.EdgeTypes {
			assert.True(t, et.Builtin, et.ID)
			allowed[et.ID] = et.Allowed
			schemas[et.ID] = et.AttributesSchema
		}
		assert.Equal(t, map[string][]model.KindPair{
			"depends_on": {pair("application", "application"), pair("application", "infrastructure"), pair("application", "external"),
				pair("external", "application")},
			"calls":          {pair("application", "application"), pair("application", "external"), pair("external", "application")},
			"runs_on":        {pair("application", "infrastructure")},
			"stores_data_in": {pair("application", "infrastructure")},
			"routes_via": {pair("application", "network"), pair("network", "network"), pair("network", "application"),
				pair("network", "external"), pair("infrastructure", "network")},
			"owned_by": {pair("application", "team"), pair("infrastructure", "team"), pair("network", "team"),
				pair("external", "team")},
		}, allowed)
		var calls any
		require.NoError(t, json.Unmarshal([]byte(`{"type":"object","properties":{
			"protocol":{"type":"string","enum":["http","https","grpc","tcp","amqp","kafka","postgres","otlp","other"]},
			"port":{"type":"number","minimum":1,"maximum":65535},"path":{"type":"string"}}}`), &calls))
		assert.Equal(t, calls, any(schemas["calls"]))
		var storesDataIn any
		require.NoError(t, json.Unmarshal([]byte(`{"type":"object","properties":{
			"port":{"type":"number","minimum":1,"maximum":65535},"role":{"type":"string"}}}`), &storesDataIn))
		assert.Equal(t, storesDataIn, any(schemas["stores_data_in"]))
		assert.Equal(t, map[string]any{"type": "object", "properties": map[string]any{}}, schemas["runs_on"])

		var envs model.EnvironmentList
		c.json(t, "/api/v1/environments", &envs)
		assert.Equal(t, []model.Environment{{ID: "dev", Name: "dev", Sort: 1}, {ID: "uat", Name: "uat", Sort: 2},
			{ID: "prod", Name: "prod", Sort: 3}}, envs.Items)

		rec := c.get(t, "/api/v1/teams", nil)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"items":[]}`, rec.Body.String())
	})

	t.Setenv("DATABASE_URL", dsn)
	t.Run("seed --sample", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 0, run([]string{"seed", "--sample"}, &stdout, &stderr), stderr.String())
		assert.Equal(t, "seeded sample graph: 7 nodes, 7 edges in prod\n", stdout.String())

		rec := c.get(t, "/api/v1/graph?environment=prod", nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var raw struct {
			Nodes []map[string]any  `json:"nodes"`
			Edges []model.GraphEdge `json:"edges"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		require.Len(t, raw.Nodes, 7)
		for _, n := range raw.Nodes {
			assert.NotContains(t, n, "attributes")
		}
		require.Len(t, raw.Edges, 7)
		for _, e := range raw.Edges {
			if e.Type == "calls" {
				assert.Equal(t, map[string]any{"protocol": "https", "port": float64(443)}, e.Attributes)
			} else {
				assert.Empty(t, e.Attributes)
			}
		}

		var disallowed int
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT count(*) FROM edges e
			JOIN nodes f ON f.environment = e.environment AND f.id = e.from_id
			JOIN node_types ft ON ft.id = f.type
			JOIN nodes t ON t.environment = e.environment AND t.id = e.to_id
			JOIN node_types tt ON tt.id = t.type
			JOIN edge_types et ON et.id = e.type
			WHERE NOT et.allowed @> jsonb_build_array(jsonb_build_object('from', ft.kind, 'to', tt.kind))`).Scan(&disallowed))
		assert.Zero(t, disallowed, "every sample edge satisfies its type's allowed pairs")

		var teams model.TeamList
		c.json(t, "/api/v1/teams", &teams)
		require.Len(t, teams.Items, 2)
		assert.Equal(t, "core", teams.Items[0].ID)
		assert.Equal(t, "Platform", teams.Items[1].Name)
	})

	t.Run("second seed refuses", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 1, run([]string{"seed", "--sample"}, &stdout, &stderr))
		assert.Equal(t, "seed: the database already has nodes; nothing was written\n", stderr.String())
		assert.Empty(t, stdout.String())
		var n int
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM nodes").Scan(&n))
		assert.Equal(t, 7, n)
	})

	t.Run("paging nodes to the end", func(t *testing.T) {
		var ids []string
		pages := 0
		cursor := ""
		for {
			path := "/api/v1/nodes?environment=prod&limit=2"
			if cursor != "" {
				path += "&cursor=" + url.QueryEscape(cursor)
			}
			var page model.NodePage
			c.json(t, path, &page)
			pages++
			require.LessOrEqual(t, len(page.Items), 2)
			for _, n := range page.Items {
				ids = append(ids, n.ID)
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
			require.Less(t, pages, 10)
		}
		assert.Equal(t, 4, pages)
		assert.Equal(t, []string{"core-banking", "direct-connect", "eks-prod", "landing-zone", "orders-db", "payments-api", "vpc-prod"}, ids)
	})

	t.Run("filters", func(t *testing.T) {
		var edges model.EdgePage
		c.json(t, "/api/v1/edges?environment=prod&type=routes_via", &edges)
		assert.Len(t, edges.Items, 4)
		assert.Nil(t, edges.NextCursor)

		var nodes model.NodePage
		c.json(t, "/api/v1/nodes?environment=prod&q=PAY", &nodes)
		require.Len(t, nodes.Items, 1)
		assert.Equal(t, "payments-api", nodes.Items[0].ID)

		c.json(t, "/api/v1/nodes?q=%25", &nodes)
		assert.Empty(t, nodes.Items, "%% is matched literally")
		c.json(t, "/api/v1/nodes?kind=network&owner=platform", &nodes)
		assert.Len(t, nodes.Items, 3)
		c.json(t, "/api/v1/nodes?type=nope", &nodes)
		assert.Empty(t, nodes.Items)

		var edgePages []string
		cursor := ""
		for {
			path := "/api/v1/edges?limit=4"
			if cursor != "" {
				path += "&cursor=" + url.QueryEscape(cursor)
			}
			c.json(t, path, &edges)
			for _, e := range edges.Items {
				edgePages = append(edgePages, e.Type+":"+e.From+">"+e.To)
			}
			if edges.NextCursor == nil {
				break
			}
			cursor = *edges.NextCursor
		}
		assert.Len(t, edgePages, 7)

		for _, path := range []string{"/api/v1/nodes?environment=nope", "/api/v1/edges?environment=nope", "/api/v1/graph?environment=nope",
			"/api/v1/nodes?cursor=zzz", "/api/v1/edges?cursor=zzz"} {
			assert.Equal(t, http.StatusUnprocessableEntity, c.get(t, path, nil).Code, path)
		}
	})

	t.Run("one node with ETag", func(t *testing.T) {
		rec := c.get(t, "/api/v1/nodes/prod/payments-api", nil)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, `"1"`, rec.Header().Get("ETag"))
		var n model.Node
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &n))
		assert.Equal(t, "application", n.Kind)
		assert.Equal(t, "platform", n.Owner)
		assert.Equal(t, map[string]any{}, n.Attributes)
		assert.Equal(t, http.StatusNotFound, c.get(t, "/api/v1/nodes/prod/nope", nil).Code)
	})

	t.Run("graph of 2,000 nodes and 10,000 edges under 1 s", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO nodes (environment, id, type, name, owner, source, tags)
			SELECT 'dev', 'node-' || i, 'service', 'Node ' || i, 'platform', 'import', ARRAY['load']
			FROM generate_series(1, 2000) i`)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `
			INSERT INTO edges (environment, type, from_id, to_id, attributes, source)
			SELECT 'dev', 'calls', 'node-' || i, 'node-' || ((i + k - 1) % 2000 + 1), '{"protocol":"https","port":443}', 'import'
			FROM generate_series(1, 2000) i, generate_series(1, 5) k`)
		require.NoError(t, err)

		gz := http.Header{"Accept-Encoding": {"gzip"}}
		c.get(t, "/api/v1/graph?environment=dev", gz) // warms the pool's connection
		start := time.Now()
		rec := c.get(t, "/api/v1/graph?environment=dev", gz)
		elapsed := time.Since(start)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Less(t, elapsed, time.Second, "[NFR-005]")
		t.Logf("GET /api/v1/graph (2,000 nodes, 10,000 edges, gzip): %s, %d bytes", elapsed, rec.Body.Len())

		assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
		r, err := gzip.NewReader(rec.Body)
		require.NoError(t, err)
		var g model.Graph
		require.NoError(t, json.NewDecoder(r).Decode(&g))
		assert.Len(t, g.Nodes, 2000)
		assert.Len(t, g.Edges, 10000)
	})
}
