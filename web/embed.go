// Package web holds the built web UI (web/dist) for embedding in the plumb binary.
//
// Run `make build-web` before `go build` to embed the real UI. On a clean checkout dist/
// holds only .gitkeep, so the package compiles and Built reports false.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built UI rooted at dist/ (index.html, assets/...).
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// fs.Sub fails only for an invalid path; "dist" is a constant.
		panic(err)
	}
	return sub
}

// Built reports whether the embedded UI contains index.html.
func Built() bool {
	_, err := fs.Stat(FS(), "index.html")
	return err == nil
}
