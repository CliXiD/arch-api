package httpapi_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/httpapi"
	"github.com/CliXiD/arch-api/internal/model"
)

var created = time.Date(2026, 11, 8, 17, 0, 0, 0, time.UTC)

var paymentsAPI = model.Node{
	Environment: "prod", ID: "payments-api", Kind: "application", Type: "service", Name: "Payments API",
	Description: "Sample: payments service", Owner: "platform", Attributes: map[string]any{}, Tags: []string{},
	Source: "import", CreatedAt: created, UpdatedAt: created, Version: 1,
}

// fakeModel records the filters it receives and answers from fixed data, or with err.
type fakeModel struct {
	err        error
	nodeFilter model.NodeFilter
	edgeFilter model.EdgeFilter
	graphEnv   string
}

func (f *fakeModel) Types(context.Context) (model.Types, error) {
	return model.Types{
		NodeTypes: []model.NodeType{{ID: "service", Kind: "application", Name: "Service",
			AttributesSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Builtin: true}},
		EdgeTypes: []model.EdgeType{{ID: "runs_on", Name: "Runs on",
			AttributesSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Allowed:          []model.KindPair{{From: "application", To: "infrastructure"}}, Builtin: true}},
	}, f.err
}

func (f *fakeModel) Teams(context.Context) (model.TeamList, error) {
	return model.TeamList{Items: []model.Team{}}, f.err
}

func (f *fakeModel) Environments(context.Context) (model.EnvironmentList, error) {
	return model.EnvironmentList{Items: []model.Environment{{ID: "dev", Name: "dev", Sort: 1}, {ID: "prod", Name: "prod", Sort: 3}}}, f.err
}

func (f *fakeModel) Nodes(_ context.Context, nf model.NodeFilter) (model.NodePage, error) {
	f.nodeFilter = nf
	if f.err != nil {
		return model.NodePage{}, f.err
	}
	next := "next-page"
	return model.NodePage{Items: []model.Node{paymentsAPI}, NextCursor: &next}, nil
}

func (f *fakeModel) Node(_ context.Context, environment, id string) (model.Node, error) {
	if f.err != nil {
		return model.Node{}, f.err
	}
	if environment != "prod" || id != "payments-api" {
		return model.Node{}, model.ErrNotFound
	}
	return paymentsAPI, nil
}

func (f *fakeModel) Edges(_ context.Context, ef model.EdgeFilter) (model.EdgePage, error) {
	f.edgeFilter = ef
	return model.EdgePage{Items: []model.Edge{}}, f.err
}

func (f *fakeModel) Graph(_ context.Context, environment string) (model.Graph, error) {
	f.graphEnv = environment
	if f.err != nil {
		return model.Graph{}, f.err
	}
	return model.Graph{
		Environment: environment,
		Nodes:       []model.GraphNode{{ID: "payments-api", Kind: "application", Type: "service", Name: "Payments API", Owner: "platform", Tags: []string{}}},
		Edges: []model.GraphEdge{{UID: "0b0f8c4e-5a0f-4a37-9d35-9b2f1f6f0c11", Type: "calls", From: "payments-api", To: "core-banking",
			Attributes: map[string]any{"protocol": "https", "port": float64(443)}}},
	}, nil
}

func modelServer(t *testing.T, m *fakeModel) (*testServer, http.Header) {
	t.Helper()
	s := newTestServer(t, func(d *httpapi.Deps) { d.Model = m })
	return s, bearer(s.token(t, nil, "viewer"))
}

type validationProblem struct {
	Type   string           `json:"type"`
	Status int              `json:"status"`
	Detail string           `json:"detail"`
	Errors []map[string]any `json:"errors"`
}

