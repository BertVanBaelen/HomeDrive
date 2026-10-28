package server

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// staticHandler serves the built PWA from web_dir. Paths that aren't files
// fall back to index.html so the client-side router can handle them. It never
// lists directories.
type staticHandler struct {
	root *os.Root
	fsys fs.FS
}

func newStaticHandler(dir string) (*staticHandler, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening web_dir: %w", err)
	}
	fsys := root.FS()
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		root.Close()
		return nil, fmt.Errorf("web_dir %s has no index.html (was the frontend built?): %w", dir, err)
	}
	return &staticHandler{root: root, fsys: fsys}, nil
}

func (h *staticHandler) Close() error { return h.root.Close() }

// contentTypes is explicit so that responses don't depend on the host's MIME
// database.
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".webmanifest": "application/manifest+json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/x-icon",
	".woff2":       "font/woff2",
	".txt":         "text/plain; charset=utf-8",
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if h.serveFile(w, r, name) {
		return
	}
	// Missing assets get a real 404; anything else is an app route.
	if name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}
	if !h.serveFile(w, r, "index.html") {
		http.Error(w, "index.html missing", http.StatusInternalServerError)
	}
}

func (h *staticHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	f, err := h.fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return false
	}
	content, ok := f.(io.ReadSeeker)
	if !ok {
		return false
	}

	ctype, ok := contentTypes[path.Ext(name)]
	if !ok {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	if strings.HasPrefix(name, "assets/") {
		// Vite puts a content hash in every asset name.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, "", fi.ModTime(), content)
	return true
}
