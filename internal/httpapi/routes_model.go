package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CliXiD/arch-api/internal/model"
)

// Model reads the typed graph. internal/model.Service implements it.
type Model interface {
	Types(ctx context.Context) (model.Types, error)
	Teams(ctx context.Context) (model.TeamList, error)
	Environments(ctx context.Context) (model.EnvironmentList, error)
	Nodes(ctx context.Context, f model.NodeFilter) (model.NodePage, error)
	Node(ctx context.Context, environment, id string) (model.Node, error)
	Edges(ctx context.Context, f model.EdgeFilter) (model.EdgePage, error)
	Graph(ctx context.Context, environment string) (model.Graph, error)
	History(ctx context.Context, environment, id string, limit int, cursor string) (model.HistoryPage, error)
}

type typesOutput struct {
	Body model.Types
}

type teamsOutput struct {
	Body model.TeamList
}

type environmentsOutput struct {
	Body model.EnvironmentList
}

type listNodesInput struct {
	Environment string `query:"environment" doc:"Only nodes of this environment; unknown gives 422."`
	Kind        string `query:"kind" enum:"application,infrastructure,network,external,team" doc:"Only nodes of this kind."`
	Type        string `query:"type" doc:"Only nodes of this node type."`
	Owner       string `query:"owner" doc:"Only nodes owned by this team."`
	Q           string `query:"q" maxLength:"200" doc:"Case-insensitive substring of the id or name."`
	Limit       int    `query:"limit" default:"100" minimum:"1" maximum:"500" doc:"Page size."`
	Cursor      string `query:"cursor" doc:"Opaque cursor from a previous page's next_cursor."`
}

type nodePageOutput struct {
	Body model.NodePage
}

type getNodeInput struct {
	Environment string `path:"environment"`
	ID          string `path:"id"`
}

type nodeOutput struct {
	ETag string `header:"ETag" doc:"The node's version, quoted."`
	Body model.Node
}

type listEdgesInput struct {
	Environment string `query:"environment" doc:"Only edges of this environment; unknown gives 422."`
	Type        string `query:"type" doc:"Only edges of this edge type."`
	From        string `query:"from" doc:"Only edges from this node id."`
	To          string `query:"to" doc:"Only edges to this node id."`
	Limit       int    `query:"limit" default:"100" minimum:"1" maximum:"500" doc:"Page size."`
	Cursor      string `query:"cursor" doc:"Opaque cursor from a previous page's next_cursor."`
}

type edgePageOutput struct {
	Body model.EdgePage
}

type graphInput struct {
	Environment string `query:"environment" required:"true" doc:"The environment; unknown gives 422."`
}

type graphOutput struct {
	Body model.Graph
}

type historyInput struct {
	Entity      string `query:"entity" required:"true" enum:"node" doc:"The entity whose history is read; only node in this version."`
	Environment string `query:"environment" required:"true" doc:"The node's environment."`
	ID          string `query:"id" required:"true" doc:"The node's id."`
	Limit       int    `query:"limit" default:"50" minimum:"1" maximum:"500" doc:"Page size."`
	Cursor      string `query:"cursor" doc:"Opaque cursor from a previous page's next_cursor."`
}

type historyOutput struct {
	Body model.HistoryPage
}

