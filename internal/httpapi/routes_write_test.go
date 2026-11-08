package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/httpapi"
	"github.com/CliXiD/arch-api/internal/model"
)

const edgeUID = "6b1f0c2e-8a4d-4f3b-9c1e-2d5a7b8c9e01"

var callsEdge = model.Edge{
	UID: edgeUID, Environment: "prod", Type: "calls", From: "payments-api", To: "ledger",
	Attributes: map[string]any{"protocol": "https", "port": float64(443)}, Source: "ui",
	CreatedAt: created, UpdatedAt: created, Version: 1,
}

// fakeWriter records the actor and If-Match version it receives. Entities are at version 1:
// another version is stale. Node and edge writes run the real secret check. err, when set, is
// returned by every call.
type fakeWriter struct {
	err     error
	calls   int
	actor   model.Actor
	version int
}

func (f *fakeWriter) call(a model.Actor, version int) error {
	f.calls++
	f.actor, f.version = a, version
	return f.err
}

func (f *fakeWriter) checkVersion(version int) error {
	if version != 1 {
		return model.ErrStale
	}
	return nil
}

func (f *fakeWriter) CreateNode(_ context.Context, a model.Actor, in model.NodeCreate) (model.Node, error) {
	if err := f.call(a, 0); err != nil {
		return model.Node{}, err
	}
	if err := model.CheckSecrets(in.Attributes); err != nil {
		return model.Node{}, err
	}
	n := paymentsAPI
	n.ID, n.Name, n.Owner, n.Source = in.ID, in.Name, in.Owner, "ui"
	return n, nil
}

func (f *fakeWriter) UpdateNode(_ context.Context, a model.Actor, _, _ string, version int, p model.NodePatch) (model.Node, error) {
	if err := f.call(a, version); err != nil {
		return model.Node{}, err
	}
	if err := f.checkVersion(version); err != nil {
		return model.Node{}, err
	}
	if err := model.CheckSecrets(p.Attributes); err != nil {
		return model.Node{}, err
	}
	n := paymentsAPI
	n.Version, n.Source = 2, "ui"
	return n, nil
}

func (f *fakeWriter) DeleteNode(_ context.Context, a model.Actor, _, _ string, version int) error {
	if err := f.call(a, version); err != nil {
		return err
	}
	return f.checkVersion(version)
}

func (f *fakeWriter) CreateEdge(_ context.Context, a model.Actor, in model.EdgeCreate) (model.Edge, error) {
	if err := f.call(a, 0); err != nil {
		return model.Edge{}, err
	}
	if err := model.CheckSecrets(in.Attributes); err != nil {
		return model.Edge{}, err
	}
	return callsEdge, nil
}

func (f *fakeWriter) UpdateEdge(_ context.Context, a model.Actor, _ string, version int, p model.EdgePatch) (model.Edge, error) {
	if err := f.call(a, version); err != nil {
		return model.Edge{}, err
	}
	if err := f.checkVersion(version); err != nil {
		return model.Edge{}, err
	}
	if err := model.CheckSecrets(p.Attributes); err != nil {
		return model.Edge{}, err
	}
	e := callsEdge
	e.Version = 2
	return e, nil
}

func (f *fakeWriter) DeleteEdge(_ context.Context, a model.Actor, _ string, version int) error {
	if err := f.call(a, version); err != nil {
		return err
	}
	return f.checkVersion(version)
}

func (f *fakeWriter) CreateTeam(_ context.Context, a model.Actor, in model.TeamCreate) (model.Team, error) {
	return model.Team{ID: in.ID, Name: in.Name, CreatedAt: created}, f.call(a, 0)
}

func (f *fakeWriter) UpdateTeam(_ context.Context, a model.Actor, id string, p model.TeamPatch) (model.Team, error) {
	return model.Team{ID: id, Name: p.Name, CreatedAt: created}, f.call(a, 0)
}

func (f *fakeWriter) DeleteTeam(_ context.Context, a model.Actor, _ string) error {
	return f.call(a, 0)
}

