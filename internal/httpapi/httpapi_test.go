package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/auth"
	"github.com/CliXiD/arch-api/internal/auth/authtest"
	"github.com/CliXiD/arch-api/internal/health"
	"github.com/CliXiD/arch-api/internal/httpapi"
	"github.com/CliXiD/arch-api/internal/observability"
)

func init() { gin.SetMode(gin.TestMode) }

type fakeReady struct{ report health.Report }

func (f fakeReady) Check(context.Context) health.Report { return f.report }

type testServer struct {
	engine *gin.Engine
	realm  *authtest.Realm
	key    authtest.Key
	logs   *bytes.Buffer
}

func newTestServer(t *testing.T, edit func(*httpapi.Deps)) *testServer {
	t.Helper()
	key := authtest.NewKey(t, "k1")
	realm := authtest.NewRealm(t, key)
	logs := &bytes.Buffer{}
	logger := observability.NewLogger(logs, slog.LevelDebug)
	d := httpapi.Deps{
		Auth: auth.NewVerifier(auth.Options{
			Issuer: realm.Issuer(), Audience: authtest.Audience, JWKSURL: realm.JWKSURL(),
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}),
		Ready:   fakeReady{health.Report{Status: "ok"}},
		Logger:  logger,
		Metrics: observability.NewMetrics(),
	}
	if edit != nil {
		edit(&d)
	}
	return &testServer{engine: httpapi.NewEngine(d), realm: realm, key: key, logs: logs}
}

func (s *testServer) token(t *testing.T, edit func(jwt.MapClaims), realmRoles ...string) string {
	t.Helper()
	c := s.realm.Claims(time.Now(), realmRoles...)
	if edit != nil {
		edit(c)
	}
	return authtest.Sign(t, s.key, c)
}

func (s *testServer) do(t *testing.T, method, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	return rec
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func TestMe(t *testing.T) {
	s := newTestServer(t, nil)
	tests := []struct {
		name      string
		token     string
		wantRoles []string
		wantTeams []string
		wantMe    *httpapi.Me
	}{
		{
			name:      "admin gets every role",
			token:     s.token(t, nil, "admin"),
			wantRoles: []string{"viewer", "editor", "admin"},
			wantTeams: []string{},
		},
		{
			name:      "editor with group gets teams",
			token:     s.token(t, func(c jwt.MapClaims) { c["teams"] = []string{"platform"} }, "editor"),
			wantRoles: []string{"viewer", "editor"},
			wantTeams: []string{"platform"},
			wantMe: &httpapi.Me{
				Subject: "8d0c6d4e-user", Name: "Editor Local", Email: "editor@example.test",
				Roles: []string{"viewer", "editor"}, Teams: []string{"platform"}, ActorKind: "user",
			},
		},
		{
			name: "client credentials token is a service",
			token: s.token(t, func(c jwt.MapClaims) {
				c["sub"] = "0b6e-service-account"
				c["client_id"] = "arch-ci-platform"
				c["azp"] = "arch-ci-platform"
				c["preferred_username"] = "service-account-arch-ci-platform"
				c["teams"] = []string{"platform"}
				delete(c, "name")
				delete(c, "email")
			}, "editor"),
			wantRoles: []string{"viewer", "editor"},
			wantTeams: []string{"platform"},
			wantMe: &httpapi.Me{
				Subject: "arch-ci-platform", Name: "arch-ci-platform", Email: "",
				Roles: []string{"viewer", "editor"}, Teams: []string{"platform"}, ActorKind: "service",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := s.do(t, http.MethodGet, "/api/v1/me", bearer(tt.token))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			assert.Empty(t, rec.Header().Get("Link"))

			var raw map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			assert.NotContains(t, raw, "$schema")

			var me httpapi.Me
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
			assert.Equal(t, tt.wantRoles, me.Roles)
			assert.Equal(t, tt.wantTeams, me.Teams)
			assert.NotNil(t, raw["teams"], "teams is never null")
			if tt.wantMe != nil {
				assert.Equal(t, *tt.wantMe, me)
			}
		})
	}
}

func TestProblems(t *testing.T) {
	s := newTestServer(t, nil)
	valid := s.token(t, nil, "editor")
	tests := []struct {
		name       string
		header     http.Header
		wantStatus int
		wantType   string
		wantDetail string
	}{
		{"no token", nil, http.StatusUnauthorized, "/problems/unauthenticated", "a valid bearer token is required"},
		{"non-Bearer", http.Header{"Authorization": {"Basic " + valid}}, http.StatusUnauthorized, "/problems/unauthenticated", "a valid bearer token is required"},
		{"wrong audience", bearer(s.token(t, func(c jwt.MapClaims) { c["aud"] = "account" }, "editor")),
			http.StatusUnauthorized, "/problems/unauthenticated", "a valid bearer token is required"},
		{"no arch role", bearer(s.token(t, nil)), http.StatusForbidden, "/problems/forbidden", "requires role viewer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := s.do(t, http.MethodGet, "/api/v1/me", tt.header)
			require.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			var p problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, problem{
				Type: tt.wantType, Title: http.StatusText(tt.wantStatus), Status: tt.wantStatus, Detail: tt.wantDetail,
			}, p)
			assert.NotContains(t, rec.Body.String(), valid, "detail never contains the token")
		})
	}
}

