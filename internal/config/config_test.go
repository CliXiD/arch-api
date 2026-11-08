package config_test

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CliXiD/arch-api/internal/config"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func serveEnv(edit func(map[string]string)) map[string]string {
	vars := map[string]string{ //nolint:gosec // dummy test credentials
		"DATABASE_URL":  "postgres://arch:dummy-pw@localhost:5432/arch?sslmode=disable",
		"OIDC_ISSUER":   "http://localhost:8180/realms/arch",
		"OIDC_AUDIENCE": "arch-api",
	}
	edit(vars)
	return vars
}

func TestLoadServe(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr string
	}{
		{"defaults", serveEnv(func(map[string]string) {}), ""},
		{"missing OIDC_ISSUER", serveEnv(func(v map[string]string) { delete(v, "OIDC_ISSUER") }), "config: OIDC_ISSUER is required"},
		{"several missing named in one message", map[string]string{},
			"config: DATABASE_URL is required, OIDC_ISSUER is required, OIDC_AUDIENCE is required"},
		{"invalid LOG_LEVEL", serveEnv(func(v map[string]string) { v["LOG_LEVEL"] = "verbose" }), "config: LOG_LEVEL is invalid"},
		{"invalid protocol", serveEnv(func(v map[string]string) { v["OTEL_EXPORTER_OTLP_PROTOCOL"] = "thrift" }),
			"config: OTEL_EXPORTER_OTLP_PROTOCOL is invalid"},
		{"invalid DATABASE_URL", serveEnv(func(v map[string]string) { v["DATABASE_URL"] = "postgres://arch:dummy-pw@[::1" }),
			"config: DATABASE_URL is invalid"},
		{"invalid OIDC_ISSUER", serveEnv(func(v map[string]string) { v["OIDC_ISSUER"] = "localhost:8180" }), "config: OIDC_ISSUER is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.LoadServe(env(tt.vars))
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, ":8080", c.HTTPAddr)
				assert.Equal(t, ":9090", c.MetricsAddr)
				assert.Equal(t, slog.LevelInfo, c.LogLevel)
				assert.Equal(t, "grpc", c.OTLPProtocol)
				assert.Empty(t, c.OTLPEndpoint)
				assert.Empty(t, c.OIDCJWKSURL)
				assert.Empty(t, c.CORSOrigins)
				return
			}
			require.EqualError(t, err, tt.wantErr)
			assert.NotContains(t, err.Error(), "dummy-pw", "never print a value")
		})
	}
}

func TestLoadServeValues(t *testing.T) {
	c, err := config.LoadServe(env(serveEnv(func(v map[string]string) {
		v["HTTP_ADDR"] = ":5130"
		v["LOG_LEVEL"] = "debug"
		v["CORS_ORIGINS"] = "http://localhost:5173, http://localhost:5131"
		v["OIDC_JWKS_URL"] = "http://keycloak:8080/realms/arch/protocol/openid-connect/certs"
		v["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://localhost:4318"
		v["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/protobuf"
	})))
	require.NoError(t, err)
	assert.Equal(t, ":5130", c.HTTPAddr)
	assert.Equal(t, slog.LevelDebug, c.LogLevel)
	assert.Equal(t, []string{"http://localhost:5173", "http://localhost:5131"}, c.CORSOrigins)
	assert.Equal(t, "http://keycloak:8080/realms/arch/protocol/openid-connect/certs", c.OIDCJWKSURL)
	assert.Equal(t, "http://localhost:4318", c.OTLPEndpoint)
	assert.Equal(t, "http/protobuf", c.OTLPProtocol)
}

func TestLoadMigrate(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		want    string
		wantErr string
	}{
		{"MIGRATE_DATABASE_URL wins", map[string]string{"MIGRATE_DATABASE_URL": "postgres://owner@db/arch", "DATABASE_URL": "postgres://app@db/arch"},
			"postgres://owner@db/arch", ""},
		{"falls back to DATABASE_URL, no OIDC needed", map[string]string{"DATABASE_URL": "postgres://app@db/arch"}, "postgres://app@db/arch", ""},
		{"missing", map[string]string{}, "", "config: DATABASE_URL is required"},
		{"invalid", map[string]string{"MIGRATE_DATABASE_URL": "postgres://[::1"}, "", "config: MIGRATE_DATABASE_URL is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.LoadMigrate(env(tt.vars))
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, c.DatabaseURL)
		})
	}
}

func TestLoadSeed(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		want    string
		wantErr string
	}{
		{"DATABASE_URL, no OIDC needed", map[string]string{"DATABASE_URL": "postgres://app@db/arch"}, "postgres://app@db/arch", ""},
		{"ignores MIGRATE_DATABASE_URL", map[string]string{"MIGRATE_DATABASE_URL": "postgres://owner@db/arch", "DATABASE_URL": "postgres://app@db/arch"},
			"postgres://app@db/arch", ""},
		{"missing", map[string]string{"MIGRATE_DATABASE_URL": "postgres://owner@db/arch"}, "", "config: DATABASE_URL is required"},
		{"invalid", map[string]string{"DATABASE_URL": "postgres://arch:dummy-pw@[::1"}, "", "config: DATABASE_URL is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.LoadSeed(env(tt.vars))
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.NotContains(t, err.Error(), "dummy-pw", "never print a value")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, c.DatabaseURL)
		})
	}
}
