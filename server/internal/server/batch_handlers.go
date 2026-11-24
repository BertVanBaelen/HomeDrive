package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// maxBatch is the most items one selection request may name.
const maxBatch = 500

// handleBatchNodes applies one ordinary file operation to at most maxBatch
// nodes. Each item goes through the same service call as the single-item
// endpoint, so access is checked per item; items that fail are reported with
// the stable code that endpoint would answer, and the rest still happen.
func (s *Server) handleBatchNodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Op       string   `json:"op"`
		IDs      []string `json:"ids"`
		ParentID string   `json:"parentId,omitempty"`
		Starred  *bool    `json:"starred,omitempty"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBatch {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	switch req.Op {
	case "move", "star", "trash", "restore", "deleteForever":
	default:
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if (req.Op == "move" && req.ParentID == "") || (req.Op == "star" && req.Starred == nil) {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if req.Op == "deleteForever" && !recentAuth(w, r) {
		return
	}

	ctx, uid := r.Context(), currentUser(r.Context()).ID
	ids := s.files.OrderBatchIDs(ctx, uid, req.IDs, req.Op == "restore")
	done := make([]string, 0, len(ids))
	skipped := make([]map[string]string, 0)
	for _, id := range ids {
		var err error
		switch req.Op {
		case "move":
			_, err = s.files.Move(ctx, uid, id, req.ParentID)
		case "star":
			_, err = s.files.SetStar(ctx, uid, id, *req.Starred)
		case "trash":
			err = s.files.Trash(ctx, uid, id)
		case "restore":
			_, err = s.files.Restore(ctx, uid, id)
		case "deleteForever":
			err = s.files.DeleteForever(ctx, uid, id)
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			skipped = append(skipped, map[string]string{"id": id, "code": s.errorCode(r, err)})
			continue
		}
		done = append(done, id)
		// The same audit event as DELETE /api/nodes/{id}?permanent=true.
		if req.Op == "deleteForever" {
			s.accounts.Audit(ctx, uid, ClientIP(ctx).String(), "deleted_forever", id)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"done": done, "skipped": skipped})
}

// errorCode is the stable code fail would answer for err; unexpected errors
// are logged and reported as "internal".
func (s *Server) errorCode(r *http.Request, err error) string {
	for _, e := range apiErrors {
		if errors.Is(err, e.err) {
			return e.code
		}
	}
	s.log.Error("batch item failed", "method", r.Method, "path", logPath(r.URL.Path), "error", err)
	return "internal"
}

// Downloading a selection as one zip takes two requests: POST /api/zips
// checks the selection and returns a single-use address, which the browser
// then opens as a normal download, so the zip streams to disk instead of
// into the page's memory. The address only works for the same user in the
// same session, for zipTicketTTL, once; access is checked again when the
// download starts.

const (
	zipTicketTTL      = 2 * time.Minute
	zipTicketsPerUser = 5
)

var zipTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

type zipTicket struct {
	userID  string
	session [32]byte
	ids     []string
	expires time.Time
}

type zipTickets struct {
	mu sync.Mutex
	m  map[string]zipTicket
}

func newZipTickets() *zipTickets { return &zipTickets{m: map[string]zipTicket{}} }

// add stores t under a new token, unless the user already has too many.
func (z *zipTickets) add(t zipTicket, now time.Time) (string, bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	pending := 0
	for k, v := range z.m {
		if now.After(v.expires) {
			delete(z.m, k)
		} else if v.userID == t.userID {
			pending++
		}
	}
	if pending >= zipTicketsPerUser {
		return "", false
	}
	b := make([]byte, 16)
	rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	z.m[token] = t
	return token, true
}

// take removes and returns the ticket for token, if it is still valid.
func (z *zipTickets) take(token string, now time.Time) (zipTicket, bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	t, ok := z.m[token]
	delete(z.m, token)
	return t, ok && !now.After(t.expires)
}

func sessionDigest(r *http.Request) [32]byte {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return [32]byte{}
	}
	return sha256.Sum256([]byte(c.Value))
}

func (s *Server) handleCreateZip(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBatch {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	uid := currentUser(r.Context()).ID
	if _, err := s.files.ArchiveMany(r.Context(), uid, req.IDs); err != nil {
		s.fail(w, r, err)
		return
	}
	now := time.Now()
	token, ok := s.zipTickets.add(zipTicket{
		userID:  uid,
		session: sessionDigest(r),
		ids:     append([]string(nil), req.IDs...),
		expires: now.Add(zipTicketTTL),
	}, now)
	if !ok {
		tooManyRequests(w, 10*time.Second)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"url": "/api/zips/" + token})
}

func (s *Server) handleGetZip(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if !zipTokenPattern.MatchString(token) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	uid := currentUser(r.Context()).ID
	t, ok := s.zipTickets.take(token, time.Now())
	digest := sessionDigest(r)
	if !ok || t.userID != uid || subtle.ConstantTimeCompare(t.session[:], digest[:]) != 1 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a, err := s.files.ArchiveMany(r.Context(), uid, t.ids)
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
