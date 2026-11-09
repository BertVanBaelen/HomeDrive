package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
	"github.com/BertVanBaelen/HomeDrive/server/internal/sheet"
)

// editor is a signed-in user of the editing tests, with the drive's file.
type editorEnv struct {
	env                           *testEnv
	owner, member, viewer, nobody *client
	drive                         driveInfo
}

func newEditorEnv(t *testing.T) editorEnv {
	t.Helper()
	env := newTestEnv(t)
	e := editorEnv{env: env, owner: env.signIn(t, "a@example.com", "Anna"), member: env.signIn(t, "b@example.com", "Ben"),
		viewer: env.signIn(t, "v@example.com", "Vera"), nobody: env.signIn(t, "n@example.com", "Nick")}
	r := e.owner.json("POST", "/api/drives", map[string]string{"name": "Team"})
	expect(t, r, 201)
	r.decode(t, &e.drive)
	members := "/api/drives/" + e.drive.ID + "/members/"
	expect(t, e.owner.json("PUT", members+userID(t, e.member), map[string]string{"role": "editor"}), 204)
	expect(t, e.owner.json("PUT", members+userID(t, e.viewer), map[string]string{"role": "viewer"}), 204)
	return e
}

type editNode struct {
	node
	Version, Editor string
	CanEdit         bool
}

// create makes a file in the drive and returns it with its lock.
func (e editorEnv) create(t *testing.T, name string) (editNode, string) {
	t.Helper()
	r := e.owner.json("POST", "/api/files", map[string]string{"parentId": e.drive.RootID, "name": name})
	expect(t, r, 201)
	var out struct {
		Node   editNode
		LockID string
	}
	r.decode(t, &out)
	return out.Node, out.LockID
}

func saveText(c *client, id, lock, version, text string) response {
	return c.send("PUT", "/api/nodes/"+id+"/content", "text/plain; charset=utf-8", []byte(text),
		map[string]string{"X-Lock-Id": lock, "If-Match": `"` + version + `"`})
}

func lockURL(id string) string { return "/api/nodes/" + id + "/lock" }

func TestEditLocks(t *testing.T) {
	e := newEditorEnv(t)
	n, lock := e.create(t, "notes.md")
	if n.Editor != "markdown" || !n.CanEdit {
		t.Fatalf("created %+v", n)
	}
	expectCode(t, e.owner.json("POST", "/api/files", map[string]string{"parentId": e.drive.RootID, "name": "a.png"}), 400, "not_editable")
	expectCode(t, e.viewer.json("POST", "/api/files", map[string]string{"parentId": e.drive.RootID, "name": "v.md"}), 403, "read_only")

	// Only editors see who is editing, and only editors can lock.
	var st struct {
		Holder struct{ Name string }
		Mine   bool
	}
	r := e.member.get(lockURL(n.ID))
	expect(t, r, 200)
	r.decode(t, &st)
	if st.Holder.Name != "Anna" || st.Mine {
		t.Fatalf("status %s", r.Body)
	}
	expectCode(t, e.viewer.get(lockURL(n.ID)), 403, "read_only")
	expectCode(t, e.viewer.json("POST", lockURL(n.ID), nil), 403, "read_only")
	expectCode(t, e.nobody.get(lockURL(n.ID)), 404, "not_found")
	r = e.member.json("POST", lockURL(n.ID), nil)
	expectCode(t, r, 409, "locked")
	var body struct{ Holder string }
	r.decode(t, &body)
	if body.Holder != "Anna" {
		t.Fatalf("holder in %s", r.Body)
	}

	// The holder editing elsewhere is told so, and can take over.
	expectCode(t, e.owner.json("POST", lockURL(n.ID), nil), 409, "locked_self")
	r = e.owner.json("POST", lockURL(n.ID)+"?takeover=1", nil)
	expect(t, r, 200)
	var l struct{ LockId string }
	r.decode(t, &l)
	expectCode(t, saveText(e.owner, n.ID, lock, n.Version, "x"), 409, "lock_lost")
	expectCode(t, e.owner.json("PUT", lockURL(n.ID), map[string]string{"lockId": lock}), 409, "lock_lost")
	expect(t, e.owner.json("PUT", lockURL(n.ID), map[string]string{"lockId": l.LockId}), 204)
	if ev := auditEvents(t, e.env, "edit."); len(ev) != 1 || !strings.HasPrefix(ev[0], "edit.lock_takeover") {
		t.Fatalf("audit %v", ev)
	}

	// Another person's lock can't be released, the holder's can.
	expect(t, e.member.send("DELETE", lockURL(n.ID), "", nil, map[string]string{"X-Lock-Id": l.LockId}), 204)
	expectCode(t, e.member.json("POST", lockURL(n.ID), nil), 409, "locked")
	expect(t, e.owner.send("DELETE", lockURL(n.ID), "", nil, map[string]string{"X-Lock-Id": l.LockId}), 204)
	expect(t, e.member.json("POST", lockURL(n.ID), nil), 200)
}

