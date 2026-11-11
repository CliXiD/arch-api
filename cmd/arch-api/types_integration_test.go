//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/auth/authtest"
	"github.com/CliXiD/arch-api/internal/model"
)

// TestTypeWriteIntegration runs the admin type writes as arch_app: creates, schema and allowed-pair
// changes checked against existing nodes and edges, built-in protection, deletes, secret and
// schema checks, and the change_log rows.
func TestTypeWriteIntegration(t *testing.T) {
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
		c["sub"], c["preferred_username"] = "admin-sub", "admin"
	}, "admin")

	const (
		nodeTypes = "/api/v1/admin/types/node-types"
		edgeTypes = "/api/v1/admin/types/edge-types"
		broker    = `{"id":"message-broker","kind":"infrastructure","name":"Message broker","attributes_schema":{"type":"object","properties":{}}}`
	)
	start := lastChangeID(t, owner)

	// typeRows returns the change_log rows of entity `type` since after.
	typeRows := func(t *testing.T, after int64) []string {
		t.Helper()
		var out []string
		for _, c := range changesSince(t, owner, after) {
			if c.Entity == "type" {
				out = append(out, c.EntityKey+" "+c.Action+" "+c.ActorName+" "+c.Source)
			}
		}
		return out
	}
	listTypes := func(t *testing.T) model.Types {
		t.Helper()
		var types model.Types
		viewer.expect(t, http.StatusOK, http.MethodGet, "/api/v1/types", "", "", &types)
		return types
	}
	createNode := func(t *testing.T, env, id, typ string) {
		t.Helper()
		admin.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/nodes", "",
			`{"environment":"`+env+`","id":"`+id+`","type":"`+typ+`","name":"`+id+`","owner":"platform"}`, nil)
	}

	t.Run("create a node type", func(t *testing.T) {
		before := lastChangeID(t, owner)
		var nt model.NodeType
		admin.expect(t, http.StatusCreated, http.MethodPost, nodeTypes, "", broker, &nt)
		assert.Equal(t, model.NodeType{
			ID: "message-broker", Kind: "infrastructure", Name: "Message broker",
			AttributesSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}, nt)

		var found *model.NodeType
		for _, nt := range listTypes(t).NodeTypes {
			if nt.ID == "message-broker" {
				found = &nt
			}
		}
		require.NotNil(t, found)
		assert.False(t, found.Builtin)

		var p problemBody
		editor.expect(t, http.StatusForbidden, http.MethodPost, nodeTypes, "", broker, &p)
		assert.Equal(t, "/problems/forbidden", p.Type)
		admin.expect(t, http.StatusConflict, http.MethodPost, nodeTypes, "", broker, &p)
		assert.Equal(t, "/problems/conflict", p.Type)
		assert.Equal(t, "a node type with this id already exists", p.Detail)

		rows := changesSince(t, owner, before)
		require.Len(t, rows, 1)
		assert.Equal(t, "type", rows[0].Entity)
		assert.Equal(t, "node/message-broker", rows[0].EntityKey)
		assert.Equal(t, "create", rows[0].Action)
		assert.Equal(t, "ui", rows[0].Source)

		var stored struct{ before, after []byte }
		require.NoError(t, owner.QueryRow(ctx, "SELECT before, after FROM change_log WHERE id = $1", rows[0].ID).
			Scan(&stored.before, &stored.after))
		assert.Nil(t, stored.before)
		assert.JSONEq(t, `{"id":"message-broker","kind":"infrastructure","name":"Message broker",
			"attributes_schema":{"type":"object","properties":{}},"builtin":false}`, string(stored.after))
	})

	t.Run("setup", func(t *testing.T) {
		admin.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/teams", "", `{"id":"platform","name":"Platform"}`, nil)
		createNode(t, "prod", "rabbit-prod", "message-broker")
		createNode(t, "dev", "rabbit-dev", "message-broker")
		createNode(t, "prod", "orders-api", "service")
		createNode(t, "prod", "billing-api", "service")
	})

	t.Run("a node type schema change is checked against existing nodes", func(t *testing.T) {
		before := lastChangeID(t, owner)
		var p problemBody
		admin.expect(t, http.StatusConflict, http.MethodPatch, nodeTypes+"/message-broker", "",
			`{"attributes_schema":{"type":"object","required":["engine"],"properties":{"engine":{"type":"string"}}}}`, &p)
		assert.Equal(t, "/problems/conflict", p.Type)
		assert.Equal(t, "existing nodes would fail the change", p.Detail)
		require.NotNil(t, p.Count)
		assert.Equal(t, 2, *p.Count, "both environments' nodes")

		var nt model.NodeType
		admin.expect(t, http.StatusOK, http.MethodPatch, nodeTypes+"/message-broker", "",
			`{"name":"Broker","attributes_schema":{"type":"object","properties":{"engine":{"type":"string","enum":["rabbitmq","kafka"]}}}}`, &nt)
		assert.Equal(t, "Broker", nt.Name)
		assert.Equal(t, map[string]any{"type": "object", "properties": map[string]any{
			"engine": map[string]any{"type": "string", "enum": []any{"rabbitmq", "kafka"}},
		}}, nt.AttributesSchema)

		admin.expect(t, http.StatusOK, http.MethodPatch, nodeTypes+"/message-broker", "",
			`{"name":"Broker","attributes_schema":{"type":"object","properties":{"engine":{"type":"string","enum":["rabbitmq","kafka"]}}}}`, &nt)
		admin.expect(t, http.StatusOK, http.MethodPatch, nodeTypes+"/message-broker", "", `{}`, &nt)
		assert.Equal(t, "Broker", nt.Name, "a no-op returns the stored type")

		admin.expect(t, http.StatusOK, http.MethodPatch, "/api/v1/nodes/prod/rabbit-prod", `"1"`, `{"attributes":{"engine":"kafka"}}`, nil)
		admin.expect(t, http.StatusConflict, http.MethodPatch, nodeTypes+"/message-broker", "",
			`{"attributes_schema":{"type":"object","properties":{"engine":{"type":"string","enum":["rabbitmq"]}}}}`, &p)
		require.NotNil(t, p.Count)
		assert.Equal(t, 1, *p.Count, "only the node holding kafka fails")

		assert.Equal(t, []string{"node/message-broker update admin ui"}, typeRows(t, before),
			"one row for the change, none for the no-ops and the rejected changes")
	})

	t.Run("an edge type allowed change is checked against existing edges", func(t *testing.T) {
		admin.expect(t, http.StatusCreated, http.MethodPost, edgeTypes, "",
			`{"id":"publishes_to","name":"Publishes to","attributes_schema":{"type":"object","properties":{}},
			"allowed":[{"from":"application","to":"infrastructure"}]}`, nil)
		admin.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"publishes_to","from":"orders-api","to":"rabbit-prod"}`, nil)
		admin.expect(t, http.StatusCreated, http.MethodPost, "/api/v1/edges", "",
			`{"environment":"prod","type":"publishes_to","from":"billing-api","to":"rabbit-prod","attributes":{"topic":"bills"}}`, nil)

		before := lastChangeID(t, owner)
		var p problemBody
		admin.expect(t, http.StatusConflict, http.MethodPatch, edgeTypes+"/publishes_to", "",
			`{"allowed":[{"from":"application","to":"application"}]}`, &p)
		assert.Equal(t, "/problems/conflict", p.Type)
		assert.Equal(t, "existing edges would fail the change", p.Detail)
		require.NotNil(t, p.Count)
		assert.Equal(t, 2, *p.Count)

		admin.expect(t, http.StatusConflict, http.MethodPatch, edgeTypes+"/publishes_to", "",
			`{"allowed":[{"from":"application","to":"application"}],
			"attributes_schema":{"type":"object","additionalProperties":false,"properties":{}}}`, &p)
		require.NotNil(t, p.Count)
		assert.Equal(t, 2, *p.Count, "an edge failing both checks counts once")

		admin.expect(t, http.StatusConflict, http.MethodPatch, edgeTypes+"/publishes_to", "",
			`{"attributes_schema":{"type":"object","additionalProperties":false,"properties":{}}}`, &p)
		require.NotNil(t, p.Count)
		assert.Equal(t, 1, *p.Count, "only the edge with a topic")

		var et model.EdgeType
		admin.expect(t, http.StatusOK, http.MethodPatch, edgeTypes+"/publishes_to", "",
			`{"allowed":[{"from":"application","to":"infrastructure"},{"from":"application","to":"external"}]}`, &et)
		assert.Equal(t, []model.KindPair{{From: "application", To: "infrastructure"}, {From: "application", To: "external"}}, et.Allowed)
		assert.Equal(t, []string{"edge/publishes_to update admin ui"}, typeRows(t, before))
	})

	t.Run("built-in types", func(t *testing.T) {
		before := lastChangeID(t, owner)
		var p problemBody
		admin.expect(t, http.StatusConflict, http.MethodDelete, nodeTypes+"/service", "", "", &p)
		assert.Equal(t, "/problems/conflict", p.Type)
		assert.Equal(t, "built-in types can't be deleted", p.Detail)
		assert.Nil(t, p.Count)
		admin.expect(t, http.StatusConflict, http.MethodDelete, edgeTypes+"/calls", "", "", &p)
		assert.Equal(t, "built-in types can't be deleted", p.Detail)

		admin.expect(t, http.StatusConflict, http.MethodPatch, edgeTypes+"/calls", "",
			`{"attributes_schema":{"type":"object","properties":{"protocol":{"type":"string"}}}}`, &p)
		assert.Equal(t, "/problems/conflict", p.Type)
		assert.Equal(t, "built-in types can't lose attributes", p.Detail)
		assert.Nil(t, p.Count)

		var et model.EdgeType
		admin.expect(t, http.StatusOK, http.MethodPatch, edgeTypes+"/calls", "",
			`{"attributes_schema":{"type":"object","properties":{
				"protocol":{"type":"string","enum":["http","https","grpc","tcp","amqp","kafka","other"]},
				"port":{"type":"number","minimum":1,"maximum":65535},"timeout_ms":{"type":"number","minimum":0}}}}`, &et)
		assert.True(t, et.Builtin)
		assert.Contains(t, et.AttributesSchema["properties"], "timeout_ms")
		var nt model.NodeType
		admin.expect(t, http.StatusOK, http.MethodPatch, nodeTypes+"/service", "", `{"name":"Application service"}`, &nt)
		assert.Equal(t, "Application service", nt.Name)
		assert.True(t, nt.Builtin)

		assert.Equal(t, []string{"edge/calls update admin ui", "node/service update admin ui"}, typeRows(t, before))
	})

	t.Run("deletes", func(t *testing.T) {
		before := lastChangeID(t, owner)
		admin.expect(t, http.StatusCreated, http.MethodPost, nodeTypes, "",
			`{"id":"unused","kind":"external","name":"Unused","attributes_schema":{"type":"object","properties":{}}}`, nil)
		admin.expect(t, http.StatusCreated, http.MethodPost, edgeTypes, "",
			`{"id":"unused_edge","name":"Unused","attributes_schema":{"type":"object","properties":{}},"allowed":[{"from":"external","to":"team"}]}`, nil)
		admin.expect(t, http.StatusNoContent, http.MethodDelete, nodeTypes+"/unused", "", "", nil)
		admin.expect(t, http.StatusNoContent, http.MethodDelete, edgeTypes+"/unused_edge", "", "", nil)
		types := listTypes(t)
		for _, nt := range types.NodeTypes {
			assert.NotEqual(t, "unused", nt.ID)
		}
		for _, et := range types.EdgeTypes {
			assert.NotEqual(t, "unused_edge", et.ID)
		}

		var p problemBody
		admin.expect(t, http.StatusNotFound, http.MethodDelete, nodeTypes+"/unused", "", "", &p)
		assert.Equal(t, "/problems/not-found", p.Type)
		assert.Equal(t, "node type not found", p.Detail)
		admin.expect(t, http.StatusNotFound, http.MethodPatch, edgeTypes+"/unused_edge", "", `{"name":"x"}`, &p)
		assert.Equal(t, "edge type not found", p.Detail)

		admin.expect(t, http.StatusConflict, http.MethodDelete, nodeTypes+"/message-broker", "", "", &p)
		assert.Equal(t, "/problems/in-use", p.Type)
		assert.Equal(t, "node type is still used by nodes", p.Detail)
		require.NotNil(t, p.Count)
		assert.Equal(t, 2, *p.Count)
		admin.expect(t, http.StatusConflict, http.MethodDelete, edgeTypes+"/publishes_to", "", "", &p)
		assert.Equal(t, "/problems/in-use", p.Type)
		assert.Equal(t, "edge type is still used by edges", p.Detail)
		require.NotNil(t, p.Count)
		assert.Equal(t, 2, *p.Count)

		assert.Equal(t, []string{
			"node/unused create admin ui", "edge/unused_edge create admin ui",
			"node/unused delete admin ui", "edge/unused_edge delete admin ui",
		}, typeRows(t, before))
		var after []byte
		require.NoError(t, owner.QueryRow(ctx,
			"SELECT before FROM change_log WHERE entity = 'type' AND entity_key = 'node/unused' AND action = 'delete'").Scan(&after))
		assert.JSONEq(t, `{"id":"unused","kind":"external","name":"Unused","attributes_schema":{"type":"object","properties":{}},"builtin":false}`,
			string(after))
	})

	t.Run("secrets and schema", func(t *testing.T) {
		before := changeCount(t, owner)
		var p problemBody
		rec := admin.expect(t, http.StatusUnprocessableEntity, http.MethodPost, nodeTypes, "",
			`{"id":"vault-proxy","kind":"infrastructure","name":"Qv8sTr4Np","attributes_schema":{"type":"object",
			"properties":{"api_key":{"type":"string","enum":["Zx9wYv3Uk"]},"region":{"type":"string"}}}}`, &p)
		assert.Equal(t, "/problems/secret-detected", p.Type)
		require.Len(t, p.Errors, 1)
		assert.Equal(t, "body.attributes_schema.properties.api_key", p.Errors[0].Location)
		assert.Equal(t, "looks like a secret", p.Errors[0].Message)
		for _, v := range []string{"Qv8sTr4Np", "Zx9wYv3Uk", "vault-proxy", "region"} {
			assert.NotContains(t, rec.Body.String(), v, "no other submitted value")
		}
		admin.expect(t, http.StatusUnprocessableEntity, http.MethodPatch, edgeTypes+"/publishes_to", "",
			`{"attributes_schema":{"type":"object","properties":{"accessToken":{"type":"string"}}}}`, &p)
		assert.Equal(t, "/problems/secret-detected", p.Type)
		assert.Equal(t, []string{"body.attributes_schema.properties.accessToken"}, p.locations())

		tests := []struct {
			name, method, path, body string
			want                     string
		}{
			{"object property", http.MethodPost, nodeTypes,
				`{"id":"broker-2","kind":"infrastructure","name":"x","attributes_schema":{"type":"object","properties":{"x":{"type":"object"}}}}`,
				"body.attributes_schema.properties.x"},
			{"pattern keyword", http.MethodPatch, nodeTypes + "/message-broker",
				`{"attributes_schema":{"type":"object","properties":{"engine":{"type":"string","pattern":"^r"}}}}`,
				"body.attributes_schema.properties.engine"},
			{"required undeclared", http.MethodPost, edgeTypes,
				`{"id":"feeds","name":"x","attributes_schema":{"type":"object","required":["x"],"properties":{}},"allowed":[{"from":"application","to":"application"}]}`,
				"body.attributes_schema.required"},
			{"bad node type id", http.MethodPost, nodeTypes,
				`{"id":"Message_Broker","kind":"infrastructure","name":"x","attributes_schema":{"type":"object","properties":{}}}`, "body.id"},
			{"unknown kind", http.MethodPost, nodeTypes,
				`{"id":"broker-3","kind":"planet","name":"x","attributes_schema":{"type":"object","properties":{}}}`, "body.kind"},
			{"unknown allowed kind", http.MethodPost, edgeTypes,
				`{"id":"feeds","name":"x","attributes_schema":{"type":"object","properties":{}},"allowed":[{"from":"application","to":"planet"}]}`,
				"body.allowed[0].to"},
			{"kind in a patch", http.MethodPatch, nodeTypes + "/message-broker", `{"kind":"network"}`, "body.kind"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var p problemBody
				admin.expect(t, http.StatusUnprocessableEntity, tt.method, tt.path, "", tt.body, &p)
				assert.Equal(t, "/problems/validation", p.Type)
				assert.Contains(t, p.locations(), tt.want)
			})
		}
		assert.Equal(t, before, changeCount(t, owner), "no change_log row for a rejected write")
	})

	t.Run("viewers and editors can't write types", func(t *testing.T) {
		before := changeCount(t, owner)
		for _, w := range []writer{viewer, editor} {
			for _, r := range [][3]string{
				{http.MethodPost, nodeTypes, broker}, {http.MethodPatch, nodeTypes + "/message-broker", `{"name":"x"}`},
				{http.MethodDelete, nodeTypes + "/unused", ""},
				{http.MethodPost, edgeTypes, `{"id":"feeds","name":"x","attributes_schema":{"type":"object","properties":{}},"allowed":[{"from":"application","to":"application"}]}`},
				{http.MethodPatch, edgeTypes + "/publishes_to", `{"name":"x"}`}, {http.MethodDelete, edgeTypes + "/publishes_to", ""},
			} {
				var p problemBody
				w.expect(t, http.StatusForbidden, r[0], r[1], "", r[2], &p)
				assert.Equal(t, "/problems/forbidden", p.Type)
			}
		}
		assert.Equal(t, before, changeCount(t, owner))
	})

	t.Run("a node write waits for a type change", func(t *testing.T) {
		tx, err := owner.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `UPDATE node_types SET attributes_schema = '{"type":"object","required":["engine"],"properties":{"engine":{"type":"string"}}}'
			WHERE id = 'message-broker'`)
		require.NoError(t, err)

		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- admin.send(t, http.MethodPost, "/api/v1/nodes", "",
				`{"environment":"prod","id":"rabbit-race","type":"message-broker","name":"r","owner":"platform"}`)
		}()
		require.Eventually(t, func() bool {
			var waiting int
			err := owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
				WHERE usename = 'arch_app' AND wait_event_type = 'Lock'`).Scan(&waiting)
			return err == nil && waiting > 0
		}, 5*time.Second, 20*time.Millisecond, "the node write waits on the type row")
		require.NoError(t, tx.Commit(ctx))

		var got *httptest.ResponseRecorder
		select {
		case got = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the node write did not finish after the commit")
		}
		require.Equal(t, http.StatusUnprocessableEntity, got.Code, "checked against the new schema: %s", got.Body.String())
	})

	var all []string
	for _, c := range changesSince(t, owner, start) {
		if c.Entity == "type" {
			all = append(all, c.EntityKey+" "+c.Action)
		}
	}
	assert.Equal(t, []string{
		"node/message-broker create", "node/message-broker update", "edge/publishes_to create", "edge/publishes_to update",
		"edge/calls update", "node/service update", "node/unused create", "edge/unused_edge create",
		"node/unused delete", "edge/unused_edge delete",
	}, all, "exactly one row per successful type write")
}
