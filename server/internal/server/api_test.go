package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// client talks to a test server like a browser: it keeps the session
// cookie and sends same-origin JSON requests.
type client struct {
	t      *testing.T
	env    *testEnv
	cookie string
}

func (e *testEnv) client(t *testing.T) *client { return &client{t: t, env: e} }

type response struct {
	*httptest.ResponseRecorder
}

func (r response) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body.Bytes(), v); err != nil {
		t.Fatalf("decoding %q: %v", r.Body.String(), err)
	}
}

func (r response) errorCode() string {
	var e struct{ Error string }
	json.Unmarshal(r.Body.Bytes(), &e)
	return e.Error
}

func (c *client) send(method, target, ctype string, body []byte, header map[string]string) response {
	c.t.Helper()
	r := httptest.NewRequest(method, target, bytes.NewReader(body))
	r.Host = publicHost
	r.Header.Set("Origin", "https://"+publicHost)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	if c.cookie != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.cookie})
	}
	w := httptest.NewRecorder()
	c.env.h.ServeHTTP(w, r)
	for _, ck := range w.Result().Cookies() {
		if ck.Name == sessionCookie {
			c.cookie = ck.Value
		}
	}
	return response{w}
}

func (c *client) json(method, target string, body any) response {
	c.t.Helper()
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			c.t.Fatal(err)
		}
	}
	return c.send(method, target, "application/json", b, nil)
}

func (c *client) get(target string) response { return c.send("GET", target, "", nil, nil) }

func expect(t *testing.T, r response, status int) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("status %d, want %d: %s", r.Code, status, r.Body.String())
	}
}

const testPassword = "correct horse battery"

// signIn creates a user and signs them in through the API.
func (e *testEnv) signIn(t *testing.T, email, name string) *client {
	return e.signInAs(t, email, name, "member")
}

