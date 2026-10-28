package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/accounts"
	"github.com/BertVanBaelen/HomeDrive/server/internal/auth"
	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
)

const sessionCookie = "__Host-hd_session"

// maxJSONBody is plenty for every JSON request the API takes.
const maxJSONBody = 64 << 10

// readJSON decodes a JSON request body into v. Only application/json is
// accepted: together with the Origin check it keeps plain HTML forms from
// other sites out.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large")
		} else {
			writeError(w, http.StatusBadRequest, "bad_request")
		}
		return false
	}
	return true
}

type sessionKey struct{}

type signedIn struct {
	user    accounts.User
	session accounts.Session
}

// currentUser is the signed-in user of an authed handler.
func currentUser(ctx context.Context) accounts.User {
	s, _ := ctx.Value(sessionKey{}).(signedIn)
	return s.user
}

func currentSession(ctx context.Context) accounts.Session {
	s, _ := ctx.Value(sessionKey{}).(signedIn)
	return s.session
}

// admin is authed for admins only.
func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r.Context()).IsAdmin() {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	})
}

// recentAuth answers 403 reauth_required unless the password was entered in
// the last few minutes; the app then asks for it and retries.
func recentAuth(w http.ResponseWriter, r *http.Request) bool {
	if !currentSession(r.Context()).RecentlyAuthenticated(time.Now()) {
		writeError(w, http.StatusForbidden, "reauth_required")
		return false
	}
	return true
}

// authed lets a request through only with a valid session cookie.
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		u, sess, err := s.accounts.SessionUser(r.Context(), c.Value)
		if err != nil {
			if !errors.Is(err, accounts.ErrNoSession) {
				s.log.Error("loading session", "error", err)
				writeError(w, http.StatusInternalServerError, "internal")
				return
			}
			clearSessionCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, signedIn{u, sess})))
	}
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		MaxAge: int((90 * 24 * time.Hour).Seconds()),
		// Browsers accept Secure cookies from http://localhost, so this
		// also works during development.
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// apiErrors maps domain errors to a status and the stable code the
// frontend translates.
var apiErrors = []struct {
	err    error
	status int
	code   string
}{
	{files.ErrNotFound, http.StatusNotFound, "not_found"},
	{files.ErrReadOnly, http.StatusForbidden, "read_only"},
	{files.ErrInvalidName, http.StatusBadRequest, "invalid_name"},
	{files.ErrNameTaken, http.StatusConflict, "name_taken"},
	{files.ErrNotFolder, http.StatusBadRequest, "not_folder"},
	{files.ErrInvalidMove, http.StatusBadRequest, "invalid_move"},
	{files.ErrRoot, http.StatusBadRequest, "drive_root"},
	{files.ErrTooLarge, http.StatusRequestEntityTooLarge, "too_large"},
	{files.ErrBadOffset, http.StatusConflict, "offset_mismatch"},
	{files.ErrIncomplete, http.StatusConflict, "incomplete"},
	{files.ErrUploadFailed, http.StatusBadRequest, "upload_failed"},
	{files.ErrNotTrashed, http.StatusConflict, "not_trashed"},
	{files.ErrDiskFull, http.StatusInsufficientStorage, "disk_full"},
	{accounts.ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
	{accounts.ErrInvalidCode, http.StatusUnauthorized, "invalid_code"},
	{accounts.ErrTooManyCodes, http.StatusTooManyRequests, "too_many_codes"},
	{accounts.ErrInvalidEmail, http.StatusBadRequest, "invalid_email"},
	{accounts.ErrInvalidName, http.StatusBadRequest, "invalid_name"},
	{accounts.ErrEmailTaken, http.StatusConflict, "email_taken"},
	{accounts.ErrSetupUnavailable, http.StatusForbidden, "setup_unavailable"},
	{accounts.ErrSetupNotStarted, http.StatusConflict, "setup_not_started"},
	{accounts.ErrNotFound, http.StatusNotFound, "not_found"},
	{accounts.ErrInvalidLink, http.StatusForbidden, "link_invalid"},
	{accounts.ErrOwnAccount, http.StatusBadRequest, "own_account"},
	{accounts.ErrLastAdmin, http.StatusConflict, "last_admin"},
	{accounts.ErrUserDisabled, http.StatusConflict, "user_disabled"},
	{accounts.ErrMailFailed, http.StatusBadGateway, "mail_failed"},
	{accounts.ErrInvalidRole, http.StatusBadRequest, "invalid_role"},
	{accounts.ErrWrongPassword, http.StatusForbidden, "wrong_password"},
	{accounts.ErrTooManyDevices, http.StatusConflict, "too_many_devices"},
	{auth.ErrPasswordTooShort, http.StatusBadRequest, "password_too_short"},
	{auth.ErrPasswordTooLong, http.StatusBadRequest, "password_too_long"},
	{auth.ErrPasswordCommon, http.StatusBadRequest, "password_common"},
}

// fail answers with the error's code, or logs it and answers "internal"
// when it is unexpected.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	for _, e := range apiErrors {
		if errors.Is(err, e.err) {
			writeError(w, e.status, e.code)
			return
		}
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal")
}

func tooManyRequests(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	writeError(w, http.StatusTooManyRequests, "rate_limited")
}
