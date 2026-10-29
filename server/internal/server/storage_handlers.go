package server

import (
	"net/http"
	"strconv"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
)

func (s *Server) storageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/storage", s.authed(s.handleStorage))
	mux.HandleFunc("GET /api/storage/meter", s.authed(s.handleStorageMeter))
	mux.HandleFunc("DELETE /api/old-versions", s.authed(s.handleDeleteOldVersions))
	mux.HandleFunc("GET /api/admin/storage", s.admin(s.handleHouseholdStorage))
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	st, err := s.files.Storage(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		files.Storage
		// Old versions go by themselves after this many days.
		RetentionDays int `json:"retentionDays"`
	}{st, s.cfg.BackupRetentionDays})
}

// handleStorageMeter is the meter in the sidebar: the user's My Files,
// and for admins the data disk.
func (s *Server) handleStorageMeter(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), currentUser(r.Context())
	var m struct {
		Used int64       `json:"used"`
		Disk *files.Disk `json:"disk,omitempty"`
	}
	var err error
	if m.Used, err = s.files.MyFilesSize(ctx, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	if u.IsAdmin() {
		if m.Disk, err = s.files.Disk(ctx); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleHouseholdStorage(w http.ResponseWriter, r *http.Request) {
	h, err := s.files.HouseholdStorage(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDeleteOldVersions(w http.ResponseWriter, r *http.Request) {
	if !recentAuth(w, r) {
		return
	}
	n, freed, err := s.files.DeleteOldVersions(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "versions.delete", strconv.Itoa(n), strconv.FormatInt(freed, 10))
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": int64(n), "freed": freed})
}