func TestAuthUnavailable(t *testing.T) {
	s := newTestServer(t, nil)
	s.realm.SetDown(true)
	start := time.Now()
	clock := start
	s.engine = httpapi.NewEngine(httpapi.Deps{
		Auth: auth.NewVerifier(auth.Options{
			Issuer: s.realm.Issuer(), Audience: authtest.Audience, JWKSURL: s.realm.JWKSURL(),
			Now:    func() time.Time { return clock },
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}),
		Ready:   fakeReady{health.Report{Status: "ok"}},
		Logger:  observability.NewLogger(io.Discard, slog.LevelDebug),
		Metrics: observability.NewMetrics(),
	})
	token := s.token(t, nil, "viewer")

	rec := s.do(t, http.MethodGet, "/api/v1/me", nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "no token fails before any key lookup")
	assert.Empty(t, rec.Header().Get("Retry-After"))

	for _, at := range []time.Duration{0, 4 * time.Second} {
		clock = start.Add(at)
		rec = s.do(t, http.MethodGet, "/api/v1/me", bearer(token))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, "at +%s", at)
		assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
		assert.Equal(t, "5", rec.Header().Get("Retry-After"))
		var p problem
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
		assert.Equal(t, problem{
			Type: "/problems/auth-unavailable", Title: "Service Unavailable", Status: http.StatusServiceUnavailable,
			Detail: "signing keys are not available yet; try again",
		}, p)
		assert.NotContains(t, rec.Body.String(), token, "detail never contains the token")
	}

	rec = s.do(t, http.MethodGet, "/api/v1/me", nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "no token stays 401 while the keys are unavailable")

	s.realm.SetDown(false)
	clock = start.Add(auth.FailureBackoff)
	rec = s.do(t, http.MethodGet, "/api/v1/me", bearer(token))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestBodyLimit(t *testing.T) {
	s := newTestServer(t, nil)
	tests := []struct {
		name    string
		size    int
		chunked bool
		want413 bool
	}{
		{"exactly 1 MB", httpapi.MaxBodyBytes, false, false},
		{"exactly 1 MB chunked", httpapi.MaxBodyBytes, true, false},
		{"1 MB + 1 byte with Content-Length", httpapi.MaxBodyBytes + 1, false, true},
		{"1 MB + 1 byte chunked", httpapi.MaxBodyBytes + 1, true, true},
	}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		for _, path := range []string{"/api/v1/me", "/healthz"} {
			for _, tt := range tests {
				t.Run(method+" "+path+" "+tt.name, func(t *testing.T) {
					req := httptest.NewRequest(method, path, bytes.NewReader(bytes.Repeat([]byte("a"), tt.size)))
					if tt.chunked {
						req.ContentLength = -1
						req.TransferEncoding = []string{"chunked"}
					}
					rec := httptest.NewRecorder()
					s.engine.ServeHTTP(rec, req)
					if !tt.want413 {
						assert.NotEqual(t, http.StatusRequestEntityTooLarge, rec.Code)
						return
					}
					require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
					assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
					var p problem
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
					assert.Equal(t, problem{
						Type: "/problems/too-large", Title: "Request Entity Too Large",
						Status: http.StatusRequestEntityTooLarge, Detail: "request body exceeds 1 MB",
					}, p)
				})
			}
		}
	}
}

