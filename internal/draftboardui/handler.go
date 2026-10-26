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

// Handler returns the embedded browser client mounted at prefix (such as
// "/draft"), which it strips before looking up a file: routers like chi pass
// the full request path to a mounted handler. The bare prefix redirects to
// prefix+"/", keeping the query, so the page's relative asset links resolve
// under the prefix. The client uses the same GraphQL session versions as
// every other draft-board consumer.
func Handler(prefix string) http.Handler {
	root, err := fs.Sub(assets, "web")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix(prefix, http.FileServer(http.FS(root)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == prefix {
			target := prefix + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		files.ServeHTTP(w, r)
	})
}
