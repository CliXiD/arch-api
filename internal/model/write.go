package model

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CliXiD/arch-api/internal/db/dbq"
)

// The change_log values this package writes.
const (
	sourceUI = "ui"

	entityNode        = "node"
	entityEdge        = "edge"
	entityTeam        = "team"
	entityEnvironment = "environment"

	actionCreate = "create"
	actionUpdate = "update"
	actionDelete = "delete"
)

// PostgreSQL error codes the writes map.
const (
	sqlstateUniqueViolation     = "23505"
	sqlstateForeignKeyViolation = "23503"
)

// can fails with a ForbiddenError unless the actor is an admin or a member of team.
func (a Actor) can(team string) error {
	if a.Admin || slices.Contains(a.Teams, team) {
		return nil
	}
	return &ForbiddenError{Team: team}
}

// inTx runs fn in one transaction and commits when it returns nil; any error rolls back, so no
// change and no change_log row remains.
func (s *Service) inTx(ctx context.Context, fn func(q *dbq.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// record writes the change_log row of one change. before and after are the entity's API values;
// nil is SQL NULL. Every row of one transaction has the same `at`.
func record(ctx context.Context, q *dbq.Queries, a Actor, entity, key, action string, before, after any) error {
	p := dbq.InsertChangeLogParams{
		Actor: a.Subject, ActorName: a.Username, ActorKind: a.Kind, Source: sourceUI,
		Entity: entity, EntityKey: key, Action: action,
	}
	var err error
	if p.Before, err = jsonOrNull(before); err != nil {
		return err
	}
	if p.After, err = jsonOrNull(after); err != nil {
		return err
	}
	return q.InsertChangeLog(ctx, p)
}

func jsonOrNull(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func nodeKey(n Node) string { return n.Environment + "/" + n.ID }

// CreateNode creates a node with source `ui` and version 1.
func (s *Service) CreateNode(ctx context.Context, a Actor, in NodeCreate) (Node, error) {
	if err := a.can(in.Owner); err != nil {
		return Node{}, err
	}
	attrs := attributesOrEmpty(in.Attributes)
	var out Node
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		var errs ValidationErrors
		envOK, err := q.EnvironmentExists(ctx, in.Environment)
		if err != nil {
			return err
		}
		if !envOK {
			errs = append(errs, &ValidationError{Location: "body.environment", Message: "unknown environment"})
		}
		teamOK, err := q.TeamExists(ctx, in.Owner)
		if err != nil {
			return err
		}
		if !teamOK {
			errs = append(errs, &ValidationError{Location: "body.owner", Message: "unknown team"})
		}
		nt, err := q.GetNodeType(ctx, in.Type)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			errs = append(errs, &ValidationError{Location: "body.type", Message: "unknown node type"})
		case err != nil:
			return err
		default:
			schema, err := object(nt.AttributesSchema)
			if err != nil {
				return err
			}
			errs = append(errs, validateAttributes(schema, attrs)...)
		}
		if len(errs) > 0 {
			return sortErrors(errs)
		}
		if err := CheckSecrets(attrs); err != nil {
			return err
		}

		attrJSON, err := json.Marshal(attrs)
		if err != nil {
			return err
		}
		tags := tags(in.Tags)
		row, err := q.CreateNode(ctx, dbq.CreateNodeParams{
			Environment: in.Environment, ID: in.ID, Type: in.Type, Name: in.Name, Description: in.Description,
			Owner: in.Owner, Attributes: attrJSON, Tags: tags,
		})
		if isSQLState(err, sqlstateUniqueViolation) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		out = Node{
			Environment: in.Environment, ID: in.ID, Kind: nt.Kind, Type: in.Type, Name: in.Name,
			Description: in.Description, Owner: in.Owner, Attributes: attrs, Tags: tags, Source: sourceUI,
			CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(), Version: int(row.Version),
		}
		return record(ctx, q, a, entityNode, nodeKey(out), actionCreate, nil, out)
	})
	if err != nil {
		return Node{}, err
	}
	return out, nil
}

