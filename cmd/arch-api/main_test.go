package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		wantCode   int
		wantStderr string
	}{
		{"no args", nil, nil, 2, "usage:"},
		{"unknown command", []string{"openapi"}, nil, 2, "usage:"},
		{"unknown migrate command", []string{"migrate", "down"}, nil, 2, "usage:"},
		{"seed without --sample", []string{"seed"}, nil, 2, "arch-api seed --sample"},
		{"seed with another flag", []string{"seed", "--force"}, nil, 2, "arch-api seed --sample"},
		{"seed with an extra argument", []string{"seed", "--sample", "dev"}, nil, 2, "usage:"},
		{"seed without DATABASE_URL", []string{"seed", "--sample"}, map[string]string{
			"DATABASE_URL": "", "MIGRATE_DATABASE_URL": "postgres://localhost/arch",
		}, 1, "config: DATABASE_URL is required"},
		{"seed with an invalid DATABASE_URL", []string{"seed", "--sample"}, map[string]string{
			"DATABASE_URL": "postgres://arch:dummy-pw@[::1",
		}, 1, "config: DATABASE_URL is invalid"},
		{"serve without OIDC_ISSUER", []string{"serve"}, map[string]string{
			"DATABASE_URL": "postgres://localhost/arch", "OIDC_ISSUER": "", "OIDC_AUDIENCE": "arch-api",
		}, 1, "config: OIDC_ISSUER is required"},
		{"migrate without a database URL", []string{"migrate", "up"}, map[string]string{
			"DATABASE_URL": "", "MIGRATE_DATABASE_URL": "",
		}, 1, "config: DATABASE_URL is required"},
		{"teams alone", []string{"teams"}, nil, 2, "usage:"},
		{"teams remove", []string{"teams", "remove", "x"}, nil, 2, "usage:"},
		{"teams add without arguments", []string{"teams", "add"}, nil, 2, "usage:"},
		{"teams add without --name", []string{"teams", "add", "platform"}, nil, 2, "usage:"},
		{"teams add with --name and no value", []string{"teams", "add", "platform", "--name"}, nil, 2, "usage:"},
		{"teams add with a second positional", []string{"teams", "add", "a", "b", "--name", "x"}, nil, 2, "usage:"},
		{"teams add with an unknown flag", []string{"teams", "add", "platform", "--name", "x", "--force"}, nil, 2, "usage:"},
		{"usage lists teams add", nil, nil, 2, "arch-api teams add <id> --name <name>"},
		{"teams add with an invalid id and no database", []string{"teams", "add", "Bad_Id", "--name", "x"}, map[string]string{
			"DATABASE_URL": "", "MIGRATE_DATABASE_URL": "",
		}, 1, "teams add: id must be a slug matching ^[a-z0-9][a-z0-9-]{1,62}$"},
		{"teams add with a blank name", []string{"teams", "add", "platform", "--name", "  "}, map[string]string{
			"DATABASE_URL": "", "MIGRATE_DATABASE_URL": "",
		}, 1, "teams add: --name must not be empty"},
		{"teams add with an invalid id and a blank name", []string{"teams", "add", "Bad_Id", "--name="}, nil, 1,
			"teams add: id must be a slug matching ^[a-z0-9][a-z0-9-]{1,62}$\nteams add: --name must not be empty\n"},
		{"teams add without a database URL", []string{"teams", "add", "platform", "--name", "Platform"}, map[string]string{
			"DATABASE_URL": "", "MIGRATE_DATABASE_URL": "",
		}, 1, "config: DATABASE_URL is required"},
		{"teams add with an invalid MIGRATE_DATABASE_URL", []string{"teams", "add", "platform", "--name", "Platform"}, map[string]string{
			"MIGRATE_DATABASE_URL": "postgres://arch:dummy-pw@[::1",
		}, 1, "config: MIGRATE_DATABASE_URL is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			var stdout, stderr bytes.Buffer
			assert.Equal(t, tt.wantCode, run(tt.args, &stdout, &stderr))
			assert.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

func TestParseTeamsAdd(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantID   string
		wantName string
		wantOK   bool
	}{
		{"flag after the id", []string{"platform", "--name", "Platform"}, "platform", "Platform", true},
		{"flag before the id", []string{"--name", "Platform", "platform"}, "platform", "Platform", true},
		{"--name=", []string{"platform", "--name=Platform"}, "platform", "Platform", true},
		{"--name= before the id", []string{"--name=Platform", "platform"}, "platform", "Platform", true},
		{"empty --name= is a name", []string{"platform", "--name="}, "platform", "", true},
		{"name kept as given", []string{"platform", "--name", " Platform "}, "platform", " Platform ", true},
		{"nothing", nil, "", "", false},
		{"no --name", []string{"platform"}, "", "", false},
		{"no id", []string{"--name", "Platform"}, "", "", false},
		{"--name without a value", []string{"platform", "--name"}, "", "", false},
		{"two ids", []string{"a", "b", "--name", "x"}, "", "", false},
		{"two names", []string{"platform", "--name", "x", "--name=y"}, "", "", false},
		{"unknown flag", []string{"platform", "--name", "x", "--force"}, "", "", false},
		{"single-dash flag", []string{"platform", "-name", "x"}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, name, ok := parseTeamsAdd(tt.args)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantID, id)
				assert.Equal(t, tt.wantName, name)
			}
		})
	}
}

// TestTeamsAddUnreachableDatabase checks that a connection failure prints the fixed message and no
// part of the connection string.
func TestTeamsAddUnreachableDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MIGRATE_DATABASE_URL", "postgres://dummy-user:dummy-pw@127.0.0.1:1/dummy-db?sslmode=disable&connect_timeout=2")
	dsn := os.Getenv("MIGRATE_DATABASE_URL")
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 1, run([]string{"teams", "add", "platform", "--name", "Platform"}, &stdout, &stderr))
	assert.Contains(t, stderr.String(), "teams add: database error")
	for _, secret := range []string{dsn, "dummy-user", "dummy-pw", "127.0.0.1", "dummy-db"} {
		assert.NotContains(t, stdout.String(), secret)
		assert.NotContains(t, stderr.String(), secret)
	}
}
