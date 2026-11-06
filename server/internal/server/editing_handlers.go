package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
	"github.com/BertVanBaelen/HomeDrive/server/internal/sheet"
)

// errInvalidText is a text save that isn't text: not UTF-8, or with NUL.
var errInvalidText = errors.New("not valid text")

func (s *Server) editingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/nodes/{id}/lock", s.authed(s.handleLockStatus))
	mux.HandleFunc("POST /api/nodes/{id}/lock", s.authed(s.handleTakeLock))
	mux.HandleFunc("PUT /api/nodes/{id}/lock", s.authed(s.handleRenewLock))
	mux.HandleFunc("DELETE /api/nodes/{id}/lock", s.authed(s.handleReleaseLock))
	mux.HandleFunc("PUT /api/nodes/{id}/content", s.authed(s.handleSaveText))
	mux.HandleFunc("GET /api/nodes/{id}/sheet", s.authed(s.handleSheet))
	mux.HandleFunc("PUT /api/nodes/{id}/sheet", s.authed(s.handleSaveSheet))
	mux.HandleFunc("POST /api/files", s.authed(s.handleCreateFile))
	mux.HandleFunc("GET /api/nodes/{id}/versions", s.authed(s.handleVersions))
	mux.HandleFunc("GET /api/nodes/{id}/versions/{vid}/content", s.authed(s.handleVersionContent))
	mux.HandleFunc("POST /api/nodes/{id}/versions/{vid}/restore", s.authed(s.handleRestoreVersion))
}

// editAllowed counts a write against the user's limit on editing requests.
func (s *Server) editAllowed(w http.ResponseWriter, r *http.Request) bool {
	ok, wait := s.editLimit.Allow(currentUser(r.Context()).ID)
	if !ok {
		tooManyRequests(w, wait)
	}
	return ok
}

// lockID is the secret that proves the request comes from the lock's
// holder. It is only ever a header, never part of a URL, so it stays out of
// logs and history.
func lockID(r *http.Request) string {
	return r.Header.Get("X-Lock-Id")
}

func (s *Server) handleLockStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.files.LockStatus(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleTakeLock(w http.ResponseWriter, r *http.Request) {
	if !s.editAllowed(w, r) {
		return
	}
	id, tookOver, err := s.files.Lock(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), r.URL.Query().Get("takeover") == "1")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tookOver {
		s.audit(r, "edit.lock_takeover", r.PathValue("id"))
	}
	writeJSON(w, http.StatusOK, map[string]string{"lockId": id})
}

func (s *Server) handleRenewLock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LockID string `json:"lockId"`
	}
	if !readJSON(w, r, &req) || !s.editAllowed(w, r) {
		return
	}
	if err := s.files.RenewLock(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), req.LockID); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReleaseLock(w http.ResponseWriter, r *http.Request) {
	if err := s.files.Unlock(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), lockID(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ifMatch is the version a save starts from; the quotes are the HTTP form.
func ifMatch(w http.ResponseWriter, r *http.Request) (string, bool) {
	v := strings.Trim(r.Header.Get("If-Match"), `"`)
	if v == "" {
		writeError(w, http.StatusBadRequest, "bad_request")
	}
	return v, v != ""
}

// handleSaveText saves Markdown or plain text: the whole body, UTF-8.
func (s *Server) handleSaveText(w http.ResponseWriter, r *http.Request) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "text/plain" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	version, ok := ifMatch(w, r)
	if !ok || !s.editAllowed(w, r) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, files.MaxTextFile))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		err = files.ErrTooLarge
	case err == nil && (!utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0):
		err = errInvalidText
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := s.files.Edit(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), lockID(r), version, "text",
		func(func() ([]byte, error)) ([]byte, error) { return data, nil })
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// handleSheet gives a spreadsheet as a grid. People who can edit get the
// editable grid, unless the workbook is more than the grid handles; they and
// everyone else then get the values of every sheet and the reason.
func (s *Server) handleSheet(w http.ResponseWriter, r *http.Request) {
	if !s.editAllowed(w, r) {
		return
	}
	f, n, err := s.files.Open(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	if !isSheetName(n.Name) {
		s.fail(w, r, files.ErrNotEditable)
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, sheet.MaxFileBytes+1))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	view := map[string]any{"version": n.Version, "editable": false, "reason": "read_only"}
	if n.CanEdit {
		var grid sheet.Grid
		if grid, err = sheet.Read(r.Context(), data); err == nil {
			view["editable"], view["grid"] = true, grid
			delete(view, "reason")
		}
		var unsupported *sheet.UnsupportedError
		if errors.As(err, &unsupported) {
			view["reason"] = unsupported.Reason
		} else if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if view["editable"] == false {
		sheets, err := sheet.ReadOnly(r.Context(), data)
		var unsupported *sheet.UnsupportedError
		switch {
		case err == nil:
			view["sheets"] = sheets
		case errors.As(err, &unsupported):
			view["reason"] = unsupported.Reason
		default:
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// handleSaveSheet applies the changed cells to the workbook. The server
// checks every formula and works out every result itself.
func (s *Server) handleSaveSheet(w http.ResponseWriter, r *http.Request) {
	version, ok := ifMatch(w, r)
	if !ok || !s.editAllowed(w, r) {
		return
	}
	var edit sheet.Edit
	if !readJSONMax(w, r, &edit, sheet.MaxEditBytes) {
		return
	}
	n, err := s.files.Edit(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), lockID(r), version, "sheet",
		func(cur func() ([]byte, error)) ([]byte, error) {
			data, err := cur()
			if err != nil {
				return nil, err
			}
			out, err := sheet.Apply(r.Context(), data, edit)
			var unsupported *sheet.UnsupportedError
			if errors.As(err, &unsupported) {
				err = fmt.Errorf("%w: %s", files.ErrNotEditable, unsupported.Reason)
			}
			return out, err
		})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func isSheetName(name string) bool { return strings.EqualFold(path.Ext(name), ".xlsx") }

// handleCreateFile makes an empty file of a type the app edits, and hands
// over its edit lock.
func (s *Server) handleCreateFile(w http.ResponseWriter, r *http.Request) {
	var req struct{ ParentID, Name string }
	if !readJSON(w, r, &req) || !s.editAllowed(w, r) {
		return
	}
	var blank []byte
	if isSheetName(req.Name) {
		var err error
		if blank, err = sheet.Blank(); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	n, id, err := s.files.CreateFile(r.Context(), currentUser(r.Context()).ID, req.ParentID, req.Name, blank)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"node": n, "lockId": id})
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	v, err := s.files.Versions(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleVersionContent downloads an old content. It is always an
// attachment: nobody opens an old version in the page.
func (s *Server) handleVersionContent(w http.ResponseWriter, r *http.Request) {
	f, n, err := s.files.OpenOldVersion(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), r.PathValue("vid"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	serveContent(w, r, f, n, true)
}

func (s *Server) handleRestoreVersion(w http.ResponseWriter, r *http.Request) {
	if !s.editAllowed(w, r) {
		return
	}
	n, err := s.files.RestoreVersion(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), r.PathValue("vid"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "version.restore", n.ID, r.PathValue("vid"))
	writeJSON(w, http.StatusOK, n)
}
