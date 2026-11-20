// Package web is the dashboard, built by Vite into dist/ and embedded in the binary.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the built dashboard. A path with no file extension is a
// page of the single-page app and gets index.html.
func Handler() http.Handler { return handler(dist) }

func handler(fsys fs.FS) http.Handler {
	sub, _ := fs.Sub(fsys, "dist")
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte("The dashboard is not built. Run make web.\n"))
			return
		}
		p := path.Clean(r.URL.Path)
		if path.Ext(p) == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(index)
			return
		}
		if _, err := fs.Stat(sub, strings.TrimPrefix(p, "/")); err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(p, "/assets/") { // Vite puts a content hash in every asset name
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

// Stylesheet is the built dashboard's stylesheet, for pages served outside
// the dashboard that should look like it (Yawl Cloud's front).
func Stylesheet() string {
	m, _ := fs.Glob(dist, "dist/assets/index-*.css")
	if len(m) == 0 {
		return ""
	}
	return "/" + strings.TrimPrefix(m[0], "dist/")
}
