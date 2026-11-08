package model

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		key  []string
	}{
		{"node key", []string{"prod", "payments-api"}},
		{"edge key", []string{"prod", "routes_via", "vpc-prod", "landing-zone"}},
		{"characters that need escaping", []string{`a"b`, `c\d`, "é/+="}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := encodeCursor(tt.key...)
			assert.NotContains(t, c, "=", "unpadded")
			assert.NotContains(t, c, "+", "URL-safe")
			got, err := decodeCursor(c, len(tt.key))
			require.NoError(t, err)
			assert.Equal(t, tt.key, got)
		})
	}
}

func TestCursorRejected(t *testing.T) {
	tests := []struct {
		name   string
		cursor string
		n      int
	}{
		{"not base64url", "zzz", 2},
		{"bad characters", "!!!", 2},
		{"base64 of non-JSON", "bm90LWpzb24", 2},
		{"JSON object, not an array", encodeRaw(`{"a":"b"}`), 2},
		{"array of numbers", encodeRaw(`[1,2]`), 2},
		{"wrong length", encodeCursor("prod", "payments-api"), 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeCursor(tt.cursor, tt.n)
			var v *ValidationError
			require.ErrorAs(t, err, &v)
			assert.Equal(t, "query.cursor", v.Location)
			assert.Equal(t, "invalid cursor", v.Message)
			assert.NotContains(t, err.Error(), tt.cursor)
		})
	}
}

func TestEscapeLike(t *testing.T) {
	assert.Equal(t, `100\%\_a\\b`, escapeLike(`100%_a\b`))
	assert.Equal(t, "pay", escapeLike("pay"))
}

func encodeRaw(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
