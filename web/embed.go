// Package webui serves the embedded llmmeter web frontend.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var assets embed.FS

// FS returns the built frontend rooted at the dist directory.
func FS() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Handler serves the embedded frontend files.
func Handler() http.Handler { return http.FileServer(http.FS(FS())) }

// Index returns the embedded index.html.
func Index() ([]byte, error) { return fs.ReadFile(FS(), "index.html") }
