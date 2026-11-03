package server

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
	"github.com/BertVanBaelen/HomeDrive/server/internal/thumbs"
)

// Thumbnails and previews (docs/architecture.md §4): WebP images of
// photos, videos, audio covers, PDFs and saved links, snippets of text,
// spreadsheets and zips, and large WebP previews of HEIC and TIFF photos
// for the viewer. They are served like file contents, to the same people.

func (s *Server) thumbRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/nodes/{id}/thumb", s.authed(s.handleThumb))
	mux.HandleFunc("GET /api/nodes/{id}/snippet", s.authed(s.handleSnippet))
	mux.HandleFunc("GET /api/nodes/{id}/preview", s.authed(s.handlePreview))
	mux.HandleFunc("GET /api/s/{token}/nodes/{id}/thumb", s.visitor(s.handleVisitorThumb))
	mux.HandleFunc("GET /api/s/{token}/nodes/{id}/snippet", s.visitor(s.handleVisitorSnippet))
	mux.HandleFunc("GET /api/s/{token}/nodes/{id}/preview", s.visitor(s.handleVisitorPreview))
}

// thumbNode loads the file a signed-in request is about.
func (s *Server) thumbNode(w http.ResponseWriter, r *http.Request) (files.Node, bool) {
	d, err := s.files.Get(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return files.Node{}, false
	}
	return d.Node, true
}

// visitorNode loads the file a link visitor asks about.
func (s *Server) visitorNode(w http.ResponseWriter, r *http.Request, v files.Visit) (files.Node, bool) {
	n, err := s.files.LinkNode(r.Context(), v, visitNode(r, v))
	if err != nil {
		s.fail(w, r, err)
		return files.Node{}, false
	}
	return n, true
}

// handleThumb serves GET /api/nodes/{id}/thumb?size=grid|list&v=<version>.
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	if n, ok := s.thumbNode(w, r); ok {
		s.sendThumb(w, r, n, currentUser(r.Context()).ID)
	}
}

func (s *Server) handleVisitorThumb(w http.ResponseWriter, r *http.Request, v files.Visit) {
	if n, ok := s.visitorNode(w, r, v); ok {
		s.sendThumb(w, r, n, "")
	}
}

// sendThumb serves a file's image. userID is empty for link visitors,
// whose requests never make Homedrive ask a saved link's provider.
func (s *Server) sendThumb(w http.ResponseWriter, r *http.Request, n files.Node, userID string) {
	size, ok := thumbs.ParseSize(r.URL.Query().Get("size"))
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if n.IsFolder() || n.Version == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var p string
	var err error
	if n.Type == "link" {
		p, err = s.linkThumb(r.Context(), n, size, userID)
	} else {
		p, err = s.thumbs.Image(r.Context(), n.Name, n.Version, size)
	}
	switch {
	case errors.Is(err, errLinkRateLimited):
		writeError(w, http.StatusTooManyRequests, "rate_limited")
	case err != nil:
		writeError(w, http.StatusNotFound, "not_found")
	default:
		serveFile(w, r, p, n.Version)
	}
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if n, ok := s.thumbNode(w, r); ok {
		s.sendPreview(w, r, n)
	}
}

func (s *Server) handleVisitorPreview(w http.ResponseWriter, r *http.Request, v files.Visit) {
	if n, ok := s.visitorNode(w, r, v); ok {
		s.sendPreview(w, r, n)
	}
}

// sendPreview serves the viewer's image of a HEIC or TIFF photo.
func (s *Server) sendPreview(w http.ResponseWriter, r *http.Request, n files.Node) {
	p, err := s.thumbs.Image(r.Context(), n.Name, n.Version, thumbs.Preview)
	if n.IsFolder() || err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	serveFile(w, r, p, n.Version)
}

func (s *Server) handleSnippet(w http.ResponseWriter, r *http.Request) {
	if n, ok := s.thumbNode(w, r); ok {
		s.sendSnippet(w, r, n)
	}
}

func (s *Server) handleVisitorSnippet(w http.ResponseWriter, r *http.Request, v files.Visit) {
	if n, ok := s.visitorNode(w, r, v); ok {
		s.sendSnippet(w, r, n)
	}
}

// sendSnippet serves the start of a text, spreadsheet or zip as JSON.
func (s *Server) sendSnippet(w http.ResponseWriter, r *http.Request, n files.Node) {
	if n.IsFolder() || n.Version == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	b, err := s.thumbs.Snippet(r.Context(), n.Name, n.Version)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	serveCached(w, r, b, "application/json; charset=utf-8", n.Version)
}

// serveCached sends a cache entry under the same rules as file contents:
// inline but sandboxed, revalidated by the version.
func serveCached(w http.ResponseWriter, r *http.Request, b []byte, ctype, version string) {
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+version+`"`)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, `"`+version+`"`) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(b)
}

// serveFile sends a cached WebP image.
func serveFile(w http.ResponseWriter, r *http.Request, path, version string) {
	b, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	serveCached(w, r, b, "image/webp", version)
}