func (f *fakeWriter) CreateEnvironment(_ context.Context, a model.Actor, in model.EnvironmentCreate) (model.Environment, error) {
	return model.Environment{ID: in.ID, Name: in.Name, Sort: int(in.Sort)}, f.call(a, 0)
}

func (f *fakeWriter) UpdateEnvironment(_ context.Context, a model.Actor, id string, _ model.EnvironmentPatch) (model.Environment, error) {
	return model.Environment{ID: id, Name: "Sandbox", Sort: 4}, f.call(a, 0)
}

func (f *fakeWriter) DeleteEnvironment(_ context.Context, a model.Actor, _ string) error {
	return f.call(a, 0)
}

func writeServer(t *testing.T, w *fakeWriter) *testServer {
	t.Helper()
	return newTestServer(t, func(d *httpapi.Deps) { d.Writer = w; d.Model = &fakeModel{} })
}

func (s *testServer) send(t *testing.T, method, path string, header http.Header, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	return rec
}

func withIfMatch(h http.Header, v string) http.Header {
	h = h.Clone()
	h.Set("If-Match", v)
	return h
}

func editorToken(t *testing.T, s *testServer) http.Header {
	t.Helper()
	return bearer(s.token(t, func(c jwt.MapClaims) { c["teams"] = []string{"platform"} }, "editor"))
}

const nodeBody = `{"environment":"prod","id":"payments-api","type":"service","name":"Payments","owner":"platform"}`

func TestWriteRoles(t *testing.T) {
	w := &fakeWriter{}
	s := writeServer(t, w)
	viewer := bearer(s.token(t, nil, "viewer"))
	editor := editorToken(t, s)
	admin := bearer(s.token(t, nil, "admin"))
	v1 := `"1"`
	tests := []struct {
		name       string
		method     string
		path       string
		header     http.Header
		body       string
		wantStatus int
		wantDetail string
	}{
		{"viewer creates a node", http.MethodPost, "/api/v1/nodes", viewer, nodeBody, http.StatusForbidden, "requires role editor"},
		{"viewer deletes an edge", http.MethodDelete, "/api/v1/edges/" + edgeUID, withIfMatch(viewer, v1), "", http.StatusForbidden, "requires role editor"},
		{"editor creates a team", http.MethodPost, "/api/v1/teams", editor, `{"id":"core","name":"Core"}`, http.StatusForbidden, "requires role admin"},
		{"editor renames a team", http.MethodPatch, "/api/v1/teams/core", editor, `{"name":"Core"}`, http.StatusForbidden, "requires role admin"},
		{"editor deletes a team", http.MethodDelete, "/api/v1/teams/core", editor, "", http.StatusForbidden, "requires role admin"},
		{"editor creates an environment", http.MethodPost, "/api/v1/environments", editor, `{"id":"sandbox","name":"Sandbox","sort":4}`,
			http.StatusForbidden, "requires role admin"},
		{"editor updates an environment", http.MethodPatch, "/api/v1/environments/sandbox", editor, `{"sort":5}`, http.StatusForbidden, "requires role admin"},
		{"editor deletes an environment", http.MethodDelete, "/api/v1/environments/sandbox", editor, "", http.StatusForbidden, "requires role admin"},
		{"editor creates a node", http.MethodPost, "/api/v1/nodes", editor, nodeBody, http.StatusCreated, ""},
		{"editor updates a node", http.MethodPatch, "/api/v1/nodes/prod/payments-api", withIfMatch(editor, v1), `{"name":"P"}`, http.StatusOK, ""},
		{"editor deletes a node", http.MethodDelete, "/api/v1/nodes/prod/payments-api", withIfMatch(editor, v1), "", http.StatusNoContent, ""},
		{"editor creates an edge", http.MethodPost, "/api/v1/edges", editor,
			`{"environment":"prod","type":"calls","from":"payments-api","to":"ledger","attributes":{"protocol":"https","port":443}}`, http.StatusCreated, ""},
		{"editor updates an edge", http.MethodPatch, "/api/v1/edges/" + edgeUID, withIfMatch(editor, v1), `{"attributes":{}}`, http.StatusOK, ""},
		{"editor deletes an edge", http.MethodDelete, "/api/v1/edges/" + edgeUID, withIfMatch(editor, v1), "", http.StatusNoContent, ""},
		{"admin creates a team", http.MethodPost, "/api/v1/teams", admin, `{"id":"core","name":"Core"}`, http.StatusCreated, ""},
		{"admin renames a team", http.MethodPatch, "/api/v1/teams/core", admin, `{"name":"Core banking"}`, http.StatusOK, ""},
		{"admin deletes a team", http.MethodDelete, "/api/v1/teams/core", admin, "", http.StatusNoContent, ""},
		{"admin creates an environment", http.MethodPost, "/api/v1/environments", admin, `{"id":"sandbox","name":"Sandbox","sort":4}`, http.StatusCreated, ""},
		{"admin updates an environment", http.MethodPatch, "/api/v1/environments/sandbox", admin, `{"sort":4}`, http.StatusOK, ""},
		{"admin deletes an environment", http.MethodDelete, "/api/v1/environments/sandbox", admin, "", http.StatusNoContent, ""},
		{"viewer reads history", http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=payments-api", viewer, "", http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w.calls = 0
			rec := s.send(t, tt.method, tt.path, tt.header, tt.body)
			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			if tt.wantStatus == http.StatusNoContent {
				assert.Empty(t, rec.Body.String())
			}
			if tt.wantDetail != "" {
				var p problem
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
				assert.Equal(t, "/problems/forbidden", p.Type)
				assert.Equal(t, tt.wantDetail, p.Detail)
				assert.Zero(t, w.calls, "the writer is not called")
			}
		})
	}
}

