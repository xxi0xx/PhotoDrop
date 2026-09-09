// Package migrations contains the ordered SQL migrations embedded in the binary.
package migrations

import "embed"

// Files must be append-only, named NNN_description.sql, and numbered from 001.
//
//go:embed *.sql
var Files embed.FS