// UpdateNode applies p to the node when version is its current version. A patch that changes
// nothing writes nothing and returns the stored node.
func (s *Service) UpdateNode(ctx context.Context, a Actor, environment, id string, version int, p NodePatch) (Node, error) {
	var out Node
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockNode(ctx, q, environment, id)
		if err != nil {
			return err
		}
		if err := a.can(cur.Owner); err != nil {
			return err
		}
		if p.Owner != nil && *p.Owner != cur.Owner {
			if err := a.can(*p.Owner); err != nil {
				return err
			}
		}
		if version != cur.Version {
			return ErrStale
		}

		next := cur
		if p.Name != nil {
			next.Name = *p.Name
		}
		if p.Description != nil {
			next.Description = *p.Description
		}
		if p.Owner != nil {
			next.Owner = *p.Owner
		}
		if p.Tags != nil {
			next.Tags = tags(*p.Tags)
		}
		var errs ValidationErrors
		if p.Owner != nil && *p.Owner != cur.Owner {
			ok, err := q.TeamExists(ctx, *p.Owner)
			if err != nil {
				return err
			}
			if !ok {
				errs = append(errs, &ValidationError{Location: "body.owner", Message: "unknown team"})
			}
		}
		if p.Attributes != nil {
			next.Attributes = p.Attributes
			nt, err := q.GetNodeType(ctx, cur.Type)
			if err != nil {
				return err
			}
			schema, err := object(nt.AttributesSchema)
			if err != nil {
				return err
			}
			errs = append(errs, validateAttributes(schema, next.Attributes)...)
		}
		if len(errs) > 0 {
			return sortErrors(errs)
		}
		if p.Attributes != nil {
			if err := CheckSecrets(next.Attributes); err != nil {
				return err
			}
		}

		if same, err := sameNode(cur, next); err != nil || same {
			out = cur
			return err
		}
		attrJSON, err := json.Marshal(next.Attributes)
		if err != nil {
			return err
		}
		row, err := q.UpdateNode(ctx, dbq.UpdateNodeParams{
			Environment: environment, ID: id, Name: next.Name, Description: next.Description, Owner: next.Owner,
			Attributes: attrJSON, Tags: next.Tags,
		})
		if err != nil {
			return err
		}
		next.Source, next.UpdatedAt, next.Version = sourceUI, row.UpdatedAt.UTC(), int(row.Version)
		out = next
		return record(ctx, q, a, entityNode, nodeKey(cur), actionUpdate, cur, next)
	})
	if err != nil {
		return Node{}, err
	}
	return out, nil
}

// DeleteNode deletes the node and every edge from or to it in its environment, whoever owns the
// other end, when version is the node's current version. Each deleted edge gets its own row.
func (s *Service) DeleteNode(ctx context.Context, a Actor, environment, id string, version int) error {
	return s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockNode(ctx, q, environment, id)
		if err != nil {
			return err
		}
		if err := a.can(cur.Owner); err != nil {
			return err
		}
		if version != cur.Version {
			return ErrStale
		}
		edges, err := q.LockNodeEdges(ctx, dbq.LockNodeEdgesParams{Environment: environment, NodeID: id})
		if err != nil {
			return err
		}
		for _, r := range edges {
			e, err := edgeFrom(r)
			if err != nil {
				return err
			}
			if err := q.DeleteEdge(ctx, r.Uid); err != nil {
				return err
			}
			if err := record(ctx, q, a, entityEdge, e.UID, actionDelete, e, nil); err != nil {
				return err
			}
		}
		if err := q.DeleteNode(ctx, dbq.DeleteNodeParams{Environment: environment, ID: id}); err != nil {
			return err
		}
		return record(ctx, q, a, entityNode, nodeKey(cur), actionDelete, cur, nil)
	})
}

