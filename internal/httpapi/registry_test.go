package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/httpapi"
)

// violations walks every operation in the OpenAPI document and reports each one without a
// declared minimum role, or public outside /healthz and /readyz, or an /api/v1 operation without
// the bearer security and 401/403 responses.
func violations(oapi *huma.OpenAPI) []string {
	var out []string
	for path, item := range oapi.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Options, item.Head, item.Patch, item.Trace} {
			if op == nil {
				continue
			}
			name := fmt.Sprintf("%s %s (%s)", op.Method, path, op.OperationID)
			minRole, ok := op.Metadata[httpapi.MetadataMinRole].(httpapi.Access)
			switch {
			case !ok:
				out = append(out, name+": no minimum role declared")
			case minRole == httpapi.Public && !httpapi.PublicPaths[path]:
				out = append(out, name+": public outside /healthz and /readyz")
			case minRole != httpapi.Public && minRole != httpapi.Viewer && minRole != httpapi.Editor && minRole != httpapi.Admin:
				out = append(out, name+": unknown minimum role "+string(minRole))
			}
			if minRole != httpapi.Public {
				if len(op.Security) != 1 || op.Security[0][httpapi.BearerScheme] == nil {
					out = append(out, name+": no bearer security")
				}
				if op.Responses["401"] == nil || op.Responses["403"] == nil {
					out = append(out, name+": 401/403 not documented")
				}
			}
		}
	}
	return out
}

func TestEveryOperationDeclaresAMinimumRole(t *testing.T) {
	api := httpapi.NewAPI(gin.New(), httpapi.Deps{})
	require.NotEmpty(t, api.OpenAPI().Paths)
	assert.Empty(t, violations(api.OpenAPI()), "every operation is registered through register() with a minimum role")
}

func TestRegistryCheckCatchesUndeclaredOperations(t *testing.T) {
	api := httpapi.NewAPI(gin.New(), httpapi.Deps{})
	huma.Register(api, huma.Operation{OperationID: "get-undeclared", Method: http.MethodGet, Path: "/api/v1/undeclared"},
		func(context.Context, *struct{}) (*struct{}, error) { return &struct{}{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "get-open", Method: http.MethodGet, Path: "/api/v1/open",
		Metadata: map[string]any{httpapi.MetadataMinRole: httpapi.Public},
	}, func(context.Context, *struct{}) (*struct{}, error) { return &struct{}{}, nil })

	got := violations(api.OpenAPI())
	assert.Contains(t, got, "GET /api/v1/undeclared (get-undeclared): no minimum role declared")
	assert.Contains(t, got, "GET /api/v1/open (get-open): public outside /healthz and /readyz")
}

func TestUndeclaredOperationIsForbiddenAtRuntime(t *testing.T) {
	engine := gin.New()
	api := httpapi.NewAPI(engine, httpapi.Deps{})
	for _, op := range []huma.Operation{
		{OperationID: "get-undeclared", Method: http.MethodGet, Path: "/api/v1/undeclared"},
		{OperationID: "get-open", Method: http.MethodGet, Path: "/api/v1/open", Metadata: map[string]any{httpapi.MetadataMinRole: httpapi.Public}},
	} {
		huma.Register(api, op, func(context.Context, *struct{}) (*struct{}, error) { return &struct{}{}, nil })
	}
	for _, path := range []string{"/api/v1/undeclared", "/api/v1/open"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
		})
	}
}
