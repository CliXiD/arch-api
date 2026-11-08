// Package model reads the typed graph: vocabulary, teams, environments, nodes, edges and the
// compact per-environment graph. It owns paging (keyset cursors, limit+1 fetches) and the checks
// that a filtered environment exists.
package model

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CliXiD/arch-api/internal/db/dbq"
)

// Service reads the model from PostgreSQL.
type Service struct {
	pool *pgxpool.Pool
	q    *dbq.Queries
}

// NewService returns a Service over pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: dbq.New(pool)}
}

// Types returns every node and edge type, each ordered by id.
func (s *Service) Types(ctx context.Context) (Types, error) {
	nts, err := s.q.ListNodeTypes(ctx)
	if err != nil {
		return Types{}, err
	}
	ets, err := s.q.ListEdgeTypes(ctx)
	if err != nil {
		return Types{}, err
	}
	out := Types{NodeTypes: make([]NodeType, 0, len(nts)), EdgeTypes: make([]EdgeType, 0, len(ets))}
	for _, t := range nts {
		schema, err := object(t.AttributesSchema)
		if err != nil {
			return Types{}, err
		}
		out.NodeTypes = append(out.NodeTypes, NodeType{
			ID: t.ID, Kind: t.Kind, Name: t.Name, AttributesSchema: schema, Builtin: t.Builtin,
		})
	}
	for _, t := range ets {
		schema, err := object(t.AttributesSchema)
		if err != nil {
			return Types{}, err
		}
		allowed := []KindPair{}
		if err := json.Unmarshal(t.Allowed, &allowed); err != nil {
			return Types{}, errInvalidJSON
		}
		out.EdgeTypes = append(out.EdgeTypes, EdgeType{
			ID: t.ID, Name: t.Name, AttributesSchema: schema, Allowed: allowed, Builtin: t.Builtin,
		})
	}
	return out, nil
}

// Teams returns every team, ordered by id.
func (s *Service) Teams(ctx context.Context) (TeamList, error) {
	rows, err := s.q.ListTeams(ctx)
	if err != nil {
		return TeamList{}, err
	}
	out := TeamList{Items: make([]Team, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, Team{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt.UTC()})
	}
	return out, nil
}

// Environments returns every environment, ordered by sort.
func (s *Service) Environments(ctx context.Context) (EnvironmentList, error) {
	rows, err := s.q.ListEnvironments(ctx)
	if err != nil {
		return EnvironmentList{}, err
	}
	out := EnvironmentList{Items: make([]Environment, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, Environment{ID: r.ID, Name: r.Name, Sort: int(r.Sort)})
	}
	return out, nil
}

// Nodes returns one page of nodes ordered by (environment, id).
func (s *Service) Nodes(ctx context.Context, f NodeFilter) (NodePage, error) {
	if err := checkEnvironment(ctx, s.q, f.Environment); err != nil {
		return NodePage{}, err
	}
	p := dbq.ListNodesParams{
		Environment: text(f.Environment),
		Kind:        text(f.Kind),
		Type:        text(f.Type),
		Owner:       text(f.Owner),
		RowLimit:    int32(f.Limit + 1), //nolint:gosec // the API bounds limit to 1..500
	}
	if f.Q != "" {
		p.Pattern = text("%" + escapeLike(f.Q) + "%")
	}
	if f.Cursor != "" {
		key, err := decodeCursor(f.Cursor, 2)
		if err != nil {
			return NodePage{}, err
		}
		p.After, p.AfterEnvironment, p.AfterID = true, key[0], key[1]
	}
	rows, err := s.q.ListNodes(ctx, p)
	if err != nil {
		return NodePage{}, err
	}
	out := NodePage{Items: make([]Node, 0, min(len(rows), f.Limit))}
	for i, r := range rows {
		if i == f.Limit {
			last := out.Items[len(out.Items)-1]
			next := encodeCursor(last.Environment, last.ID)
			out.NextCursor = &next
			break
		}
		n, err := nodeFrom(dbq.GetNodeRow(r))
		if err != nil {
			return NodePage{}, err
		}
		out.Items = append(out.Items, n)
	}
	return out, nil
}

