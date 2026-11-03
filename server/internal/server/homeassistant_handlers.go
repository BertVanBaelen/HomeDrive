package server

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/accounts"
)

// Home Assistant login (add-on only, opt-in): the Supervisor checks a Home
// Assistant username and password, and the account linked to that username
// then gets the usual email code. s.ha is nil when it is off.

func (s *Server) homeAssistantRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/options", s.handleAuthOptions)
	mux.HandleFunc("POST /api/auth/homeassistant", s.handleHomeAssistantLogin)
	mux.HandleFunc("GET /api/me/homeassistant", s.authed(s.handleHomeAssistantLink))
	mux.HandleFunc("PUT /api/me/homeassistant", s.authed(s.handleLinkHomeAssistant))
	mux.HandleFunc("DELETE /api/me/homeassistant", s.authed(s.handleUnlinkHomeAssistant))
}

// handleAuthOptions tells the login page which ways to log in there are.
func (s *Server) handleAuthOptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"homeAssistant": s.ha != nil})
}

// haPasswordOK asks the Supervisor about a Home Assistant password, within
// the sign-in limits: per client address, and per Home Assistant username
// as the account. It answers the request itself unless the password is
// right.
func (s *Server) haPasswordOK(w http.ResponseWriter, r *http.Request, userID, typed, password string) (string, bool) {
	ctx, ip := r.Context(), ClientIP(r.Context()).String()
	username, err := accounts.NormalizeHAUsername(typed)
	account := "ha:" + username
	if blocked, wait := s.accountLimit.Blocked(account); blocked {
		tooManyRequests(w, wait)
		return "", false
	}
	ok := false
	if err == nil && password != "" && len(password) <= 1024 {
		if ok, err = s.ha.CheckPassword(ctx, strings.TrimSpace(typed), password); err != nil {
			s.log.Warn("checking a Home Assistant password", "error", err)
			writeError(w, http.StatusServiceUnavailable, "homeassistant_unavailable")
			return "", false
		}
	}
	if !ok {
		s.accountLimit.Add(account)
		event := "login.homeassistant"
		if userID != "" {
			event = "homeassistant_link_failed"
		}
		s.accounts.Audit(ctx, userID, ip, event, "failed "+username)
		writeError(w, http.StatusUnauthorized, "ha_invalid_credentials")
		return "", false
	}
	return username, true
}

// handleHomeAssistantLogin is the password step of a Home Assistant login.
// The Supervisor is asked before the link is looked up, so unknown,
// unlinked and disabled users get the same answer as a wrong password, in
// about the same time.
func (s *Server) handleHomeAssistantLogin(w http.ResponseWriter, r *http.Request) {
	if s.ha == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Username, Password string }
	if !readJSON(w, r, &req) {
		return
	}
	ctx, ip := r.Context(), ClientIP(r.Context()).String()
	username, ok := s.haPasswordOK(w, r, "", req.Username, req.Password)
	if !ok {
		return
	}
	u, err := s.accounts.HAUser(ctx, username)
	if errors.Is(err, accounts.ErrInvalidCredentials) {
		s.accountLimit.Add("ha:" + username)
		s.accounts.Audit(ctx, "", ip, "login.homeassistant", "failed "+username)
		writeError(w, http.StatusUnauthorized, "ha_invalid_credentials")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	challenge, err := s.accounts.SendLoginCode(ctx, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.accountLimit.Reset("ha:" + username)
	s.accounts.Audit(ctx, u.ID, ip, "login.homeassistant", username)
	writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge, "email": maskEmail(u.Email)})
}

// maskEmail shows where the code went without giving the whole address to
// someone who only has the Home Assistant password: a…@example.com.
func maskEmail(email string) string {
	local, domain, _ := strings.Cut(email, "@")
	r, _ := utf8.DecodeRuneInString(local)
	return string(r) + "…@" + domain
}

func (s *Server) handleHomeAssistantLink(w http.ResponseWriter, r *http.Request) {
	if s.ha == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	name, err := s.accounts.HAUsername(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": name})
}

// handleLinkHomeAssistant links the signed-in user to their Home Assistant
// account. It needs the Homedrive password recently, and the Home Assistant
// password to prove the username is theirs.
func (s *Server) handleLinkHomeAssistant(w http.ResponseWriter, r *http.Request) {
	if s.ha == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if !recentAuth(w, r) || !s.limitIP(w, r) {
		return
	}
	var req struct{ Username, Password string }
	if !readJSON(w, r, &req) {
		return
	}
	ctx, u := r.Context(), currentUser(r.Context())
	if _, err := accounts.NormalizeHAUsername(req.Username); err != nil {
		s.fail(w, r, err)
		return
	}
	username, ok := s.haPasswordOK(w, r, u.ID, req.Username, req.Password)
	if !ok {
		return
	}
	if err := s.accounts.SetHAUsername(ctx, u.ID, username); err != nil {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "homeassistant_linked", username)
	writeJSON(w, http.StatusOK, map[string]string{"username": username})
}

func (s *Server) handleUnlinkHomeAssistant(w http.ResponseWriter, r *http.Request) {
	if s.ha == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	ctx, u := r.Context(), currentUser(r.Context())
	if err := s.accounts.SetHAUsername(ctx, u.ID, ""); err != nil {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "homeassistant_unlinked", "")
	w.WriteHeader(http.StatusNoContent)
}
