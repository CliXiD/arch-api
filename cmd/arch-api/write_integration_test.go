//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/auth/authtest"
	"github.com/CliXiD/arch-api/internal/db"
	"github.com/CliXiD/arch-api/internal/model"
)

// writer sends requests with one token.
type writer struct {
	engine *gin.Engine
	token  string
}

func (w writer) send(t *testing.T, method, path, ifMatch, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	rec := httptest.NewRecorder()
	w.engine.ServeHTTP(rec, req)
	return rec
}

// expect sends a request, requires status and decodes the body into out (when not nil).
func (w writer) expect(t *testing.T, status int, method, path, ifMatch, body string, out any) *httptest.ResponseRecorder {
	t.Helper()
	rec := w.send(t, method, path, ifMatch, body)
	require.Equal(t, status, rec.Code, "%s %s: %s", method, path, rec.Body.String())
	if out != nil {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), out))
	}
	return rec
}

type problemBody struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
	Count  *int   `json:"count"`
	Errors []struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	} `json:"errors"`
}

func (p problemBody) locations() []string {
	out := []string{}
	for _, e := range p.Errors {
		out = append(out, e.Location)
	}
	return out
}

func changeCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM change_log").Scan(&n))
	return n
}

// changesSince returns the change_log rows with an id above after, oldest first.
func changesSince(t *testing.T, pool *pgxpool.Pool, after int64) []model.Change {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT id, at, actor, actor_name, actor_kind, source, entity, entity_key, action
		FROM change_log WHERE id > $1 ORDER BY id`, after)
	require.NoError(t, err)
	defer rows.Close()
	var out []model.Change
	for rows.Next() {
		var c model.Change
		require.NoError(t, rows.Scan(&c.ID, &c.At, &c.Actor, &c.ActorName, &c.ActorKind, &c.Source, &c.Entity, &c.EntityKey, &c.Action))
		out = append(out, c)
	}
	require.NoError(t, rows.Err())
	return out
}

func lastChangeID(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT coalesce(max(id), 0) FROM change_log").Scan(&id))
	return id
}

func sqlstate(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// TestWriteIntegration runs the write and history API as the runtime role arch_app on a database
// migrated by the owner, and checks the change_log and its protections.
func TestWriteIntegration(t *testing.T) {
	ctx := context.Background()
	ownerDSN, appDSN := startPostgres(t)
	owner := newPool(t, ownerDSN)
	app := newPool(t, appDSN)
	engine, realm, key := newEngine(t, app)

	token := func(edit func(jwt.MapClaims), roles ...string) writer {
		c := realm.Claims(time.Now(), roles...)
		if edit != nil {
			edit(c)
		}
		return writer{engine: engine, token: authtest.Sign(t, key, c)}
	}
	viewer := token(nil, "viewer")
	editor := token(func(c jwt.MapClaims) { c["teams"] = []string{"platform"} }, "editor")
	admin := token(func(c jwt.MapClaims) {
		c["sub"], c["preferred_username"], c["name"] = "admin-sub", "admin", "Admin Local"
	}, "admin")
	service := token(func(c jwt.MapClaims) {
		c["sub"] = "0b6e-service-account"
		c["client_id"], c["azp"] = "arch-ci-platform", "arch-ci-platform"
		c["preferred_username"] = "service-account-arch-ci-platform"
		c["teams"] = []string{"platform"}
		delete(c, "name")
	}, "editor")

	t.Run("readiness as arch_app", func(t *testing.T) {
		v, err := db.VersionReader{Pool: app}.AppliedVersion(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(7), v)
	})

	t.Run("teams and environments", func(t *testing.T) {
		before := lastChangeID(t, owner)
		var team model.Team
		admin.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/teams", "", `{"id":"platform","name":"Platform"}`, &team)
		assert.Equal(t, "Platform", team.Name)
		admin.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/teams", "", `{"id":"core","name":"Core"}`, nil)
		admin.expect(t, http.StatusOK, http.MethodPatch, "/api/v1/teams/core", "", `{"name":"Core banking"}`, &team)
		assert.Equal(t, model.Team{ID: "core", Name: "Core banking", CreatedAt: team.CreatedAt}, team)
		var p problemBody
		admin.expect(t, http.StatusConflict, http.MethodPost, "/api/v1/teams", "", `{"id":"core","name":"Core"}`, &p)
		assert.Equal(t, "/problems/conflict", p.Type)
		admin.expect(t, http.StatusNotFound, http.MethodDelete, "/api/v1/teams/nope", "", "", nil)

		var env model.Environment
		admin.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/environments", "", `{"id":"sandbox","name":"Sandbox","sort":4}`, &env)
		assert.Equal(t, model.Environment{ID: "sandbox", Name: "Sandbox", Sort: 4}, env)
		admin.expect(t, http.StatusConflict, http.MethodPost, "/api/v1/environments", "", `{"id":"qa","name":"QA","sort":4}`, &p)
		assert.Equal(t, "/problems/conflict", p.Type, "duplicate sort")
		admin.expect(t, http.StatusOK, http.MethodPatch, "/api/v1/environments/sandbox", "", `{"name":"Sandbox 2"}`, &env)
		assert.Equal(t, model.Environment{ID: "sandbox", Name: "Sandbox 2", Sort: 4}, env)
		admin.expect(t, http.StatusNoContent, http.MethodDelete, "/api/v1/environments/sandbox", "", "", nil)

		for _, r := range [][3]string{
			{http.MethodPost, "/api/v1/teams", `{"id":"ops","name":"Ops"}`}, {http.MethodPatch, "/api/v1/teams/core", `{"name":"x"}`},
			{http.MethodDelete, "/api/v1/teams/core", ""}, {http.MethodPost, "/api/v1/environments", `{"id":"qa","name":"QA","sort":9}`},
			{http.MethodPatch, "/api/v1/environments/dev", `{"sort":9}`}, {http.MethodDelete, "/api/v1/environments/dev", ""},
		} {
			editor.expect(t, http.StatusForbidden, r[0], r[1], "", r[2], nil)
		}

		var got []string
		for _, c := range changesSince(t, owner, before) {
			got = append(got, c.Entity+" "+c.EntityKey+" "+c.Action+" "+c.ActorName)
		}
		assert.Equal(t, []string{
			"team platform create admin", "team core create admin", "team core update admin",
			"environment sandbox create admin", "environment sandbox update admin", "environment sandbox delete admin",
		}, got, "one row per successful write, none for rejected ones")
	})

	createNode := func(t *testing.T, w writer, env, id, typ, owner string) {
		t.Helper()
		w.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/nodes", "",
			`{"environment":"`+env+`","id":"`+id+`","type":"`+typ+`","name":"`+id+`","owner":"`+owner+`"}`, nil)
	}

	t.Run("create nodes", func(t *testing.T) {
		var n model.Node
		rec := editor.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/nodes", "",
			`{"environment":"prod","id":"payments-api","type":"service","name":"Payments","owner":"platform","tags":["pci"]}`, &n)
		assert.Equal(t, `"1"`, rec.Header().Get("ETag"))
		assert.Equal(t, "ui", n.Source)
		assert.Equal(t, "application", n.Kind)
		assert.Equal(t, []string{"pci"}, n.Tags)
		assert.Equal(t, map[string]any{}, n.Attributes)
		assert.Equal(t, 1, n.Version)

		var p problemBody
		editor.expect(t, http.StatusForbidden, http.MethodPost, "/api/v1/nodes", "",
			`{"environment":"prod","id":"ledger","type":"service","name":"Ledger","owner":"core"}`, &p)
		assert.Equal(t, "/problems/forbidden", p.Type)
		assert.Contains(t, p.Detail, "core")
		viewer.expect(t, http.StatusForbidden, http.MethodPost, "/api/v1/nodes", "",
			`{"environment":"prod","id":"ledger","type":"service","name":"Ledger","owner":"platform"}`, nil)
		editor.expect(t, http.StatusConflict, http.MethodPost, "/api/v1/nodes", "",
			`{"environment":"prod","id":"payments-api","type":"service","name":"Payments","owner":"platform"}`, &p)
		assert.Equal(t, "/problems/conflict", p.Type)

		createNode(t, admin, "prod", "ledger", "service", "core")
		createNode(t, admin, "prod", "core-banking", "on-prem-system", "core")
		createNode(t, editor, "prod", "eks-prod", "k8s-cluster", "platform")
		createNode(t, editor, "prod", "vpc-prod", "vpc", "platform")
		createNode(t, editor, "dev", "dev-only", "service", "platform")
	})

	t.Run("rejected writes leave no row", func(t *testing.T) {
		before := changeCount(t, owner)
		tests := []struct {
			name, path, body string
			want             []string
			wantType         string
		}{
			{"bad id", "/api/v1/nodes", `{"environment":"prod","id":"Bad_Slug","type":"service","name":"x","owner":"platform"}`,
				[]string{"body.id"}, "/problems/validation"},
			{"unknown references", "/api/v1/nodes", `{"environment":"nope","id":"x1","type":"planet","name":"x","owner":"platform"}`,
				[]string{"body.environment", "body.type"}, "/problems/validation"},
			{"unsupported attribute type", "/api/v1/nodes",
				`{"environment":"prod","id":"x2","type":"service","name":"x","owner":"platform","attributes":{"settings":{"a":1}}}`,
				[]string{"body.attributes.settings"}, "/problems/validation"},
			{"port out of range", "/api/v1/edges",
				`{"environment":"prod","type":"calls","from":"payments-api","to":"ledger","attributes":{"protocol":"https","port":70000}}`,
				[]string{"body.attributes.port"}, "/problems/validation"},
			{"kind pair not allowed", "/api/v1/edges", `{"environment":"prod","type":"runs_on","from":"payments-api","to":"vpc-prod"}`,
				[]string{"body.to"}, "/problems/validation"},
			{"from kind starts no allowed pair", "/api/v1/edges", `{"environment":"prod","type":"calls","from":"eks-prod","to":"payments-api"}`,
				[]string{"body.from"}, "/problems/validation"},
			{"to kind not allowed", "/api/v1/edges", `{"environment":"prod","type":"calls","from":"payments-api","to":"eks-prod"}`,
				[]string{"body.to"}, "/problems/validation"},
			{"to only in another environment", "/api/v1/edges", `{"environment":"prod","type":"calls","from":"payments-api","to":"dev-only"}`,
				[]string{"body.to"}, "/problems/validation"},
			{"secret value", "/api/v1/nodes",
				`{"environment":"prod","id":"x3","type":"service","name":"x","owner":"platform","attributes":{"dsn":"postgres://u:p@h/db"}}`,
				[]string{"body.attributes.dsn"}, "/problems/secret-detected"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var p problemBody
				editor.expect(t, http.StatusUnprocessableEntity, http.MethodPost, tt.path, "", tt.body, &p)
				assert.Equal(t, tt.wantType, p.Type)
				assert.Equal(t, tt.want, p.locations())
			})
		}
		var p problemBody
		editor.expect(t, http.StatusUnprocessableEntity, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"runs_on","from":"payments-api","to":"vpc-prod"}`, &p)
		assert.Equal(t, "edge type runs_on does not allow application → network", p.Errors[0].Message)
		editor.expect(t, http.StatusUnprocessableEntity, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"calls","from":"eks-prod","to":"payments-api"}`, &p)
		assert.Equal(t, "edge type calls does not allow infrastructure → application", p.Errors[0].Message)

		unknownOwner := func(t *testing.T, w writer, method, path, ifMatch, body string) {
			t.Helper()
			var p problemBody
			w.expect(t, http.StatusUnprocessableEntity, method, path, ifMatch, body, &p)
			assert.Equal(t, "/problems/validation", p.Type)
			assert.Equal(t, []string{"body.owner"}, p.locations())
			assert.Equal(t, "team does not exist", p.Errors[0].Message)
		}
		for name, w := range map[string]writer{"editor": editor, "admin": admin} {
			t.Run("unknown owner as "+name, func(t *testing.T) {
				unknownOwner(t, w, http.MethodPost, "/api/v1/nodes", "",
					`{"environment":"prod","id":"x9","type":"service","name":"x","owner":"no-such-team"}`)
			})
		}
		t.Run("owner changed to an unknown team", func(t *testing.T) {
			unknownOwner(t, editor, http.MethodPatch, "/api/v1/nodes/prod/eks-prod", `"1"`, `{"owner":"no-such-team"}`)
		})
		assert.Equal(t, before, changeCount(t, owner), "no change_log row for a rejected write")
		var n int
		require.NoError(t, owner.QueryRow(ctx, "SELECT count(*) FROM nodes WHERE id LIKE 'x%'").Scan(&n))
		assert.Zero(t, n)
	})

	var callsUID, dependsUID, runsOnUID string
	t.Run("create edges", func(t *testing.T) {
		var e model.Edge
		rec := editor.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"calls","from":"payments-api","to":"ledger","attributes":{"protocol":"https","port":443}}`, &e)
		assert.Equal(t, `"1"`, rec.Header().Get("ETag"))
		assert.Equal(t, map[string]any{"protocol": "https", "port": float64(443)}, e.Attributes)
		assert.Equal(t, "ui", e.Source)
		callsUID = e.UID

		var p problemBody
		editor.expect(t, http.StatusForbidden, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"depends_on","from":"ledger","to":"payments-api"}`, &p)
		assert.Equal(t, "/problems/forbidden", p.Type)
		assert.Contains(t, p.Detail, "core")

		admin.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"depends_on","from":"ledger","to":"payments-api"}`, &e)
		dependsUID = e.UID
		admin.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"runs_on","from":"payments-api","to":"eks-prod"}`, &e)
		runsOnUID = e.UID

		editor.expect(t, http.StatusConflict, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"calls","from":"payments-api","to":"ledger"}`, &p)
		assert.Equal(t, "/problems/conflict", p.Type)
	})

	// Edges into and out of payments-api that the wider vocabulary allows (hub issue #17).
	var vocabularyUIDs []string
	t.Run("wider vocabulary", func(t *testing.T) {
		createNode(t, editor, "prod", "browser", "client", "platform")
		createNode(t, editor, "prod", "nightly-reconcile", "job", "platform")
		createNode(t, editor, "prod", "ghcr", "container-registry", "platform")

		var e model.Edge
		for _, body := range []string{
			`{"environment":"prod","type":"calls","from":"browser","to":"payments-api","attributes":{"protocol":"https","port":443}}`,
			`{"environment":"prod","type":"depends_on","from":"browser","to":"payments-api"}`,
			`{"environment":"prod","type":"calls","from":"nightly-reconcile","to":"payments-api","attributes":{"protocol":"otlp","path":"/api/v1"}}`,
			`{"environment":"prod","type":"stores_data_in","from":"payments-api","to":"ghcr","attributes":{"port":5432,"role":"arch_app"}}`,
		} {
			e = model.Edge{}
			editor.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/edges", "", body, &e)
			vocabularyUIDs = append(vocabularyUIDs, e.UID)
		}
		assert.Equal(t, map[string]any{"port": float64(5432), "role": "arch_app"}, e.Attributes)

		tests := []struct {
			name, body, want string
		}{
			{"protocol not in the enum",
				`{"environment":"prod","type":"calls","from":"browser","to":"ledger","attributes":{"protocol":"smtp"}}`,
				"body.attributes.protocol"},
			{"stores_data_in port out of range",
				`{"environment":"prod","type":"stores_data_in","from":"payments-api","to":"eks-prod","attributes":{"port":70000}}`,
				"body.attributes.port"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var p problemBody
				editor.expect(t, http.StatusUnprocessableEntity, http.MethodPost, "/api/v1/edges", "", tt.body, &p)
				assert.Equal(t, "/problems/validation", p.Type)
				assert.Equal(t, []string{tt.want}, p.locations())
			})
		}

		var g model.Graph
		viewer.expect(t, http.StatusOK, http.MethodGet, "/api/v1/graph?environment=prod", "", "", &g)
		var found bool
		for _, ge := range g.Edges {
			if ge.Type == "calls" && ge.From == "browser" && ge.To == "payments-api" {
				found = true
				assert.Equal(t, vocabularyUIDs[0], ge.UID)
			}
		}
		assert.True(t, found, "the graph lists the client's calls edge")
	})

	t.Run("optimistic locking", func(t *testing.T) {
		node := "/api/v1/nodes/prod/payments-api"
		edge := "/api/v1/edges/" + callsUID
		for _, r := range [][2]string{{http.MethodPatch, node}, {http.MethodDelete, node}, {http.MethodPatch, edge}, {http.MethodDelete, edge}} {
			body := ""
			if r[0] == http.MethodPatch {
				body = `{"attributes":{}}`
			}
			var p problemBody
			editor.expect(t, http.StatusPreconditionFailed, r[0], r[1], `"999"`, body, &p)
			assert.Equal(t, "/problems/stale", p.Type)
			editor.expect(t, http.StatusPreconditionFailed, r[0], r[1], "*", body, &p)
			assert.Equal(t, "/problems/stale", p.Type)
			editor.expect(t, http.StatusPreconditionRequired, r[0], r[1], "", body, &p)
			assert.Equal(t, "/problems/precondition-required", p.Type)
		}

		var n model.Node
		rec := editor.expect(t, http.StatusOK, http.MethodPatch, node, `"1"`, `{"name":"Payments API","tags":["pci","tier-1"]}`, &n)
		assert.Equal(t, `"2"`, rec.Header().Get("ETag"))
		assert.Equal(t, 2, n.Version)
		assert.Equal(t, []string{"pci", "tier-1"}, n.Tags)

		before := changeCount(t, owner)
		rec = editor.expect(t, http.StatusOK, http.MethodPatch, node, `"2"`, `{"name":"Payments API","owner":"platform","attributes":{}}`, &n)
		assert.Equal(t, `"2"`, rec.Header().Get("ETag"), "a no-op keeps the version")
		assert.Equal(t, 2, n.Version)
		assert.Equal(t, before, changeCount(t, owner), "a no-op writes no row")

		var p problemBody
		editor.expect(t, http.StatusForbidden, http.MethodPatch, node, `"2"`, `{"owner":"core"}`, &p)
		assert.Contains(t, p.Detail, "core", "an owner change needs the new team too")

		var e model.Edge
		rec = editor.expect(t, http.StatusOK, http.MethodPatch, edge, `"1"`, `{"attributes":{"protocol":"grpc","port":8443}}`, &e)
		assert.Equal(t, `"2"`, rec.Header().Get("ETag"))
		assert.Equal(t, map[string]any{"protocol": "grpc", "port": float64(8443)}, e.Attributes)
	})

	t.Run("service account writes", func(t *testing.T) {
		before := lastChangeID(t, owner)
		service.expect(t, http.StatusOK, http.MethodPatch, "/api/v1/nodes/prod/payments-api", `"2"`, `{"description":"Payments service"}`, nil)
		rows := changesSince(t, owner, before)
		require.Len(t, rows, 1)
		assert.Equal(t, "service", rows[0].ActorKind)
		assert.Equal(t, "arch-ci-platform", rows[0].Actor)
		assert.Equal(t, "arch-ci-platform", rows[0].ActorName)
		assert.Equal(t, "api", rows[0].Source)

		var n model.Node
		service.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/nodes", "",
			`{"environment":"prod","id":"ci-built","type":"service","name":"CI built","owner":"platform"}`, &n)
		assert.Equal(t, "api", n.Source)
		var page model.HistoryPage
		viewer.expect(t, http.StatusOK, http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=ci-built", "", "", &page)
		require.Len(t, page.Items, 1)
		assert.Equal(t, "create", page.Items[0].Action)
		assert.Equal(t, "api", page.Items[0].Source)
		assert.Equal(t, "service", page.Items[0].ActorKind)

		var e model.Edge
		service.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"calls","from":"ci-built","to":"ledger"}`, &e)
		assert.Equal(t, "api", e.Source)

		// Remove the node and its edge again, so the later counts hold.
		before = lastChangeID(t, owner)
		service.expect(t, http.StatusNoContent, http.MethodDelete, "/api/v1/nodes/prod/ci-built", `"1"`, "", nil)
		rows = changesSince(t, owner, before)
		require.Len(t, rows, 2)
		for _, r := range rows {
			assert.Equal(t, "api", r.Source, "%s %s", r.Entity, r.Action)
		}
	})

	t.Run("delete an edge", func(t *testing.T) {
		editor.expect(t, http.StatusNoContent, http.MethodDelete, "/api/v1/edges/"+runsOnUID, `"1"`, "", nil)
		editor.expect(t, http.StatusNotFound, http.MethodDelete, "/api/v1/edges/"+runsOnUID, `"1"`, "", nil)
	})

	t.Run("history", func(t *testing.T) {
		var page model.HistoryPage
		viewer.expect(t, http.StatusOK, http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=payments-api", "", "", &page)
		var got []string
		for _, c := range page.Items {
			got = append(got, c.Entity+" "+c.Action+" "+c.ActorName)
			want := "ui"
			if c.ActorName == "arch-ci-platform" {
				want = "api"
			}
			assert.Equal(t, want, c.Source, "%s %s %s", c.Entity, c.Action, c.ActorName)
		}
		assert.Equal(t, []string{
			"edge delete editor", // runs_on
			"node update arch-ci-platform",
			"edge update editor",
			"node update editor",
			"edge create editor", // stores_data_in, into ghcr
			"edge create editor", // calls, from nightly-reconcile
			"edge create editor", // depends_on, from browser
			"edge create editor", // calls, from browser
			"edge create admin",  // runs_on
			"edge create admin",  // depends_on, into payments-api
			"edge create editor",
			"node create editor",
		}, got)
		assert.Nil(t, page.NextCursor)
		for i := 1; i < len(page.Items); i++ {
			prev, cur := page.Items[i-1], page.Items[i]
			assert.True(t, prev.At.After(cur.At) || (prev.At.Equal(cur.At) && prev.ID > cur.ID), "newest first")
		}
		first, last := page.Items[0], page.Items[len(page.Items)-1]
		assert.Equal(t, runsOnUID, first.EntityKey)
		assert.Equal(t, "user", first.ActorKind)
		assert.Equal(t, "payments-api", first.Before["from"])
		assert.Nil(t, first.After)
		assert.Equal(t, "prod/payments-api", last.EntityKey)
		assert.Nil(t, last.Before)
		assert.Equal(t, "Payments", last.After["name"])
		assert.Equal(t, "editor", last.ActorName)
		assert.Equal(t, "8d0c6d4e-user", last.Actor)

		var one model.HistoryPage
		viewer.expect(t, http.StatusOK, http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=payments-api&limit=1", "", "", &one)
		require.Len(t, one.Items, 1)
		require.NotNil(t, one.NextCursor)
		assert.Equal(t, page.Items[0].ID, one.Items[0].ID)
		var next model.HistoryPage
		viewer.expect(t, http.StatusOK, http.MethodGet,
			"/api/v1/history?entity=node&environment=prod&id=payments-api&limit=1&cursor="+url.QueryEscape(*one.NextCursor), "", "", &next)
		require.Len(t, next.Items, 1)
		assert.Equal(t, page.Items[1].ID, next.Items[0].ID)

		var p problemBody
		viewer.expect(t, http.StatusUnprocessableEntity, http.MethodGet,
			"/api/v1/history?entity=node&environment=prod&id=payments-api&cursor=garbage", "", "", &p)
		assert.Equal(t, []string{"query.cursor"}, p.locations())

		viewer.expect(t, http.StatusOK, http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=nope", "", "", &page)
		assert.Empty(t, page.Items)
		viewer.expect(t, http.StatusOK, http.MethodGet, "/api/v1/history?entity=node&environment=dev&id=payments-api", "", "", &page)
		assert.Empty(t, page.Items, "another environment's history is separate")
	})

	t.Run("in use", func(t *testing.T) {
		before := changeCount(t, owner)
		var owned int
		require.NoError(t, owner.QueryRow(ctx, "SELECT count(*) FROM nodes WHERE owner = 'platform'").Scan(&owned))
		require.Equal(t, 7, owned)
		var p problemBody
		admin.expect(t, http.StatusConflict, http.MethodDelete, "/api/v1/teams/platform", "", "", &p)
		assert.Equal(t, "/problems/in-use", p.Type)
		require.NotNil(t, p.Count)
		assert.Equal(t, owned, *p.Count)
		admin.expect(t, http.StatusConflict, http.MethodDelete, "/api/v1/environments/prod", "", "", &p)
		assert.Equal(t, "/problems/in-use", p.Type)
		require.NotNil(t, p.Count)
		assert.Equal(t, 8, *p.Count)
		assert.Equal(t, before, changeCount(t, owner))
	})

	t.Run("an edge write waits for its source node's owner change", func(t *testing.T) {
		tests := []struct {
			name, node, method, body string
		}{
			{"patch", "race-patch", http.MethodPatch, `{"attributes":{"protocol":"https","port":8443}}`},
			{"delete", "race-delete", http.MethodDelete, ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				createNode(t, editor, "prod", tt.node, "service", "platform")
				var e model.Edge
				rec := editor.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/edges", "",
					`{"environment":"prod","type":"calls","from":"`+tt.node+`","to":"ledger","attributes":{"protocol":"https","port":443}}`, &e)
				etag := rec.Header().Get("ETag")
				before := lastChangeID(t, owner)

				tx, err := owner.Begin(ctx)
				require.NoError(t, err)
				defer func() { _ = tx.Rollback(ctx) }()
				_, err = tx.Exec(ctx, "UPDATE nodes SET owner = 'core' WHERE environment = 'prod' AND id = $1", tt.node)
				require.NoError(t, err)

				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- editor.send(t, tt.method, "/api/v1/edges/"+e.UID, etag, tt.body) }()
				require.Eventually(t, func() bool {
					var waiting int
					err := owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
						WHERE usename = 'arch_app' AND wait_event_type = 'Lock'`).Scan(&waiting)
					return err == nil && waiting > 0
				}, 5*time.Second, 20*time.Millisecond, "the edge write waits on the node lock")
				require.NoError(t, tx.Commit(ctx))

				var got *httptest.ResponseRecorder
				select {
				case got = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("the edge write did not finish after the commit")
				}
				require.Equal(t, http.StatusForbidden, got.Code, got.Body.String())
				var p problemBody
				require.NoError(t, json.Unmarshal(got.Body.Bytes(), &p))
				assert.Equal(t, "/problems/forbidden", p.Type)
				assert.Contains(t, p.Detail, "core")
				for _, c := range changesSince(t, owner, before) {
					assert.NotEqual(t, e.UID, c.EntityKey, "no change_log row for the edge")
				}
			})
		}
	})

	t.Run("deleting a node deletes its edges", func(t *testing.T) {
		before := lastChangeID(t, owner)
		var n model.Node
		rec := viewer.expect(t, http.StatusOK, http.MethodGet, "/api/v1/nodes/prod/payments-api", "", "", &n)
		editor.expect(t, http.StatusNoContent, http.MethodDelete, "/api/v1/nodes/prod/payments-api", rec.Header().Get("ETag"), "", nil)

		var edges int
		require.NoError(t, owner.QueryRow(ctx, "SELECT count(*) FROM edges WHERE uid = ANY($1::uuid[])",
			append([]string{callsUID, dependsUID}, vocabularyUIDs...)).Scan(&edges))
		assert.Zero(t, edges)
		viewer.expect(t, http.StatusNotFound, http.MethodGet, "/api/v1/nodes/prod/payments-api", "", "", nil)

		rows := changesSince(t, owner, before)
		require.Len(t, rows, 3+len(vocabularyUIDs))
		keys := map[string]string{}
		for _, r := range rows {
			assert.Equal(t, "delete", r.Action)
			assert.Equal(t, rows[0].At, r.At, "one transaction")
			keys[r.EntityKey] = r.Entity
		}
		want := map[string]string{callsUID: "edge", dependsUID: "edge", "prod/payments-api": "node"}
		for _, uid := range vocabularyUIDs {
			want[uid] = "edge"
		}
		assert.Equal(t, want, keys)

		var page model.HistoryPage
		viewer.expect(t, http.StatusOK, http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=payments-api", "", "", &page)
		require.NotEmpty(t, page.Items)
		assert.Equal(t, "node", page.Items[0].Entity, "the node's delete is the newest row")
		assert.Equal(t, "delete", page.Items[0].Action)
	})

	t.Run("change_log is append-only", func(t *testing.T) {
		require.Positive(t, changeCount(t, owner))
		for _, stmt := range []string{"UPDATE change_log SET actor = 'x'", "DELETE FROM change_log"} {
			_, err := app.Exec(ctx, stmt)
			assert.Equal(t, "42501", sqlstate(err), "arch_app: %s", stmt)
		}
		for _, stmt := range []string{"UPDATE change_log SET actor = 'x'", "DELETE FROM change_log", "TRUNCATE change_log"} {
			_, err := owner.Exec(ctx, stmt)
			require.Error(t, err, "owner: %s", stmt)
			var pg *pgconn.PgError
			require.ErrorAs(t, err, &pg)
			assert.Equal(t, "P0001", pg.Code, stmt)
			assert.Contains(t, pg.Message, "change_log is append-only", stmt)
		}
		var actors int
		require.NoError(t, owner.QueryRow(ctx, "SELECT count(*) FROM change_log WHERE actor = 'x'").Scan(&actors))
		assert.Zero(t, actors)
	})
}
