package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
	"github.com/BertVanBaelen/HomeDrive/server/internal/savedlink"
)

// Saved links (docs/architecture.md §4, "Descriptions and saved links"):
// links shared from a phone become .url files shown as cards. For YouTube
// and TikTok the server asks their oEmbed endpoints for the title, author
// and thumbnail; browsers never load anything from them.

func (s *Server) savedLinkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/link-preview", s.authed(s.handleLinkPreview))
	mux.HandleFunc("GET /api/nodes/{id}/thumb", s.authed(s.handleLinkThumb))
	mux.HandleFunc("GET /api/s/{token}/nodes/{id}/thumb", s.visitor(s.handleVisitorThumb))
	// Like POST /api/inbox, for the iPhone Shortcut: a device token, no
	// cookie.
	mux.HandleFunc("POST /api/inbox/link", s.handleInboxLink)
}

// linkPreview is what the Save page shows before a link is saved.
type linkPreview struct {
	URL    string `json:"url"`
	Site   string `json:"site"`
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
}

// handleLinkPreview answers GET /api/link-preview?url=…: the provider's
// title and author for YouTube and TikTok, only the site for others.
// Saving never depends on it.
func (s *Server) handleLinkPreview(w http.ResponseWriter, r *http.Request) {
	u, err := savedlink.Parse(r.URL.Query().Get("url"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_url")
		return
	}
	out := linkPreview{URL: u.String(), Site: savedlink.Site(u)}
	if savedlink.HasPreview(u) {
		pv, err := s.previews.Lookup(r.Context(), currentUser(r.Context()).ID, u)
		switch {
		case errors.Is(err, savedlink.ErrRateLimited):
			writeError(w, http.StatusTooManyRequests, "rate_limited")
			return
		case err != nil:
			s.log.Debug("link preview failed", "site", out.Site, "error", err)
		default:
			out.Title, out.Author = pv.Title, pv.Author
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// describeLink asks the provider about a link that was just saved and
// stores the author it gives. Failures only mean a card without one.
func (s *Server) describeLink(ctx context.Context, userID string, n *files.Node) {
	if n.Link == nil {
		return
	}
	u, err := url.Parse(n.Link.URL)
	if err != nil || !savedlink.HasPreview(u) {
		return
	}
	pv, err := s.previews.Lookup(ctx, userID, u)
	if err != nil {
		s.log.Debug("link preview failed", "site", n.Link.Site, "error", err)
		return
	}
	s.keepAuthor(ctx, n.ID, pv)
	n.Link.Author = pv.Author
}

func (s *Server) keepAuthor(ctx context.Context, nodeID string, pv savedlink.Preview) {
	if err := s.files.SetLinkAuthor(ctx, nodeID, pv.Author); err != nil {
		s.log.Warn("storing a link's author", "error", err)
	}
}

// handleLinkThumb serves a saved link's thumbnail from the cache. On a
// miss it asks the provider again, because TikTok's thumbnail addresses
// expire, and caches what it gets.
func (s *Server) handleLinkThumb(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), currentUser(r.Context()).ID
	d, err := s.files.Get(ctx, uid, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n := d.Node
	if n.Link == nil || n.Version == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if b, err := s.thumbs.Get(n.Version); err == nil {
		serveThumb(w, r, b, n.Version)
		return
	}
	u, err := url.Parse(n.Link.URL)
	if err != nil || !savedlink.HasPreview(u) || s.thumbs.Failed(n.Version) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	pv, err := s.previews.Lookup(ctx, uid, u)
	if errors.Is(err, savedlink.ErrRateLimited) {
		writeError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	var b []byte
	if err == nil {
		s.keepAuthor(ctx, n.ID, pv)
		b, _, err = s.previews.Thumbnail(ctx, pv)
	}
	if err != nil {
		if ctx.Err() == nil {
			s.log.Debug("link thumbnail failed", "site", n.Link.Site, "error", err)
			s.thumbs.Fail(n.Version)
		}
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := s.thumbs.Put(n.Version, b); err != nil {
		s.log.Warn("caching a link thumbnail", "error", err)
	}
	serveThumb(w, r, b, n.Version)
}

// handleVisitorThumb serves a cached thumbnail to a link visitor. Visitors
// never make Homedrive ask a provider.
func (s *Server) handleVisitorThumb(w http.ResponseWriter, r *http.Request, v files.Visit) {
	page, err := s.files.LinkPage(r.Context(), v, visitNode(r, v))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	b, err := s.thumbs.Get(page.Node.Version)
	if page.Node.Link == nil || err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	serveThumb(w, r, b, page.Node.Version)
}

// serveThumb sends a cached image under the same rules as file contents.
func serveThumb(w http.ResponseWriter, r *http.Request, b []byte, version string) {
	ctype := savedlink.ImageType(b)
	if ctype == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+version+`"`)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, `"`+version+`"`) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(b)
}

// handleInboxLink saves a link from the iPhone Shortcut to the Inbox:
//
//	POST /api/inbox/link
//	Authorization: Bearer hd_…
//	{"url": "https://…", "title": "…", "text": "…"}
//
// Without a url the first address in the text is used. The title is the
// shared one, else the provider's, else the host; the text becomes the
// description.
func (s *Server) handleInboxLink(w http.ResponseWriter, r *http.Request) {
	u, deviceID, ok := s.deviceUser(w, r)
	if !ok {
		return
	}
	var req struct{ URL, Title, Text string }
	if !readJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	text := req.Text
	var link *url.URL
	var err error
	if strings.TrimSpace(req.URL) != "" {
		link, err = savedlink.Parse(req.URL)
	} else if link, text = savedlink.Find(text); link == nil {
		err = savedlink.ErrInvalidURL
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_url")
		return
	}
	title := strings.TrimSpace(req.Title)
	var pv savedlink.Preview
	var looked bool
	if savedlink.HasPreview(link) {
		lookupCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		pv, err = s.previews.Lookup(lookupCtx, u.ID, link)
		cancel()
		looked = err == nil
	}
	if title == "" {
		title = pv.Title
	}
	if title == "" {
		title = savedlink.Title(link)
	}
	n, err := s.files.SaveLinkToInbox(ctx, u.ID, link, title, text)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if looked && n.Link != nil {
		s.keepAuthor(ctx, n.ID, pv)
		n.Link.Author = pv.Author
	}
	s.accounts.Audit(ctx, u.ID, ClientIP(ctx).String(), "device_upload", deviceID+" "+n.ID)
	writeJSON(w, http.StatusCreated, n)
}
