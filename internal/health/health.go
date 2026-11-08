// Package health decides readiness: the database answers and migrations are current.
package health

import (
	"context"
	"net/http"
	"time"
)

// Timeout bounds every readiness check.
const Timeout = 2 * time.Second

// Check names reported in Report.Failing.
const (
	CheckDatabase   = "database"
	CheckMigrations = "migrations"
)

// Pinger checks that the database answers.
type Pinger interface {
	Ping(ctx context.Context) error
}

// VersionReader returns the highest applied migration version. It must only read.
type VersionReader interface {
	AppliedVersion(ctx context.Context) (int64, error)
}

// Report is the readiness result.
type Report struct {
	Status  string
	Failing []string
}

// HTTPStatus is 200 when ready, else 503.
func (r Report) HTTPStatus() int {
	if len(r.Failing) == 0 {
		return http.StatusOK
	}
	return http.StatusServiceUnavailable
}

// Checker runs the readiness checks.
type Checker struct {
	DB       Pinger
	Versions VersionReader
	// Expected is the highest migration version embedded in the binary.
	Expected int64
}

// Check pings the database and, only when that succeeds, compares the applied migration
// version with Expected.
func (c Checker) Check(ctx context.Context) Report {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	if err := c.DB.Ping(ctx); err != nil {
		return Report{Status: "unavailable", Failing: []string{CheckDatabase}}
	}
	if v, err := c.Versions.AppliedVersion(ctx); err != nil || v != c.Expected {
		return Report{Status: "unavailable", Failing: []string{CheckMigrations}}
	}
	return Report{Status: "ok"}
}
