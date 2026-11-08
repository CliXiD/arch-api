// Command arch-api is the Architecture E2E API server: `serve`, `migrate up`, `migrate status` and
// `seed --sample`.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/CliXiD/arch-api/internal/config"
	"github.com/CliXiD/arch-api/internal/db"
	"github.com/CliXiD/arch-api/internal/httpapi"
	"github.com/CliXiD/arch-api/internal/model"
	"github.com/CliXiD/arch-api/internal/observability"
)

const usage = `usage:
  arch-api serve            start the API (HTTP_ADDR) and metrics (METRICS_ADDR) servers
  arch-api migrate up       apply pending migrations (MIGRATE_DATABASE_URL, else DATABASE_URL)
  arch-api migrate status   print applied and pending migrations
  arch-api seed --sample    load the sample graph into prod (DATABASE_URL); refuses once any node exists
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch {
	case len(args) == 1 && args[0] == "serve":
		cfg, err := config.LoadServe(os.Getenv)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		if err := httpapi.Serve(ctx, cfg, observability.NewLogger(stdout, cfg.LogLevel)); err != nil {
			_, _ = fmt.Fprintln(stderr, "serve:", err)
			return 1
		}
		return 0
	case len(args) == 2 && args[0] == "migrate" && (args[1] == "up" || args[1] == "status"):
		cfg, err := config.LoadMigrate(os.Getenv)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		if err := db.Migrate(ctx, cfg.DatabaseURL, args[1], stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, "migrate:", err)
			return 1
		}
		return 0
	case len(args) == 2 && args[0] == "seed" && args[1] == "--sample":
		cfg, err := config.LoadSeed(os.Getenv)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return seedSample(ctx, cfg, stdout, stderr)
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
}

func seedSample(ctx context.Context, cfg config.Seed, stdout, stderr io.Writer) int {
	pool, err := db.NewPool(cfg.DatabaseURL, false)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "seed:", err)
		return 1
	}
	defer pool.Close()
	switch err := model.SeedSample(ctx, pool); {
	case errors.Is(err, model.ErrNotEmpty):
		_, _ = fmt.Fprintln(stderr, "seed: the database already has nodes; nothing was written")
		return 1
	case err != nil:
		_, _ = fmt.Fprintln(stderr, "seed:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "seeded sample graph: %d nodes, %d edges in %s\n",
		model.SampleCounts.Nodes, model.SampleCounts.Edges, model.SampleEnvironment)
	return 0
}