// CreateEdge creates an edge with source `ui` and version 1. The actor needs rights on the source
// node's owner; both nodes must be in the edge's environment and their kinds allowed by its type.
func (s *Service) CreateEdge(ctx context.Context, a Actor, in EdgeCreate) (Edge, error) {
	attrs := attributesOrEmpty(in.Attributes)
	var out Edge
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		from, fromOK, err := nodeOwnerKind(ctx, q, in.Environment, in.From)
		if err != nil {
			return err
		}
		if fromOK {
			if err := a.can(from.Owner); err != nil {
				return err
			}
		}

		var errs ValidationErrors
		envOK, err := q.EnvironmentExists(ctx, in.Environment)
		if err != nil {
			return err
		}
		if !envOK {
			errs = append(errs, &ValidationError{Location: "body.environment", Message: "unknown environment"})
		}
		if !fromOK {
			errs = append(errs, &ValidationError{Location: "body.from", Message: "no such node in the environment"})
		}
		to, toOK, err := nodeOwnerKind(ctx, q, in.Environment, in.To)
		if err != nil {
			return err
		}
		if !toOK {
			errs = append(errs, &ValidationError{Location: "body.to", Message: "no such node in the environment"})
		}
		et, err := q.GetEdgeType(ctx, in.Type)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			errs = append(errs, &ValidationError{Location: "body.type", Message: "unknown edge type"})
		case err != nil:
			return err
		default:
			allowed := []KindPair{}
			if err := json.Unmarshal(et.Allowed, &allowed); err != nil {
				return errInvalidJSON
			}
			if fromOK && toOK {
				if v := checkKindPair(in.Type, allowed, from.Kind, to.Kind); v != nil {
					errs = append(errs, v)
				}
			}
			schema, err := object(et.AttributesSchema)
			if err != nil {
				return err
			}
			errs = append(errs, validateAttributes(schema, attrs)...)
		}
		if len(errs) > 0 {
			return sortErrors(errs)
		}
		if err := CheckSecrets(attrs); err != nil {
			return err
		}

		attrJSON, err := json.Marshal(attrs)
		if err != nil {
			return err
		}
		row, err := q.CreateEdge(ctx, dbq.CreateEdgeParams{
			Environment: in.Environment, Type: in.Type, FromID: in.From, ToID: in.To, Attributes: attrJSON,
		})
		if isSQLState(err, sqlstateUniqueViolation) {
			return ErrConflict
		}
		if v := edgeForeignKeyError(err); v != nil {
			return ValidationErrors{v}
		}
		if err != nil {
			return err
		}
		out = Edge{
			UID: row.Uid.String(), Environment: in.Environment, Type: in.Type, From: in.From, To: in.To,
			Attributes: attrs, Source: sourceUI, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
			Version: int(row.Version),
		}
		return record(ctx, q, a, entityEdge, out.UID, actionCreate, nil, out)
	})
	if err != nil {
		return Edge{}, err
	}
	return out, nil
}

// UpdateEdge replaces the edge's attributes when version is its current version. Attributes equal
// to the stored ones write nothing and return the stored edge.
func (s *Service) UpdateEdge(ctx context.Context, a Actor, uid string, version int, p EdgePatch) (Edge, error) {
	var out Edge
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockEdgeFor(ctx, q, a, uid, version)
		if err != nil {
			return err
		}
		attrs := attributesOrEmpty(p.Attributes)
		et, err := q.GetEdgeType(ctx, cur.Type)
		if err != nil {
			return err
		}
		schema, err := object(et.AttributesSchema)
		if err != nil {
			return err
		}
		if errs := validateAttributes(schema, attrs); len(errs) > 0 {
			return errs
		}
		if err := CheckSecrets(attrs); err != nil {
			return err
		}

		next := cur
		next.Attributes = attrs
		if same, err := sameJSON(cur.Attributes, next.Attributes); err != nil || same {
			out = cur
			return err
		}
		attrJSON, err := json.Marshal(attrs)
		if err != nil {
			return err
		}
		row, err := q.UpdateEdge(ctx, dbq.UpdateEdgeParams{Uid: uuid.MustParse(cur.UID), Attributes: attrJSON})
		if err != nil {
			return err
		}
		next.Source, next.UpdatedAt, next.Version = sourceUI, row.UpdatedAt.UTC(), int(row.Version)
		out = next
		return record(ctx, q, a, entityEdge, cur.UID, actionUpdate, cur, next)
	})
	if err != nil {
		return Edge{}, err
	}
	return out, nil
}

