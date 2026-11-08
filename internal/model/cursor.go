package model

import (
	"encoding/base64"
	"encoding/json"
)

// encodeCursor makes the opaque cursor for the sort key of a page's last row.
func encodeCursor(key ...string) string {
	b, _ := json.Marshal(key) // a []string always marshals
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor returns the n-part sort key in cursor, or a validation error.
func decodeCursor(cursor string, n int) ([]string, error) {
	invalid := &ValidationError{Location: "query.cursor", Message: "invalid cursor"}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, invalid
	}
	var key []string
	if err := json.Unmarshal(b, &key); err != nil || len(key) != n {
		return nil, invalid
	}
	return key, nil
}
