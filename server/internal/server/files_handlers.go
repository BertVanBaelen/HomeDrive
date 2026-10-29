package server

import (
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
)

func (s *Server) fileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/drives", s.authed(s.handleDrives))
	mux.HandleFunc("GET /api/nodes/{id}", s.authed(s.handleNode))
	mux.HandleFunc("GET /api/nodes/{id}/children", s.authed(s.handleChildren))
	mux.HandleFunc("GET /api/nodes/{id}/content", s.authed(s.handleContent))
	mux.HandleFunc("PATCH /api/nodes/{id}", s.authed(s.handlePatchNode))
	mux.HandleFunc("DELETE /api/nodes/{id}", s.authed(s.handleTrashNode))
	mux.HandleFunc("POST /api/nodes/{id}/restore", s.authed(s.handleRestore))
	mux.HandleFunc("POST /api/folders", s.authed(s.handleCreateFolder))
	mux.HandleFunc("GET /api/trash", s.authed(s.handleTrash))
	mux.HandleFunc("DELETE /api/trash", s.authed(s.handleEmptyTrash))
	mux.HandleFunc("GET /api/starred", s.authed(s.handleList(func(r *http.Request, uid string) ([]files.Located, error) {
		return s.files.Starred(r.Context(), uid)
	})))
	mux.HandleFunc("GET /api/recent", s.authed(s.handleList(func(r *http.Request, uid string) ([]files.Located, error) {
		return s.files.Recent(r.Context(), uid)
	})))
	mux.HandleFunc("GET /api/search", s.authed(s.handleList(func(r *http.Request, uid string) ([]files.Located, error) {
		return s.files.Search(r.Context(), uid, r.URL.Query().Get("q"))
	})))
	mux.HandleFunc("POST /api/uploads", s.authed(s.handleCreateUpload))
	mux.HandleFunc("GET /api/uploads/{id}", s.authed(s.handleUploadStatus))
	mux.HandleFunc("PUT /api/uploads/{id}", s.authed(s.handleUploadChunk))
	mux.HandleFunc("POST /api/uploads/{id}/complete", s.authed(s.handleCompleteUpload))
	mux.HandleFunc("DELETE /api/uploads/{id}", s.authed(s.handleCancelUpload))
}

func (s *Server) handleDrives(w http.ResponseWriter, r *http.Request) {
	drives, err := s.files.Drives(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, drives)
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	d, err := s.files.Get(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleChildren(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sortBy := files.SortKey(q.Get("sort"))
	switch sortBy {
	case files.SortName, files.SortModified, files.SortSize:
	default:
		sortBy = files.SortName
	}
	nodes, err := s.files.Children(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), sortBy, q.Get("order") == "desc")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var req struct{ ParentID, Name string }
	if !readJSON(w, r, &req) {
		return
	}
	n, err := s.files.CreateFolder(r.Context(), currentUser(r.Context()).ID, req.ParentID, req.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

// handlePatchNode renames, moves and/or stars a node.
func (s *Server) handlePatchNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     *string `json:"name"`
		ParentID *string `json:"parentId"`
		Starred  *bool   `json:"starred"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ctx, uid, id := r.Context(), currentUser(r.Context()).ID, r.PathValue("id")
	var n files.Node
	var err error
	if req.ParentID != nil {
		if n, err = s.files.Move(ctx, uid, id, *req.ParentID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if req.Name != nil {
		if n, err = s.files.Rename(ctx, uid, id, *req.Name); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if req.Starred != nil {
		if n, err = s.files.SetStar(ctx, uid, id, *req.Starred); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if req.Name == nil && req.ParentID == nil && req.Starred == nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// handleTrashNode moves an item to the trash, or with ?permanent=true
// deletes an item that is already there for good.
func (s *Server) handleTrashNode(w http.ResponseWriter, r *http.Request) {
	ctx, u, id := r.Context(), currentUser(r.Context()), r.PathValue("id")
	if r.URL.Query().Get("permanent") == "true" {
		if !recentAuth(w, r) {
			return
		}
		if err := s.files.DeleteForever(ctx, u.ID, id); err != nil {
			s.fail(w, r, err)
			return
		}
		s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "deleted_forever", id)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.files.Trash(ctx, u.ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	if !recentAuth(w, r) {
		return
	}
	ctx, u := r.Context(), currentUser(r.Context())
	n, err := s.files.EmptyTrash(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "trash_emptied", strconv.Itoa(n))
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	n, err := s.files.Restore(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.files.TrashList(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

// handleList serves a list of items from across the user's drives.
func (s *Server) handleList(list func(r *http.Request, userID string) ([]files.Located, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodes, err := list(r, currentUser(r.Context()).ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, nodes)
	}
}

func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParentID string `json:"parentId"`
		Name     string `json:"name"`
		Size     int64  `json:"size"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.files.CreateUpload(r.Context(), currentUser(r.Context()).ID, req.ParentID, req.Name, req.Size)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.files.UploadStatus(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// handleUploadChunk takes one chunk: PUT /api/uploads/{id}?offset=N with an
// application/octet-stream body and a Content-Length.
func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/octet-stream" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "length_required")
		return
	}
	if r.ContentLength > files.MaxChunk {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	// A 32 MiB chunk on a slow phone connection needs longer than the
	// server-wide read timeout.
	rc := http.NewResponseController(w)
	rc.SetReadDeadline(time.Now().Add(15 * time.Minute))
	body := http.MaxBytesReader(w, r.Body, files.MaxChunk)
	received, err := s.files.WriteChunk(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), offset, r.ContentLength, body)
	if err != nil {
		if err == files.ErrBadOffset {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "offset_mismatch", "received": received})
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"received": received})
}

