package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/BertVanBaelen/HomeDrive/server/internal/accounts"
)

func (s *Server) accountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/reauth", s.authed(s.handleReauth))
	mux.HandleFunc("PATCH /api/me", s.authed(s.handleUpdateMe))
	mux.HandleFunc("POST /api/me/password", s.authed(s.handleChangePassword))
	mux.HandleFunc("GET /api/me/sessions", s.authed(s.handleSessions))
	mux.HandleFunc("DELETE /api/me/sessions/{id}", s.authed(s.handleRevokeSession))

	mux.HandleFunc("GET /api/admin/users", s.admin(s.handleListUsers))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.handleInviteUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", s.admin(s.handleUpdateUser))
	mux.HandleFunc("POST /api/admin/users/{id}/link", s.admin(s.handleSendLink))

	mux.HandleFunc("POST /api/welcome/check", s.handleWelcomeCheck)
	mux.HandleFunc("POST /api/welcome/finish", s.handleWelcomeFinish)
}

// checkPasswordAttempt counts a password entered by a signed-in user
// against the same limits as sign-in.
func (s *Server) checkPasswordAttempt(r *http.Request, err error) {
	if errors.Is(err, accounts.ErrWrongPassword) {
		u := currentUser(r.Context())
		s.accountLimit.Add(u.Email)
		s.accounts.Audit(r.Context(), u.ID, ClientIP(r.Context()).String(), "password_failed", "")
	}
}

func (s *Server) passwordAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !s.limitIP(w, r) {
		return false
	}
	if blocked, wait := s.accountLimit.Blocked(currentUser(r.Context()).Email); blocked {
		tooManyRequests(w, wait)
		return false
	}
	return true
}

func (s *Server) handleReauth(w http.ResponseWriter, r *http.Request) {
	if !s.passwordAllowed(w, r) {
		return
	}
	var req struct{ Password string }
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.accounts.Reauth(r.Context(), currentSession(r.Context()), req.Password); err != nil {
		s.checkPasswordAttempt(r, err)
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   *string `json:"name"`
		Locale *string `json:"locale"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.accounts.UpdateProfile(r.Context(), currentUser(r.Context()).ID, req.Name, req.Locale)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if !s.passwordAllowed(w, r) {
		return
	}
	var req struct{ Current, Password string }
	if !readJSON(w, r, &req) {
		return
	}
	ctx, u := r.Context(), currentUser(r.Context())
	if err := s.accounts.ChangePassword(ctx, u, currentSession(ctx), req.Current, req.Password); err != nil {
		s.checkPasswordAttempt(r, err)
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "password_changed", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.accounts.Sessions(r.Context(), currentSession(r.Context()))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRevokeSession signs out a device. It needs no fresh password:
// signing out a lost phone should be quick.
func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), currentUser(r.Context())
	if err := s.accounts.RevokeSession(ctx, u.ID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "session_revoked", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.accounts.ListUsers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleInviteUser(w http.ResponseWriter, r *http.Request) {
	if !recentAuth(w, r) {
		return
	}
	var req struct {
		Email, Name, Role, Locale string
		// DriveIDs are the shared drives the new user joins; without it,
		// the drives marked join by default.
		DriveIDs []string `json:"driveIds"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ctx, admin := r.Context(), currentUser(r.Context())
	u, err := s.accounts.InviteUser(ctx, admin, req.Email, req.Name, req.Role, req.Locale, req.DriveIDs)
	if err != nil && !errors.Is(err, accounts.ErrMailFailed) {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, admin.ID, ClientIP(ctx).String(), "user_invited", u.ID)
	if err != nil {
		// The account exists; tell the admin the email didn't go out.
		writeJSON(w, http.StatusCreated, map[string]any{"user": u, "error": "mail_failed"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": u})
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !recentAuth(w, r) {
		return
	}
	var req accounts.UserPatch
	if !readJSON(w, r, &req) {
		return
	}
	ctx, admin := r.Context(), currentUser(r.Context())
	u, err := s.accounts.UpdateUser(ctx, admin, r.PathValue("id"), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var changes []string
	if req.Name != nil {
		changes = append(changes, "name")
	}
	if req.Role != nil {
		changes = append(changes, "role="+*req.Role)
	}
	if req.Disabled != nil {
		changes = append(changes, map[bool]string{true: "disabled", false: "enabled"}[*req.Disabled])
	}
	s.accounts.Audit(ctx, admin.ID, ClientIP(ctx).String(), "user_updated", u.ID+" "+strings.Join(changes, ","))
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleSendLink(w http.ResponseWriter, r *http.Request) {
	if !recentAuth(w, r) {
		return
	}
	ctx, admin := r.Context(), currentUser(r.Context())
	if err := s.accounts.SendPasswordLink(ctx, admin, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, admin.ID, ClientIP(ctx).String(), "password_link_sent", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWelcomeCheck(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Token string }
	if !readJSON(w, r, &req) {
		return
	}
	link, err := s.accounts.CheckLink(r.Context(), req.Token)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

func (s *Server) handleWelcomeFinish(w http.ResponseWriter, r *http.Request) {
	if !s.limitIP(w, r) {
		return
	}
	var req struct{ Token, Password string }
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.accounts.AcceptLink(r.Context(), req.Token, req.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.startSession(w, r, u, "password_set")
}

func (s *Server) handleBackupStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.backup.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleBackupNow starts a backup in the background; the page polls the
// status to see it finish.
func (s *Server) handleBackupNow(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		writeError(w, http.StatusConflict, "backup_disabled")
		return
	}
	if err := s.backup.Start(); err != nil {
		writeError(w, http.StatusConflict, "backup_running")
		return
	}
	ctx := r.Context()
	s.accounts.Audit(ctx, currentUser(ctx).ID, ClientIP(ctx).String(), "backup_started", "")
	w.WriteHeader(http.StatusAccepted)
}