// DeleteEdge deletes the edge when version is its current version.
func (s *Service) DeleteEdge(ctx context.Context, a Actor, uid string, version int) error {
	return s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockEdgeFor(ctx, q, a, uid, version)
		if err != nil {
			return err
		}
		if err := q.DeleteEdge(ctx, uuid.MustParse(cur.UID)); err != nil {
			return err
		}
		return record(ctx, q, a, entityEdge, cur.UID, actionDelete, cur, nil)
	})
}

// lockEdgeFor share-locks the edge's source node and reads its owner, then locks the edge (node
// before edge, the order DeleteNode uses, so the two can't deadlock), then checks the actor's
// rights on that owner, then the version: ErrNotFound, a ForbiddenError, or ErrStale.
func lockEdgeFor(ctx context.Context, q *dbq.Queries, a Actor, uid string, version int) (Edge, error) {
	id, err := uuid.Parse(uid)
	if err != nil {
		return Edge{}, ErrNotFound
	}
	owner, err := q.ShareEdgeSourceOwner(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Edge{}, ErrNotFound
	}
	if err != nil {
		return Edge{}, err
	}
	r, err := q.LockEdge(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Edge{}, ErrNotFound
	}
	if err != nil {
		return Edge{}, err
	}
	cur, err := edgeFrom(r)
	if err != nil {
		return Edge{}, err
	}
	if err := a.can(owner); err != nil {
		return Edge{}, err
	}
	if version != cur.Version {
		return Edge{}, ErrStale
	}
	return cur, nil
}

// CreateTeam creates a team.
func (s *Service) CreateTeam(ctx context.Context, a Actor, in TeamCreate) (Team, error) {
	var out Team
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		r, err := q.CreateTeam(ctx, dbq.CreateTeamParams{ID: in.ID, Name: in.Name})
		if isSQLState(err, sqlstateUniqueViolation) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		out = Team{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt.UTC()}
		return record(ctx, q, a, entityTeam, out.ID, actionCreate, nil, out)
	})
	if err != nil {
		return Team{}, err
	}
	return out, nil
}

// UpdateTeam renames a team. The same name writes nothing.
func (s *Service) UpdateTeam(ctx context.Context, a Actor, id string, p TeamPatch) (Team, error) {
	var out Team
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockTeam(ctx, q, id)
		if err != nil {
			return err
		}
		out = cur
		if p.Name == cur.Name {
			return nil
		}
		if err := q.UpdateTeam(ctx, dbq.UpdateTeamParams{ID: id, Name: p.Name}); err != nil {
			return err
		}
		out.Name = p.Name
		return record(ctx, q, a, entityTeam, id, actionUpdate, cur, out)
	})
	if err != nil {
		return Team{}, err
	}
	return out, nil
}

// DeleteTeam deletes a team that owns no node; otherwise it returns an InUseError.
func (s *Service) DeleteTeam(ctx context.Context, a Actor, id string) error {
	return s.inTx(ctx, func(q *dbq.Queries) error {
		cur, err := lockTeam(ctx, q, id)
		if err != nil {
			return err
		}
		n, err := q.CountTeamNodes(ctx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return &InUseError{Count: int(n)}
		}
		if err := q.DeleteTeam(ctx, id); err != nil {
			return err
		}
		return record(ctx, q, a, entityTeam, id, actionDelete, cur, nil)
	})
}

// CreateEnvironment creates an environment.
func (s *Service) CreateEnvironment(ctx context.Context, a Actor, in EnvironmentCreate) (Environment, error) {
	out := Environment{ID: in.ID, Name: in.Name, Sort: int(in.Sort)}
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		err := q.CreateEnvironment(ctx, dbq.CreateEnvironmentParams{ID: in.ID, Name: in.Name, Sort: in.Sort})
		if isSQLState(err, sqlstateUniqueViolation) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		return record(ctx, q, a, entityEnvironment, out.ID, actionCreate, nil, out)
	})
	if err != nil {
		return Environment{}, err
	}
	return out, nil
}