func TestCreateNode(t *testing.T) {
	w := &fakeWriter{}
	s := writeServer(t, w)
	rec := s.send(t, http.MethodPost, "/api/v1/nodes", editorToken(t, s), nodeBody)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, `"1"`, rec.Header().Get("ETag"))
	var n model.Node
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &n))
	assert.Equal(t, "ui", n.Source)
	assert.Equal(t, model.Actor{
		Subject: "8d0c6d4e-user", Username: "editor", Kind: "user", Admin: false, Teams: []string{"platform"},
	}, w.actor)

	rec = s.send(t, http.MethodPost, "/api/v1/edges", editorToken(t, s), `{"environment":"prod","type":"calls","from":"payments-api","to":"ledger"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, `"1"`, rec.Header().Get("ETag"))
}

func TestWriteActor(t *testing.T) {
	w := &fakeWriter{}
	s := writeServer(t, w)
	tests := []struct {
		name  string
		token string
		want  model.Actor
	}{
		{
			name: "service account is identified by its client id",
			token: s.token(t, func(c jwt.MapClaims) {
				c["sub"] = "0b6e-service-account"
				c["client_id"] = "arch-ci-platform"
				c["azp"] = "arch-ci-platform"
				c["preferred_username"] = "service-account-arch-ci-platform"
				c["teams"] = []string{"platform"}
			}, "editor"),
			want: model.Actor{Subject: "arch-ci-platform", Username: "arch-ci-platform", Kind: "service", Teams: []string{"platform"}},
		},
		{
			name:  "admin user",
			token: s.token(t, nil, "admin"),
			want:  model.Actor{Subject: "8d0c6d4e-user", Username: "editor", Kind: "user", Admin: true, Teams: []string{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := s.send(t, http.MethodPost, "/api/v1/nodes", bearer(tt.token), nodeBody)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			assert.Equal(t, tt.want, w.actor)
		})
	}
}

func TestPreconditions(t *testing.T) {
	type route struct{ method, path, body string }
	routes := []route{
		{http.MethodPatch, "/api/v1/nodes/prod/payments-api", `{"name":"Payments"}`},
		{http.MethodDelete, "/api/v1/nodes/prod/payments-api", ""},
		{http.MethodPatch, "/api/v1/edges/" + edgeUID, `{"attributes":{"protocol":"http"}}`},
		{http.MethodDelete, "/api/v1/edges/" + edgeUID, ""},
	}
	tests := []struct {
		name        string
		ifMatch     string
		wantStatus  int
		wantType    string
		wantVersion int
	}{
		{"missing", "", http.StatusPreconditionRequired, "/problems/precondition-required", -1},
		{"old version", `"999"`, http.StatusPreconditionFailed, "/problems/stale", 999},
		{"star", "*", http.StatusPreconditionFailed, "/problems/stale", 0},
		{"weak", `W/"1"`, http.StatusPreconditionFailed, "/problems/stale", 0},
		{"unquoted", "1", http.StatusPreconditionFailed, "/problems/stale", 0},
		{"signed", `"+1"`, http.StatusPreconditionFailed, "/problems/stale", 0},
		{"current version", `"1"`, 0, "", 1},
	}
	for _, r := range routes {
		for _, tt := range tests {
			t.Run(r.method+" "+r.path+" "+tt.name, func(t *testing.T) {
				w := &fakeWriter{}
				s := writeServer(t, w)
				h := editorToken(t, s)
				if tt.ifMatch != "" {
					h = withIfMatch(h, tt.ifMatch)
				}
				rec := s.send(t, r.method, r.path, h, r.body)
				if tt.wantStatus == 0 {
					if r.method == http.MethodDelete {
						require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
						return
					}
					require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
					assert.Equal(t, `"2"`, rec.Header().Get("ETag"), "one higher")
					return
				}
				require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
				assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
				var p problem
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
				assert.Equal(t, tt.wantType, p.Type)
				assert.Empty(t, rec.Header().Get("ETag"))
				if tt.wantVersion < 0 {
					assert.Zero(t, w.calls, "428 before the writer is called")
				} else {
					assert.Equal(t, tt.wantVersion, w.version)
				}
			})
		}
	}
}

func TestWriteRequestValidation(t *testing.T) {
	w := &fakeWriter{}
	s := writeServer(t, w)
	editor := editorToken(t, s)
	admin := bearer(s.token(t, nil, "admin"))
	v1 := withIfMatch(editor, `"1"`)
	tests := []struct {
		name         string
		method       string
		path         string
		header       http.Header
		body         string
		wantLocation string
	}{
		{"bad node id", http.MethodPost, "/api/v1/nodes", editor,
			`{"environment":"prod","id":"Bad_Slug","type":"service","name":"x","owner":"platform"}`, "body.id"},
		{"node without owner", http.MethodPost, "/api/v1/nodes", editor,
			`{"environment":"prod","id":"payments-api","type":"service","name":"x"}`, "body"},
		{"unknown node property", http.MethodPost, "/api/v1/nodes", editor,
			`{"environment":"prod","id":"payments-api","type":"service","name":"x","owner":"platform","kind":"network"}`, "body.kind"},
		{"patch node id", http.MethodPatch, "/api/v1/nodes/prod/payments-api", v1, `{"id":"other"}`, "body.id"},
		{"patch node type", http.MethodPatch, "/api/v1/nodes/prod/payments-api", v1, `{"type":"vpc"}`, "body.type"},
		{"patch node environment", http.MethodPatch, "/api/v1/nodes/prod/payments-api", v1, `{"environment":"dev"}`, "body.environment"},
		{"patch edge without attributes", http.MethodPatch, "/api/v1/edges/" + edgeUID, v1, `{}`, "body"},
		{"patch edge from", http.MethodPatch, "/api/v1/edges/" + edgeUID, v1, `{"attributes":{},"from":"x"}`, "body.from"},
		{"bad edge uid", http.MethodDelete, "/api/v1/edges/not-a-uuid", v1, "", "path.uid"},
		{"bad team id", http.MethodPost, "/api/v1/teams", admin, `{"id":"Core Team","name":"Core"}`, "body.id"},
		{"environment without sort", http.MethodPost, "/api/v1/environments", admin, `{"id":"sandbox","name":"Sandbox"}`, "body"},
		{"history without entity", http.MethodGet, "/api/v1/history?environment=prod&id=payments-api", editor, "", "query.entity"},
		{"history of an edge", http.MethodGet, "/api/v1/history?entity=edge&environment=prod&id=payments-api", editor, "", "query.entity"},
		{"history without id", http.MethodGet, "/api/v1/history?entity=node&environment=prod", editor, "", "query.id"},
		{"history limit 501", http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=a&limit=501", editor, "", "query.limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w.calls = 0
			rec := s.send(t, tt.method, tt.path, tt.header, tt.body)
			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			var p validationProblem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, "/problems/validation", p.Type)
			locations := []any{}
			for _, e := range p.Errors {
				assert.NotContains(t, e, "value", "received values are never echoed")
				locations = append(locations, e["location"])
			}
			assert.Contains(t, locations, tt.wantLocation)
			assert.Zero(t, w.calls, "the writer is not called")
		})
	}
}

func TestPreconditionAfterSchema(t *testing.T) {
	w := &fakeWriter{}
	s := writeServer(t, w)
	rec := s.send(t, http.MethodPatch, "/api/v1/nodes/prod/payments-api", editorToken(t, s), `{"id":"x"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "the request schema is checked before If-Match")
}

