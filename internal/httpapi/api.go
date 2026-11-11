// Package httpapi builds the gin engine and the Huma API: middleware, routes and handlers.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"

	"github.com/CliXiD/arch-api/internal/auth"
	"github.com/CliXiD/arch-api/internal/observability"
)

// Access is an operation's minimum role. Every operation declares one (default deny).
type Access string

// The accepted minimum roles.
const (
	Public Access = "public"
	Viewer Access = "viewer"
	Editor Access = "editor"
	Admin  Access = "admin"
)

// MetadataMinRole is the huma.Operation.Metadata key holding the operation's Access.
const MetadataMinRole = "minRole"

// BearerScheme is the OpenAPI security scheme of every /api/v1 operation.
const BearerScheme = "bearer"

// PublicPaths are the only paths that may be Public.
var PublicPaths = map[string]bool{"/healthz": true, "/readyz": true}

// Problem types.
const (
	ProblemUnauthenticated = "/problems/unauthenticated"
	ProblemForbidden       = "/problems/forbidden"
	ProblemNotFound        = "/problems/not-found"
	ProblemValidation      = "/problems/validation"
	ProblemAuthUnavailable = "/problems/auth-unavailable"
	ProblemTooLarge        = "/problems/too-large"

	ProblemConflict             = "/problems/conflict"
	ProblemInUse                = "/problems/in-use"
	ProblemStale                = "/problems/stale"
	ProblemPreconditionRequired = "/problems/precondition-required"
	ProblemSecretDetected       = "/problems/secret-detected"
)

// Authenticator validates the `Authorization` header value.
type Authenticator interface {
	Authenticate(ctx context.Context, authorization string) (auth.Principal, error)
}

// NewAPI creates the Huma API on r and registers every route. It serves no OpenAPI, docs or
// schema routes and adds no `$schema` fields or `Link` headers.
func NewAPI(r *gin.Engine, d Deps) huma.API {
	huma.NewError = newProblem
	cfg := huma.DefaultConfig("Architecture E2E API", "0.1.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	cfg.CreateHooks = nil
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		BearerScheme: {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
	}
	api := humagin.New(r, cfg)
	api.UseMiddleware(accessMiddleware(d.Auth))
	registerRoutes(api, d)
	return api
}

// register is the only way operations are added. It records the minimum role in the operation's
// metadata, and gives /api/v1 operations the bearer security and the 401, 403, 413 and 503 problem
// responses.
func register[I, O any](api huma.API, op huma.Operation, minRole Access, handler func(context.Context, *I) (*O, error)) {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[MetadataMinRole] = minRole
	if strings.HasPrefix(op.Path, "/api/v1/") {
		op.Security = []map[string][]string{{BearerScheme: {}}}
		op.Errors = append(op.Errors, http.StatusUnauthorized, http.StatusForbidden,
			http.StatusRequestEntityTooLarge, http.StatusServiceUnavailable)
	}
	huma.Register(api, op, handler)
}

// accessMiddleware enforces each operation's minimum role: 401 without a valid token, 503 (with
// Retry-After) while the signing keys have never been loaded, 403 when the role is too low or the
// operation declares none.
func accessMiddleware(authn Authenticator) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		minRole, _ := op.Metadata[MetadataMinRole].(Access)
		switch minRole {
		case Public:
			if PublicPaths[op.Path] {
				next(ctx)
				return
			}
			writeProblem(ctx, http.StatusForbidden, ProblemForbidden, "operation is not allowed to be public")
			return
		case Viewer, Editor, Admin:
		default:
			writeProblem(ctx, http.StatusForbidden, ProblemForbidden, "operation declares no minimum role")
			return
		}

		p, err := authn.Authenticate(ctx.Context(), ctx.Header("Authorization"))
		if errors.Is(err, auth.ErrUnavailable) {
			ctx.SetHeader("Retry-After", strconv.Itoa(int(auth.FailureBackoff.Seconds())))
			writeProblem(ctx, http.StatusServiceUnavailable, ProblemAuthUnavailable, "signing keys are not available yet; try again")
			return
		}
		if err != nil {
			writeProblem(ctx, http.StatusUnauthorized, ProblemUnauthenticated, "a valid bearer token is required")
			return
		}
		humagin.Unwrap(ctx).Set(observability.ActorKey, p.Subject)
		if !p.Has(auth.Role(minRole)) {
			writeProblem(ctx, http.StatusForbidden, ProblemForbidden, "requires role "+string(minRole))
			return
		}
		next(huma.WithContext(ctx, auth.WithPrincipal(ctx.Context(), p)))
	}
}

// newProblem is huma.NewError: Huma's error model, with the problem type of 404 and 422 set and
// every received value cleared from errors[], so no input (`q`, ids, cursors) is echoed.
func newProblem(status int, msg string, errs ...error) huma.StatusError {
	details := make([]*huma.ErrorDetail, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		var d *huma.ErrorDetail
		if converted, ok := err.(huma.ErrorDetailer); ok {
			d = converted.ErrorDetail()
		} else {
			d = &huma.ErrorDetail{Message: err.Error()}
		}
		d.Value = nil
		details = append(details, d)
	}
	p := &huma.ErrorModel{Status: status, Title: http.StatusText(status), Detail: msg, Errors: details}
	switch status {
	case http.StatusNotFound:
		p.Type = ProblemNotFound
	case http.StatusUnprocessableEntity:
		p.Type = ProblemValidation
	}
	return p
}

// problemOf is newProblem with the given problem type, for the statuses newProblem leaves untyped
// or types differently (403, 409, 412, 428, the 422 of a secret).
func problemOf(status int, problemType, detail string, errs ...error) huma.StatusError {
	p := newProblem(status, detail, errs...).(*huma.ErrorModel)
	p.Type = problemType
	return p
}

// InUseProblem is a 409 problem with a count: `/problems/in-use` with the number of nodes or edges
// that still use the team, environment or type, or `/problems/conflict` with the number of nodes or
// edges a type change would make fail.
type InUseProblem struct {
	huma.ErrorModel
	Count int `json:"count" doc:"Number of nodes or edges that still use the entity, or that the change would make fail."`
}

// writeProblem writes an RFC 9457 problem. detail is fixed text: never a token, claim or header.
func writeProblem(ctx huma.Context, status int, problemType, detail string) {
	writeHTTPProblem(humagin.Unwrap(ctx).Writer, status, problemType, detail)
}

// writeHTTPProblem writes an RFC 9457 problem to w; every problem the API writes itself goes
// through it. detail is fixed text: never a token, claim or header.
func writeHTTPProblem(w http.ResponseWriter, status int, problemType, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(huma.ErrorModel{
		Type:   problemType,
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}
