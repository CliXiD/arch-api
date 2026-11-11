package model

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/CliXiD/arch-api/internal/db/dbq"
)

func nodeTypeKey(id string) string { return "node/" + id }

func edgeTypeKey(id string) string { return "edge/" + id }

// checkTypeSchema validates an attributes_schema against the subset, then checks its property
// names for secrets.
func checkTypeSchema(schema map[string]any) error {
	if errs := validateSchema(schema); len(errs) > 0 {
		return errs
	}
	return checkSchemaSecrets(schema)
}

// losesProperty reports whether next lacks a property key that cur declares.
func losesProperty(cur, next map[string]any) bool {
	curProps, _ := cur["properties"].(map[string]any)
	nextProps, _ := next["properties"].(map[string]any)
	for name := range curProps {
		if _, ok := nextProps[name]; !ok {
			return true
		}
	}
	return false
}

// CreateNodeType creates a custom node type.
func (s *Service) CreateNodeType(ctx context.Context, a Actor, in NodeTypeCreate) (NodeType, error) {
	if err := checkTypeSchema(in.AttributesSchema); err != nil {
		return NodeType{}, err
	}
	out := NodeType{ID: in.ID, Kind: in.Kind, Name: in.Name, AttributesSchema: in.AttributesSchema}
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		schemaJSON, err := json.Marshal(in.AttributesSchema)
		if err != nil {
			return err
		}
		err = q.CreateNodeType(ctx, dbq.CreateNodeTypeParams{
			ID: in.ID, Kind: in.Kind, Name: in.Name, AttributesSchema: schemaJSON,
		})
		if isSQLState(err, sqlstateUniqueViolation) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		return record(ctx, q, a, entityType, nodeTypeKey(out.ID), actionCreate, nil, out)
	})
	if err != nil {
		return NodeType{}, err
	}
	return out, nil
}

// UpdateNodeType applies p to the node type when every existing node of the type still passes the
// new schema. A built-in type can't lose a property. A patch that changes nothing writes nothing
// and returns the stored type.
func (s *Service) UpdateNodeType(ctx context.Context, a Actor, id string, p NodeTypePatch) (NodeType, error) {
	var out NodeType
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockNodeType(ctx, q, id)
		if err != nil {
			return err
		}
		next := cur
		if p.Name != nil {
			next.Name = *p.Name
		}
		schemaChanged := false
		if p.AttributesSchema != nil {
			if err := checkTypeSchema(p.AttributesSchema); err != nil {
				return err
			}
			if cur.Builtin && losesProperty(cur.AttributesSchema, p.AttributesSchema) {
				return ErrBuiltin
			}
			next.AttributesSchema = p.AttributesSchema
			same, err := sameJSON(cur.AttributesSchema, next.AttributesSchema)
			if err != nil {
				return err
			}
			schemaChanged = !same
		}
		if schemaChanged {
			rows, err := q.ListNodeAttributesOfType(ctx, id)
			if err != nil {
				return err
			}
			failing := 0
			for _, raw := range rows {
				attrs, err := object(raw)
				if err != nil {
					return err
				}
				if len(validateAttributes(next.AttributesSchema, attrs)) > 0 {
					failing++
				}
			}
			if failing > 0 {
				return &IncompatibleError{Count: failing}
			}
		}

		out = cur
		if same, err := sameJSON(cur, next); err != nil || same {
			return err
		}
		schemaJSON, err := json.Marshal(next.AttributesSchema)
		if err != nil {
			return err
		}
		if err := q.UpdateNodeType(ctx, dbq.UpdateNodeTypeParams{ID: id, Name: next.Name, AttributesSchema: schemaJSON}); err != nil {
			return err
		}
		out = next
		return record(ctx, q, a, entityType, nodeTypeKey(id), actionUpdate, cur, next)
	})
	if err != nil {
		return NodeType{}, err
	}
	return out, nil
}

// DeleteNodeType deletes a custom node type that no node uses; otherwise ErrBuiltin or an
// InUseError.
func (s *Service) DeleteNodeType(ctx context.Context, a Actor, id string) error {
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockNodeType(ctx, q, id)
		if err != nil {
			return err
		}
		if cur.Builtin {
			return ErrBuiltin
		}
		n, err := q.CountNodesOfType(ctx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return &InUseError{Count: int(n)}
		}
		if err := q.DeleteNodeType(ctx, id); err != nil {
			return err
		}
		return record(ctx, q, a, entityType, nodeTypeKey(id), actionDelete, cur, nil)
	})
	if isSQLState(err, sqlstateForeignKeyViolation) {
		n, err := s.q.CountNodesOfType(ctx, id)
		if err != nil {
			return err
		}
		return &InUseError{Count: int(n)}
	}
	return err
}

// CreateEdgeType creates a custom edge type.
func (s *Service) CreateEdgeType(ctx context.Context, a Actor, in EdgeTypeCreate) (EdgeType, error) {
	if err := checkTypeSchema(in.AttributesSchema); err != nil {
		return EdgeType{}, err
	}
	out := EdgeType{ID: in.ID, Name: in.Name, AttributesSchema: in.AttributesSchema, Allowed: in.Allowed}
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		schemaJSON, err := json.Marshal(in.AttributesSchema)
		if err != nil {
			return err
		}
		allowedJSON, err := json.Marshal(in.Allowed)
		if err != nil {
			return err
		}
		err = q.CreateEdgeType(ctx, dbq.CreateEdgeTypeParams{
			ID: in.ID, Name: in.Name, AttributesSchema: schemaJSON, Allowed: allowedJSON,
		})
		if isSQLState(err, sqlstateUniqueViolation) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		return record(ctx, q, a, entityType, edgeTypeKey(out.ID), actionCreate, nil, out)
	})
	if err != nil {
		return EdgeType{}, err
	}
	return out, nil
}

