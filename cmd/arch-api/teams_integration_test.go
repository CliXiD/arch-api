//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/auth/authtest"
	"github.com/CliXiD/arch-api/internal/model"
)

// TestTeamsAddIntegration runs `teams add` as the schema owner on a freshly migrated database and
// reads the result back through the API as the runtime role arch_app.
func TestTeamsAddIntegration(t *testing.T) {
	ctx := context.Background()
	ownerDSN, appDSN := startPostgres(t)
	owner := newPool(t, ownerDSN)
	engine, realm, key := newEngine(t, newPool(t, appDSN))
	c := client{engine: engine, token: authtest.Sign(t, key, realm.Claims(time.Now(), "viewer"))}
	u, err := url.Parse(ownerDSN)
	require.NoError(t, err)
	password, _ := u.User.Password()
	require.NotEmpty(t, password)

	teamsAdd := func(t *testing.T, args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"teams", "add"}, args...), &stdout, &stderr)
		for _, out := range []string{stdout.String(), stderr.String()} {
			assert.NotContains(t, out, ownerDSN)
			assert.NotContains(t, out, ":"+password+"@")
		}
		return code, stdout.String(), stderr.String()
	}
	teamCount := func(t *testing.T) int {
		t.Helper()
		var n int
		require.NoError(t, owner.QueryRow(ctx, "SELECT count(*) FROM teams").Scan(&n))
		return n
	}

	t.Setenv("DATABASE_URL", "")
	t.Setenv("MIGRATE_DATABASE_URL", ownerDSN)

	t.Run("creates the team and one change_log row", func(t *testing.T) {
		before := lastChangeID(t, owner)
		code, stdout, stderr := teamsAdd(t, "platform", "--name", "Platform")
		require.Equal(t, 0, code, stderr)
		assert.Equal(t, "team platform created\n", stdout)
		assert.Empty(t, stderr)

		var teams model.TeamList
		c.json(t, "/api/v1/teams", &teams)
		require.Len(t, teams.Items, 1)
		assert.Equal(t, "platform", teams.Items[0].ID)
		assert.Equal(t, "Platform", teams.Items[0].Name)

		changes := changesSince(t, owner, before)
		require.Len(t, changes, 1)
		ch := changes[0]
		assert.Equal(t, "system", ch.Actor)
		assert.Equal(t, "arch-api teams add", ch.ActorName)
		assert.Equal(t, "service", ch.ActorKind)
		assert.Equal(t, "api", ch.Source)
		assert.Equal(t, "team", ch.Entity)
		assert.Equal(t, "platform", ch.EntityKey)
		assert.Equal(t, "create", ch.Action)

		var beforeNull bool
		var after []byte
		require.NoError(t, owner.QueryRow(ctx, "SELECT before IS NULL, after FROM change_log WHERE id = $1", ch.ID).
			Scan(&beforeNull, &after))
		assert.True(t, beforeNull)
		var team map[string]any
		require.NoError(t, json.Unmarshal(after, &team))
		assert.Equal(t, "platform", team["id"])
		assert.Equal(t, "Platform", team["name"])
		assert.NotEmpty(t, team["created_at"])
	})

	t.Run("a second run reports the existing team and writes nothing", func(t *testing.T) {
		changes := changeCount(t, owner)
		code, stdout, stderr := teamsAdd(t, "platform", "--name", "Other")
		require.Equal(t, 0, code, stderr)
		assert.Equal(t, "team platform already exists\n", stdout)
		assert.Empty(t, stderr)
		assert.Equal(t, changes, changeCount(t, owner))
		var name string
		require.NoError(t, owner.QueryRow(ctx, "SELECT name FROM teams WHERE id = 'platform'").Scan(&name))
		assert.Equal(t, "Platform", name)
	})

	t.Run("an invalid id writes nothing", func(t *testing.T) {
		teams, changes := teamCount(t), changeCount(t, owner)
		code, stdout, stderr := teamsAdd(t, "Bad_Id", "--name", "x")
		assert.Equal(t, 1, code)
		assert.Empty(t, stdout)
		assert.Equal(t, "teams add: id must be a slug matching ^[a-z0-9][a-z0-9-]{1,62}$\n", stderr)
		assert.Equal(t, teams, teamCount(t))
		assert.Equal(t, changes, changeCount(t, owner))
	})

	t.Run("falls back to DATABASE_URL", func(t *testing.T) {
		t.Setenv("MIGRATE_DATABASE_URL", "")
		t.Setenv("DATABASE_URL", ownerDSN)
		code, stdout, stderr := teamsAdd(t, "core", "--name", "Core")
		require.Equal(t, 0, code, stderr)
		assert.Equal(t, "team core created\n", stdout)
		assert.Equal(t, 2, teamCount(t))
	})
}