func TestWriteProblems(t *testing.T) {
	v1 := `"1"`
	tests := []struct {
		name       string
		err        error
		method     string
		path       string
		body       string
		wantStatus int
		wantType   string
		wantDetail string
	}{
		{"node not found", model.ErrNotFound, http.MethodPatch, "/api/v1/nodes/prod/nope", `{}`, http.StatusNotFound, "/problems/not-found", "node not found"},
		{"edge not found", model.ErrNotFound, http.MethodDelete, "/api/v1/edges/" + edgeUID, "", http.StatusNotFound, "/problems/not-found", "edge not found"},
		{"team not found", model.ErrNotFound, http.MethodPatch, "/api/v1/teams/nope", `{"name":"x"}`, http.StatusNotFound, "/problems/not-found", "team not found"},
		{"environment not found", model.ErrNotFound, http.MethodDelete, "/api/v1/environments/nope", "", http.StatusNotFound, "/problems/not-found", "environment not found"},
		{"other team's node", &model.ForbiddenError{Team: "core"}, http.MethodPost, "/api/v1/nodes", nodeBody,
			http.StatusForbidden, "/problems/forbidden", "node is owned by team core"},
		{"other team's edge", &model.ForbiddenError{Team: "core"}, http.MethodDelete, "/api/v1/edges/" + edgeUID, "",
			http.StatusForbidden, "/problems/forbidden", "node is owned by team core"},
		{"duplicate node", model.ErrConflict, http.MethodPost, "/api/v1/nodes", nodeBody,
			http.StatusConflict, "/problems/conflict", "a node with this environment and id already exists"},
		{"duplicate edge", model.ErrConflict, http.MethodPost, "/api/v1/edges", `{"environment":"prod","type":"calls","from":"a","to":"b"}`,
			http.StatusConflict, "/problems/conflict", "an edge of this type between these nodes already exists"},
		{"duplicate team", model.ErrConflict, http.MethodPost, "/api/v1/teams", `{"id":"core","name":"Core"}`,
			http.StatusConflict, "/problems/conflict", "a team with this id already exists"},
		{"duplicate environment sort", model.ErrConflict, http.MethodPatch, "/api/v1/environments/uat", `{"sort":3}`,
			http.StatusConflict, "/problems/conflict", "an environment with this id or sort already exists"},
		{"stale", model.ErrStale, http.MethodDelete, "/api/v1/nodes/prod/payments-api", "",
			http.StatusPreconditionFailed, "/problems/stale", "If-Match is not the current version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := writeServer(t, &fakeWriter{err: tt.err})
			h := withIfMatch(bearer(s.token(t, nil, "admin")), v1)
			rec := s.send(t, tt.method, tt.path, h, tt.body)
			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			var p problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, problem{Type: tt.wantType, Title: http.StatusText(tt.wantStatus), Status: tt.wantStatus, Detail: tt.wantDetail}, p)
		})
	}
}

