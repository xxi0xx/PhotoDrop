// Package web provides the compiled Svelte application. Run npm ci and
// npm run build in web/ before building or testing Go from a fresh checkout.
package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var compiled embed.FS

func Assets() (fs.FS, error) {
	return fs.Sub(compiled, "dist")
}
