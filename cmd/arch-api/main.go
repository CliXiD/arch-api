// Command arch-api is the Architecture E2E API server: `serve`, `migrate up`, `migrate status`,
// `seed --sample` and `teams add <id> --name <name>`.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"

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
  arch-api teams add <id> --name <name>   create a team if it is missing (MIGRATE_DATABASE_URL, else DATABASE_URL)
`

// The change_log actor of `teams add`.
const (
	teamsAddSubject  = "system"
	teamsAddUsername = "arch-api teams add"
	teamsAddKind     = "service"
)

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
	case len(args) >= 2 && args[0] == "teams" && args[1] == "add":
		id, name, ok := parseTeamsAdd(args[2:])
		if !ok {
			_, _ = fmt.Fprint(stderr, usage)
			return 2
		}
		return teamsAdd(ctx, id, name, stdout, stderr)
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

// parseTeamsAdd parses the arguments after `teams add`: exactly one positional id and one
// `--name <name>` or `--name=<name>`, in either order. ok is false for any other shape.
func parseTeamsAdd(args []string) (id, name string, ok bool) {
	var haveID, haveName bool
	for len(args) > 0 {
		a := args[0]
		args = args[1:]
		switch {
		case a == "--name":
			if haveName || len(args) == 0 {
				return "", "", false
			}
			name, haveName, args = args[0], true, args[1:]
		case strings.HasPrefix(a, "--name="):
			if haveName {
				return "", "", false
			}
			name, haveName = strings.TrimPrefix(a, "--name="), true
		case strings.HasPrefix(a, "-"), haveID:
			return "", "", false
		default:
			id, haveID = a, true
		}
	}
	return id, name, haveID && haveName
}

// teamsAdd creates the team as the schema owner, or reports that it already exists. It checks the
// input before reading any config, and never prints a database error's text: it can hold the
// connection's host, user or password.
func teamsAdd(ctx context.Context, id, name string, stdout, stderr io.Writer) int {
	invalid := false
	if !model.IsSlug(id) {
		_, _ = fmt.Fprintln(stderr, "teams add: id must be a slug matching "+model.SlugPattern)
		invalid = true
	}
	if strings.TrimSpace(name) == "" {
		_, _ = fmt.Fprintln(stderr, "teams add: --name must not be empty")
		invalid = true
	}
	if invalid {
		return 1
	}
	cfg, err := config.LoadMigrate(os.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	pool, err := db.NewPool(cfg.DatabaseURL, false)
	if err != nil {
		return teamsAddDBError(stderr, err)
	}
	defer pool.Close()
	actor := model.Actor{Subject: teamsAddSubject, Username: teamsAddUsername, Kind: teamsAddKind}
	switch _, err := model.NewService(pool).CreateTeam(ctx, actor, model.TeamCreate{ID: id, Name: name}); {
	case errors.Is(err, model.ErrConflict):
		_, _ = fmt.Fprintf(stdout, "team %s already exists\n", id)
		return 0
	case err != nil:
		return teamsAddDBError(stderr, err)
	}
	_, _ = fmt.Fprintf(stdout, "team %s created\n", id)
	return 0
}

// teamsAddDBError prints the fixed database error message, with the SQLSTATE when there is one.
func teamsAddDBError(stderr io.Writer, err error) int {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		_, _ = fmt.Fprintf(stderr, "teams add: database error (SQLSTATE %s)\n", pg.Code)
	} else {
		_, _ = fmt.Fprintln(stderr, "teams add: database error")
	}
	return 1
}