func (s *Server) handleCompleteUpload(w http.ResponseWriter, r *http.Request) {
	n, err := s.files.CompleteUpload(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleCancelUpload(w http.ResponseWriter, r *http.Request) {
	if err := s.files.CancelUpload(r.Context(), currentUser(r.Context()).ID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// inlineTypes may be shown by the browser itself. Everything else, notably
// HTML and SVG, is only ever downloaded.
var inlineTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/avif": true, "image/bmp": true,
	"application/pdf": true,
	"audio/mpeg":      true, "audio/mp4": true, "audio/aac": true, "audio/wav": true, "audio/ogg": true, "audio/flac": true,
	"video/mp4": true, "video/webm": true, "video/quicktime": true,
}

// servingType decides the Content-Type and whether the content may be
// shown inline. Text of any kind (Markdown, code, even HTML) is shown as
// plain text.
func servingType(name string) (ctype string, inline bool) {
	m := files.MimeOf(name)
	switch {
	case inlineTypes[m]:
		return m, true
	case m == "text/html" || m == "image/svg+xml":
		return "application/octet-stream", false
	case strings.HasPrefix(m, "text/") || m == "application/json" || m == "application/xml":
		return "text/plain; charset=utf-8", true
	}
	return "application/octet-stream", false
}

// handleContent serves a file's bytes with Range support. ?download=1
// always gives an attachment.
func (s *Server) handleContent(w http.ResponseWriter, r *http.Request) {
	f, n, err := s.files.Open(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	serveContent(w, r, f, n)
}

// serveContent sends an open file, for signed-in users and link visitors.
func serveContent(w http.ResponseWriter, r *http.Request, f io.ReadSeeker, n files.Node) {
	ctype, inline := servingType(n.Name)
	disposition := "inline"
	if !inline || r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	if cd := mime.FormatMediaType(disposition, map[string]string{"filename": n.Name}); cd != "" {
		h.Set("Content-Disposition", cd)
	} else {
		h.Set("Content-Disposition", disposition)
	}
	// Whatever the file contains, it can't run script in Homedrive's origin.
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'")
	h.Set("Cache-Control", "private, no-cache")
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(6 * time.Hour))
	http.ServeContent(w, r, "", n.ModifiedAt, f)
}
