package savedlink

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestParse(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://www.youtube.com/watch?v=abc":                true,
		"  http://Example.COM/a b  ":                         true,
		"http://example.com/a%20b":                           true,
		"ftp://example.com/file":                             false,
		"javascript:alert(1)":                                false,
		"https://user:pw@example.com/":                       false,
		"https:///path":                                      false,
		"example.com":                                        false,
		"https://example.com/\x00":                           false,
		"https://example.com/" + strings.Repeat("a", MaxURL): false,
	} {
		if _, err := Parse(raw); (err == nil) != ok {
			t.Errorf("Parse(%q) error = %v, want ok %v", raw, err, ok)
		}
	}
	u, _ := Parse("HTTPS://WWW.Example.com/Path")
	if u.String() != "https://www.example.com/Path" {
		t.Errorf("host not lower-cased: %s", u)
	}
}

func TestFind(t *testing.T) {
	u, rest := Find("Look at this! https://vm.tiktok.com/ZMabc/ #cats")
	if u == nil || u.String() != "https://vm.tiktok.com/ZMabc/" || rest != "Look at this! #cats" {
		t.Fatalf("Find = %v, %q", u, rest)
	}
	u, rest = Find("Watch (https://youtu.be/xyz).")
	if u == nil || u.String() != "https://youtu.be/xyz" || rest != "Watch ()." {
		t.Fatalf("Find in brackets = %v, %q", u, rest)
	}
	if u, rest := Find("no link here"); u != nil || rest != "no link here" {
		t.Fatalf("Find without a link = %v, %q", u, rest)
	}
}

func TestShortcutRoundTrip(t *testing.T) {
	u, _ := Parse("https://example.com/a?b=c&d=e")
	got, err := Read(Shortcut(u))
	if err != nil || got.String() != u.String() {
		t.Fatalf("Read(Shortcut) = %v, %v", got, err)
	}
	// What Windows writes, with a byte order mark and other sections.
	win := "\xef\xbb\xbf[{000214A0-0000-0000-C000-000000000046}]\r\nProp3=19,11\r\n[InternetShortcut]\r\nIDList=\r\nurl=https://example.org/\r\n"
	if got, err := Read([]byte(win)); err != nil || got.String() != "https://example.org/" {
		t.Fatalf("Windows shortcut = %v, %v", got, err)
	}
	for _, bad := range []string{"", "URL=https://example.com/", "[InternetShortcut]\nURL=file:///etc/passwd", "[InternetShortcut]\nURL=javascript:x"} {
		if _, err := Read([]byte(bad)); err == nil {
			t.Errorf("Read(%q) accepted", bad)
		}
	}
}

func TestFileName(t *testing.T) {
	for title, want := range map[string]string{
		"AC/DC – Thunderstruck": "AC-DC – Thunderstruck.url",
		"  line\nbreak\t ":      "line break.url",
		"":                      "Link.url",
		"...":                   "Link.url",
	} {
		if got := FileName(title); got != want {
			t.Errorf("FileName(%q) = %q, want %q", title, got, want)
		}
	}
	if got := FileName(strings.Repeat("é", 300)); len(got) > 255 || !strings.HasSuffix(got, ".url") {
		t.Errorf("long title gives %d bytes: %q", len(got), got)
	}
}

func TestSite(t *testing.T) {
	for raw, want := range map[string]string{
		"https://youtu.be/abc":               "YouTube",
		"https://m.youtube.com/shorts/abc":   "YouTube",
		"https://www.tiktok.com/@a/video/1":  "TikTok",
		"https://www.instagram.com/reel/abc": "instagram.com",
		"https://evil-youtube.com/":          "evil-youtube.com",
	} {
		u, _ := Parse(raw)
		if got := Site(u); got != want {
			t.Errorf("Site(%s) = %q, want %q", raw, got, want)
		}
	}
}

// fakeWeb answers like the providers, and records every request.
type fakeWeb struct {
	mu       sync.Mutex
	requests []string
	answer   func(r *http.Request) *http.Response
}

func (f *fakeWeb) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.String())
	f.mu.Unlock()
	if res := f.answer(r); res != nil {
		return res, nil
	}
	return nil, errors.New("no network in tests")
}

