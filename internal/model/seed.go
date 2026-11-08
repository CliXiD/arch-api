package model

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CliXiD/arch-api/internal/db/dbq"
)

// SampleEnvironment is where `seed --sample` writes.
const SampleEnvironment = "prod"

var sampleTeams = []dbq.InsertTeamParams{
	{ID: "platform", Name: "Platform"},
	{ID: "core", Name: "Core"},
}

var sampleNodes = []dbq.InsertNodeParams{
	{ID: "payments-api", Type: "service", Name: "Payments API", Description: "Sample: payments service", Owner: "platform"},
	{ID: "eks-prod", Type: "k8s-cluster", Name: "EKS prod", Description: "Sample: production Kubernetes cluster", Owner: "platform"},
	{ID: "orders-db", Type: "database", Name: "Orders DB", Description: "Sample: orders database", Owner: "platform"},
	{ID: "core-banking", Type: "on-prem-system", Name: "Core banking", Description: "Sample: on-prem core banking system", Owner: "core"},
	{ID: "vpc-prod", Type: "vpc", Name: "VPC prod", Description: "Sample: production VPC", Owner: "platform"},
	{ID: "landing-zone", Type: "landing-zone", Name: "Landing zone", Description: "Sample: AWS landing zone", Owner: "platform"},
	{ID: "direct-connect", Type: "direct-connect", Name: "Direct Connect", Description: "Sample: Direct Connect to on-prem", Owner: "platform"},
}

var sampleEdges = []dbq.InsertEdgeParams{
	{Type: "runs_on", FromID: "payments-api", ToID: "eks-prod", Attributes: []byte(`{}`)},
	{Type: "stores_data_in", FromID: "payments-api", ToID: "orders-db", Attributes: []byte(`{}`)},
	{Type: "calls", FromID: "payments-api", ToID: "core-banking", Attributes: []byte(`{"protocol":"https","port":443}`)},
	{Type: "routes_via", FromID: "payments-api", ToID: "vpc-prod", Attributes: []byte(`{}`)},
	{Type: "routes_via", FromID: "vpc-prod", ToID: "landing-zone", Attributes: []byte(`{}`)},
	{Type: "routes_via", FromID: "landing-zone", ToID: "direct-connect", Attributes: []byte(`{}`)},
}

// SampleCounts are the node and edge counts SeedSample writes.
var SampleCounts = struct{ Nodes, Edges int }{len(sampleNodes), len(sampleEdges)}

// SeedSample loads the sample graph into prod in one transaction. It returns ErrNotEmpty, and
// writes nothing, when any node exists.
func SeedSample(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbq.New(tx)

	exists, err := q.AnyNodeExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return ErrNotEmpty
	}
	for _, t := range sampleTeams {
		if err := q.InsertTeam(ctx, t); err != nil {
			return err
		}
	}
	for _, n := range sampleNodes {
		n.Environment, n.Source = SampleEnvironment, "import"
		if err := q.InsertNode(ctx, n); err != nil {
			return err
		}
	}
	for _, e := range sampleEdges {
		e.Environment, e.Source = SampleEnvironment, "import"
		if err := q.InsertEdge(ctx, e); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
