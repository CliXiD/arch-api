//go:build integration

package main

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/migrations"
)

// vocabulary returns every node_types and edge_types row as JSON, ordered by id.
func vocabulary(t *testing.T, sqlDB *sql.DB) []string {
	t.Helper()
	var out []string
	for _, q := range []string{
		"SELECT to_jsonb(t)::text FROM node_types t ORDER BY id",
		"SELECT to_jsonb(t)::text FROM edge_types t ORDER BY id",
	} {
		rows, err := sqlDB.QueryContext(context.Background(), q)
		require.NoError(t, err)
		for rows.Next() {
			var row string
			require.NoError(t, rows.Scan(&row))
			out = append(out, row)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	return out
}

// TestMigrationVocabularyRollback applies 00006_model_vocabulary.sql on a database at version 5,
// then rolls it back one step, and checks that the vocabulary is exactly as it was at version 5.
func TestMigrationVocabularyRollback(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("pgx", startEmptyPostgres(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	require.NoError(t, err)

	_, err = provider.UpTo(ctx, 5)
	require.NoError(t, err)
	before := vocabulary(t, sqlDB)

	_, err = provider.UpTo(ctx, 6)
	require.NoError(t, err)
	assert.NotEqual(t, before, vocabulary(t, sqlDB), "version 6 changes the vocabulary")

	_, err = provider.Down(ctx)
	require.NoError(t, err)
	v, err := provider.GetDBVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(5), v)
	after := vocabulary(t, sqlDB)
	require.Len(t, after, len(before))
	for i := range before {
		assert.JSONEq(t, before[i], after[i])
	}
}
