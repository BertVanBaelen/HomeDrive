package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
)

// Public link pages (docs/architecture.md §4, "Public links"). Visitors
// aren't signed in: the token in the address is their only key, plus a
// grant cookie when the link has a password.

func (s *Server) linkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/s/{token}", s.visitor(s.handleLinkPage))
	mux.HandleFunc("GET /api/s/{token}/nodes/{id}", s.visitor(s.handleLinkPage))
	mux.HandleFunc("GET /api/s/{token}/nodes/{id}/content", s.visitor(s.handleLinkContent))
	mux.HandleFunc("GET /api/s/{token}/nodes/{id}/zip", s.visitor(s.handleLinkZip))
	mux.HandleFunc("POST /api/s/{token}/unlock", s.handleUnlock)
}

// linkGrantAge is how long a link's password is remembered.
const linkGrantAge = 12 * time.Hour

func linkCookie(linkID string) string { return "__Host-hd_link_" + linkID }

// grant is the cookie value that proves the password of a link was given.
// It covers the password hash, so a new password ends earlier grants.
func (s *Server) grant(v files.Visit) string {
	mac := hmac.New(sha256.New, s.grantKey)
	mac.Write([]byte(v.LinkID))
	mac.Write([]byte{0})
	mac.Write([]byte(v.PasswordHash))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) granted(r *http.Request, v files.Visit) bool {
	if v.PasswordHash == "" {
		return true
	}
	c, err := r.Cookie(linkCookie(v.LinkID))
	return err == nil && hmac.Equal([]byte(c.Value), []byte(s.grant(v)))
}

// visit finds the link of a request. Every way a link can fail gets the
// same answer, and each failure counts against the client address.
func (s *Server) visit(w http.ResponseWriter, r *http.Request) (files.Visit, bool) {
	w.Header().Set("Cache-Control", "no-store")
	ip := ClientIP(r.Context()).String()
	if blocked, wait := s.linkLimit.Blocked(ip); blocked {
		tooManyRequests(w, wait)
		return files.Visit{}, false
	}
	v, err := s.files.Visit(r.Context(), r.PathValue("token"))
	if errors.Is(err, files.ErrNotFound) {
		s.linkLimit.Add(ip)
		writeError(w, http.StatusNotFound, "link_dead")
		return files.Visit{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return files.Visit{}, false
	}
	return v, true
}

// visitor serves a link request once the link works and its password, if
// any, was given.
func (s *Server) visitor(next func(http.ResponseWriter, *http.Request, files.Visit)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.visit(w, r)
		if !ok {
			return
		}
		if !s.granted(r, v) {
			writeError(w, http.StatusUnauthorized, "link_locked")
			return
		}
		next(w, r, v)
	}
}

// visitNode is the node a link request names: {id}, or the linked item.
func visitNode(r *http.Request, v files.Visit) string {
	if id := r.PathValue("id"); id != "" {
		return id
	}
	return v.NodeID
}

func (s *Server) handleLinkPage(w http.ResponseWriter, r *http.Request, v files.Visit) {
	page, err := s.files.LinkPage(r.Context(), v, visitNode(r, v))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleLinkContent(w http.ResponseWriter, r *http.Request, v files.Visit) {
	f, n, err := s.files.LinkOpen(r.Context(), v, visitNode(r, v))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	serveContent(w, r, f, n)
}

func (s *Server) handleLinkZip(w http.ResponseWriter, r *http.Request, v files.Visit) {
	a, err := s.files.LinkArchive(r.Context(), v, visitNode(r, v))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ip := "ip:" + ClientIP(r.Context()).String()
	if !s.zips.enter(ip, zipsPerVisitor) {
		tooManyRequests(w, 10*time.Second)
		return
	}
	defer s.zips.leave(ip)
	if !s.zips.enter("links", zipsForLinks) {
		tooManyRequests(w, 10*time.Second)
		return
	}
	defer s.zips.leave("links")
	s.sendZip(w, r, a)
}

// handleUnlock checks a link's password and, when it is right, sets the
// grant cookie: {password}.
func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	v, ok := s.visit(w, r)
	if !ok {
		return
	}
	if v.PasswordHash == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	perClient := v.LinkID + " " + ClientIP(r.Context()).String()
	blocked, wait := s.linkPassLimit.Blocked(perClient)
	if !blocked {
		blocked, wait = s.linkPassTotal.Blocked(v.LinkID)
	}
	if blocked {
		tooManyRequests(w, wait)
		return
	}
	var req struct{ Password string }
	if !readJSON(w, r, &req) {
		return
	}
	ok, err := s.accounts.Hasher().Verify(r.Context(), v.PasswordHash, req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		s.linkPassLimit.Add(perClient)
		s.linkPassTotal.Add(v.LinkID)
		writeError(w, http.StatusForbidden, "wrong_password")
		return
	}
	s.linkPassLimit.Reset(perClient)
	http.SetCookie(w, &http.Cookie{
		Name: linkCookie(v.LinkID), Value: s.grant(v), Path: "/",
		MaxAge: int(linkGrantAge.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// logPath hides link tokens from logs: /s/<token>/… and /api/s/<token>/…
// are written with … in place of the token.
func logPath(p string) string {
	for _, prefix := range []string{"/s/", "/api/s/"} {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			if i := strings.IndexByte(rest, '/'); i >= 0 {
				return prefix + "…" + rest[i:]
			}
			return prefix + "…"
		}
	}
	return p
}