// UpdateEdgeType applies p to the edge type when every existing edge of the type still passes the
// new schema and uses one of the new allowed pairs. A built-in type can't lose a property. A patch
// that changes nothing writes nothing and returns the stored type.
func (s *Service) UpdateEdgeType(ctx context.Context, a Actor, id string, p EdgeTypePatch) (EdgeType, error) {
	var out EdgeType
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockEdgeType(ctx, q, id)
		if err != nil {
			return err
		}
		next := cur
		if p.Name != nil {
			next.Name = *p.Name
		}
		schemaChanged, allowedChanged := false, false
		if p.AttributesSchema != nil {
			if err := checkTypeSchema(p.AttributesSchema); err != nil {
				return err
			}
			if cur.Builtin && losesProperty(cur.AttributesSchema, p.AttributesSchema) {
				return ErrBuiltin
			}
			next.AttributesSchema = p.AttributesSchema
			same, err := sameJSON(cur.AttributesSchema, next.AttributesSchema)
			if err != nil {
				return err
			}
			schemaChanged = !same
		}
		if p.Allowed != nil {
			next.Allowed = *p.Allowed
			same, err := sameJSON(cur.Allowed, next.Allowed)
			if err != nil {
				return err
			}
			allowedChanged = !same
		}
		if schemaChanged || allowedChanged {
			rows, err := q.ListEdgesOfType(ctx, id)
			if err != nil {
				return err
			}
			failing := 0
			for _, r := range rows {
				if allowedChanged && !slices.Contains(next.Allowed, KindPair{From: r.FromKind, To: r.ToKind}) {
					failing++
					continue
				}
				if !schemaChanged {
					continue
				}
				attrs, err := object(r.Attributes)
				if err != nil {
					return err
				}
				if len(validateAttributes(next.AttributesSchema, attrs)) > 0 {
					failing++
				}
			}
			if failing > 0 {
				return &IncompatibleError{Count: failing}
			}
		}

		out = cur
		if same, err := sameJSON(cur, next); err != nil || same {
			return err
		}
		schemaJSON, err := json.Marshal(next.AttributesSchema)
		if err != nil {
			return err
		}
		allowedJSON, err := json.Marshal(next.Allowed)
		if err != nil {
			return err
		}
		if err := q.UpdateEdgeType(ctx, dbq.UpdateEdgeTypeParams{
			ID: id, Name: next.Name, AttributesSchema: schemaJSON, Allowed: allowedJSON,
		}); err != nil {
			return err
		}
		out = next
		return record(ctx, q, a, entityType, edgeTypeKey(id), actionUpdate, cur, next)
	})
	if err != nil {
		return EdgeType{}, err
	}
	return out, nil
}

// DeleteEdgeType deletes a custom edge type that no edge uses; otherwise ErrBuiltin or an
// InUseError.
func (s *Service) DeleteEdgeType(ctx context.Context, a Actor, id string) error {
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockEdgeType(ctx, q, id)
		if err != nil {
			return err
		}
		if cur.Builtin {
			return ErrBuiltin
		}
		n, err := q.CountEdgesOfType(ctx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return &InUseError{Count: int(n)}
		}
		if err := q.DeleteEdgeType(ctx, id); err != nil {
			return err
		}
		return record(ctx, q, a, entityType, edgeTypeKey(id), actionDelete, cur, nil)
	})
	if isSQLState(err, sqlstateForeignKeyViolation) {
		n, err := s.q.CountEdgesOfType(ctx, id)
		if err != nil {
			return err
		}
		return &InUseError{Count: int(n)}
	}
	return err
}

func lockNodeType(ctx context.Context, q *dbq.Queries, id string) (NodeType, error) {
	r, err := q.LockNodeType(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeType{}, ErrNotFound
	}
	if err != nil {
		return NodeType{}, err
	}
	schema, err := object(r.AttributesSchema)
	if err != nil {
		return NodeType{}, err
	}
	return NodeType{ID: r.ID, Kind: r.Kind, Name: r.Name, AttributesSchema: schema, Builtin: r.Builtin}, nil
}

func lockEdgeType(ctx context.Context, q *dbq.Queries, id string) (EdgeType, error) {
	r, err := q.LockEdgeType(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return EdgeType{}, ErrNotFound
	}
	if err != nil {
		return EdgeType{}, err
	}
	schema, err := object(r.AttributesSchema)
	if err != nil {
		return EdgeType{}, err
	}
	allowed := []KindPair{}
	if err := json.Unmarshal(r.Allowed, &allowed); err != nil {
		return EdgeType{}, errInvalidJSON
	}
	return EdgeType{ID: r.ID, Name: r.Name, AttributesSchema: schema, Allowed: allowed, Builtin: r.Builtin}, nil
}
