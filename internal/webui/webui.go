// Package webui embeds the built single-page frontend and serves it with an
// SPA fallback so client-side routes resolve to index.html.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded dist directory. Unknown extension-less paths
// (client-side routes) fall back to index.html; missing files under /assets/
// or with a file extension answer 404. /assets/* (content-hashed by the
// bundler) get immutable caching.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("webui: dist directory missing: " + err.Error())
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic("webui: dist/index.html missing: " + err.Error())
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		clean := path.Clean("/" + r.URL.Path)
		name := strings.TrimPrefix(clean, "/")
		if name != "" && name != "index.html" && exists(sub, name) {
			if strings.HasPrefix(clean, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = clean
			files.ServeHTTP(w, r2)
			return
		}
		if !spaFallback(clean) {
			w.Header().Set("Cache-Control", "no-cache")
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}

// spaFallback reports whether a missing path should resolve to index.html.
// Asset paths and paths whose last segment carries a file extension are
// static-file requests and must 404 instead.
func spaFallback(clean string) bool {
	if clean == "/" || clean == "/index.html" {
		return true
	}
	if strings.HasPrefix(clean, "/assets/") {
		return false
	}
	return path.Ext(path.Base(clean)) == ""
}

func exists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
