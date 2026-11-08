// Package db wires pgxpool for the server and goose for migrations.
package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidURL means the database URL could not be parsed. It never carries the URL.
var ErrInvalidURL = errors.New("invalid database URL")

// NewPool returns a pool for databaseURL without connecting, so the server starts while the
// database is down. With tracing, the pool traces queries without their arguments.
func NewPool(databaseURL string, tracing bool) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, ErrInvalidURL
	}
	if tracing {
		cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	}
	return pgxpool.NewWithConfig(context.Background(), cfg)
}

// VersionReader reads the highest applied goose version with a plain SELECT. It never creates
// goose_db_version, so readiness works under a read-only role.
type VersionReader struct {
	Pool *pgxpool.Pool
}

// AppliedVersion returns the highest version in goose_db_version. A missing table is an error.
func (r VersionReader) AppliedVersion(ctx context.Context) (int64, error) {
	var v *int64
	if err := r.Pool.QueryRow(ctx, "SELECT max(version_id) FROM goose_db_version").Scan(&v); err != nil {
		return 0, err
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}

// LatestVersion returns the highest migration version in fsys, from the `<version>_*.sql` names.
func LatestVersion(fsys fs.FS) (int64, error) {
	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, f := range files {
		prefix, _, ok := strings.Cut(path.Base(f), "_")
		if !ok {
			return 0, fmt.Errorf("migration %s: no version prefix", f)
		}
		v, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("migration %s: %w", f, err)
		}
		latest = max(latest, v)
	}
	return latest, nil
}
