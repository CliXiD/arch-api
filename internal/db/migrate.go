package db

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"github.com/CliXiD/arch-api/migrations"
)

// Migrate runs `migrate up` or `migrate status` with the embedded migrations, writing a short
// report to w.
func Migrate(ctx context.Context, databaseURL, command string, w io.Writer) error {
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return ErrInvalidURL
	}
	defer func() { _ = sqlDB.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return err
	}

	switch command {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			return err
		}
		if len(results) == 0 {
			_, err = fmt.Fprintln(w, "no pending migrations")
			return err
		}
		for _, r := range results {
			if _, err := fmt.Fprintf(w, "applied %d %s (%s)\n", r.Source.Version, r.Source.Path, r.Duration); err != nil {
				return err
			}
		}
		return nil
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			if _, err := fmt.Fprintf(w, "%-8s %d %s\n", s.State, s.Source.Version, s.Source.Path); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown migrate command %q", command)
	}
}
