package savedlink

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/ratelimit"
)

// provider is an oEmbed endpoint and the hosts it answers for. thumbHosts
// are where its thumbnails may come from: exact hosts, or suffixes
// starting with a dot.
type provider struct {
	name       string
	endpoint   string
	hosts      []string
	thumbHosts []string
}

// providers is the fixed list. Instagram's oEmbed needs a Facebook
// developer app, so it isn't on it.
var providers = []*provider{
	{
		name:       "YouTube",
		endpoint:   "https://www.youtube.com/oembed",
		hosts:      []string{"youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be"},
		thumbHosts: []string{"i.ytimg.com"},
	},
	{
		name:       "TikTok",
		endpoint:   "https://www.tiktok.com/oembed",
		hosts:      []string{"tiktok.com", "www.tiktok.com", "m.tiktok.com", "vm.tiktok.com", "vt.tiktok.com"},
		thumbHosts: []string{".tiktokcdn.com", ".tiktokcdn-us.com"},
	},
}

func providerOf(u *url.URL) *provider {
	host := strings.ToLower(u.Hostname())
	for _, p := range providers {
		for _, h := range p.hosts {
			if host == h {
				return p
			}
		}
	}
	return nil
}

// HasPreview reports whether a link's site has an oEmbed provider.
func HasPreview(u *url.URL) bool { return providerOf(u) != nil }

func (p *provider) allowsThumb(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range p.thumbHosts {
		if host == h || strings.HasPrefix(h, ".") && strings.HasSuffix(host, h) && len(host) > len(h) {
			return true
		}
	}
	return false
}

const (
	timeout   = 5 * time.Second
	maxAnswer = 64 << 10
	// MaxThumb is the largest thumbnail fetched from a provider.
	MaxThumb = 2 << 20
)

var (
	// ErrNoProvider is for links to sites without an oEmbed provider.
	ErrNoProvider  = errors.New("no preview for this site")
	ErrRateLimited = errors.New("too many link lookups")
	ErrLookup      = errors.New("link lookup failed")
)

// Preview is what a provider tells about a link.
type Preview struct {
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	Site   string `json:"site"`
	// thumb is the provider's thumbnail address, already checked against
	// its list of hosts. Browsers never get it.
	thumb *url.URL
	p     *provider
}

// Client asks the providers. Each user may make 30 lookups a minute.
type Client struct {
	http  *http.Client
	limit *ratelimit.Limiter
}

// NewClient makes a client; a nil transport means the default one. Tests
// pass their own so no request leaves the machine.
func NewClient(transport http.RoundTripper) *Client {
	return &Client{
		http: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			// Redirects could lead anywhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		limit: ratelimit.New(30, time.Minute),
	}
}

// Lookup asks the link's provider about it, on behalf of a user.
func (c *Client) Lookup(ctx context.Context, userID string, u *url.URL) (Preview, error) {
	p := providerOf(u)
	if p == nil {
		return Preview{}, ErrNoProvider
	}
	if ok, _ := c.limit.Allow(userID); !ok {
		return Preview{}, ErrRateLimited
	}
	q := url.Values{"url": {lookupURL(u)}, "format": {"json"}}
	body, err := c.get(ctx, p.endpoint+"?"+q.Encode(), maxAnswer)
	if err != nil {
		return Preview{}, err
	}
	var answer struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return Preview{}, errors.Join(ErrLookup, err)
	}
	pv := Preview{Title: clean(answer.Title, 300), Author: clean(answer.AuthorName, 100), Site: p.name, p: p}
	if t, err := url.Parse(answer.ThumbnailURL); err == nil && answer.ThumbnailURL != "" && p.allowsThumb(t) {
		pv.thumb = t
	}
	return pv, nil
}

// lookupURL is the address sent to the provider. YouTube's endpoint
// doesn't know Shorts addresses, so those are asked as normal videos.
func lookupURL(u *url.URL) string {
	if id, ok := strings.CutPrefix(u.Path, "/shorts/"); ok && providerOf(u).name == "YouTube" && id != "" {
		id, _, _ = strings.Cut(id, "/")
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(id)
	}
	return u.String()
}

// Thumbnail fetches a preview's thumbnail: an image of at most MaxThumb
// bytes from one of the provider's hosts. It returns the bytes and their
// type, decided from the bytes themselves.
func (c *Client) Thumbnail(ctx context.Context, pv Preview) ([]byte, string, error) {
	if pv.thumb == nil {
		return nil, "", ErrNoProvider
	}
	body, err := c.get(ctx, pv.thumb.String(), MaxThumb)
	if err != nil {
		return nil, "", err
	}
	ctype := ImageType(body)
	if ctype == "" {
		return nil, "", ErrLookup
	}
	return body, ctype, nil
}

// ImageType is the type of an image browsers can show, or "" when the
// bytes are something else.
func ImageType(b []byte) string {
	switch t := http.DetectContentType(b); t {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return t
	}
	return ""
}

func (c *Client) get(ctx context.Context, target string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, errors.Join(ErrLookup, err)
	}
	req.Header.Set("User-Agent", "Homedrive")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Join(ErrLookup, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, ErrLookup
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, errors.Join(ErrLookup, err)
	}
	if int64(len(body)) > limit {
		return nil, ErrLookup
	}
	return body, nil
}

// clean keeps a provider's text to one line of at most max characters.
func clean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}
