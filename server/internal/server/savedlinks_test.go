package server

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProviders answers for YouTube's oEmbed endpoint and thumbnail host
// and records every request; anything else fails, so tests never reach
// the network.
type fakeProviders struct {
	mu       sync.Mutex
	requests []string
}

const fakeJPEG = "\xff\xd8\xff\xe0\x00\x10JFIF\x00"

func (f *fakeProviders) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.String())
	f.mu.Unlock()
	body := ""
	switch r.URL.Host {
	case "www.youtube.com":
		body = `{"title":"Cat video","author_name":"Cats Inc","thumbnail_url":"https://i.ytimg.com/vi/abc/hqdefault.jpg"}`
	case "i.ytimg.com":
		body = fakeJPEG
	default:
		return nil, errors.New("no network in tests")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (f *fakeProviders) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeProviders) asked(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}

type linkNode struct {
	ID, Name, Type, Description, Version string
	ModifiedAt                           time.Time
	Link                                 *struct{ URL, Site, Author string }
}

func nodeOf(t *testing.T, c *client, id string) linkNode {
	t.Helper()
	r := c.get("/api/nodes/" + id)
	expect(t, r, 200)
	var d struct{ Node linkNode }
	r.decode(t, &d)
	return d.Node
}

func describe(c *client, id, text string) response {
	return c.json("PATCH", "/api/nodes/"+id, map[string]string{"description": text})
}

func TestDescriptions(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	root := rootOf(t, a, "personal")

	// Text shared with a file comes along with its upload.
	r := a.json("POST", "/api/uploads", map[string]any{"parentId": root, "name": "beach.jpg", "size": 4,
		"description": "  Holiday in Ölberg\r\nDay one \x07"})
	expect(t, r, 201)
	var up struct{ ID string }
	r.decode(t, &up)
	expect(t, a.send("PUT", "/api/uploads/"+up.ID+"?offset=0", "application/octet-stream", []byte("jpeg"), nil), 200)
	r = a.json("POST", "/api/uploads/"+up.ID+"/complete", nil)
	expect(t, r, 201)
	var photo linkNode
	r.decode(t, &photo)
	if photo.Description != "Holiday in Ölberg\nDay one" {
		t.Fatalf("description from the upload = %q", photo.Description)
	}

	// Editing it leaves the modification time, which tracks the contents.
	before := nodeOf(t, a, photo.ID).ModifiedAt
	time.Sleep(5 * time.Millisecond)
	expect(t, describe(a, photo.ID, "Sunset at the pier"), 200)
	if n := nodeOf(t, a, photo.ID); n.Description != "Sunset at the pier" || !n.ModifiedAt.Equal(before) {
		t.Fatalf("after editing: %+v (modified before %v)", n, before)
	}
	expectCode(t, describe(a, photo.ID, strings.Repeat("é", 5001)), 400, "invalid_description")
	expect(t, describe(a, photo.ID, strings.Repeat("é", 5000)), 200)
	expectCode(t, describe(a, root, "My stuff"), 400, "drive_root")
	expect(t, describe(a, photo.ID, "Sunset at the PIER in Ölberg"), 200)

	// It follows the item through rename, move and trash.
	folder := mkdir(t, a, root, "Trips")
	expect(t, a.json("PATCH", "/api/nodes/"+photo.ID, map[string]string{"name": "pier.jpg", "parentId": folder.ID}), 200)
	expect(t, a.json("DELETE", "/api/nodes/"+photo.ID, nil), 204)
	expect(t, a.json("POST", "/api/nodes/"+photo.ID+"/restore", nil), 200)
	if n := nodeOf(t, a, photo.ID); n.Description != "Sunset at the PIER in Ölberg" {
		t.Fatalf("after moving and restoring: %q", n.Description)
	}

	// Search matches descriptions, ignoring case beyond ASCII.
	var found []linkNode
	a.get("/api/search?q="+url.QueryEscape("ölberg")).decode(t, &found)
	if len(found) != 1 || found[0].ID != photo.ID {
		t.Fatalf("search by description: %+v", found)
	}
	a.get("/api/search?q=pier").decode(t, &found)
	if len(found) != 1 {
		t.Fatalf("search by name and description: %+v", found)
	}
	// Others don't find it.
	b.get("/api/search?q=sunset").decode(t, &found)
	if len(found) != 0 {
		t.Fatalf("B found %+v", found)
	}

	// Only people who may edit an item describe it.
	expect(t, describe(b, photo.ID, "mine"), 404)
	shareWith(t, a, folder.ID, userID(t, b), "view")
	if n := nodeOf(t, b, photo.ID); n.Description == "" {
		t.Fatal("viewer doesn't see the description")
	}
	expectCode(t, describe(b, photo.ID, "mine"), 403, "read_only")
	shareWith(t, a, folder.ID, userID(t, b), "edit")
	expect(t, describe(b, folder.ID, "Our trips"), 200)
	expect(t, describe(b, photo.ID, "Taken by B"), 200)

	// Link visitors never see descriptions.
	r = a.json("PUT", "/api/nodes/"+folder.ID+"/link", map[string]any{})
	expect(t, r, 200)
	var l struct{ Token string }
	r.decode(t, &l)
	visitor := env.client(t)
	for _, p := range []string{"/api/s/" + l.Token, "/api/s/" + l.Token + "/nodes/" + photo.ID} {
		r := visitor.get(p)
		expect(t, r, 200)
		if strings.Contains(r.Body.String(), "Taken by B") || strings.Contains(r.Body.String(), "Our trips") ||
			strings.Contains(r.Body.String(), "description") {
			t.Fatalf("visitor sees a description: %s", r.Body)
		}
	}
}