func TestHealth(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		ready      health.Report
		wantStatus int
		wantBody   string
	}{
		{"healthz", "/healthz", health.Report{Status: "unavailable", Failing: []string{"database"}}, http.StatusOK, `{"status":"ok"}`},
		{"ready", "/readyz", health.Report{Status: "ok"}, http.StatusOK, `{"status":"ok"}`},
		{"database down", "/readyz", health.Report{Status: "unavailable", Failing: []string{"database"}},
			http.StatusServiceUnavailable, `{"status":"unavailable","failing":["database"]}`},
		{"migrations behind", "/readyz", health.Report{Status: "unavailable", Failing: []string{"migrations"}},
			http.StatusServiceUnavailable, `{"status":"unavailable","failing":["migrations"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, func(d *httpapi.Deps) { d.Ready = fakeReady{tt.ready} })
			rec := s.do(t, http.MethodGet, tt.path, nil)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			assert.Empty(t, rec.Header().Get("Link"))
			assert.JSONEq(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestUnservedRoutes(t *testing.T) {
	s := newTestServer(t, nil)
	for _, path := range []string{"/metrics", "/openapi.yaml", "/openapi.json", "/docs", "/schemas/Me.json"} {
		t.Run(path, func(t *testing.T) {
			assert.Equal(t, http.StatusNotFound, s.do(t, http.MethodGet, path, nil).Code)
		})
	}
}

func TestCORS(t *testing.T) {
	preflight := http.Header{
		"Origin":                         {"http://localhost:5173"},
		"Access-Control-Request-Method":  {"GET"},
		"Access-Control-Request-Headers": {"authorization,traceparent"},
	}

	t.Run("preflight answered before authentication", func(t *testing.T) {
		s := newTestServer(t, func(d *httpapi.Deps) { d.CORSOrigins = []string{"http://localhost:5173"} })
		rec := s.do(t, http.MethodOptions, "/api/v1/me", preflight)
		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, "http://localhost:5173", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "PATCH")
		assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Traceparent")
	})

	t.Run("allowed origin on a request", func(t *testing.T) {
		s := newTestServer(t, func(d *httpapi.Deps) { d.CORSOrigins = []string{"http://localhost:5173"} })
		rec := s.do(t, http.MethodGet, "/api/v1/me", http.Header{"Origin": {"http://localhost:5173"}})
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, "http://localhost:5173", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("other origin gets no CORS headers", func(t *testing.T) {
		s := newTestServer(t, func(d *httpapi.Deps) { d.CORSOrigins = []string{"http://localhost:5173"} })
		rec := s.do(t, http.MethodGet, "/healthz", http.Header{"Origin": {"http://evil.example"}})
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("empty CORS_ORIGINS sends no CORS headers", func(t *testing.T) {
		s := newTestServer(t, nil)
		rec := s.do(t, http.MethodOptions, "/api/v1/me", preflight)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		assert.NotEqual(t, http.StatusNoContent, rec.Code)
	})
}

func TestRequestLogNeverContainsTheToken(t *testing.T) {
	s := newTestServer(t, nil)
	token := s.token(t, nil, "viewer")
	rec := s.do(t, http.MethodGet, "/api/v1/me?secret=query-value", bearer(token))
	require.Equal(t, http.StatusOK, rec.Code)

	line := s.logs.String()
	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
	assert.Equal(t, "request", entry["msg"])
	assert.Equal(t, "GET", entry["method"])
	assert.Equal(t, "/api/v1/me", entry["route"])
	assert.InDelta(t, 200, entry["status"], 0)
	assert.Contains(t, entry, "duration_ms")
	assert.Contains(t, entry, "trace_id")
	assert.Empty(t, entry["trace_id"], "tracing is off")
	assert.Equal(t, "8d0c6d4e-user", entry["actor"])

	assert.NotContains(t, line, token)
	assert.NotContains(t, line, "Authorization")
	assert.NotContains(t, line, "Bearer")
	assert.NotContains(t, line, "query-value")
	assert.NotContains(t, line, "editor@example.test")
}

func TestRequestLogUnmatchedAndUnauthenticated(t *testing.T) {
	s := newTestServer(t, nil)
	s.do(t, http.MethodGet, "/no/such/path", nil)
	var entry map[string]any
	require.NoError(t, json.Unmarshal(s.logs.Bytes(), &entry))
	assert.Equal(t, "unmatched", entry["route"])
	assert.Empty(t, entry["actor"])
}
