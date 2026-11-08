package main

import (
	"bytes"
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
		{"serve without OIDC_ISSUER", []string{"serve"}, map[string]string{
			"DATABASE_URL": "postgres://localhost/arch", "OIDC_ISSUER": "", "OIDC_AUDIENCE": "arch-api",
		}, 1, "config: OIDC_ISSUER is required"},
		{"migrate without a database URL", []string{"migrate", "up"}, map[string]string{
			"DATABASE_URL": "", "MIGRATE_DATABASE_URL": "",
		}, 1, "config: DATABASE_URL is required"},
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