func reply(status int, body string, header ...string) *http.Response {
	h := http.Header{}
	for i := 0; i+1 < len(header); i += 2 {
		h.Set(header[i], header[i+1])
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

const jpeg = "\xff\xd8\xff\xe0\x00\x10JFIF\x00"

func TestLookup(t *testing.T) {
	web := &fakeWeb{answer: func(r *http.Request) *http.Response {
		switch r.URL.Host {
		case "www.youtube.com":
			return reply(200, `{"title":"Cat\nvideo","author_name":"Cats Inc","thumbnail_url":"https://i.ytimg.com/vi/abc/hqdefault.jpg"}`)
		case "www.tiktok.com":
			return reply(200, `{"title":"Dance","author_name":"dancer","thumbnail_url":"https://evil.example/x.jpg"}`)
		case "i.ytimg.com":
			return reply(200, jpeg)
		}
		return nil
	}}
	c := NewClient(web)
	ctx := context.Background()

	u, _ := Parse("https://www.youtube.com/shorts/abc?feature=share")
	pv, err := c.Lookup(ctx, "u1", u)
	if err != nil || pv.Title != "Cat video" || pv.Author != "Cats Inc" || pv.Site != "YouTube" {
		t.Fatalf("Lookup = %+v, %v", pv, err)
	}
	// The address only travels as a parameter of the fixed endpoint; Shorts
	// are asked as normal videos.
	want := "https://www.youtube.com/oembed?format=json&url=" + url.QueryEscape("https://www.youtube.com/watch?v=abc")
	if web.requests[0] != want {
		t.Fatalf("asked %s, want %s", web.requests[0], want)
	}
	b, ctype, err := c.Thumbnail(ctx, pv)
	if err != nil || ctype != "image/jpeg" || string(b) != jpeg {
		t.Fatalf("Thumbnail = %q %s %v", b, ctype, err)
	}

	// A thumbnail from a host that isn't the provider's is never fetched.
	tt, _ := Parse("https://www.tiktok.com/@dancer/video/1")
	pv, err = c.Lookup(ctx, "u1", tt)
	if err != nil || pv.Title != "Dance" {
		t.Fatalf("TikTok lookup = %+v, %v", pv, err)
	}
	if _, _, err := c.Thumbnail(ctx, pv); err == nil {
		t.Fatal("fetched a thumbnail from another host")
	}

	// Other sites are never asked anything.
	n := len(web.requests)
	other, _ := Parse("https://www.instagram.com/reel/abc")
	if _, err := c.Lookup(ctx, "u1", other); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("other site: %v", err)
	}
	for _, r := range web.requests[n:] {
		t.Fatalf("asked %s", r)
	}
	for _, r := range web.requests {
		if strings.Contains(r, "evil.example") {
			t.Fatalf("contacted %s", r)
		}
	}
}

func TestLookupLimits(t *testing.T) {
	var answer func() *http.Response
	web := &fakeWeb{answer: func(r *http.Request) *http.Response { return answer() }}
	c := NewClient(web)
	ctx := context.Background()
	u, _ := Parse("https://youtu.be/abc")

	// Redirects aren't followed.
	answer = func() *http.Response { return reply(302, "", "Location", "https://evil.example/") }
	if _, err := c.Lookup(ctx, "u1", u); !errors.Is(err, ErrLookup) {
		t.Fatalf("redirect: %v", err)
	}
	if len(web.requests) != 1 {
		t.Fatalf("followed a redirect: %v", web.requests)
	}
	// Answers over 64 KB are refused.
	answer = func() *http.Response { return reply(200, `{"title":"`+strings.Repeat("a", maxAnswer)+`"}`) }
	if _, err := c.Lookup(ctx, "u1", u); !errors.Is(err, ErrLookup) {
		t.Fatalf("large answer: %v", err)
	}
	// So are thumbnails that aren't images or are too large.
	pv := Preview{thumb: &url.URL{Scheme: "https", Host: "i.ytimg.com", Path: "/x.jpg"}}
	answer = func() *http.Response { return reply(200, "<html>") }
	if _, _, err := c.Thumbnail(ctx, pv); err == nil {
		t.Fatal("accepted HTML as a thumbnail")
	}
	answer = func() *http.Response { return reply(200, jpeg+strings.Repeat("a", MaxThumb)) }
	if _, _, err := c.Thumbnail(ctx, pv); err == nil {
		t.Fatal("accepted a thumbnail over 2 MB")
	}

	// 30 lookups per user per minute.
	answer = func() *http.Response { return reply(200, `{"title":"x"}`) }
	for i := 2; i < 30; i++ {
		if _, err := c.Lookup(ctx, "u1", u); err != nil {
			t.Fatalf("lookup %d: %v", i+1, err)
		}
	}
	if _, err := c.Lookup(ctx, "u1", u); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("lookup 31: %v", err)
	}
	if _, err := c.Lookup(ctx, "u2", u); err != nil {
		t.Fatalf("another user: %v", err)
	}
}

func TestAllowsThumb(t *testing.T) {
	tiktok := providers[1]
	for raw, ok := range map[string]bool{
		"https://p16-sign-va.tiktokcdn.com/obj/x.jpeg": true,
		"https://p16.tiktokcdn-us.com/x.jpeg":          true,
		"https://tiktokcdn.com/x.jpeg":                 false,
		"https://eviltiktokcdn.com/x.jpeg":             false,
		"http://p16.tiktokcdn.com/x.jpeg":              false,
		"https://p16.tiktokcdn.com:8443/x.jpeg":        false,
		"https://i.ytimg.com/vi/x/hqdefault.jpg":       false,
	} {
		u, _ := url.Parse(raw)
		if got := tiktok.allowsThumb(u); got != ok {
			t.Errorf("allowsThumb(%s) = %v", raw, got)
		}
	}
}
