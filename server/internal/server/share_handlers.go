package server

import (
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
)

func (s *Server) shareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/shared", s.authed(s.handleSharedWithMe))
	mux.HandleFunc("GET /api/nodes/{id}/shares", s.authed(s.handleSharing))
	mux.HandleFunc("PUT /api/nodes/{id}/shares/{user}", s.authed(s.handleSetShare))
	mux.HandleFunc("DELETE /api/nodes/{id}/shares/{user}", s.authed(s.handleRemoveShare))
	mux.HandleFunc("GET /api/nodes/{id}/link", s.authed(s.handleLink))
	mux.HandleFunc("PUT /api/nodes/{id}/link", s.authed(s.handleSetLink))
	mux.HandleFunc("DELETE /api/nodes/{id}/link", s.authed(s.handleRemoveLink))
	mux.HandleFunc("GET /api/nodes/{id}/zip", s.authed(s.handleZip))
}

func (s *Server) handleSharedWithMe(w http.ResponseWriter, r *http.Request) {
	list, err := s.files.SharedWithMe(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSharing(w http.ResponseWriter, r *http.Request) {
	sh, err := s.files.Sharing(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

func (s *Server) handleSetShare(w http.ResponseWriter, r *http.Request) {
	var req struct{ Access string }
	if !readJSON(w, r, &req) {
		return
	}
	id, person := r.PathValue("id"), r.PathValue("user")
	change, err := s.files.SetShare(r.Context(), currentUser(r.Context()).ID, id, person, req.Access)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if change != files.ShareSame {
		s.audit(r, string(change), id, person, req.Access)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveShare(w http.ResponseWriter, r *http.Request) {
	id, person := r.PathValue("id"), r.PathValue("user")
	if err := s.files.RemoveShare(r.Context(), currentUser(r.Context()).ID, id, person); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, string(files.ShareRemoved), id, person)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	l, err := s.files.Link(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// handleSetLink makes a link or changes it: {expiry?, password?}. An
// empty password removes it; a missing one leaves it as it is.
func (s *Server) handleSetLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expiry   *string `json:"expiry"`
		Password *string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ctx, uid, id := r.Context(), currentUser(r.Context()).ID, r.PathValue("id")
	if req.Expiry != nil {
		if err := files.ValidExpiry(*req.Expiry); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	set := files.LinkSettings{Expiry: req.Expiry}
	if req.Password != nil {
		hash := ""
		if *req.Password != "" {
			if err := files.ValidLinkPassword(*req.Password); err != nil {
				s.fail(w, r, err)
				return
			}
			// Check access before spending a password hash on the request.
			if _, err := s.files.Link(ctx, uid, id); err != nil {
				s.fail(w, r, err)
				return
			}
			var err error
			if hash, err = s.accounts.Hasher().Hash(ctx, *req.Password); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		set.PasswordHash = &hash
	}
	l, change, err := s.files.SetLink(ctx, uid, id, set)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if change != files.LinkSame {
		detail := []string{id}
		if req.Expiry != nil {
			detail = append(detail, "expiry="+*req.Expiry)
		}
		if req.Password != nil {
			if *req.Password != "" {
				detail = append(detail, "password=set")
			} else {
				detail = append(detail, "password=removed")
			}
		}
		s.audit(r, string(change), detail...)
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) handleRemoveLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	change, err := s.files.RemoveLink(r.Context(), currentUser(r.Context()).ID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if change != files.LinkSame {
		s.audit(r, string(change), id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Zips running at the same time, per user and for link visitors.
const (
	zipsPerUser    = 2
	zipsPerVisitor = 1
	zipsForLinks   = 2
)

func (s *Server) handleZip(w http.ResponseWriter, r *http.Request) {
	uid := currentUser(r.Context()).ID
	a, err := s.files.Archive(r.Context(), uid, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.zips.enter("user:"+uid, zipsPerUser) {
		tooManyRequests(w, 10*time.Second)
		return
	}
	defer s.zips.leave("user:" + uid)
	s.sendZip(w, r, a)
}

// sendZip streams a folder as a zip. Its size isn't known in advance. When
// something fails halfway, the connection is cut so the browser reports
// the download as failed instead of saving half a zip.
func (s *Server) sendZip(w http.ResponseWriter, r *http.Request, a files.Archive) {
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	if cd := mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}); cd != "" {
		h.Set("Content-Disposition", cd)
	} else {
		h.Set("Content-Disposition", "attachment")
	}
	h.Set("Cache-Control", "no-store")
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(6 * time.Hour))
	w.WriteHeader(http.StatusOK)
	if err := s.files.WriteZip(r.Context(), w, a); err != nil {
		if r.Context().Err() == nil {
			s.log.Error("zip failed", "path", logPath(r.URL.Path), "error", err)
		}
		panic(http.ErrAbortHandler)
	}
}

// gate counts work in progress per key.
type gate struct {
	mu sync.Mutex
	n  map[string]int
}

func newGate() *gate { return &gate{n: map[string]int{}} }

// enter reserves a place for key unless max are taken.
func (g *gate) enter(key string, max int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n[key] >= max {
		return false
	}
	g.n[key]++
	return true
}

func (g *gate) leave(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n[key]--; g.n[key] <= 0 {
		delete(g.n, key)
	}
}
