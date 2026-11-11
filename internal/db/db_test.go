package db_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/db"
	"github.com/CliXiD/arch-api/migrations"
)

func TestLatestVersion(t *testing.T) {
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		want    int64
		wantErr bool
	}{
		{"empty", fstest.MapFS{}, 0, false},
		{"highest wins", fstest.MapFS{"00001_a.sql": {}, "00003_c.sql": {}, "00002_b.sql": {}}, 3, false},
		{"bad name", fstest.MapFS{"baseline.sql": {}}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := db.LatestVersion(tt.fsys)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEmbeddedMigrations(t *testing.T) {
	got, err := db.LatestVersion(migrations.FS)
	require.NoError(t, err)
	assert.Equal(t, int64(7), got)
}

func TestNewPoolInvalidURLHidesValue(t *testing.T) {
	_, err := db.NewPool("postgres://arch:s3cret@[::1", false)
	require.ErrorIs(t, err, db.ErrInvalidURL)
	assert.NotContains(t, err.Error(), "s3cret")
}