// Node returns one node, or ErrNotFound.
func (s *Service) Node(ctx context.Context, environment, id string) (Node, error) {
	r, err := s.q.GetNode(ctx, dbq.GetNodeParams{Environment: environment, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, err
	}
	return nodeFrom(r)
}

// Edges returns one page of edges ordered by (environment, type, from, to).
func (s *Service) Edges(ctx context.Context, f EdgeFilter) (EdgePage, error) {
	if err := checkEnvironment(ctx, s.q, f.Environment); err != nil {
		return EdgePage{}, err
	}
	p := dbq.ListEdgesParams{
		Environment: text(f.Environment),
		Type:        text(f.Type),
		FromID:      text(f.From),
		ToID:        text(f.To),
		RowLimit:    int32(f.Limit + 1), //nolint:gosec // the API bounds limit to 1..500
	}
	if f.Cursor != "" {
		key, err := decodeCursor(f.Cursor, 4)
		if err != nil {
			return EdgePage{}, err
		}
		p.After, p.AfterEnvironment, p.AfterType, p.AfterFromID, p.AfterToID = true, key[0], key[1], key[2], key[3]
	}
	rows, err := s.q.ListEdges(ctx, p)
	if err != nil {
		return EdgePage{}, err
	}
	out := EdgePage{Items: make([]Edge, 0, min(len(rows), f.Limit))}
	for i, r := range rows {
		if i == f.Limit {
			last := out.Items[len(out.Items)-1]
			next := encodeCursor(last.Environment, last.Type, last.From, last.To)
			out.NextCursor = &next
			break
		}
		attrs, err := object(r.Attributes)
		if err != nil {
			return EdgePage{}, err
		}
		out.Items = append(out.Items, Edge{
			UID: r.Uid.String(), Environment: r.Environment, Type: r.Type, From: r.FromID, To: r.ToID,
			Attributes: attrs, Source: r.Source, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Version: int(r.Version),
		})
	}
	return out, nil
}

// Graph returns the environment's nodes and edges from one consistent snapshot.
func (s *Service) Graph(ctx context.Context, environment string) (Graph, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Graph{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	if err := checkEnvironment(ctx, q, environment); err != nil {
		return Graph{}, err
	}
	nodes, err := q.GraphNodes(ctx, environment)
	if err != nil {
		return Graph{}, err
	}
	edges, err := q.GraphEdges(ctx, environment)
	if err != nil {
		return Graph{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Graph{}, err
	}

	out := Graph{Environment: environment, Nodes: make([]GraphNode, 0, len(nodes)), Edges: make([]GraphEdge, 0, len(edges))}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, GraphNode{ID: n.ID, Kind: n.Kind, Type: n.Type, Name: n.Name, Owner: n.Owner, Tags: tags(n.Tags)})
	}
	for _, e := range edges {
		attrs, err := object(e.Attributes)
		if err != nil {
			return Graph{}, err
		}
		out.Edges = append(out.Edges, GraphEdge{UID: e.Uid.String(), Type: e.Type, From: e.FromID, To: e.ToID, Attributes: attrs})
	}
	return out, nil
}

// checkEnvironment fails with a validation error when environment is set and does not exist.
func checkEnvironment(ctx context.Context, q *dbq.Queries, environment string) error {
	if environment == "" {
		return nil
	}
	ok, err := q.EnvironmentExists(ctx, environment)
	if err != nil {
		return err
	}
	if !ok {
		return &ValidationError{Location: "query.environment", Message: "unknown environment"}
	}
	return nil
}

func nodeFrom(r dbq.GetNodeRow) (Node, error) {
	attrs, err := object(r.Attributes)
	if err != nil {
		return Node{}, err
	}
	return Node{
		Environment: r.Environment, ID: r.ID, Kind: r.Kind, Type: r.Type, Name: r.Name,
		Description: r.Description, Owner: r.Owner, Attributes: attrs, Tags: tags(r.Tags), Source: r.Source,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Version: int(r.Version),
	}, nil
}

var errInvalidJSON = errors.New("model: stored JSON is malformed")

// object decodes a jsonb object column. The error never carries the column's content.
func object(b []byte) (map[string]any, error) {
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, errInvalidJSON
	}
	return m, nil
}

func tags(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}

func text(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// escapeLike escapes the ILIKE wildcards and the escape character itself.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
