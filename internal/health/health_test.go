package health_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/CliXiD/arch-api/internal/health"
)

type fakeDB struct {
	pingErr    error
	version    int64
	versionErr error
	reads      int
}

func (f *fakeDB) Ping(context.Context) error { return f.pingErr }

func (f *fakeDB) AppliedVersion(context.Context) (int64, error) {
	f.reads++
	return f.version, f.versionErr
}

func TestChecker(t *testing.T) {
	tests := []struct {
		name       string
		db         *fakeDB
		wantStatus int
		wantBody   health.Report
		wantReads  int
	}{
		{"ping fails", &fakeDB{pingErr: errors.New("down"), version: 1}, http.StatusServiceUnavailable,
			health.Report{Status: "unavailable", Failing: []string{"database"}}, 0},
		{"version behind", &fakeDB{version: 0}, http.StatusServiceUnavailable,
			health.Report{Status: "unavailable", Failing: []string{"migrations"}}, 1},
		{"version ahead", &fakeDB{version: 2}, http.StatusServiceUnavailable,
			health.Report{Status: "unavailable", Failing: []string{"migrations"}}, 1},
		{"table missing", &fakeDB{versionErr: errors.New(`relation "goose_db_version" does not exist`)}, http.StatusServiceUnavailable,
			health.Report{Status: "unavailable", Failing: []string{"migrations"}}, 1},
		{"both fine", &fakeDB{version: 1}, http.StatusOK, health.Report{Status: "ok"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := health.Checker{DB: tt.db, Versions: tt.db, Expected: 1}
			got := c.Check(context.Background())
			assert.Equal(t, tt.wantBody, got)
			assert.Equal(t, tt.wantStatus, got.HTTPStatus())
			assert.Equal(t, tt.wantReads, tt.db.reads, "migrations are checked only after a successful ping")
		})
	}
}
