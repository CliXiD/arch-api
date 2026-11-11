package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CliXiD/arch-api/internal/model"
)

type createNodeTypeInput struct {
	Body model.NodeTypeCreate
}

type updateNodeTypeInput struct {
	ID   string `path:"id"`
	Body model.NodeTypePatch
}

type nodeTypeOutput struct {
	Body model.NodeType
}

type createEdgeTypeInput struct {
	Body model.EdgeTypeCreate
}

type updateEdgeTypeInput struct {
	ID   string `path:"id"`
	Body model.EdgeTypePatch
}

type edgeTypeOutput struct {
	Body model.EdgeType
}

// registerTypeRoutes registers the admin writes of the vocabulary: node and edge types.
func registerTypeRoutes(api huma.API, d Deps) {
	fail := entityFailure(d.Logger)
	tags := []string{"model"}
	countSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[InUseProblem](), true, "")
	// conflict is a fresh map per operation: Huma adds the other responses to it.
	conflict := func(description string) map[string]*huma.Response {
		return map[string]*huma.Response{
			"409": {
				Description: description,
				Content:     map[string]*huma.MediaType{"application/problem+json": {Schema: countSchema}},
			},
		}
	}
	const (
		patchConflict = "Conflict: a built-in type would lose an attribute (`/problems/conflict`), or existing " +
			"%s would fail the change (`/problems/conflict`, with `count`)"
		deleteConflict = "Conflict: a built-in type (`/problems/conflict`), or still used by %s " +
			"(`/problems/in-use`, with `count`)"
	)

	register(api, huma.Operation{
		OperationID:   "create-node-type",
		Method:        http.MethodPost,
		Path:          "/api/v1/admin/types/node-types",
		Summary:       "Create a node type",
		Tags:          tags,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, Admin, func(ctx context.Context, in *createNodeTypeInput) (*nodeTypeOutput, error) {
		out, err := d.Writer.CreateNodeType(ctx, actorFrom(ctx), in.Body)
		if err != nil {
			return nil, fail(ctx, "create-node-type", "node type", err)
		}
		return &nodeTypeOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "update-node-type",
		Method:      http.MethodPatch,
		Path:        "/api/v1/admin/types/node-types/{id}",
		Summary:     "Update a node type: each field present replaces the stored value",
		Tags:        tags,
		Responses:   conflict(fmt.Sprintf(patchConflict, "nodes")),
		Errors:      []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, Admin, func(ctx context.Context, in *updateNodeTypeInput) (*nodeTypeOutput, error) {
		out, err := d.Writer.UpdateNodeType(ctx, actorFrom(ctx), in.ID, in.Body)
		if err != nil {
			return nil, fail(ctx, "update-node-type", "node type", err)
		}
		return &nodeTypeOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID:   "delete-node-type",
		Method:        http.MethodDelete,
		Path:          "/api/v1/admin/types/node-types/{id}",
		Summary:       "Delete a custom node type that no node uses",
		Tags:          tags,
		DefaultStatus: http.StatusNoContent,
		Responses:     conflict(fmt.Sprintf(deleteConflict, "nodes")),
		Errors:        []int{http.StatusNotFound},
	}, Admin, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := d.Writer.DeleteNodeType(ctx, actorFrom(ctx), in.ID); err != nil {
			return nil, fail(ctx, "delete-node-type", "node type", err)
		}
		return nil, nil
	})

	register(api, huma.Operation{
		OperationID:   "create-edge-type",
		Method:        http.MethodPost,
		Path:          "/api/v1/admin/types/edge-types",
		Summary:       "Create an edge type",
		Tags:          tags,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, Admin, func(ctx context.Context, in *createEdgeTypeInput) (*edgeTypeOutput, error) {
		out, err := d.Writer.CreateEdgeType(ctx, actorFrom(ctx), in.Body)
		if err != nil {
			return nil, fail(ctx, "create-edge-type", "edge type", err)
		}
		return &edgeTypeOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID: "update-edge-type",
		Method:      http.MethodPatch,
		Path:        "/api/v1/admin/types/edge-types/{id}",
		Summary:     "Update an edge type: each field present replaces the stored value",
		Tags:        tags,
		Responses:   conflict(fmt.Sprintf(patchConflict, "edges")),
		Errors:      []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, Admin, func(ctx context.Context, in *updateEdgeTypeInput) (*edgeTypeOutput, error) {
		out, err := d.Writer.UpdateEdgeType(ctx, actorFrom(ctx), in.ID, in.Body)
		if err != nil {
			return nil, fail(ctx, "update-edge-type", "edge type", err)
		}
		return &edgeTypeOutput{Body: out}, nil
	})

	register(api, huma.Operation{
		OperationID:   "delete-edge-type",
		Method:        http.MethodDelete,
		Path:          "/api/v1/admin/types/edge-types/{id}",
		Summary:       "Delete a custom edge type that no edge uses",
		Tags:          tags,
		DefaultStatus: http.StatusNoContent,
		Responses:     conflict(fmt.Sprintf(deleteConflict, "edges")),
		Errors:        []int{http.StatusNotFound},
	}, Admin, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := d.Writer.DeleteEdgeType(ctx, actorFrom(ctx), in.ID); err != nil {
			return nil, fail(ctx, "delete-edge-type", "edge type", err)
		}
		return nil, nil
	})
}