func TestEditSaving(t *testing.T) {
	e := newEditorEnv(t)
	n, lock := e.create(t, "notes.txt")
	url := "/api/nodes/" + n.ID + "/content"

	r := saveText(e.owner, n.ID, lock, n.Version, "hello")
	expect(t, r, 200)
	var saved editNode
	r.decode(t, &saved)
	if saved.Size != 5 || saved.Version == n.Version {
		t.Fatalf("saved %+v", saved)
	}
	r = saveText(e.owner, n.ID, lock, saved.Version, "hello again")
	expect(t, r, 200)
	r.decode(t, &saved)

	// Everything that must be refused.
	badRequest := func(name, ctype string, body []byte, h map[string]string, status int, code string) {
		t.Helper()
		if r := e.owner.send("PUT", url, ctype, body, h); r.Code != status || r.errorCode() != code {
			t.Errorf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	ok := map[string]string{"X-Lock-Id": lock, "If-Match": `"` + saved.Version + `"`}
	badRequest("no If-Match", "text/plain", []byte("x"), map[string]string{"X-Lock-Id": lock}, 400, "bad_request")
	badRequest("not text/plain", "application/json", []byte("x"), ok, 415, "unsupported_media_type")
	badRequest("NUL", "text/plain", []byte("a\x00b"), ok, 400, "invalid_text")
	badRequest("not UTF-8", "text/plain", []byte{0xff, 0xfe}, ok, 400, "invalid_text")
	badRequest("too big", "text/plain", []byte(strings.Repeat("x", 1<<20+1)), ok, 413, "too_large")
	expectCode(t, saveText(e.viewer, n.ID, lock, saved.Version, "x"), 403, "read_only")
	expectCode(t, saveText(e.nobody, n.ID, lock, saved.Version, "x"), 404, "not_found")
	expectCode(t, saveText(e.member, n.ID, lock, saved.Version, "x"), 409, "lock_lost")

	// A save from a version someone else replaced says who and when.
	expect(t, e.owner.send("DELETE", lockURL(n.ID), "", nil, map[string]string{"X-Lock-Id": lock}), 204)
	var l struct{ LockId string }
	e.member.json("POST", lockURL(n.ID), nil).decode(t, &l)
	var bens editNode
	r = saveText(e.member, n.ID, l.LockId, saved.Version, "ben's")
	expect(t, r, 200)
	r.decode(t, &bens)
	e.member.send("DELETE", lockURL(n.ID), "", nil, map[string]string{"X-Lock-Id": l.LockId})
	e.owner.json("POST", lockURL(n.ID), nil).decode(t, &l)
	r = saveText(e.owner, n.ID, l.LockId, saved.Version, "late")
	expectCode(t, r, 412, "edit_conflict")
	var c struct{ By, At string }
	r.decode(t, &c)
	if c.By != "Ben" || c.At == "" {
		t.Fatalf("conflict body %s", r.Body)
	}

	// An image has no editor; the save is refused whoever asks.
	img := upload(t, e.owner, e.drive.RootID, "p.png", []byte("png"), 10)
	expectCode(t, saveText(e.owner, img.ID, "x", "y", "z"), 400, "not_editable")
	expectCode(t, e.owner.json("POST", lockURL(img.ID), nil), 400, "not_editable")
}

func TestVersionsAndRestore(t *testing.T) {
	e := newEditorEnv(t)
	n, lock := e.create(t, "notes.md")
	v1 := n.Version
	var saved editNode
	r := saveText(e.owner, n.ID, lock, v1, "# first")
	expect(t, r, 200)
	r.decode(t, &saved)
	url := "/api/nodes/" + n.ID + "/versions"

	var vs []struct {
		ID      string
		Current bool
		By      string
	}
	r = e.member.get(url)
	expect(t, r, 200)
	r.decode(t, &vs)
	if len(vs) != 2 || !vs[0].Current || vs[0].ID != saved.Version || vs[1].ID != v1 || vs[0].By != "Anna" {
		t.Fatalf("versions %s", r.Body)
	}
	// Whoever can't edit sees no history, not even that there is one.
	for _, c := range []*client{e.viewer, e.nobody} {
		if r := c.get(url); r.Code != 403 && r.Code != 404 {
			t.Fatalf("history for a non-editor: %d", r.Code)
		}
		if r := c.get(url + "/" + v1 + "/content"); r.Code != 403 && r.Code != 404 {
			t.Fatalf("old content for a non-editor: %d", r.Code)
		}
	}
	expectCode(t, e.owner.get(url+"/0190a6e0-0000-7000-8000-000000000000/content"), 404, "not_found")

	// Old content is a download, never shown in the page.
	r = e.owner.get(url + "/" + v1 + "/content")
	expect(t, r, 200)
	if cd := r.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || r.Body.Len() != 0 || r.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("old version served as %q, %d bytes, headers %v", cd, r.Body.Len(), r.Header())
	}

	// Restoring waits for the editor to finish, then keeps the history.
	restore := func(c *client, vid string) response { return c.json("POST", url+"/"+vid+"/restore", nil) }
	expectCode(t, restore(e.member, v1), 409, "locked")
	expectCode(t, restore(e.viewer, v1), 403, "read_only")
	expect(t, e.owner.send("DELETE", lockURL(n.ID), "", nil, map[string]string{"X-Lock-Id": lock}), 204)
	r = restore(e.member, v1)
	expect(t, r, 200)
	var restored editNode
	r.decode(t, &restored)
	if restored.Size != 0 || restored.Version == v1 || restored.Version == saved.Version {
		t.Fatalf("restored %+v", restored)
	}
	r = e.owner.get(url)
	r.decode(t, &vs)
	if len(vs) != 3 {
		t.Fatalf("history after restore: %s", r.Body)
	}
	if ev := auditEvents(t, e.env, "version."); len(ev) != 1 {
		t.Fatalf("audit %v", ev)
	}
}

// replaceUpload starts an upload that ends in the same name; it returns the
// start response.
func replaceUpload(c *client, parent, name, conflict string) response {
	return c.json("POST", "/api/uploads", map[string]any{"parentId": parent, "name": name, "size": 3, "conflict": conflict})
}

func finishUpload(t *testing.T, c *client, r response) node {
	t.Helper()
	expect(t, r, 201)
	var u struct{ ID string }
	r.decode(t, &u)
	expect(t, c.send("PUT", fmt.Sprintf("/api/uploads/%s?offset=0", u.ID), "application/octet-stream", []byte("new"), nil), 200)
	r = c.json("POST", "/api/uploads/"+u.ID+"/complete", nil)
	expect(t, r, 201)
	var n node
	r.decode(t, &n)
	return n
}

func TestReplaceOnUpload(t *testing.T) {
	e := newEditorEnv(t)
	orig := upload(t, e.owner, e.drive.RootID, "photo.txt", []byte("old"), 10)
	root := e.drive.RootID

	// Ask says what is in the way; the default keeps both.
	r := replaceUpload(e.member, root, "Photo.txt", "ask")
	expectCode(t, r, 409, "name_taken")
	var taken struct{ Existing node }
	r.decode(t, &taken)
	if taken.Existing.ID != orig.ID {
		t.Fatalf("existing %s", r.Body)
	}
	if n := finishUpload(t, e.member, replaceUpload(e.member, root, "photo.txt", "")); n.Name != "photo (2).txt" {
		t.Fatalf("default named %q", n.Name)
	}
	expectCode(t, replaceUpload(e.member, root, "photo.txt", "overwrite"), 400, "bad_request")
	expectCode(t, replaceUpload(e.viewer, root, "photo.txt", "replace"), 403, "read_only")
	expectCode(t, replaceUpload(e.nobody, root, "photo.txt", "replace"), 404, "not_found")

	// Replace keeps the file, adds a version, and waits for an editor.
	var l struct{ LockId string }
	e.owner.json("POST", lockURL(orig.ID), nil).decode(t, &l)
	expectCode(t, replaceUpload(e.member, root, "photo.txt", "replace"), 409, "locked")
	e.owner.send("DELETE", lockURL(orig.ID), "", nil, map[string]string{"X-Lock-Id": l.LockId})
	n := finishUpload(t, e.member, replaceUpload(e.member, root, "photo.txt", "replace"))
	if n.ID != orig.ID || n.Size != 3 {
		t.Fatalf("replaced %+v", n)
	}
	var vs []struct{ ID string }
	e.owner.get("/api/nodes/"+orig.ID+"/versions").decode(t, &vs)
	if len(vs) != 2 {
		t.Fatalf("%d versions after replace", len(vs))
	}
	if ev := auditEvents(t, e.env, "upload."); len(ev) != 1 || !strings.HasPrefix(ev[0], "upload.replace") {
		t.Fatalf("audit %v", ev)
	}
}

func TestEditableMarks(t *testing.T) {
	e := newEditorEnv(t)
	root := e.drive.RootID
	upload(t, e.owner, root, "a.md", []byte("x"), 10)
	upload(t, e.owner, root, "b.png", []byte("x"), 10)
	kids := func(c *client) map[string]editNode {
		var list []editNode
		c.get("/api/nodes/"+root+"/children").decode(t, &list)
		m := map[string]editNode{}
		for _, k := range list {
			m[k.Name] = k
		}
		return m
	}
	if a := kids(e.owner)["a.md"]; !a.CanEdit || a.Editor != "markdown" {
		t.Errorf("owner's a.md: %+v", a)
	}
	if b := kids(e.owner)["b.png"]; !b.CanEdit || b.Editor != "" {
		t.Errorf("owner's b.png: %+v", b)
	}
	if a := kids(e.viewer)["a.md"]; a.CanEdit || a.Editor != "" {
		t.Errorf("viewer's a.md: %+v", a)
	}
	if r := e.owner.get("/api/recent"); !strings.Contains(r.Body.String(), `"editor":"markdown"`) {
		t.Errorf("recent lacks the editor: %s", r.Body)
	}
}

type sheetView struct {
	Version  string
	Editable bool
	Reason   string
	Grid     sheet.Grid
	Sheets   []sheet.Sheet
}

func TestSpreadsheets(t *testing.T) {
	if files.MaxSheetFile != sheet.MaxFileBytes {
		t.Fatal("the files and sheet packages disagree on the size limit")
	}
	e := newEditorEnv(t)
	n, lock := e.create(t, "budget.xlsx")
	if n.Editor != "sheet" {
		t.Fatalf("created %+v", n)
	}
	sheetURL := "/api/nodes/" + n.ID + "/sheet"
	put := func(c *client, lock, version string, cells map[string]string) response {
		b, _ := json.Marshal(map[string]any{"rows": 20, "cols": 8, "cells": cells})
		return c.send("PUT", sheetURL, "application/json", b, map[string]string{"X-Lock-Id": lock, "If-Match": `"` + version + `"`})
	}
	var view sheetView
	read := func(c *client) (v sheetView) {
		r := c.get(sheetURL)
		expect(t, r, 200)
		r.decode(t, &v)
		return v
	}

	view = read(e.owner)
	if !view.Editable || view.Version != n.Version || view.Grid.Rows < sheet.MinRows {
		t.Fatalf("blank sheet: %+v", view)
	}

	// The server works out results itself and the next save starts from the new version.
	r := put(e.owner, lock, n.Version, map[string]string{"A1": "2", "A2": "3", "A3": "=SUM(A1:A2)"})
	expect(t, r, 200)
	var saved editNode
	r.decode(t, &saved)
	if cell := read(e.owner).Grid.Cells["A3"]; cell.Raw != "=SUM(A1:A2)" || cell.V != "5" {
		t.Fatalf("A3 = %+v", cell)
	}
	expectCode(t, put(e.owner, lock, n.Version, map[string]string{"A1": "9"}), 412, "edit_conflict")
	expectCode(t, put(e.owner, "wrong", saved.Version, map[string]string{"A1": "9"}), 409, "lock_lost")
	for _, bad := range []string{`=WEBSERVICE("x")`, "=INDIRECT(A1)", "=1+", `='C:\x[1]S'!A1`} {
		expectCode(t, put(e.owner, lock, saved.Version, map[string]string{"B1": bad}), 400, "invalid_sheet")
	}
	expectCode(t, saveText(e.owner, n.ID, lock, saved.Version, "x"), 400, "not_editable")

	// Others only read; a workbook beyond the grid opens read-only with its values.
	if v := read(e.viewer); v.Editable || v.Reason != "read_only" || len(v.Sheets) != 1 || v.Sheets[0].Cells["A3"] != "5" {
		t.Errorf("viewer: %+v", v)
	}
	expectCode(t, e.nobody.get(sheetURL), 404, "not_found")
	two := excelize.NewFile()
	two.NewSheet("Two")
	buf, _ := two.WriteToBuffer()
	multi := upload(t, e.owner, e.drive.RootID, "multi.xlsx", buf.Bytes(), 1<<20)
	sheetURL = "/api/nodes/" + multi.ID + "/sheet"
	if v := read(e.owner); v.Editable || v.Reason != "multiple_sheets" || len(v.Sheets) != 2 {
		t.Errorf("multi: %+v", v)
	}
	expectCode(t, e.owner.get("/api/nodes/"+upload(t, e.owner, e.drive.RootID, "a.md", []byte("x"), 10).ID+"/sheet"), 400, "not_editable")
}