func TestInUse(t *testing.T) {
	for _, path := range []string{"/api/v1/teams/platform", "/api/v1/environments/prod"} {
		t.Run(path, func(t *testing.T) {
			s := writeServer(t, &fakeWriter{err: &model.InUseError{Count: 7}})
			rec := s.send(t, http.MethodDelete, path, bearer(s.token(t, nil, "admin")), "")
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			var p struct {
				problem
				Count int `json:"count"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, "/problems/in-use", p.Type)
			assert.Equal(t, http.StatusConflict, p.Status)
			assert.Equal(t, 7, p.Count)
		})
	}
}

func TestModelValidationErrors(t *testing.T) {
	err := model.ValidationErrors{
		{Location: "body.attributes.port", Message: "must be at most 65535"},
		{Location: "body.to", Message: "edge type runs_on does not allow application → network"},
	}
	s := writeServer(t, &fakeWriter{err: err})
	rec := s.send(t, http.MethodPost, "/api/v1/edges", editorToken(t, s),
		`{"environment":"prod","type":"runs_on","from":"payments-api","to":"vpc-prod","attributes":{"port":70000}}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	var p validationProblem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "/problems/validation", p.Type)
	assert.Equal(t, []map[string]any{
		{"location": "body.attributes.port", "message": "must be at most 65535"},
		{"location": "body.to", "message": "edge type runs_on does not allow application → network"},
	}, p.Errors)
	assert.NotContains(t, rec.Body.String(), "70000")
}

func TestSecretsAreNeverEchoed(t *testing.T) {
	pem := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmU=\n-----END OPENSSH PRIVATE KEY-----"
	tests := []struct {
		name     string
		attr     string
		value    string
		location string
	}{
		{"password name", "password", "Zq7xWv9Lm2Kp", "body.attributes.password"},
		{"API_KEY name", "API_KEY", "c0ffee-d00d-f00d", "body.attributes.API_KEY"},
		{"privateKey name", "privateKey", "MIIEvQIBADAN", "body.attributes.privateKey"},
		{"URL with userinfo", "dsn", "postgres://u:p@h/db", "body.attributes.dsn"},
		{"URL password with a slash", "dsn", "postgres://u:p/w@h", "body.attributes.dsn"},
		{"URL password with an at sign", "dsn", "https://usr:p@ssw0rd-Qx7@db.example.test", "body.attributes.dsn"},
		{"access_token name", "access_token", "Tq8vNw3Rk5Jy", "body.attributes.access_token"},
		{"PEM block", "cert", pem, "body.attributes.cert"},
		{"AWS access key", "key_id", "AKIAIOSFODNN7EXAMPLE", "body.attributes.key_id"},
	}
	type route struct {
		method, path, body string
	}
	for _, tt := range tests {
		value, err := json.Marshal(tt.value)
		require.NoError(t, err)
		attrs := `{"` + tt.attr + `":` + string(value) + `}`
		for _, r := range []route{
			{http.MethodPost, "/api/v1/nodes", `{"environment":"prod","id":"payments-api","type":"service","name":"P","owner":"platform","attributes":` + attrs + `}`},
			{http.MethodPatch, "/api/v1/nodes/prod/payments-api", `{"attributes":` + attrs + `}`},
			{http.MethodPost, "/api/v1/edges", `{"environment":"prod","type":"calls","from":"a","to":"b","attributes":` + attrs + `}`},
			{http.MethodPatch, "/api/v1/edges/" + edgeUID, `{"attributes":` + attrs + `}`},
		} {
			t.Run(tt.name+" "+r.method+" "+r.path, func(t *testing.T) {
				s := writeServer(t, &fakeWriter{})
				rec := s.send(t, r.method, r.path, withIfMatch(editorToken(t, s), `"1"`), r.body)
				require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
				var p validationProblem
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
				assert.Equal(t, "/problems/secret-detected", p.Type)
				assert.Equal(t, []map[string]any{{"location": tt.location, "message": "looks like a secret"}}, p.Errors)
				body, logs := rec.Body.String(), s.logs.String()
				for i := 0; i+6 <= len(tt.value); i++ {
					part := tt.value[i : i+6]
					require.NotContains(t, body, part, "response body")
					require.NotContains(t, logs, part, "logs")
				}
			})
		}
	}
}

func TestTokenWordsAreAccepted(t *testing.T) {
	s := writeServer(t, &fakeWriter{})
	body := `{"environment":"prod","id":"payments-api","type":"service","name":"P","owner":"platform","attributes":{"tokens_per_minute":600}}`
	rec := s.send(t, http.MethodPost, "/api/v1/nodes", editorToken(t, s), body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func TestWriteDatabaseError(t *testing.T) {
	err := &pgconn.PgError{Code: "40001", Message: "could not serialize", Detail: "Key (id)=(secret-host.internal)"}
	s := writeServer(t, &fakeWriter{err: err})
	rec := s.send(t, http.MethodPost, "/api/v1/nodes", editorToken(t, s), nodeBody)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secret-host")
	assert.Contains(t, s.logs.String(), `"operation":"create-node"`)
	assert.Contains(t, s.logs.String(), `"error_class":"sqlstate 40001"`)
	assert.NotContains(t, s.logs.String(), "secret-host")
}

func TestHistory(t *testing.T) {
	m := &fakeModel{}
	s := newTestServer(t, func(d *httpapi.Deps) { d.Model = m })
	h := bearer(s.token(t, nil, "viewer"))

	rec := s.send(t, http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=payments-api", h, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, historyCall{environment: "prod", id: "payments-api", limit: 50}, m.history, "limit defaults to 50")
	assert.JSONEq(t, `{"items":[{"id":42,"at":"2026-11-08T17:00:00Z","actor":"8d0c6d4e-user","actor_name":"editor",
		"actor_kind":"user","source":"ui","entity":"node","entity_key":"prod/payments-api","action":"create",
		"before":null,"after":{"environment":"prod","id":"payments-api"}}],"next_cursor":"next-page"}`, rec.Body.String())

	rec = s.send(t, http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=payments-api&limit=1&cursor=abc", h, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, historyCall{environment: "prod", id: "payments-api", limit: 1, cursor: "abc"}, m.history)

	m.err = &model.ValidationError{Location: "query.cursor", Message: "invalid cursor"}
	rec = s.send(t, http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=payments-api&cursor=garbage", h, "")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	var p validationProblem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "query.cursor", p.Errors[0]["location"])
	assert.NotContains(t, rec.Body.String(), "garbage")
}

func TestWriteRoutesNeedAToken(t *testing.T) {
	s := writeServer(t, &fakeWriter{})
	for _, r := range [][2]string{
		{http.MethodPost, "/api/v1/nodes"}, {http.MethodPatch, "/api/v1/nodes/prod/payments-api"},
		{http.MethodDelete, "/api/v1/nodes/prod/payments-api"}, {http.MethodPost, "/api/v1/edges"},
		{http.MethodPatch, "/api/v1/edges/" + edgeUID}, {http.MethodDelete, "/api/v1/edges/" + edgeUID},
		{http.MethodPost, "/api/v1/teams"}, {http.MethodPatch, "/api/v1/teams/core"}, {http.MethodDelete, "/api/v1/teams/core"},
		{http.MethodPost, "/api/v1/environments"}, {http.MethodPatch, "/api/v1/environments/dev"},
		{http.MethodDelete, "/api/v1/environments/dev"}, {http.MethodGet, "/api/v1/history?entity=node&environment=prod&id=a"},
	} {
		t.Run(r[0]+" "+r[1], func(t *testing.T) {
			rec := s.send(t, r[0], r[1], nil, "")
			require.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}
