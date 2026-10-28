package server

import (
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
)

func (s *Server) deviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/me/devices", s.authed(s.handleDevices))
	mux.HandleFunc("POST /api/me/devices", s.authed(s.handleCreateDevice))
	mux.HandleFunc("DELETE /api/me/devices/{id}", s.authed(s.handleRevokeDevice))
	// Device uploads authenticate with a bearer token, never a cookie, so
	// a web page can't make a browser send one.
	mux.HandleFunc("POST /api/inbox", s.handleInboxUpload)
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.accounts.DeviceTokens(r.Context(), currentUser(r.Context()).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	if !recentAuth(w, r) {
		return
	}
	var req struct{ Name string }
	if !readJSON(w, r, &req) {
		return
	}
	ctx, u := r.Context(), currentUser(r.Context())
	d, token, err := s.accounts.CreateDeviceToken(ctx, u.ID, req.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "device_token_created", d.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"device": d, "token": token})
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), currentUser(r.Context())
	if err := s.accounts.RevokeDeviceToken(ctx, u.ID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "device_token_revoked", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

// extensions gives a name an extension when the Shortcut sends a file
// without one.
var extensions = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/heic": ".heic", "image/heif": ".heif",
	"image/gif": ".gif", "image/webp": ".webp", "application/pdf": ".pdf",
	"video/quicktime": ".mov", "video/mp4": ".mp4", "audio/mpeg": ".mp3", "audio/x-m4a": ".m4a",
	"audio/mp4": ".m4a", "text/plain": ".txt", "application/zip": ".zip",
}

// handleInboxUpload takes one file per request:
//
//	POST /api/inbox?name=<file name>
//	Authorization: Bearer hd_…
//	<the file's bytes>
func (s *Server) handleInboxUpload(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_token")
		return
	}
	// Only wrong tokens count against the address, so a phone uploading a
	// stack of photos isn't slowed down.
	ctx := r.Context()
	ip := ClientIP(ctx).String()
	if blocked, wait := s.ipLimit.Blocked(ip); blocked {
		tooManyRequests(w, wait)
		return
	}
	u, deviceID, err := s.accounts.DeviceUser(ctx, strings.TrimSpace(token))
	if err != nil {
		s.ipLimit.Add(ip)
		s.log.Warn("upload with an invalid device token", "ip", ip)
		writeError(w, http.StatusUnauthorized, "invalid_token")
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.Header.Get("X-Filename")
	}
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	if name == "" || name == "." || name == "/" {
		name = "Shared " + time.Now().Format("2006-01-02 15-04-05")
	}
	if path.Ext(name) == "" {
		mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		name += extensions[mt]
	}

	rc := http.NewResponseController(w)
	rc.SetReadDeadline(time.Now().Add(15 * time.Minute))
	n, err := s.files.UploadToInbox(ctx, u.ID, name, http.MaxBytesReader(w, r.Body, files.MaxInboxUpload))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "device_upload", deviceID+" "+n.ID)
	writeJSON(w, http.StatusCreated, n)
}
