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
	"github.com/BertVanBaelen/HomeDrive/server/internal/thumbs"
)

// Saved links (docs/architecture.md §4, "Descriptions and saved links"):
// links shared from a phone become .url files shown as cards. For YouTube
// and TikTok the server asks their oEmbed endpoints for the title, author
// and thumbnail; browsers never load anything from them.

func (s *Server) savedLinkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/link-preview", s.authed(s.handleLinkPreview))
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

// errLinkRateLimited is a link thumbnail that would need a lookup the
// user has no lookups left for.
var errLinkRateLimited = errors.New("link lookups rate limited")

// linkThumb finds a saved link's image at a size. On a miss it asks the
// provider again, because TikTok's thumbnail addresses expire, and makes
// thumbnails of what it gets. Link visitors (no userID) get the cache only.
func (s *Server) linkThumb(ctx context.Context, n files.Node, size thumbs.Size, userID string) (string, error) {
	if p, err := s.thumbs.LinkImage(n.Version, size); err == nil {
		return p, nil
	}
	if n.Link == nil || s.thumbs.Kind(n.Name, n.Version) != thumbs.KindImage {
		return "", thumbs.ErrNone
	}
	u, err := url.Parse(n.Link.URL)
	if err != nil || !savedlink.HasPreview(u) {
		return "", thumbs.ErrNone
	}
	// v2 before thumbnails cached the provider's image as it was.
	if b := s.thumbs.LegacyLink(n.Version); b != nil && s.thumbs.MakeLink(ctx, n.Version, b) == nil {
		return s.thumbs.LinkImage(n.Version, size)
	}
	if userID == "" {
		return "", thumbs.ErrNone
	}
	pv, err := s.previews.Lookup(ctx, userID, u)
	if errors.Is(err, savedlink.ErrRateLimited) {
		return "", errLinkRateLimited
	}
	var b []byte
	if err == nil {
		s.keepAuthor(ctx, n.ID, pv)
		b, _, err = s.previews.Thumbnail(ctx, pv)
	}
	if err == nil {
		err = s.thumbs.MakeLink(ctx, n.Version, b)
	}
	if err != nil {
		if ctx.Err() == nil {
			s.log.Debug("link thumbnail failed", "site", n.Link.Site, "error", err)
			s.thumbs.FailLink(n.Version)
		}
		return "", thumbs.ErrNone
	}
	return s.thumbs.LinkImage(n.Version, size)
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
