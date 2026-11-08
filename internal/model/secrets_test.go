package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckSecrets(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY-----" //nolint:gosec // test fixture, not a key
	tests := []struct {
		name  string
		attrs map[string]any
		want  []string
	}{
		{"none", map[string]any{"protocol": "https", "port": float64(443), "url": "https://example.test/path"}, nil},
		{"password name", map[string]any{"password": "x"}, []string{"body.attributes.password"}},
		{"passwd in a name", map[string]any{"db_passwd": "x"}, []string{"body.attributes.db_passwd"}},
		{"API_KEY name", map[string]any{"API_KEY": "x"}, []string{"body.attributes.API_KEY"}},
		{"api-key name", map[string]any{"api-key": "x"}, []string{"body.attributes.api-key"}},
		{"privateKey name", map[string]any{"privateKey": "x"}, []string{"body.attributes.privateKey"}},
		{"private_key name", map[string]any{"private_key": "x"}, []string{"body.attributes.private_key"}},
		{"token name, any value type", map[string]any{"token": float64(1)}, []string{"body.attributes.token"}},
		{"secret and credential names", map[string]any{"client_secret": "x", "Credentials": "y"},
			[]string{"body.attributes.Credentials", "body.attributes.client_secret"}},
		{"URL with userinfo", map[string]any{"dsn": "postgres://u:p@h/db"}, []string{"body.attributes.dsn"}}, //nolint:gosec // test fixture
		{"URL with empty user", map[string]any{"cache": "redis://:hunter2@cache:6379"}, []string{"body.attributes.cache"}},
		{"URL with user only", map[string]any{"repo": "ssh://git@example.test/repo"}, nil},
		{"PEM block", map[string]any{"cert": pem}, []string{"body.attributes.cert"}},
		{"AWS access key id", map[string]any{"key_id": "AKIAIOSFODNN7EXAMPLE"}, []string{"body.attributes.key_id"}}, //nolint:gosec // AWS's documented example id
		{"AKIA too short", map[string]any{"key_id": "AKIAIOSFODNN7"}, nil},
		{"array element", map[string]any{"urls": []any{"https://a.test", "https://u:p@b.test"}},
			[]string{"body.attributes.urls[1]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckSecrets(tt.attrs)
			if tt.want == nil {
				assert.NoError(t, err)
				return
			}
			var s *SecretError
			require.ErrorAs(t, err, &s)
			assert.Equal(t, tt.want, s.Locations)
			for _, v := range tt.attrs {
				if str, ok := v.(string); ok && len(str) > 1 {
					assert.NotContains(t, err.Error(), str, "the error never holds the value")
				}
			}
		})
	}
}
