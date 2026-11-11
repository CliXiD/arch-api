package httpapi

import (
	"context"
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CliXiD/arch-api/internal/auth"
	"github.com/CliXiD/arch-api/internal/model"
)

// Writer changes the model and records each change in the change_log, in one transaction per
// call. internal/model.Service implements it. version is the If-Match version; 0 never matches.
type Writer interface {
	CreateNode(ctx context.Context, a model.Actor, in model.NodeCreate) (model.Node, error)
	UpdateNode(ctx context.Context, a model.Actor, environment, id string, version int, p model.NodePatch) (model.Node, error)
	DeleteNode(ctx context.Context, a model.Actor, environment, id string, version int) error
	CreateEdge(ctx context.Context, a model.Actor, in model.EdgeCreate) (model.Edge, error)
	UpdateEdge(ctx context.Context, a model.Actor, uid string, version int, p model.EdgePatch) (model.Edge, error)
	DeleteEdge(ctx context.Context, a model.Actor, uid string, version int) error
	CreateTeam(ctx context.Context, a model.Actor, in model.TeamCreate) (model.Team, error)
	UpdateTeam(ctx context.Context, a model.Actor, id string, p model.TeamPatch) (model.Team, error)
	DeleteTeam(ctx context.Context, a model.Actor, id string) error
	CreateEnvironment(ctx context.Context, a model.Actor, in model.EnvironmentCreate) (model.Environment, error)
	UpdateEnvironment(ctx context.Context, a model.Actor, id string, p model.EnvironmentPatch) (model.Environment, error)
	DeleteEnvironment(ctx context.Context, a model.Actor, id string) error
	CreateNodeType(ctx context.Context, a model.Actor, in model.NodeTypeCreate) (model.NodeType, error)
	UpdateNodeType(ctx context.Context, a model.Actor, id string, p model.NodeTypePatch) (model.NodeType, error)
	DeleteNodeType(ctx context.Context, a model.Actor, id string) error
	CreateEdgeType(ctx context.Context, a model.Actor, in model.EdgeTypeCreate) (model.EdgeType, error)
	UpdateEdgeType(ctx context.Context, a model.Actor, id string, p model.EdgeTypePatch) (model.EdgeType, error)
	DeleteEdgeType(ctx context.Context, a model.Actor, id string) error
}

type createNodeInput struct {
	Body model.NodeCreate
}

type updateNodeInput struct {
	Environment string `path:"environment"`
	ID          string `path:"id"`
	IfMatch     string `header:"If-Match" doc:"The current version, quoted, as in the ETag. Required: 428 without it, 412 when it is not the current version."`
	Body        model.NodePatch
}

type deleteNodeInput struct {
	Environment string `path:"environment"`
	ID          string `path:"id"`
	IfMatch     string `header:"If-Match" doc:"The current version, quoted, as in the ETag. Required: 428 without it, 412 when it is not the current version."`
}

type createEdgeInput struct {
	Body model.EdgeCreate
}

type updateEdgeInput struct {
	UID     string `path:"uid" format:"uuid"`
	IfMatch string `header:"If-Match" doc:"The current version, quoted, as in the ETag. Required: 428 without it, 412 when it is not the current version."`
	Body    model.EdgePatch
}

type deleteEdgeInput struct {
	UID     string `path:"uid" format:"uuid"`
	IfMatch string `header:"If-Match" doc:"The current version, quoted, as in the ETag. Required: 428 without it, 412 when it is not the current version."`
}

type edgeOutput struct {
	ETag string `header:"ETag" doc:"The edge's version, quoted."`
	Body model.Edge
}

type createTeamInput struct {
	Body model.TeamCreate
}

type updateTeamInput struct {
	ID   string `path:"id"`
	Body model.TeamPatch
}

type idInput struct {
	ID string `path:"id"`
}

type teamOutput struct {
	Body model.Team
}

type createEnvironmentInput struct {
	Body model.EnvironmentCreate
}

type updateEnvironmentInput struct {
	ID   string `path:"id"`
	Body model.EnvironmentPatch
}

type environmentOutput struct {
	Body model.Environment
}

