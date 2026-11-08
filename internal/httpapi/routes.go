package httpapi

import (
	"context"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CliXiD/arch-api/internal/auth"
	"github.com/CliXiD/arch-api/internal/health"
)

// Readiness reports whether the service can take traffic.
type Readiness interface {
	Check(ctx context.Context) health.Report
}

// Status is the body of /healthz.
type Status struct {
	Status string `json:"status" enum:"ok" example:"ok"`
}

// ReadinessReport is the body of /readyz.
type ReadinessReport struct {
	Status  string   `json:"status" enum:"ok,unavailable" example:"ok"`
	Failing []string `json:"failing,omitempty" nullable:"false" enum:"database,migrations" doc:"Failing checks, only when unavailable."`
}

// Me is the caller's identity and effective permissions.
type Me struct {
	Subject   string   `json:"subject" doc:"Token sub; for a service account, its client id."`
	Name      string   `json:"name" doc:"Display name; for a service account, its client id."`
	Email     string   `json:"email" doc:"Email, empty when the token has none."`
	Roles     []string `json:"roles" nullable:"false" enum:"viewer,editor,admin" doc:"Effective roles, ordered viewer, editor, admin."`
	Teams     []string `json:"teams" nullable:"false" doc:"Teams from the token's teams claim; empty when absent."`
	ActorKind string   `json:"actor_kind" enum:"user,service"`
}

type statusOutput struct {
	Body Status
}

type readinessOutput struct {
	Status int
	Body   ReadinessReport
}

type meOutput struct {
	Body Me
}

func registerRoutes(api huma.API, d Deps) {
	register(api, huma.Operation{
		OperationID: "get-healthz",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Liveness",
		Tags:        []string{"health"},
	}, Public, func(context.Context, *struct{}) (*statusOutput, error) {
		return &statusOutput{Body: Status{Status: "ok"}}, nil
	})

	readinessSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[ReadinessReport](), true, "")
	register(api, huma.Operation{
		OperationID: "get-readyz",
		Method:      http.MethodGet,
		Path:        "/readyz",
		Summary:     "Readiness: database reachable and migrations current",
		Tags:        []string{"health"},
		Responses: map[string]*huma.Response{
			"503": {
				Description: "Service Unavailable",
				Content:     map[string]*huma.MediaType{"application/json": {Schema: readinessSchema}},
			},
		},
	}, Public, func(ctx context.Context, _ *struct{}) (*readinessOutput, error) {
		r := d.Ready.Check(ctx)
		return &readinessOutput{Status: r.HTTPStatus(), Body: ReadinessReport{Status: r.Status, Failing: r.Failing}}, nil
	})

	register(api, huma.Operation{
		OperationID: "get-me",
		Method:      http.MethodGet,
		Path:        "/api/v1/me",
		Summary:     "The caller's identity, roles and teams",
		Tags:        []string{"identity"},
	}, Viewer, func(ctx context.Context, _ *struct{}) (*meOutput, error) {
		p, _ := auth.PrincipalFrom(ctx)
		return &meOutput{Body: meFrom(p)}, nil
	})
}

func meFrom(p auth.Principal) Me {
	roles := make([]string, 0, len(p.Roles))
	for _, r := range p.Roles {
		roles = append(roles, string(r))
	}
	teams := p.Teams
	if teams == nil {
		teams = []string{}
	}
	return Me{
		Subject:   p.Subject,
		Name:      p.Name,
		Email:     p.Email,
		Roles:     roles,
		Teams:     teams,
		ActorKind: p.ActorKind,
	}
}
