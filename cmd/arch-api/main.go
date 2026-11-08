// Command arch-api is the Architecture E2E API server: `serve`, `migrate up` and `migrate status`.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/CliXiD/arch-api/internal/config"
	"github.com/CliXiD/arch-api/internal/db"
	"github.com/CliXiD/arch-api/internal/httpapi"
	"github.com/CliXiD/arch-api/internal/observability"
)

const usage = `usage:
  arch-api serve            start the API (HTTP_ADDR) and metrics (METRICS_ADDR) servers
  arch-api migrate up       apply pending migrations (MIGRATE_DATABASE_URL, else DATABASE_URL)
  arch-api migrate status   print applied and pending migrations
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
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
}