// UpdateEnvironment renames or re-sorts an environment. Unchanged values write nothing.
func (s *Service) UpdateEnvironment(ctx context.Context, a Actor, id string, p EnvironmentPatch) (Environment, error) {
	var out Environment
	err := s.inTx(ctx, func(q *dbq.Queries) error {
		r, err := q.LockEnvironment(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		cur := Environment{ID: r.ID, Name: r.Name, Sort: int(r.Sort)}
		next, sort := cur, r.Sort
		if p.Name != nil {
			next.Name = *p.Name
		}
		if p.Sort != nil {
			next.Sort, sort = int(*p.Sort), *p.Sort
		}
		out = next
		if next == cur {
			return nil
		}
		err = q.UpdateEnvironment(ctx, dbq.UpdateEnvironmentParams{ID: id, Name: next.Name, Sort: sort})
		if isSQLState(err, sqlstateUniqueViolation) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		return record(ctx, q, a, entityEnvironment, id, actionUpdate, cur, next)
	})
	if err != nil {
		return Environment{}, err
	}
	return out, nil
}

// DeleteEnvironment deletes an environment that holds no node; otherwise it returns an
// InUseError.
func (s *Service) DeleteEnvironment(ctx context.Context, a Actor, id string) error {
	return s.inTx(ctx, func(q *dbq.Queries) error {
		r, err := q.LockEnvironment(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		n, err := q.CountEnvironmentNodes(ctx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return &InUseError{Count: int(n)}
		}
		if err := q.DeleteEnvironment(ctx, id); err != nil {
			return err
		}
		return record(ctx, q, a, entityEnvironment, id, actionDelete, Environment{ID: r.ID, Name: r.Name, Sort: int(r.Sort)}, nil)
	})
}

func lockNode(ctx context.Context, q *dbq.Queries, environment, id string) (Node, error) {
	r, err := q.LockNode(ctx, dbq.LockNodeParams{Environment: environment, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, err
	}
	return nodeFrom(dbq.GetNodeRow(r))
}

func lockTeam(ctx context.Context, q *dbq.Queries, id string) (Team, error) {
	r, err := q.LockTeam(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, ErrNotFound
	}
	if err != nil {
		return Team{}, err
	}
	return Team{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt.UTC()}, nil
}

// nodeOwnerKind returns the owner and kind of a node; ok is false when it does not exist.
func nodeOwnerKind(ctx context.Context, q *dbq.Queries, environment, id string) (dbq.NodeOwnerKindRow, bool, error) {
	r, err := q.NodeOwnerKind(ctx, dbq.NodeOwnerKindParams{Environment: environment, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

func edgeFrom(r dbq.Edge) (Edge, error) {
	attrs, err := object(r.Attributes)
	if err != nil {
		return Edge{}, err
	}
	return Edge{
		UID: r.Uid.String(), Environment: r.Environment, Type: r.Type, From: r.FromID, To: r.ToID,
		Attributes: attrs, Source: r.Source, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Version: int(r.Version),
	}, nil
}

// sameNode reports whether the patchable fields are equal, attributes and tags as JSON values.
func sameNode(a, b Node) (bool, error) {
	if a.Name != b.Name || a.Description != b.Description || a.Owner != b.Owner {
		return false, nil
	}
	if !slices.Equal(a.Tags, b.Tags) {
		return false, nil
	}
	return sameJSON(a.Attributes, b.Attributes)
}

func sameJSON(a, b any) (bool, error) {
	x, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	y, err := json.Marshal(b)
	if err != nil {
		return false, err
	}
	return string(x) == string(y), nil
}

func attributesOrEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func sortErrors(errs ValidationErrors) ValidationErrors {
	slices.SortStableFunc(errs, func(a, b *ValidationError) int { return strings.Compare(a.Location, b.Location) })
	return errs
}

func isSQLState(err error, code string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == code
}

// edgeForeignKeyError maps a foreign key violation of an edge insert (a node deleted meanwhile) to
// the body.from or body.to validation error.
func edgeForeignKeyError(err error) *ValidationError {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != sqlstateForeignKeyViolation {
		return nil
	}
	if strings.Contains(pg.ConstraintName, "to_id") {
		return &ValidationError{Location: "body.to", Message: "no such node in the environment"}
	}
	return &ValidationError{Location: "body.from", Message: "no such node in the environment"}
}
