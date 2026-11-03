package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BertVanBaelen/HomeDrive/server/internal/thumbs"
)

// thumbNode is what listings say about a file's thumbnail.
type thumbNode struct {
	ID, Name, Version, Thumb string
	DurationMs               int64
	Preview                  []struct{ ID, Version, Thumb string }
}

func TestThumbnails(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	root := rootOf(t, a, "personal")
	album := mkdir(t, a, root, "Album")
	photo := upload(t, a, album.ID, "Beach.jpg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00photo"), 1<<20)
	heic := upload(t, a, album.ID, "IMG_0001.HEIC", []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"), 1<<20)
	clip := upload(t, a, album.ID, "Clip.mp4", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isom"), 1<<20)
	notes := upload(t, a, root, "Notes.md", []byte("# Groceries\n- milk\n"), 1<<20)
	letter := upload(t, a, root, "Letter.docx", []byte("PK\x03\x04"), 1<<20)

	var kids []thumbNode
	a.get("/api/nodes/"+album.ID+"/children").decode(t, &kids)
	kinds := map[string]string{}
	for _, k := range kids {
		kinds[k.Name] = k.Thumb
	}
	if kinds["Beach.jpg"] != "image" || kinds["IMG_0001.HEIC"] != "image" || kinds["Clip.mp4"] != "image" {
		t.Fatalf("kinds %v", kinds)
	}
	var top []thumbNode
	a.get("/api/nodes/"+root+"/children").decode(t, &top)
	for _, n := range top {
		switch n.Name {
		case "Notes.md":
			if n.Thumb != "snippet" {
				t.Fatalf("notes %+v", n)
			}
		case "Letter.docx":
			if n.Thumb != "" {
				t.Fatalf("letter %+v", n)
			}
		case "Album":
			// Folder tiles carry their items' thumbnails.
			if len(n.Preview) != 3 || n.Preview[0].Thumb != "image" || n.Preview[0].Version == "" {
				t.Fatalf("album tiles %+v", n.Preview)
			}
		}
	}

	// Images are WebP, served like file contents.
	r := a.get("/api/nodes/" + photo.ID + "/thumb?size=grid&v=x")
	expect(t, r, 200)
	h := r.Header()
	if r.Body.String() != thumbs.FakeWebP || h.Get("Content-Type") != "image/webp" || h.Get("Cache-Control") != "private, no-cache" ||
		!strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("thumb %q %v", r.Body.String(), h)
	}
	if r := a.send("GET", "/api/nodes/"+photo.ID+"/thumb", "", nil, map[string]string{"If-None-Match": h.Get("ETag")}); r.Code != 304 {
		t.Fatalf("revalidate %d", r.Code)
	}
	expect(t, a.get("/api/nodes/"+photo.ID+"/thumb?size=list"), 200)
	expect(t, a.get("/api/nodes/"+photo.ID+"/thumb?size=huge"), 400)
	expect(t, a.get("/api/nodes/"+photo.ID+"/preview"), 404)
	expect(t, a.get("/api/nodes/"+heic.ID+"/preview"), 200)
	expect(t, a.get("/api/nodes/"+letter.ID+"/thumb"), 404)
	expect(t, a.get("/api/nodes/"+album.ID+"/thumb"), 404)
	// Videos get their length once the thumbnail is made.
	expect(t, a.get("/api/nodes/"+clip.ID+"/thumb"), 200)
	var d struct{ Node thumbNode }
	a.get("/api/nodes/"+clip.ID).decode(t, &d)
	if d.Node.DurationMs != 12500 {
		t.Fatalf("duration %+v", d.Node)
	}

	// Snippets are JSON for the web app to lay out.
	r = a.get("/api/nodes/" + notes.ID + "/snippet")
	expect(t, r, 200)
	var sn thumbs.Snippet
	r.decode(t, &sn)
	if sn.Kind != "text" || len(sn.Lines) != 2 || sn.Lines[0] != "# Groceries" || r.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("snippet %+v %v", sn, r.Header())
	}
	expect(t, a.get("/api/nodes/"+photo.ID+"/snippet"), 404)

	// Other people get nothing.
	for _, p := range []string{"/thumb", "/snippet", "/preview"} {
		expect(t, b.get("/api/nodes/"+photo.ID+p), 404)
		expect(t, b.get("/api/nodes/"+notes.ID+p), 404)
		expect(t, env.client(t).get("/api/nodes/"+photo.ID+p), 401)
	}

	// Link visitors get the thumbnails of what is in the link.
	var l struct{ Token string }
	a.json("PUT", "/api/nodes/"+album.ID+"/link", map[string]any{}).decode(t, &l)
	v := env.client(t)
	var page struct{ Children []thumbNode }
	v.get("/api/s/"+l.Token).decode(t, &page)
	if len(page.Children) != 3 || page.Children[0].Thumb != "image" {
		t.Fatalf("visitor page %+v", page)
	}
	base := "/api/s/" + l.Token + "/nodes/"
	expect(t, v.get(base+photo.ID+"/thumb"), 200)
	expect(t, v.get(base+heic.ID+"/preview"), 200)
	expect(t, v.get(base+notes.ID+"/snippet"), 404)
	expect(t, v.get(base+notes.ID+"/thumb"), 404)
	expect(t, a.json("PUT", "/api/nodes/"+album.ID+"/link", map[string]string{"password": "open sesame 1"}), 200)
	expectCode(t, v.get(base+photo.ID+"/thumb"), 401, "link_locked")
}

// Saved links from before thumbnails had their provider's image cached as
// it was; it is made into thumbnails without asking the provider again,
// also for link visitors.
func TestLegacyLinkThumbnail(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	root := rootOf(t, a, "personal")
	up := upload(t, a, root, "Cats.url", []byte("[InternetShortcut]\r\nURL=https://youtu.be/abc\r\n"), 1<<20)
	n := nodeOf(t, a, up.ID)
	legacy := filepath.Join(env.state, "cache", "thumbs", n.Version[:2], n.Version+"-link")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(fakeJPEG), 0o640); err != nil {
		t.Fatal(err)
	}
	var l struct{ Token string }
	a.json("PUT", "/api/nodes/"+n.ID+"/link", map[string]any{}).decode(t, &l)
	asked := env.web.count()
	r := env.client(t).get("/api/s/" + l.Token + "/nodes/" + n.ID + "/thumb?size=list")
	expect(t, r, 200)
	if r.Body.String() != thumbs.FakeWebP || env.web.count() != asked {
		t.Fatalf("legacy thumbnail: %q, %d lookups", r.Body.String(), env.web.count()-asked)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy image kept")
	}
}
