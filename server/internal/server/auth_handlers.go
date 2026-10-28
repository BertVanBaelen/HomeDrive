package server

import (
	"net/http"
	"strings"

	"github.com/BertVanBaelen/HomeDrive/server/internal/accounts"
)

func (s *Server) authRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me", s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, currentUser(r.Context()))
	}))
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/verify", s.handleVerify)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/setup/check", s.handleSetupCheck)
	mux.HandleFunc("POST /api/setup/begin", s.handleSetupBegin)
	mux.HandleFunc("POST /api/setup/finish", s.handleSetupFinish)
	mux.HandleFunc("POST /api/recover/check", s.handleRecoverCheck)
	mux.HandleFunc("POST /api/recover/begin", s.handleRecoverBegin)
	mux.HandleFunc("POST /api/recover/finish", s.handleRecoverFinish)
}

func (s *Server) handleRecoverCheck(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Token string }
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.accounts.CheckRecovery(req.Token); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRecoverBegin(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Token, Email, Password string }
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.accounts.BeginRecovery(r.Context(), req.Token, req.Email, req.Password); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRecoverFinish(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Token, Code string }
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.accounts.FinishRecovery(r.Context(), req.Token, strings.ReplaceAll(req.Code, " ", ""))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Warn("admin password reset through admin_recovery; set admin_recovery back to false", "email", u.Email)
	s.startSession(w, r, u, "admin_recovered")
}

// limitIP counts a sign-in attempt from the client's address and answers
// 429 when there were too many.
func (s *Server) limitIP(w http.ResponseWriter, r *http.Request) bool {
	if ok, wait := s.ipLimit.Allow(ClientIP(r.Context()).String()); !ok {
		s.log.Warn("sign-in rate limit reached", "ip", ClientIP(r.Context()))
		tooManyRequests(w, wait)
		return false
	}
	return true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Email, Password string }
	if !readJSON(w, r, &req) {
		return
	}
	ctx, ip := r.Context(), ClientIP(r.Context()).String()
	account := strings.ToLower(strings.TrimSpace(req.Email))
	if blocked, wait := s.accountLimit.Blocked(account); blocked {
		tooManyRequests(w, wait)
		return
	}
	u, err := s.accounts.Authenticate(ctx, req.Email, req.Password)
	if err != nil {
		if err == accounts.ErrInvalidCredentials {
			s.accountLimit.Add(account)
			s.accounts.Audit(ctx, "", ip, "login_failed", account)
		}
		s.fail(w, r, err)
		return
	}
	challenge, err := s.accounts.SendLoginCode(ctx, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, u.ID, ip, "code_sent", "")
	writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Challenge, Code string }
	if !readJSON(w, r, &req) {
		return
	}
	ctx, ip := r.Context(), ClientIP(r.Context()).String()
	u, err := s.accounts.VerifyLoginCode(ctx, req.Challenge, strings.ReplaceAll(req.Code, " ", ""))
	if err != nil {
		if err == accounts.ErrInvalidCode {
			s.accounts.Audit(ctx, "", ip, "code_failed", "")
		}
		s.fail(w, r, err)
		return
	}
	s.startSession(w, r, u, "login")
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u accounts.User, event string) {
	ctx, ip := r.Context(), ClientIP(r.Context()).String()
	token, err := s.accounts.CreateSession(ctx, u.ID, r.UserAgent(), ip)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.accountLimit.Reset(u.Email)
	s.accounts.Audit(ctx, u.ID, ip, event, "")
	setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.accounts.DeleteSession(r.Context(), c.Value); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetupCheck(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Token string }
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.accounts.CheckSetup(req.Token); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetupBegin(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Token, Email, Name, Password, Locale string }
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.accounts.BeginSetup(r.Context(), req.Token, req.Email, req.Name, req.Password, req.Locale); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetupFinish(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Token, Code string }
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.accounts.FinishSetup(r.Context(), req.Token, strings.ReplaceAll(req.Code, " ", ""))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("admin account created", "email", u.Email)
	s.startSession(w, r, u, "setup_complete")
}
