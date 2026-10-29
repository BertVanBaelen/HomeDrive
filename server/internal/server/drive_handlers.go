package server

import (
	"net/http"
	"strings"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
)

func (s *Server) driveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/drives", s.authed(s.handleCreateDrive))
	mux.HandleFunc("PATCH /api/drives/{id}", s.authed(s.handleRenameDrive))
	mux.HandleFunc("DELETE /api/drives/{id}", s.authed(s.handleDeleteDrive))
	mux.HandleFunc("GET /api/drives/{id}/members", s.authed(s.handleMembers))
	mux.HandleFunc("PUT /api/drives/{id}/members/{user}", s.authed(s.handleSetMember(false)))
	mux.HandleFunc("DELETE /api/drives/{id}/members/{user}", s.authed(s.handleRemoveMember(false)))
	mux.HandleFunc("GET /api/people", s.authed(s.handlePeople))

	mux.HandleFunc("GET /api/admin/drives", s.admin(s.handleAdminDrives))
	mux.HandleFunc("PATCH /api/admin/drives/{id}", s.admin(s.handleAdminPatchDrive))
	mux.HandleFunc("PUT /api/admin/drives/{id}/members/{user}", s.admin(s.handleSetMember(true)))
	mux.HandleFunc("DELETE /api/admin/drives/{id}/members/{user}", s.admin(s.handleRemoveMember(true)))
}

func (s *Server) audit(r *http.Request, event string, detail ...string) {
	ctx := r.Context()
	s.accounts.Audit(ctx, currentUser(ctx).ID, ClientIP(ctx).String(), event, strings.Join(detail, " "))
}

func (s *Server) handleCreateDrive(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if !readJSON(w, r, &req) {
		return
	}
	d, err := s.files.CreateDrive(r.Context(), currentUser(r.Context()).ID, req.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "drive.create", d.ID)
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleRenameDrive(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if !readJSON(w, r, &req) {
		return
	}
	d, err := s.files.RenameDrive(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), req.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "drive.rename", d.ID)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeleteDrive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.files.DeleteDrive(r.Context(), currentUser(r.Context()).ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "drive.delete", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request) {
	list, err := s.files.Members(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// memberAudit records a membership change; admin changes say so.
func (s *Server) memberAudit(r *http.Request, change files.MemberChange, role string, asAdmin bool) {
	if change == files.MemberSame {
		return
	}
	detail := []string{r.PathValue("id"), string(change), r.PathValue("user")}
	if role != "" {
		detail = append(detail, role)
	}
	if asAdmin {
		detail = append(detail, "admin")
	}
	s.audit(r, "drive.member", detail...)
}

func (s *Server) handleSetMember(asAdmin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Role string }
		if !readJSON(w, r, &req) {
			return
		}
		change, err := s.files.SetMember(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), r.PathValue("user"), req.Role, asAdmin)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.memberAudit(r, change, req.Role, asAdmin)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleRemoveMember(asAdmin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		change, err := s.files.RemoveMember(r.Context(), currentUser(r.Context()).ID, r.PathValue("id"), r.PathValue("user"), asAdmin)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.memberAudit(r, change, "", asAdmin)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	list, err := s.accounts.People(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleAdminDrives(w http.ResponseWriter, r *http.Request) {
	list, err := s.files.AdminDrives(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleAdminPatchDrive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JoinByDefault *bool `json:"joinByDefault"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.JoinByDefault == nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := s.files.SetJoinByDefault(r.Context(), r.PathValue("id"), *req.JoinByDefault); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "drive.default", r.PathValue("id"), map[bool]string{true: "on", false: "off"}[*req.JoinByDefault])
	w.WriteHeader(http.StatusNoContent)
}