func registerModelRoutes(api huma.API, d Deps) {
	fail := modelFailure(d.Logger)
	tags := []string{"model"}

	register(api, huma.Operation{
		OperationID: "list-types",
		Method:      http.MethodGet,
		Path:        "/api/v1/types",
		Summary:     "Node and edge types",
		Tags:        tags,
	}, Viewer, func(ctx context.Context, _ *struct{}) (*typesOutput, error) {
		out, err := d.Model.Types(ctx)
		if err != nil {
			return nil, fail(ctx, "list-types", err)
		}
		return &typesOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "list-teams",
		Method:      http.MethodGet,
		Path:        "/api/v1/teams",
		Summary:     "Teams",
		Tags:        tags,
	}, Viewer, func(ctx context.Context, _ *struct{}) (*teamsOutput, error) {
		out, err := d.Model.Teams(ctx)
		if err != nil {
			return nil, fail(ctx, "list-teams", err)
		}
		return &teamsOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "list-environments",
		Method:      http.MethodGet,
		Path:        "/api/v1/environments",
		Summary:     "Environments",
		Tags:        tags,
	}, Viewer, func(ctx context.Context, _ *struct{}) (*environmentsOutput, error) {
		out, err := d.Model.Environments(ctx)
		if err != nil {
			return nil, fail(ctx, "list-environments", err)
		}
		return &environmentsOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "list-nodes",
		Method:      http.MethodGet,
		Path:        "/api/v1/nodes",
		Summary:     "A page of nodes",
		Tags:        tags,
		Errors:      []int{http.StatusUnprocessableEntity},
	}, Viewer, func(ctx context.Context, in *listNodesInput) (*nodePageOutput, error) {
		out, err := d.Model.Nodes(ctx, model.NodeFilter{
			Environment: in.Environment, Kind: in.Kind, Type: in.Type, Owner: in.Owner, Q: in.Q,
			Limit: in.Limit, Cursor: in.Cursor,
		})
		if err != nil {
			return nil, fail(ctx, "list-nodes", err)
		}
		return &nodePageOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "get-node",
		Method:      http.MethodGet,
		Path:        "/api/v1/nodes/{environment}/{id}",
		Summary:     "One node",
		Tags:        tags,
		Errors:      []int{http.StatusNotFound},
	}, Viewer, func(ctx context.Context, in *getNodeInput) (*nodeOutput, error) {
		out, err := d.Model.Node(ctx, in.Environment, in.ID)
		if err != nil {
			return nil, fail(ctx, "get-node", err)
		}
		return &nodeOutput{ETag: strconv.Quote(strconv.Itoa(out.Version)), Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "list-edges",
		Method:      http.MethodGet,
		Path:        "/api/v1/edges",
		Summary:     "A page of edges",
		Tags:        tags,
		Errors:      []int{http.StatusUnprocessableEntity},
	}, Viewer, func(ctx context.Context, in *listEdgesInput) (*edgePageOutput, error) {
		out, err := d.Model.Edges(ctx, model.EdgeFilter{
			Environment: in.Environment, Type: in.Type, From: in.From, To: in.To, Limit: in.Limit, Cursor: in.Cursor,
		})
		if err != nil {
			return nil, fail(ctx, "list-edges", err)
		}
		return &edgePageOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "get-graph",
		Method:      http.MethodGet,
		Path:        "/api/v1/graph",
		Summary:     "One environment's compact graph (gzip when accepted)",
		Tags:        tags,
		Errors:      []int{http.StatusUnprocessableEntity},
	}, Viewer, func(ctx context.Context, in *graphInput) (*graphOutput, error) {
		out, err := d.Model.Graph(ctx, in.Environment)
		if err != nil {
			return nil, fail(ctx, "get-graph", err)
		}
		return &graphOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "get-history",
		Method:      http.MethodGet,
		Path:        "/api/v1/history",
		Summary:     "A node's change_log rows and its edges' rows, newest first",
		Tags:        tags,
		Errors:      []int{http.StatusUnprocessableEntity},
	}, Viewer, func(ctx context.Context, in *historyInput) (*historyOutput, error) {
		out, err := d.Model.History(ctx, in.Environment, in.ID, in.Limit, in.Cursor)
		if err != nil {
			return nil, fail(ctx, "get-history", err)
		}
		return &historyOutput{Body: out}, nil
	})
}

// modelFailure maps a model read error to a problem; a 404 names a node.
func modelFailure(logger *slog.Logger) func(ctx context.Context, op string, err error) error {
	fail := entityFailure(logger)
	return func(ctx context.Context, op string, err error) error { return fail(ctx, op, "node", err) }
}

// conflictDetail is the 409 detail per entity.
var conflictDetail = map[string]string{
	"node":        "a node with this environment and id already exists",
	"edge":        "an edge of this type between these nodes already exists",
	"team":        "a team with this id already exists",
	"environment": "an environment with this id or sort already exists",
	"node type":   "a node type with this id already exists",
	"edge type":   "an edge type with this id already exists",
}

// usedBy names what still uses an entity, or would fail its change: edges for an edge type, nodes
// for everything else.
func usedBy(entity string) string {
	if entity == "edge type" {
		return "edges"
	}
	return "nodes"
}

// builtinDetail is the 409 detail of a write a built-in type refuses.
func builtinDetail(op string) string {
	if strings.HasPrefix(op, "delete-") {
		return "built-in types can't be deleted"
	}
	return "built-in types can't lose attributes"
}

// entityFailure maps a model error to a problem: 404 (naming entity), 403, 409, 412, 422, or a
// 500 with fixed text. A 500 is logged with the operation and the error class only: never query
// arguments, attribute values or row data.
func entityFailure(logger *slog.Logger) func(ctx context.Context, op, entity string, err error) error {
	return func(ctx context.Context, op, entity string, err error) error {
		var (
			v      *model.ValidationError
			vs     model.ValidationErrors
			forbid *model.ForbiddenError
			inUse  *model.InUseError
			incomp *model.IncompatibleError
			secret *model.SecretError
		)
		switch {
		case errors.Is(err, model.ErrNotFound):
			return huma.Error404NotFound(entity + " not found")
		case errors.Is(err, model.ErrConflict):
			return problemOf(http.StatusConflict, ProblemConflict, conflictDetail[entity])
		case errors.Is(err, model.ErrStale):
			return problemOf(http.StatusPreconditionFailed, ProblemStale, "If-Match is not the current version")
		case errors.As(err, &forbid):
			return problemOf(http.StatusForbidden, ProblemForbidden, forbid.Error())
		case errors.Is(err, model.ErrBuiltin):
			return problemOf(http.StatusConflict, ProblemConflict, builtinDetail(op))
		case errors.As(err, &inUse):
			return &InUseProblem{
				ErrorModel: huma.ErrorModel{
					Type: ProblemInUse, Title: http.StatusText(http.StatusConflict), Status: http.StatusConflict,
					Detail: entity + " is still used by " + usedBy(entity),
				},
				Count: inUse.Count,
			}
		case errors.As(err, &incomp):
			return &InUseProblem{
				ErrorModel: huma.ErrorModel{
					Type: ProblemConflict, Title: http.StatusText(http.StatusConflict), Status: http.StatusConflict,
					Detail: "existing " + usedBy(entity) + " would fail the change",
				},
				Count: incomp.Count,
			}
		case errors.As(err, &secret):
			details := make([]error, 0, len(secret.Locations))
			for _, loc := range secret.Locations {
				details = append(details, &huma.ErrorDetail{Location: loc, Message: "looks like a secret"})
			}
			return problemOf(http.StatusUnprocessableEntity, ProblemSecretDetected, "attributes look like secrets", details...)
		case errors.As(err, &vs):
			details := make([]error, 0, len(vs))
			for _, e := range vs {
				details = append(details, &huma.ErrorDetail{Location: e.Location, Message: e.Message})
			}
			return huma.Error422UnprocessableEntity("validation failed", details...)
		case errors.As(err, &v):
			return huma.Error422UnprocessableEntity("validation failed", &huma.ErrorDetail{Location: v.Location, Message: v.Message})
		}
		if logger != nil {
			logger.ErrorContext(ctx, "model operation failed", "operation", op, "error_class", errorClass(err))
		}
		return huma.Error500InternalServerError("internal error")
	}
}

// errorClass names an error without its message: the SQLSTATE of a PostgreSQL error, else the
// Go type. Messages can hold hostnames or row values ([NFR-001]).
func errorClass(err error) string {
	var pg *pgconn.PgError
	switch {
	case errors.As(err, &pg):
		return "sqlstate " + pg.Code
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline exceeded"
	default:
		return fmt.Sprintf("%T", err)
	}
}