func TestSavedLinks(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	root := rootOf(t, a, "personal")

	// A .url file uploaded from a PC becomes a link with the provider's
	// author.
	shortcut := "[InternetShortcut]\r\nURL=https://www.youtube.com/watch?v=abc\r\n"
	up := upload(t, a, root, "Cats.url", []byte(shortcut), 1<<20)
	n := nodeOf(t, a, up.ID)
	if n.Type != "link" || n.Link == nil || n.Link.URL != "https://www.youtube.com/watch?v=abc" ||
		n.Link.Site != "YouTube" || n.Link.Author != "Cats Inc" {
		t.Fatalf("saved link = %+v %+v", n, n.Link)
	}
	if !env.web.asked("https://www.youtube.com/oembed?") {
		t.Fatalf("provider not asked: %v", env.web.requests)
	}
	// The file itself is only ever downloaded.
	r := a.get("/api/nodes/" + n.ID + "/content")
	expect(t, r, 200)
	if cd := r.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q", cd)
	}

	// The thumbnail comes from the provider once, then from the cache.
	thumb := "/api/nodes/" + n.ID + "/thumb?v=" + n.Version
	r = a.get(thumb)
	expect(t, r, 200)
	if r.Body.String() != fakeJPEG || r.Header().Get("Content-Type") != "image/jpeg" ||
		!strings.HasPrefix(r.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("thumbnail: %q %v", r.Body.String(), r.Header())
	}
	asked := env.web.count()
	expect(t, a.get(thumb), 200)
	if env.web.count() != asked {
		t.Fatal("thumbnail not cached")
	}
	if r := a.send("GET", thumb, "", nil, map[string]string{"If-None-Match": `"` + n.Version + `"`}); r.Code != 304 {
		t.Fatalf("If-None-Match: %d", r.Code)
	}
	expect(t, b.get(thumb), 404)

	// Other sites get the host only, and Homedrive never contacts them.
	other := upload(t, a, root, "Recipe.url", []byte("[InternetShortcut]\nURL=https://www.example.com/soup\n"), 1<<20)
	if n := nodeOf(t, a, other.ID); n.Link == nil || n.Link.Site != "example.com" || n.Link.Author != "" {
		t.Fatalf("other site: %+v", n.Link)
	}
	expect(t, a.get("/api/nodes/"+other.ID+"/thumb"), 404)
	// A .url file without a usable address is a link without a card.
	bad := upload(t, a, root, "Bad.url", []byte("[InternetShortcut]\nURL=javascript:alert(1)\n"), 1<<20)
	if n := nodeOf(t, a, bad.ID); n.Type != "link" || n.Link != nil {
		t.Fatalf("bad shortcut: %+v", n)
	}
	if env.web.asked("example.com") || env.web.asked("javascript") {
		t.Fatalf("contacted a user's host: %v", env.web.requests)
	}

	// The Save page's preview.
	var pv struct{ URL, Site, Title, Author string }
	r = a.get("/api/link-preview?url=" + url.QueryEscape("https://youtu.be/abc"))
	expect(t, r, 200)
	r.decode(t, &pv)
	if pv.Title != "Cat video" || pv.Author != "Cats Inc" || pv.Site != "YouTube" {
		t.Fatalf("preview = %+v", pv)
	}
	asked = env.web.count()
	pv.Title, pv.Author = "", ""
	a.get("/api/link-preview?url="+url.QueryEscape("https://www.instagram.com/reel/x")).decode(t, &pv)
	if pv.Site != "instagram.com" || pv.Title != "" || env.web.count() != asked {
		t.Fatalf("instagram preview = %+v", pv)
	}
	expectCode(t, a.get("/api/link-preview?url="+url.QueryEscape("file:///etc/passwd")), 400, "invalid_url")
	expect(t, env.client(t).get("/api/link-preview?url=https://youtu.be/abc"), 401)
	// The address isn't logged.
	if strings.Contains(env.logs.String(), "youtu.be") {
		t.Fatal("link address in the logs")
	}

	// Visitors of a public link see the card and the cached thumbnail, and
	// never make Homedrive ask a provider.
	r = a.json("PUT", "/api/nodes/"+n.ID+"/link", map[string]any{})
	expect(t, r, 200)
	var l struct{ Token string }
	r.decode(t, &l)
	visitor := env.client(t)
	var page struct {
		Node struct {
			Link *struct{ URL, Site string }
		}
	}
	visitor.get("/api/s/"+l.Token).decode(t, &page)
	if page.Node.Link == nil || page.Node.Link.Site != "YouTube" {
		t.Fatalf("visitor page: %+v", page)
	}
	expect(t, visitor.get("/api/s/"+l.Token+"/nodes/"+n.ID+"/thumb"), 200)
	r = a.json("PUT", "/api/nodes/"+other.ID+"/link", map[string]any{})
	r.decode(t, &l)
	asked = env.web.count()
	expect(t, visitor.get("/api/s/"+l.Token+"/nodes/"+other.ID+"/thumb"), 404)
	if env.web.count() != asked {
		t.Fatal("a visitor made Homedrive ask a provider")
	}
}