func registerWriteRoutes(api huma.API, d Deps) {
	fail := entityFailure(d.Logger)
	tags := []string{"model"}
	inUseSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[InUseProblem](), true, "")
	// inUse is a fresh map per operation: Huma adds the other responses to it.
	inUse := func() map[string]*huma.Response {
		return map[string]*huma.Response{
			"409": {
				Description: "Conflict: still used by nodes (`/problems/in-use`, with `count`)",
				Content:     map[string]*huma.MediaType{"application/problem+json": {Schema: inUseSchema}},
			},
		}
	}

	register(api, huma.Operation{
		OperationID:   "create-node",
		Method:        http.MethodPost,
		Path:          "/api/v1/nodes",
		Summary:       "Create a node (editor of the owning team, or admin)",
		Tags:          tags,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, Editor, func(ctx context.Context, in *createNodeInput) (*nodeOutput, error) {
		out, err := d.Writer.CreateNode(ctx, actorFrom(ctx), in.Body)
		if err != nil {
			return nil, fail(ctx, "create-node", "node", err)
		}
		return &nodeOutput{ETag: etag(out.Version), Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "update-node",
		Method:      http.MethodPatch,
		Path:        "/api/v1/nodes/{environment}/{id}",
		Summary:     "Update a node: each field present replaces the stored value",
		Tags:        tags,
		Errors: []int{http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity,
			http.StatusPreconditionRequired},
	}, Editor, func(ctx context.Context, in *updateNodeInput) (*nodeOutput, error) {
		if in.IfMatch == "" {
			return nil, preconditionRequired()
		}
		out, err := d.Writer.UpdateNode(ctx, actorFrom(ctx), in.Environment, in.ID, ifMatchVersion(in.IfMatch), in.Body)
		if err != nil {
			return nil, fail(ctx, "update-node", "node", err)
		}
		return &nodeOutput{ETag: etag(out.Version), Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID:   "delete-node",
		Method:        http.MethodDelete,
		Path:          "/api/v1/nodes/{environment}/{id}",
		Summary:       "Delete a node and every edge from or to it",
		Tags:          tags,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusNotFound, http.StatusPreconditionFailed, http.StatusPreconditionRequired},
	}, Editor, func(ctx context.Context, in *deleteNodeInput) (*struct{}, error) {
		if in.IfMatch == "" {
			return nil, preconditionRequired()
		}
		if err := d.Writer.DeleteNode(ctx, actorFrom(ctx), in.Environment, in.ID, ifMatchVersion(in.IfMatch)); err != nil {
			return nil, fail(ctx, "delete-node", "node", err)
		}
		return nil, nil
	})

	register(api, huma.Operation{
		OperationID:   "create-edge",
		Method:        http.MethodPost,
		Path:          "/api/v1/edges",
		Summary:       "Create an edge (editor of the source node's team, or admin)",
		Tags:          tags,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, Editor, func(ctx context.Context, in *createEdgeInput) (*edgeOutput, error) {
		out, err := d.Writer.CreateEdge(ctx, actorFrom(ctx), in.Body)
		if err != nil {
			return nil, fail(ctx, "create-edge", "edge", err)
		}
		return &edgeOutput{ETag: etag(out.Version), Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "update-edge",
		Method:      http.MethodPatch,
		Path:        "/api/v1/edges/{uid}",
		Summary:     "Replace an edge's attributes",
		Tags:        tags,
		Errors: []int{http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity,
			http.StatusPreconditionRequired},
	}, Editor, func(ctx context.Context, in *updateEdgeInput) (*edgeOutput, error) {
		if in.IfMatch == "" {
			return nil, preconditionRequired()
		}
		out, err := d.Writer.UpdateEdge(ctx, actorFrom(ctx), in.UID, ifMatchVersion(in.IfMatch), in.Body)
		if err != nil {
			return nil, fail(ctx, "update-edge", "edge", err)
		}
		return &edgeOutput{ETag: etag(out.Version), Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID:   "delete-edge",
		Method:        http.MethodDelete,
		Path:          "/api/v1/edges/{uid}",
		Summary:       "Delete an edge",
		Tags:          tags,
		DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity,
			http.StatusPreconditionRequired},
	}, Editor, func(ctx context.Context, in *deleteEdgeInput) (*struct{}, error) {
		if in.IfMatch == "" {
			return nil, preconditionRequired()
		}
		if err := d.Writer.DeleteEdge(ctx, actorFrom(ctx), in.UID, ifMatchVersion(in.IfMatch)); err != nil {
			return nil, fail(ctx, "delete-edge", "edge", err)
		}
		return nil, nil
	})

	register(api, huma.Operation{
		OperationID:   "create-team",
		Method:        http.MethodPost,
		Path:          "/api/v1/teams",
		Summary:       "Create a team",
		Tags:          tags,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, Admin, func(ctx context.Context, in *createTeamInput) (*teamOutput, error) {
		out, err := d.Writer.CreateTeam(ctx, actorFrom(ctx), in.Body)
		if err != nil {
			return nil, fail(ctx, "create-team", "team", err)
		}
		return &teamOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "update-team",
		Method:      http.MethodPatch,
		Path:        "/api/v1/teams/{id}",
		Summary:     "Rename a team",
		Tags:        tags,
		Errors:      []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, Admin, func(ctx context.Context, in *updateTeamInput) (*teamOutput, error) {
		out, err := d.Writer.UpdateTeam(ctx, actorFrom(ctx), in.ID, in.Body)
		if err != nil {
			return nil, fail(ctx, "update-team", "team", err)
		}
		return &teamOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID:   "delete-team",
		Method:        http.MethodDelete,
		Path:          "/api/v1/teams/{id}",
		Summary:       "Delete a team that owns no node",
		Tags:          tags,
		DefaultStatus: http.StatusNoContent,
		Responses:     inUse(),
		Errors:        []int{http.StatusNotFound},
	}, Admin, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := d.Writer.DeleteTeam(ctx, actorFrom(ctx), in.ID); err != nil {
			return nil, fail(ctx, "delete-team", "team", err)
		}
		return nil, nil
	})

	register(api, huma.Operation{
		OperationID:   "create-environment",
		Method:        http.MethodPost,
		Path:          "/api/v1/environments",
		Summary:       "Create an environment",
		Tags:          tags,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, Admin, func(ctx context.Context, in *createEnvironmentInput) (*environmentOutput, error) {
		out, err := d.Writer.CreateEnvironment(ctx, actorFrom(ctx), in.Body)
		if err != nil {
			return nil, fail(ctx, "create-environment", "environment", err)
		}
		return &environmentOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "update-environment",
		Method:      http.MethodPatch,
		Path:        "/api/v1/environments/{id}",
		Summary:     "Rename or re-sort an environment",
		Tags:        tags,
		Errors:      []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, Admin, func(ctx context.Context, in *updateEnvironmentInput) (*environmentOutput, error) {
		out, err := d.Writer.UpdateEnvironment(ctx, actorFrom(ctx), in.ID, in.Body)
		if err != nil {
			return nil, fail(ctx, "update-environment", "environment", err)
		}
		return &environmentOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID:   "delete-environment",
		Method:        http.MethodDelete,
		Path:          "/api/v1/environments/{id}",
		Summary:       "Delete an environment that holds no node",
		Tags:          tags,
		DefaultStatus: http.StatusNoContent,
		Responses:     inUse(),
		Errors:        []int{http.StatusNotFound},
	}, Admin, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := d.Writer.DeleteEnvironment(ctx, actorFrom(ctx), in.ID); err != nil {
			return nil, fail(ctx, "delete-environment", "environment", err)
		}
		return nil, nil
	})
}

// actorFrom builds the model's Actor from the request's principal.
func actorFrom(ctx context.Context) model.Actor {
	p, _ := auth.PrincipalFrom(ctx)
	return model.Actor{
		Subject: p.Subject, Username: p.Username, Kind: p.ActorKind, Admin: p.Has(auth.RoleAdmin), Teams: p.Teams,
	}
}

func etag(version int) string {
	return strconv.Quote(strconv.Itoa(version))
}

// ifMatchVersion returns the version of `If-Match: "<n>"`, or 0, which never matches, for `*`, a
// weak or a malformed value: locking is never skipped.
func ifMatchVersion(h string) int {
	if len(h) < 3 || h[0] != '"' || h[len(h)-1] != '"' {
		return 0
	}
	digits := h[1 : len(h)-1]
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return n
}

func preconditionRequired() error {
	return problemOf(http.StatusPreconditionRequired, ProblemPreconditionRequired, "If-Match is required")
}
