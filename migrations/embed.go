// Package migrations holds the goose SQL migrations, embedded into the binary.
package migrations

import "embed"

// FS holds every *.sql migration in this folder.
//
//go:embed *.sql
var FS embed.FS