func TestInboxLink(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	r := a.json("POST", "/api/me/devices", map[string]string{"name": "iPhone"})
	expect(t, r, 201)
	var created struct{ Token string }
	r.decode(t, &created)
	shortcut := func(path, ctype string, body []byte, header map[string]string) response {
		h := map[string]string{"Authorization": "Bearer " + created.Token, "Origin": "", "Sec-Fetch-Site": ""}
		for k, v := range header {
			h[k] = v
		}
		return env.client(t).send("POST", path, ctype, body, h)
	}
	saveLink := func(body string) response {
		return shortcut("/api/inbox/link", "application/json", []byte(body), nil)
	}

	// Without a title, the provider's.
	r = saveLink(`{"url":"https://youtu.be/abc","text":"So cute"}`)
	expect(t, r, 201)
	var n linkNode
	r.decode(t, &n)
	if n.Name != "Cat video.url" || n.Description != "So cute" || n.Link == nil || n.Link.Author != "Cats Inc" {
		t.Fatalf("saved %+v %+v", n, n.Link)
	}
	var d struct{ Path []struct{ Name string } }
	a.get("/api/nodes/"+n.ID).decode(t, &d)
	if len(d.Path) != 2 || d.Path[1].Name != "Inbox" {
		t.Fatalf("path = %+v", d.Path)
	}
	// The shared title wins; the address may come inside the text; other
	// sites are named after their host.
	r = saveLink(`{"title":"Soup / dinner","text":"Try this https://www.example.com/soup tonight"}`)
	expect(t, r, 201)
	r.decode(t, &n)
	if n.Name != "Soup - dinner.url" || n.Description != "Try this tonight" || n.Link.URL != "https://www.example.com/soup" {
		t.Fatalf("saved %+v %+v", n, n.Link)
	}
	r = saveLink(`{"url":"https://www.example.com/a"}`)
	expect(t, r, 201)
	r.decode(t, &n)
	if n.Name != "example.com.url" {
		t.Fatalf("saved %+v", n)
	}
	expectCode(t, saveLink(`{"url":"ftp://example.com/x"}`), 400, "invalid_url")
	expectCode(t, saveLink(`{"text":"no link"}`), 400, "invalid_url")
	expect(t, env.client(t).send("POST", "/api/inbox/link", "application/json", []byte(`{"url":"https://example.com"}`),
		map[string]string{"Authorization": "Bearer hd_wrong", "Origin": "", "Sec-Fetch-Site": ""}), 401)
	if env.web.asked("example.com/") {
		t.Fatalf("contacted a user's host: %v", env.web.requests)
	}

	// A file can bring its description in a header.
	r = shortcut("/api/inbox?name=Receipt.pdf", "application/pdf", []byte("%PDF"),
		map[string]string{"X-Homedrive-Description": "Receipt%20from%20the%20shop%0AJuly"})
	expect(t, r, 201)
	r.decode(t, &n)
	if n.Description != "Receipt from the shop\nJuly" {
		t.Fatalf("description = %q", n.Description)
	}
	expectCode(t, shortcut("/api/inbox?name=x.pdf", "application/pdf", []byte("%PDF"),
		map[string]string{"X-Homedrive-Description": "%zz"}), 400, "invalid_description")
}