func TestModelReads(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantBody string
	}{
		{"types", "/api/v1/types", `{
			"node_types":[{"id":"service","kind":"application","name":"Service","attributes_schema":{"type":"object","properties":{}},"builtin":true}],
			"edge_types":[{"id":"runs_on","name":"Runs on","attributes_schema":{"type":"object","properties":{}},
				"allowed":[{"from":"application","to":"infrastructure"}],"builtin":true}]}`},
		{"teams are an empty list, not null", "/api/v1/teams", `{"items":[]}`},
		{"environments", "/api/v1/environments", `{"items":[{"id":"dev","name":"dev","sort":1},{"id":"prod","name":"prod","sort":3}]}`},
		{"nodes page", "/api/v1/nodes?environment=prod", `{"items":[{"environment":"prod","id":"payments-api","kind":"application",
			"type":"service","name":"Payments API","description":"Sample: payments service","owner":"platform","attributes":{},
			"tags":[],"source":"import","created_at":"2026-11-08T17:00:00Z","updated_at":"2026-11-08T17:00:00Z","version":1}],
			"next_cursor":"next-page"}`},
		{"last edges page has a null next_cursor", "/api/v1/edges", `{"items":[],"next_cursor":null}`},
		{"graph", "/api/v1/graph?environment=prod", `{"environment":"prod",
			"nodes":[{"id":"payments-api","kind":"application","type":"service","name":"Payments API","owner":"platform","tags":[]}],
			"edges":[{"uid":"0b0f8c4e-5a0f-4a37-9d35-9b2f1f6f0c11","type":"calls","from":"payments-api","to":"core-banking",
				"attributes":{"protocol":"https","port":443}}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, h := modelServer(t, &fakeModel{})
			rec := s.do(t, http.MethodGet, tt.path, h)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			assert.JSONEq(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestGetNode(t *testing.T) {
	s, h := modelServer(t, &fakeModel{})

	rec := s.do(t, http.MethodGet, "/api/v1/nodes/prod/payments-api", h)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, `"1"`, rec.Header().Get("ETag"))
	assert.JSONEq(t, `{"environment":"prod","id":"payments-api","kind":"application","type":"service","name":"Payments API",
		"description":"Sample: payments service","owner":"platform","attributes":{},"tags":[],"source":"import",
		"created_at":"2026-11-08T17:00:00Z","updated_at":"2026-11-08T17:00:00Z","version":1}`, rec.Body.String())

	rec = s.do(t, http.MethodGet, "/api/v1/nodes/prod/nope", h)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	assert.Empty(t, rec.Header().Get("ETag"))
	var p problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, problem{Type: "/problems/not-found", Title: "Not Found", Status: http.StatusNotFound, Detail: "node not found"}, p)
	assert.NotContains(t, rec.Body.String(), "nope", "the id is never echoed")
}

func TestModelFilters(t *testing.T) {
	m := &fakeModel{}
	s, h := modelServer(t, m)

	rec := s.do(t, http.MethodGet, "/api/v1/nodes?environment=prod&kind=network&type=vpc&owner=platform&q=PAY&cursor=abc", h)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, model.NodeFilter{
		Environment: "prod", Kind: "network", Type: "vpc", Owner: "platform", Q: "PAY", Limit: 100, Cursor: "abc",
	}, m.nodeFilter, "limit defaults to 100")

	rec = s.do(t, http.MethodGet, "/api/v1/edges?environment=prod&type=routes_via&from=a&to=b&limit=500", h)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, model.EdgeFilter{Environment: "prod", Type: "routes_via", From: "a", To: "b", Limit: 500}, m.edgeFilter)

	rec = s.do(t, http.MethodGet, "/api/v1/nodes?limit=1", h)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, m.nodeFilter.Limit)
}

func TestModelValidation(t *testing.T) {
	long := string(bytes.Repeat([]byte("x"), 201))
	tests := []struct {
		name         string
		model        *fakeModel
		path         string
		wantLocation string
		secret       string
	}{
		{"limit 0", &fakeModel{}, "/api/v1/nodes?limit=0", "query.limit", ""},
		{"limit 501", &fakeModel{}, "/api/v1/nodes?limit=501", "query.limit", ""},
		{"limit not an integer", &fakeModel{}, "/api/v1/edges?limit=abc-secret", "query.limit", "abc-secret"},
		{"unknown kind", &fakeModel{}, "/api/v1/nodes?kind=planet-secret", "query.kind", "planet-secret"},
		{"q too long", &fakeModel{}, "/api/v1/nodes?q=" + long, "query.q", long},
		{"missing graph environment", &fakeModel{}, "/api/v1/graph", "query.environment", ""},
		{"bad cursor", &fakeModel{err: &model.ValidationError{Location: "query.cursor", Message: "invalid cursor"}},
			"/api/v1/nodes?cursor=zzz-secret", "query.cursor", "zzz-secret"},
		{"unknown environment", &fakeModel{err: &model.ValidationError{Location: "query.environment", Message: "unknown environment"}},
			"/api/v1/graph?environment=nope-secret", "query.environment", "nope-secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, h := modelServer(t, tt.model)
			rec := s.do(t, http.MethodGet, tt.path, h)
			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

			var p validationProblem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, "/problems/validation", p.Type)
			assert.Equal(t, "validation failed", p.Detail)
			require.NotEmpty(t, p.Errors)
			for _, e := range p.Errors {
				assert.NotContains(t, e, "value", "received values are never echoed")
			}
			assert.Equal(t, tt.wantLocation, p.Errors[0]["location"])
			if tt.secret != "" {
				assert.NotContains(t, rec.Body.String(), tt.secret)
			}
		})
	}
}

func TestModelDatabaseError(t *testing.T) {
	m := &fakeModel{err: &pgconn.PgError{Code: "57P01", Message: "terminating connection", Detail: "Key (id)=(secret-host.internal)"}}
	s, h := modelServer(t, m)
	for _, path := range []string{"/api/v1/types", "/api/v1/nodes/prod/payments-api", "/api/v1/graph?environment=prod"} {
		t.Run(path, func(t *testing.T) {
			s.logs.Reset()
			rec := s.do(t, http.MethodGet, path, h)
			require.Equal(t, http.StatusInternalServerError, rec.Code)
			var p problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, "internal error", p.Detail)
			assert.NotContains(t, rec.Body.String(), "secret-host")
			assert.Contains(t, s.logs.String(), `"error_class":"sqlstate 57P01"`)
			assert.NotContains(t, s.logs.String(), "secret-host")
			assert.NotContains(t, s.logs.String(), "terminating connection")
		})
	}

	s, h = modelServer(t, &fakeModel{err: errors.New("dial tcp secret-host.internal:5432")})
	rec := s.do(t, http.MethodGet, "/api/v1/teams", h)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, s.logs.String(), "secret-host")
}

func TestModelRoutesNeedAToken(t *testing.T) {
	s, _ := modelServer(t, &fakeModel{})
	for _, path := range []string{
		"/api/v1/types", "/api/v1/teams", "/api/v1/environments", "/api/v1/nodes",
		"/api/v1/nodes/prod/payments-api", "/api/v1/edges", "/api/v1/graph?environment=prod",
	} {
		t.Run(path, func(t *testing.T) {
			rec := s.do(t, http.MethodGet, path, nil)
			require.Equal(t, http.StatusUnauthorized, rec.Code)
			var p problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, "/problems/unauthenticated", p.Type)
		})
	}
}

func TestGraphGzip(t *testing.T) {
	s, h := modelServer(t, &fakeModel{})

	t.Run("with Accept-Encoding gzip", func(t *testing.T) {
		gz := h.Clone()
		gz.Set("Accept-Encoding", "gzip, deflate")
		rec := s.do(t, http.MethodGet, "/api/v1/graph?environment=prod", gz)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
		assert.Contains(t, rec.Header().Values("Vary"), "Accept-Encoding")
		r, err := gzip.NewReader(rec.Body)
		require.NoError(t, err)
		body, err := io.ReadAll(r)
		require.NoError(t, err)
		var g model.Graph
		require.NoError(t, json.Unmarshal(body, &g))
		assert.Equal(t, "prod", g.Environment)
		assert.Len(t, g.Nodes, 1)
	})

	t.Run("without Accept-Encoding", func(t *testing.T) {
		rec := s.do(t, http.MethodGet, "/api/v1/graph?environment=prod", h)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Content-Encoding"))
		var g model.Graph
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &g))
		assert.Equal(t, "prod", g.Environment)
	})

	t.Run("errors are not compressed", func(t *testing.T) {
		gz := h.Clone()
		gz.Set("Accept-Encoding", "gzip")
		rec := s.do(t, http.MethodGet, "/api/v1/graph", gz)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		assert.Empty(t, rec.Header().Get("Content-Encoding"))
	})

	t.Run("other routes are not compressed", func(t *testing.T) {
		gz := h.Clone()
		gz.Set("Accept-Encoding", "gzip")
		rec := s.do(t, http.MethodGet, "/api/v1/nodes/prod/payments-api", gz)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Content-Encoding"))
		assert.Equal(t, `"1"`, rec.Header().Get("ETag"))
	})
}
