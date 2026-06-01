// Package web embeds the built React frontend so the Go binary can serve it.
//
// Run `just frontend` (or the Dockerfile's frontend stage) to populate
// dist/ before building the Go binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Assets returns the built frontend rooted at "dist".
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web: dist directory missing from embed: " + err.Error())
	}
	return sub
}