func (e *testEnv) signInAs(t *testing.T, email, name, role string) *client {
	t.Helper()
	if _, err := e.accounts.CreateUser(context.Background(), email, name, testPassword, role, "en"); err != nil {
		t.Fatal(err)
	}
	c := e.client(t)
	r := c.json("POST", "/api/auth/login", map[string]string{"email": email, "password": testPassword})
	expect(t, r, 200)
	var ch struct{ Challenge string }
	r.decode(t, &ch)
	expect(t, c.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": e.mail.lastCode(t, email)}), 200)
	return c
}

func TestSetupThenLogin(t *testing.T) {
	env := newTestEnv(t)
	link, err := env.accounts.StartSetup(context.Background())
	if err != nil || !strings.HasPrefix(link, "https://"+publicHost+"/setup#") {
		t.Fatalf("setup link %q, %v", link, err)
	}
	token := link[strings.IndexByte(link, '#')+1:]
	c := env.client(t)

	expect(t, c.json("POST", "/api/setup/check", map[string]string{"token": "wrong"}), 403)
	expect(t, c.json("POST", "/api/setup/check", map[string]string{"token": token}), 204)
	r := c.json("POST", "/api/setup/begin", map[string]string{"token": token, "email": "Admin@Example.com", "name": "Admin", "password": "short"})
	if r.Code != 400 || r.errorCode() != "password_too_short" {
		t.Fatalf("weak password: %d %s", r.Code, r.Body)
	}
	expect(t, c.json("POST", "/api/setup/begin", map[string]string{
		"token": token, "email": "Admin@Example.com", "name": " Admin ", "password": testPassword}), 204)
	expect(t, c.json("POST", "/api/setup/finish", map[string]string{"token": token, "code": "00000000"}), 401)
	r = c.json("POST", "/api/setup/finish", map[string]string{"token": token, "code": env.mail.lastCode(t, "admin@example.com")})
	expect(t, r, 200)
	var me struct{ Email, Name, Role string }
	r.decode(t, &me)
	if me.Email != "admin@example.com" || me.Name != "Admin" || me.Role != "admin" {
		t.Fatalf("admin = %+v", me)
	}
	expect(t, c.get("/api/me"), 200)

	// The link is single use.
	expect(t, c.json("POST", "/api/setup/check", map[string]string{"token": token}), 403)
	if link, _ := env.accounts.StartSetup(context.Background()); link != "" {
		t.Fatal("setup offered again after the first admin exists")
	}

	// The admin has a personal drive and manages the first shared drive.
	var drives []struct{ Kind, Name, Role string }
	c.get("/api/drives").decode(t, &drives)
	if len(drives) != 2 || drives[0].Kind != "personal" || drives[1].Kind != "shared" || drives[1].Role != "manager" || drives[1].Name != "Family" {
		t.Fatalf("drives = %+v", drives)
	}

	expect(t, c.json("POST", "/api/auth/logout", nil), 204)
	expect(t, c.get("/api/me"), 401)

	// Wrong password and unknown account look the same.
	bad := c.json("POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "not the password"})
	unknown := c.json("POST", "/api/auth/login", map[string]string{"email": "nobody@example.com", "password": testPassword})
	if bad.Code != 401 || unknown.Code != 401 || bad.Body.String() != unknown.Body.String() {
		t.Fatalf("login errors differ: %d %s / %d %s", bad.Code, bad.Body, unknown.Code, unknown.Body)
	}

	r = c.json("POST", "/api/auth/login", map[string]string{"email": "ADMIN@example.com", "password": testPassword})
	expect(t, r, 200)
	var ch struct{ Challenge string }
	r.decode(t, &ch)
	code := env.mail.lastCode(t, "admin@example.com")
	expect(t, c.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": "12345678"}), 401)
	expect(t, c.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": code[:4] + " " + code[4:]}), 200)
	expect(t, c.get("/api/me"), 200)
	// A code works once.
	expect(t, env.client(t).json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": code}), 401)
}

func TestCodeAttemptsAreLimited(t *testing.T) {
	env := newTestEnv(t)
	env.accounts.CreateUser(context.Background(), "a@example.com", "A", testPassword, "member", "")
	c := env.client(t)
	r := c.json("POST", "/api/auth/login", map[string]string{"email": "a@example.com", "password": testPassword})
	var ch struct{ Challenge string }
	r.decode(t, &ch)
	code := env.mail.lastCode(t, "a@example.com")
	wrong := "00000000"
	if code == wrong {
		wrong = "11111111"
	}
	for range 5 {
		expect(t, c.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": wrong}), 401)
	}
	expect(t, c.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": code}), 401)
}

func TestAPIRequiresSession(t *testing.T) {
	c := newTestEnv(t).client(t)
	for _, p := range []string{"/api/me", "/api/drives", "/api/trash", "/api/nodes/0190a6e0-0000-7000-8000-000000000000"} {
		if r := c.get(p); r.Code != 401 {
			t.Errorf("GET %s without session: %d", p, r.Code)
		}
	}
}

type node struct {
	ID, ParentID, DriveID, Kind, Type, Name string
	Size                                    int64
	ChildCount                              *int
}

func rootOf(t *testing.T, c *client, kind string) string {
	t.Helper()
	var drives []struct{ Kind, RootID string }
	c.get("/api/drives").decode(t, &drives)
	for _, d := range drives {
		if d.Kind == kind {
			return d.RootID
		}
	}
	t.Fatalf("no %s drive", kind)
	return ""
}

func mkdir(t *testing.T, c *client, parent, name string) node {
	t.Helper()
	r := c.json("POST", "/api/folders", map[string]string{"parentId": parent, "name": name})
	expect(t, r, 201)
	var n node
	r.decode(t, &n)
	return n
}

// upload sends content in chunks of chunk bytes.
func upload(t *testing.T, c *client, parent, name string, content []byte, chunk int) node {
	t.Helper()
	r := c.json("POST", "/api/uploads", map[string]any{"parentId": parent, "name": name, "size": len(content)})
	expect(t, r, 201)
	var u struct{ ID string }
	r.decode(t, &u)
	for off := 0; off < len(content); off += chunk {
		end := min(off+chunk, len(content))
		expect(t, c.send("PUT", fmt.Sprintf("/api/uploads/%s?offset=%d", u.ID, off), "application/octet-stream", content[off:end], nil), 200)
	}
	r = c.json("POST", "/api/uploads/"+u.ID+"/complete", nil)
	expect(t, r, 201)
	var n node
	r.decode(t, &n)
	return n
}

func TestFileOperations(t *testing.T) {
	env := newTestEnv(t)
	c := env.signIn(t, "a@example.com", "A")
	root := rootOf(t, c, "personal")

	docs := mkdir(t, c, root, "Documents")
	if r := c.json("POST", "/api/folders", map[string]string{"parentId": root, "name": "documents"}); r.Code != 409 {
		t.Fatalf("duplicate name ignoring case: %d", r.Code)
	}
	for _, bad := range []string{"", "  ", "a/b", `a\b`, "..", "x\x00y"} {
		if r := c.json("POST", "/api/folders", map[string]string{"parentId": root, "name": bad}); r.Code != 400 {
			t.Errorf("name %q: %d", bad, r.Code)
		}
	}

	content := []byte(strings.Repeat("Homedrive test content. ", 100))
	f := upload(t, c, docs.ID, "Notes.txt", content, 1000)
	if f.Size != int64(len(content)) || f.Type != "text" || f.ParentID != docs.ID {
		t.Fatalf("uploaded node = %+v", f)
	}
	// Same name again gets a number.
	if f2 := upload(t, c, docs.ID, "notes.txt", []byte("x"), 10); f2.Name != "notes (2).txt" {
		t.Fatalf("second upload named %q", f2.Name)
	}
	// Empty files work.
	upload(t, c, docs.ID, "empty.md", nil, 10)

	var kids []node
	c.get("/api/nodes/"+docs.ID+"/children?sort=name").decode(t, &kids)
	if len(kids) != 3 || kids[0].Name != "empty.md" {
		t.Fatalf("children = %+v", kids)
	}
	var rootKids []node
	c.get("/api/nodes/"+root+"/children").decode(t, &rootKids)
	if len(rootKids) != 1 || *rootKids[0].ChildCount != 3 {
		t.Fatalf("root children = %+v", rootKids)
	}

	r := c.get("/api/nodes/" + f.ID + "/content")
	expect(t, r, 200)
	if !bytes.Equal(r.Body.Bytes(), content) {
		t.Fatal("downloaded content differs")
	}
	r = c.send("GET", "/api/nodes/"+f.ID+"/content", "", nil, map[string]string{"Range": "bytes=10-19"})
	expect(t, r, 206)
	if !bytes.Equal(r.Body.Bytes(), content[10:20]) {
		t.Fatalf("range = %q", r.Body)
	}

	// Rename, then move into a new folder.
	r = c.json("PATCH", "/api/nodes/"+f.ID, map[string]string{"name": "Ideas.md"})
	expect(t, r, 200)
	var renamed node
	r.decode(t, &renamed)
	if renamed.Name != "Ideas.md" || renamed.Type != "text" {
		t.Fatalf("renamed = %+v", renamed)
	}
	archive := mkdir(t, c, root, "Archive")
	expect(t, c.json("PATCH", "/api/nodes/"+f.ID, map[string]string{"parentId": archive.ID}), 200)
	// A folder can't go into itself or below itself.
	sub := mkdir(t, c, archive.ID, "Sub")
	if r := c.json("PATCH", "/api/nodes/"+archive.ID, map[string]string{"parentId": sub.ID}); r.errorCode() != "invalid_move" {
		t.Fatalf("move into own subfolder: %d %s", r.Code, r.Body)
	}
	// The drive's top folder can't be renamed or trashed.
	if r := c.json("PATCH", "/api/nodes/"+root, map[string]string{"name": "x"}); r.Code != 400 {
		t.Fatalf("rename root: %d", r.Code)
	}

	// Breadcrumbs.
	var d struct {
		Path []struct{ ID, Name string }
	}
	c.get("/api/nodes/"+sub.ID).decode(t, &d)
	if len(d.Path) != 2 || d.Path[0].ID != root || d.Path[1].Name != "Archive" {
		t.Fatalf("path = %+v", d.Path)
	}

	// Move a folder to the family drive and back: contents travel along.
	family := rootOf(t, c, "shared")
	expect(t, c.json("PATCH", "/api/nodes/"+archive.ID, map[string]string{"parentId": family}), 200)
	var moved struct{ Node node }
	c.get("/api/nodes/"+f.ID).decode(t, &moved)
	if moved.Node.DriveID == renamed.DriveID {
		t.Fatal("file inside a moved folder stayed on the old drive")
	}

	// Trash a folder: it and its contents disappear; restore brings both.
	expect(t, c.json("DELETE", "/api/nodes/"+archive.ID, nil), 204)
	expect(t, c.get("/api/nodes/"+f.ID), 404)
	expect(t, c.get("/api/nodes/"+f.ID+"/content"), 404)
	var trash []node
	c.get("/api/trash").decode(t, &trash)
	if len(trash) != 1 || trash[0].ID != archive.ID {
		t.Fatalf("trash = %+v", trash)
	}
	// While it's in the trash, a new folder may take its name.
	mkdir(t, c, family, "Archive")
	r = c.json("POST", "/api/nodes/"+archive.ID+"/restore", nil)
	expect(t, r, 200)
	var restored node
	r.decode(t, &restored)
	if restored.Name != "Archive (2)" {
		t.Fatalf("restored as %q", restored.Name)
	}
	expect(t, c.get("/api/nodes/"+f.ID), 200)
}

func TestUploadResume(t *testing.T) {
	env := newTestEnv(t)
	c := env.signIn(t, "a@example.com", "A")
	root := rootOf(t, c, "personal")
	r := c.json("POST", "/api/uploads", map[string]any{"parentId": root, "name": "a.bin", "size": 10})
	var u struct{ ID string }
	r.decode(t, &u)
	expect(t, c.send("PUT", "/api/uploads/"+u.ID+"?offset=0", "application/octet-stream", []byte("01234"), nil), 200)
	// A repeated or skipped chunk is refused with the right offset.
	r = c.send("PUT", "/api/uploads/"+u.ID+"?offset=0", "application/octet-stream", []byte("01234"), nil)
	if r.Code != 409 || !strings.Contains(r.Body.String(), `"received":5`) {
		t.Fatalf("repeated chunk: %d %s", r.Code, r.Body)
	}
	expect(t, c.json("POST", "/api/uploads/"+u.ID+"/complete", nil), 409)
	// More than announced is refused.
	expect(t, c.send("PUT", "/api/uploads/"+u.ID+"?offset=5", "application/octet-stream", []byte("56789X"), nil), 413)
	var st struct{ Received int }
	c.get("/api/uploads/"+u.ID).decode(t, &st)
	if st.Received != 5 {
		t.Fatalf("received = %d", st.Received)
	}
	expect(t, c.send("PUT", "/api/uploads/"+u.ID+"?offset=5", "application/octet-stream", []byte("56789"), nil), 200)
	expect(t, c.json("POST", "/api/uploads/"+u.ID+"/complete", nil), 201)
	// JSON isn't accepted as chunk data.
	expect(t, c.send("PUT", "/api/uploads/"+u.ID+"?offset=0", "application/json", []byte("{}"), nil), 415)
}

// TestOtherUsersFiles checks that one user can't reach another's personal
// drive through any endpoint, while both share the family drive.
func TestOtherUsersFiles(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	aRoot := rootOf(t, a, "personal")
	secret := mkdir(t, a, aRoot, "Private")
	file := upload(t, a, secret.ID, "diary.txt", []byte("dear diary"), 100)

	r := b.json("POST", "/api/uploads", map[string]any{"parentId": aRoot, "name": "x", "size": 1})
	if r.Code != 404 {
		t.Errorf("upload into other's drive: %d", r.Code)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/nodes/" + secret.ID, nil},
		{"GET", "/api/nodes/" + aRoot + "/children", nil},
		{"GET", "/api/nodes/" + file.ID + "/content", nil},
		{"PATCH", "/api/nodes/" + file.ID, map[string]string{"name": "mine.txt"}},
		{"PATCH", "/api/nodes/" + file.ID, map[string]string{"parentId": rootOf(t, b, "personal")}},
		{"DELETE", "/api/nodes/" + file.ID, nil},
		{"POST", "/api/nodes/" + file.ID + "/restore", nil},
		{"POST", "/api/folders", map[string]string{"parentId": secret.ID, "name": "x"}},
	} {
		if r := b.json(tc.method, tc.path, tc.body); r.Code != 404 {
			t.Errorf("%s %s by another user: %d %s", tc.method, tc.path, r.Code, r.Body)
		}
	}
	// B can't move their own file into A's folder either.
	bFile := upload(t, b, rootOf(t, b, "personal"), "b.txt", []byte("b"), 10)
	if r := b.json("PATCH", "/api/nodes/"+bFile.ID, map[string]string{"parentId": secret.ID}); r.Code != 404 {
		t.Errorf("move into other's folder: %d", r.Code)
	}
	// A's trash isn't B's.
	a.json("DELETE", "/api/nodes/"+file.ID, nil)
	var trash []node
	b.get("/api/trash").decode(t, &trash)
	if len(trash) != 0 {
		t.Errorf("B sees A's trash: %+v", trash)
	}
	// Uploads are per user too.
	r = a.json("POST", "/api/uploads", map[string]any{"parentId": aRoot, "name": "y", "size": 1})
	var u struct{ ID string }
	r.decode(t, &u)
	if r := b.send("PUT", "/api/uploads/"+u.ID+"?offset=0", "application/octet-stream", []byte("y"), nil); r.Code != 404 {
		t.Errorf("chunk into other's upload: %d", r.Code)
	}

	// The family drive is shared.
	family := rootOf(t, a, "shared")
	if rootOf(t, b, "shared") != family {
		t.Fatal("users have different family drives")
	}
	shared := upload(t, a, family, "shopping.md", []byte("- milk"), 100)
	expect(t, b.get("/api/nodes/"+shared.ID+"/content"), 200)
	expect(t, b.json("PATCH", "/api/nodes/"+shared.ID, map[string]string{"name": "Shopping list.md"}), 200)
}

func TestFileContentIsSandboxed(t *testing.T) {
	env := newTestEnv(t)
	c := env.signIn(t, "a@example.com", "A")
	root := rootOf(t, c, "personal")
	for _, tc := range []struct {
		name, ctype, disposition string
	}{
		{"page.html", "application/octet-stream", "attachment"},
		{"logo.svg", "application/octet-stream", "attachment"},
		{"photo.jpg", "image/jpeg", "inline"},
		{"notes.md", "text/plain; charset=utf-8", "inline"},
		{"archive.zip", "application/octet-stream", "attachment"},
		{"Ünïcode name.pdf", "application/pdf", "inline"},
	} {
		n := upload(t, c, root, tc.name, []byte("<script>alert(1)</script>"), 100)
		r := c.get("/api/nodes/" + n.ID + "/content")
		h := r.Header()
		if h.Get("Content-Type") != tc.ctype || !strings.HasPrefix(h.Get("Content-Disposition"), tc.disposition) {
			t.Errorf("%s: Content-Type %q, Content-Disposition %q", tc.name, h.Get("Content-Type"), h.Get("Content-Disposition"))
		}
		if !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox") || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: CSP %q", tc.name, h.Get("Content-Security-Policy"))
		}
		dl := c.get("/api/nodes/" + n.ID + "/content?download=1")
		if !strings.HasPrefix(dl.Header().Get("Content-Disposition"), "attachment") {
			t.Errorf("%s: download=1 gave %q", tc.name, dl.Header().Get("Content-Disposition"))
		}
	}
}

func TestJSONOnly(t *testing.T) {
	env := newTestEnv(t)
	c := env.signIn(t, "a@example.com", "A")
	r := c.send("POST", "/api/folders", "text/plain", []byte(`{"parentId":"x","name":"y"}`), nil)
	if r.Code != 415 {
		t.Fatalf("text/plain body: %d", r.Code)
	}
	r = c.send("POST", "/api/folders", "application/x-www-form-urlencoded", []byte("name=y"), nil)
	if r.Code != 415 {
		t.Fatalf("form body: %d", r.Code)
	}
}

func TestStarsSearchRecent(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	aRoot, family := rootOf(t, a, "personal"), rootOf(t, a, "shared")

	taxes := mkdir(t, a, aRoot, "Taxes 2026")
	private := upload(t, a, taxes.ID, "Tax return.pdf", []byte("%PDF"), 100)
	shared := upload(t, a, family, "Family tax notes.md", []byte("notes"), 100)
	old := mkdir(t, a, aRoot, "Old taxes")
	upload(t, a, old.ID, "tax 2019.pdf", []byte("%PDF"), 100)

	// Stars are personal.
	r := a.json("PATCH", "/api/nodes/"+private.ID, map[string]bool{"starred": true})
	expect(t, r, 200)
	var starredNode node
	r.decode(t, &starredNode)
	a.json("PATCH", "/api/nodes/"+taxes.ID, map[string]bool{"starred": true})
	b.json("PATCH", "/api/nodes/"+shared.ID, map[string]bool{"starred": true})
	var aStars, bStars []struct {
		node
		Starred bool
		Path    []struct{ Name string }
	}
	a.get("/api/starred").decode(t, &aStars)
	b.get("/api/starred").decode(t, &bStars)
	if len(aStars) != 2 || aStars[0].ID != taxes.ID || aStars[1].ID != private.ID || !aStars[1].Starred {
		t.Fatalf("A's starred = %+v", aStars)
	}
	if len(bStars) != 1 || bStars[0].ID != shared.ID {
		t.Fatalf("B's starred = %+v", bStars)
	}
	// B can't star A's private file.
	if r := b.json("PATCH", "/api/nodes/"+private.ID, map[string]bool{"starred": true}); r.Code != 404 {
		t.Fatalf("B starring A's file: %d", r.Code)
	}
	// The star shows in folder listings for its owner only.
	var kids []struct{ Starred bool }
	a.get("/api/nodes/"+taxes.ID+"/children").decode(t, &kids)
	if len(kids) != 1 || !kids[0].Starred {
		t.Fatalf("children = %+v", kids)
	}

	// Search: case-insensitive, across A's drives, never into B's view of
	// A's personal drive, and not into the trash.
	expect(t, a.json("DELETE", "/api/nodes/"+old.ID, nil), 204)
	names := func(c *client, q string) []string {
		var res []node
		c.get("/api/search?q="+q).decode(t, &res)
		var out []string
		for _, n := range res {
			out = append(out, n.Name)
		}
		return out
	}
	if got := names(a, "TAX"); strings.Join(got, "|") != "Taxes 2026|Family tax notes.md|Tax return.pdf" {
		t.Fatalf("A searching TAX: %v", got)
	}
	if got := names(b, "tax"); strings.Join(got, "|") != "Family tax notes.md" {
		t.Fatalf("B searching tax: %v", got)
	}
	if got := names(a, "%25"); len(got) != 0 {
		t.Fatalf("%% matched everything: %v", got)
	}

	// Recent: files only, newest first, without trashed ones.
	var recent []node
	a.get("/api/recent").decode(t, &recent)
	if len(recent) != 2 || recent[0].ID != shared.ID || recent[1].ID != private.ID {
		t.Fatalf("recent = %+v", recent)
	}
	b.get("/api/recent").decode(t, &recent)
	if len(recent) != 1 || recent[0].ID != shared.ID {
		t.Fatalf("B's recent = %+v", recent)
	}
}

func TestDeleteForever(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	root, family := rootOf(t, a, "personal"), rootOf(t, a, "shared")
	folder := mkdir(t, a, root, "Old")
	inner := upload(t, a, folder.ID, "inner.txt", []byte("inner"), 100)
	loose := upload(t, a, root, "loose.txt", []byte("loose"), 100)
	shared := upload(t, b, family, "b's.txt", []byte("b"), 100)

	// Only trashed items can go for good.
	if r := a.json("DELETE", "/api/nodes/"+loose.ID+"?permanent=true", nil); r.errorCode() != "not_trashed" {
		t.Fatalf("delete live item: %d %s", r.Code, r.Body)
	}
	expect(t, a.json("DELETE", "/api/nodes/"+folder.ID, nil), 204)
	// It needs a recent password.
	env.expireRecentAuth(t)
	if r := a.json("DELETE", "/api/nodes/"+folder.ID+"?permanent=true", nil); r.errorCode() != "reauth_required" {
		t.Fatalf("stale session: %s", r.Body)
	}
	expect(t, a.json("POST", "/api/auth/reauth", map[string]string{"password": testPassword}), 204)
	expect(t, a.json("DELETE", "/api/nodes/"+folder.ID+"?permanent=true", nil), 204)
	expect(t, a.json("POST", "/api/nodes/"+folder.ID+"/restore", nil), 404)
	// Its contents went with it, including the stored bytes.
	var objects int
	env.conn.QueryRow(`SELECT count(*) FROM versions WHERE node_id = ?`, inner.ID).Scan(&objects)
	if objects != 0 {
		t.Fatal("versions of a deleted file remain")
	}

	// Emptying the trash covers every drive the user can change: their own
	// and the shared family drive.
	expect(t, a.json("DELETE", "/api/nodes/"+loose.ID, nil), 204)
	expect(t, b.json("DELETE", "/api/nodes/"+shared.ID, nil), 204)
	r := a.json("DELETE", "/api/trash", nil)
	expect(t, r, 200)
	var res struct{ Deleted int }
	r.decode(t, &res)
	if res.Deleted != 2 {
		t.Fatalf("deleted %d", res.Deleted)
	}
	var trash []node
	b.get("/api/trash").decode(t, &trash)
	if len(trash) != 0 {
		t.Fatalf("B's trash = %+v", trash)
	}
}
