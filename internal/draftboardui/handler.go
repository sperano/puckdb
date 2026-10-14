// Package draftboardui serves the dependency-free Maurice live draft board.
package draftboardui

import (
	"embed"
	"io/fs"
	"net/http"
)

// The assets are listed one by one so the client's Node tests (web/*_test.js)
// are not served.
//
//go:embed web/index.html web/app.js web/style.css
var assets embed.FS

// Handler returns the embedded browser client. The client uses the same
// GraphQL session versions as every other draft-board consumer.
func Handler() http.Handler {
	root, err := fs.Sub(assets, "web")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(root))
}
