package server

import (
	"archive/zip"
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

type details struct {
	Node struct {
		ID, Name string
		Shared   bool
	}
	Drive struct {
		ID, Kind, Name, RootID, Role, SharedBy string
		CanShare                               bool
	}
	Path []struct{ ID, Name string }
}

func detailsOf(t *testing.T, c *client, id string) details {
	t.Helper()
	r := c.get("/api/nodes/" + id)
	expect(t, r, 200)
	var d details
	r.decode(t, &d)
	return d
}

func shareWith(t *testing.T, c *client, nodeID, userID, access string) {
	t.Helper()
	expect(t, c.json("PUT", "/api/nodes/"+nodeID+"/shares/"+userID, map[string]string{"access": access}), 204)
}

type sharing struct {
	Drive     struct{ Kind, Name string }
	Members   int
	Editors   []string
	People    []shareInfo
	Inherited []shareInfo
	Link      *linkInfo
}

type shareInfo struct {
	UserID, Name, Access string
	Disabled             bool
	From                 *struct{ ID, Name string }
}

type linkInfo struct {
	ID, Token, Expiry string
	ExpiresAt         *time.Time
	Expired, Password bool
}

func sharingOf(t *testing.T, c *client, id string) sharing {
	t.Helper()
	r := c.get("/api/nodes/" + id + "/shares")
	expect(t, r, 200)
	var s sharing
	r.decode(t, &s)
	return s
}

func auditEvents(t *testing.T, env *testEnv, prefix string) []string {
	t.Helper()
	rows, err := env.conn.Query(`SELECT event || ' ' || detail FROM audit_log WHERE event LIKE ? ORDER BY id`, prefix+"%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func nodeNames[T interface{ name() string }](list []T) string {
	var out []string
	for _, n := range list {
		out = append(out, n.name())
	}
	return strings.Join(out, "|")
}

func (n node) name() string { return n.Name }

// TestSharingAccess is the access matrix for sharing with people: drive
// viewer, editor and manager, Can view and Can edit recipients, an admin
// who isn't a member and a stranger.
func TestSharingAccess(t *testing.T) {
	env := newTestEnv(t)
	admin := env.signInAs(t, "a@example.com", "Admin", "admin")
	viewer := env.signIn(t, "v@example.com", "Viewer")
	editor := env.signIn(t, "e@example.com", "Editor")
	manager := env.signIn(t, "m@example.com", "Manager")
	canView := env.signIn(t, "cv@example.com", "Can View")
	canEdit := env.signIn(t, "ce@example.com", "Can Edit")
	stranger := env.signIn(t, "s@example.com", "Stranger")
	viewerID, editorID, managerID := userID(t, viewer), userID(t, editor), userID(t, manager)
	cvID, ceID, strangerID := userID(t, canView), userID(t, canEdit), userID(t, stranger)

	r := manager.json("POST", "/api/drives", map[string]string{"name": "Holiday house"})
	expect(t, r, 201)
	var d driveInfo
	r.decode(t, &d)
	members := "/api/drives/" + d.ID + "/members/"
	expect(t, manager.json("PUT", members+viewerID, map[string]string{"role": "viewer"}), 204)
	expect(t, manager.json("PUT", members+editorID, map[string]string{"role": "editor"}), 204)

	trips := mkdir(t, manager, d.RootID, "Trips")
	paris := mkdir(t, manager, trips.ID, "Paris")
	photo := upload(t, manager, paris.ID, "eiffel.jpg", []byte("jpeg"), 100)
	notes := upload(t, manager, trips.ID, "notes.txt", []byte("notes"), 100)
	secret := mkdir(t, manager, d.RootID, "Secret plans")
	upload(t, manager, secret.ID, "surprise.txt", []byte("party"), 100)
	shares := "/api/nodes/" + trips.ID + "/shares/"
	viewAccess := map[string]string{"access": "view"}

	// Editors and managers share. Viewers, recipients and people outside
	// the drive don't, admins included.
	shareWith(t, editor, trips.ID, cvID, "view")
	shareWith(t, manager, trips.ID, ceID, "edit")
	expectCode(t, viewer.json("PUT", shares+strangerID, viewAccess), 403, "read_only")
	expectCode(t, viewer.get("/api/nodes/"+trips.ID+"/shares"), 403, "read_only")
	expectCode(t, canView.get("/api/nodes/"+trips.ID+"/shares"), 403, "read_only")
	expectCode(t, canEdit.json("PUT", "/api/nodes/"+paris.ID+"/shares/"+strangerID, viewAccess), 403, "read_only")
	expectCode(t, canEdit.json("PUT", "/api/nodes/"+paris.ID+"/link", map[string]any{}), 403, "read_only")
	expectCode(t, canEdit.get("/api/nodes/"+trips.ID+"/link"), 403, "read_only")
	for _, c := range []*client{stranger, admin} {
		expect(t, c.json("PUT", shares+strangerID, viewAccess), 404)
		expect(t, c.json("DELETE", shares+cvID, nil), 404)
		expect(t, c.get("/api/nodes/"+trips.ID+"/shares"), 404)
		expect(t, c.json("PUT", "/api/nodes/"+trips.ID+"/link", map[string]any{}), 404)
	}

	// Only real people, and only those it adds something for.
	expectCode(t, manager.json("PUT", shares+cvID, map[string]string{"access": "manager"}), 400, "bad_request")
	expectCode(t, manager.json("PUT", shares+editorID, viewAccess), 409, "has_access")
	expectCode(t, editor.json("PUT", shares+editorID, viewAccess), 409, "has_access")
	expectCode(t, manager.json("PUT", shares+"0190a6e0-0000-7000-8000-000000000000", viewAccess), 404, "not_found")
	expectCode(t, manager.json("PUT", shares+"x", viewAccess), 404, "not_found")
	expectCode(t, manager.json("PUT", "/api/nodes/"+d.RootID+"/shares/"+cvID, viewAccess), 400, "drive_root")
	expectCode(t, manager.get("/api/nodes/"+d.RootID+"/shares"), 400, "drive_root")

	// The dialog: the drive's members as one count, its editors (to leave
	// out of Add people), the people on the item and those from above.
	sh := sharingOf(t, editor, trips.ID)
	slices.Sort(sh.Editors)
	wantEditors := []string{editorID, managerID}
	slices.Sort(wantEditors)
	if sh.Drive.Name != "Holiday house" || sh.Members != 3 || !slices.Equal(sh.Editors, wantEditors) || sh.Link != nil ||
		len(sh.People) != 2 || sh.People[0].UserID != ceID || sh.People[0].Access != "edit" ||
		sh.People[1].UserID != cvID || sh.People[1].Access != "view" || len(sh.Inherited) != 0 {
		t.Fatalf("sharing of Trips: %+v", sh)
	}
	sh = sharingOf(t, manager, paris.ID)
	if len(sh.People) != 0 || len(sh.Inherited) != 2 || sh.Inherited[0].From == nil || sh.Inherited[0].From.ID != trips.ID {
		t.Fatalf("sharing of Paris: %+v", sh)
	}
	// The people icon, for those who manage sharing only.
	var kids []struct {
		ID     string
		Shared bool
	}
	manager.get("/api/nodes/"+d.RootID+"/children").decode(t, &kids)
	for _, k := range kids {
		if k.Shared != (k.ID == trips.ID) {
			t.Fatalf("shared marks for the manager: %+v", kids)
		}
	}
	kids = nil
	viewer.get("/api/nodes/"+d.RootID+"/children").decode(t, &kids)
	for _, k := range kids {
		if k.Shared {
			t.Fatalf("shared marks for a viewer: %+v", kids)
		}
	}

	// Recipients see the shared item as the top of their own drive: no
	// folder names from above it, nothing next to it.
	got := detailsOf(t, canView, paris.ID)
	if got.Drive.Kind != "share" || got.Drive.Name != "Trips" || got.Drive.RootID != trips.ID || got.Drive.SharedBy != "Editor" ||
		got.Drive.Role != "viewer" || got.Drive.CanShare || len(got.Path) != 1 || got.Path[0].ID != trips.ID {
		t.Fatalf("Paris for Can view: %+v", got)
	}
	if got := detailsOf(t, canEdit, trips.ID); got.Drive.Role != "editor" || got.Drive.CanShare || len(got.Path) != 0 || got.Drive.SharedBy != "Manager" {
		t.Fatalf("Trips for Can edit: %+v", got)
	}
	if got := detailsOf(t, editor, paris.ID); got.Drive.Kind != "shared" || !got.Drive.CanShare || len(got.Path) != 2 {
		t.Fatalf("Paris for the editor: %+v", got)
	}
	for _, c := range []*client{canView, canEdit} {
		for _, p := range []string{"/api/nodes/" + paris.ID, "/api/shared", "/api/search?q=e", "/api/recent", "/api/nodes/" + trips.ID + "/children"} {
			if r := c.get(p); r.Code != 200 || strings.Contains(r.Body.String(), "Holiday") || strings.Contains(r.Body.String(), "Secret") {
				t.Fatalf("GET %s: %d %s", p, r.Code, r.Body)
			}
		}
	}
	for _, c := range []*client{canView, canEdit, stranger, admin} {
		expect(t, c.get("/api/nodes/"+secret.ID), 404)
		expect(t, c.get("/api/nodes/"+d.RootID), 404)
		expect(t, c.get("/api/nodes/"+d.RootID+"/children"), 404)
		expect(t, c.get("/api/nodes/"+d.RootID+"/zip"), 404)
	}
	for _, c := range []*client{stranger, admin} {
		expect(t, c.get("/api/nodes/"+trips.ID), 404)
		expect(t, c.get("/api/nodes/"+photo.ID+"/content"), 404)
		expect(t, c.get("/api/nodes/"+trips.ID+"/zip"), 404)
		var list []node
		c.get("/api/shared").decode(t, &list)
		if len(list) != 0 {
			t.Fatalf("shared with an outsider: %+v", list)
		}
	}

	// Shared with me: the item, who shared it and how.
	var mine []struct {
		node
		SharedBy, Access string
		SharedAt         time.Time
		Drive            struct{ Kind string }
	}
	canView.get("/api/shared").decode(t, &mine)
	if len(mine) != 1 || mine[0].ID != trips.ID || mine[0].SharedBy != "Editor" || mine[0].Access != "view" ||
		mine[0].Drive.Kind != "share" || time.Since(mine[0].SharedAt) > time.Minute || mine[0].ChildCount == nil || *mine[0].ChildCount != 2 {
		t.Fatalf("Can view's shared: %+v", mine)
	}

	// Can view: look, download, star; nothing else.
	expect(t, canView.get("/api/nodes/"+photo.ID+"/content"), 200)
	expect(t, canView.json("PATCH", "/api/nodes/"+trips.ID, map[string]bool{"starred": true}), 200)
	expectCode(t, canView.json("POST", "/api/folders", map[string]string{"parentId": trips.ID, "name": "x"}), 403, "read_only")
	expectCode(t, canView.json("PATCH", "/api/nodes/"+notes.ID, map[string]string{"name": "y.txt"}), 403, "read_only")
	expectCode(t, canView.json("DELETE", "/api/nodes/"+notes.ID, nil), 403, "read_only")
	r = canView.json("POST", "/api/uploads", map[string]any{"parentId": paris.ID, "name": "x", "size": 1})
	expectCode(t, r, 403, "read_only")

	// Can edit: change what is in the shared folder, but not the folder
	// itself, and don't take anything out of it.
	day1 := mkdir(t, canEdit, paris.ID, "Day 1")
	upload(t, canEdit, day1.ID, "louvre.jpg", []byte("jpeg"), 100)
	expect(t, canEdit.json("PATCH", "/api/nodes/"+paris.ID, map[string]string{"name": "Paris 2026"}), 200)
	expect(t, canEdit.json("PATCH", "/api/nodes/"+day1.ID, map[string]string{"parentId": trips.ID}), 200)
	expect(t, canEdit.json("PATCH", "/api/nodes/"+day1.ID, map[string]string{"parentId": paris.ID}), 200)
	expectCode(t, canEdit.json("PATCH", "/api/nodes/"+trips.ID, map[string]string{"name": "Mine"}), 403, "shared_item")
	expectCode(t, canEdit.json("DELETE", "/api/nodes/"+trips.ID, nil), 403, "shared_item")
	ceRoot := rootOf(t, canEdit, "personal")
	expectCode(t, canEdit.json("PATCH", "/api/nodes/"+trips.ID, map[string]string{"parentId": ceRoot}), 403, "shared_item")
	expectCode(t, canEdit.json("PATCH", "/api/nodes/"+notes.ID, map[string]string{"parentId": ceRoot}), 400, "move_out")
	expect(t, canEdit.json("PATCH", "/api/nodes/"+notes.ID, map[string]string{"parentId": secret.ID}), 404)
	expect(t, canEdit.json("PATCH", "/api/nodes/"+notes.ID, map[string]string{"parentId": d.RootID}), 404)
	// Bringing something in is fine.
	own := upload(t, canEdit, ceRoot, "tickets.pdf", []byte("%PDF"), 100)
	expect(t, canEdit.json("PATCH", "/api/nodes/"+own.ID, map[string]string{"parentId": trips.ID}), 200)
	if got := detailsOf(t, manager, own.ID); got.Drive.ID != d.ID {
		t.Fatalf("moved in, but in drive %s", got.Drive.ID)
	}

	// What a recipient trashes shows in their trash and the drive's; both
	// can restore it, only the drive's editors delete it for good.
	expect(t, canEdit.json("DELETE", "/api/nodes/"+notes.ID, nil), 204)
	var trash []node
	canEdit.get("/api/trash").decode(t, &trash)
	if nodeNames(trash) != "notes.txt" {
		t.Fatalf("Can edit's trash: %+v", trash)
	}
	editor.get("/api/trash").decode(t, &trash)
	if nodeNames(trash) != "notes.txt" {
		t.Fatalf("the editor's trash: %+v", trash)
	}
	canView.get("/api/trash").decode(t, &trash)
	if len(trash) != 0 {
		t.Fatalf("Can view's trash: %+v", trash)
	}
	if r := canEdit.get("/api/trash"); strings.Contains(r.Body.String(), "Holiday") {
		t.Fatalf("trash shows the drive: %s", r.Body)
	}
	expectCode(t, canEdit.json("DELETE", "/api/nodes/"+notes.ID+"?permanent=true", nil), 403, "read_only")
	r = canEdit.json("DELETE", "/api/trash", nil)
	expect(t, r, 200)
	if !strings.Contains(r.Body.String(), `"deleted":0`) {
		t.Fatalf("Can edit emptied the drive's trash: %s", r.Body)
	}
	expect(t, canEdit.json("POST", "/api/nodes/"+notes.ID+"/restore", nil), 200)

	// A drive viewer with an edit share edits there, but doesn't share.
	shareWith(t, manager, paris.ID, viewerID, "edit")
	mkdir(t, viewer, paris.ID, "From the viewer")
	expectCode(t, viewer.json("POST", "/api/folders", map[string]string{"parentId": trips.ID, "name": "x"}), 403, "read_only")
	expectCode(t, viewer.json("PATCH", "/api/nodes/"+paris.ID, map[string]string{"name": "x"}), 403, "shared_item")
	expectCode(t, viewer.get("/api/nodes/"+paris.ID+"/shares"), 403, "read_only")
	if got := detailsOf(t, viewer, paris.ID); got.Drive.Kind != "shared" || got.Drive.Role != "editor" || got.Drive.CanShare {
		t.Fatalf("Paris for the viewer: %+v", got)
	}

	// Access changes in place; the same access changes nothing.
	shareWith(t, manager, trips.ID, cvID, "view")
	shareWith(t, manager, trips.ID, cvID, "edit")
	mkdir(t, canView, trips.ID, "Now I can")
	shareWith(t, manager, trips.ID, cvID, "view")

	// Trashing the shared item hides it; restoring brings it back.
	expect(t, manager.json("DELETE", "/api/nodes/"+trips.ID, nil), 204)
	expect(t, canView.get("/api/nodes/"+paris.ID), 404)
	canView.get("/api/shared").decode(t, &mine)
	if len(mine) != 0 {
		t.Fatalf("shared while in the trash: %+v", mine)
	}
	expect(t, manager.json("POST", "/api/nodes/"+trips.ID+"/restore", nil), 200)
	expect(t, canView.get("/api/nodes/"+paris.ID), 200)

	// Disabled people can't be added; their shares stay, marked.
	if _, err := env.conn.Exec(`UPDATE users SET disabled_at = ? WHERE id IN (?, ?)`, db.FormatTime(time.Now()), strangerID, cvID); err != nil {
		t.Fatal(err)
	}
	expectCode(t, manager.json("PUT", shares+strangerID, viewAccess), 404, "not_found")
	sh = sharingOf(t, manager, trips.ID)
	if len(sh.People) != 2 || !sh.People[1].Disabled || sh.Members != 3 {
		t.Fatalf("sharing with a disabled person: %+v", sh)
	}

	// Removing a share ends it.
	expect(t, manager.json("DELETE", shares+ceID, nil), 204)
	expectCode(t, manager.json("DELETE", shares+ceID, nil), 404, "not_found")
	expect(t, canEdit.get("/api/nodes/"+trips.ID), 404)
	expect(t, canEdit.get("/api/nodes/"+paris.ID), 404)

	events := auditEvents(t, env, "share.")
	want := []string{
		"share.add " + trips.ID + " " + cvID + " view",
		"share.add " + trips.ID + " " + ceID + " edit",
		"share.add " + paris.ID + " " + viewerID + " edit",
		"share.change " + trips.ID + " " + cvID + " edit",
		"share.change " + trips.ID + " " + cvID + " view",
		"share.remove " + trips.ID + " " + ceID,
	}
	if !slices.Equal(events, want) {
		t.Fatalf("audit:\n%s\nwant\n%s", strings.Join(events, "\n"), strings.Join(want, "\n"))
	}
}

// TestSharingFromMyFiles covers sharing out of a personal drive.
func TestSharingFromMyFiles(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "Alex")
	b := env.signIn(t, "b@example.com", "Sam")
	aID, bID := userID(t, a), userID(t, b)
	root := rootOf(t, a, "personal")
	holiday := mkdir(t, a, root, "Holiday")
	mkdir(t, a, holiday.ID, "Day 1")
	mkdir(t, a, root, "Taxes")

	expectCode(t, a.json("PUT", "/api/nodes/"+holiday.ID+"/shares/"+aID, map[string]string{"access": "view"}), 409, "has_access")
	shareWith(t, a, holiday.ID, bID, "edit")
	sh := sharingOf(t, a, holiday.ID)
	if sh.Drive.Kind != "personal" || sh.Members != 0 || !slices.Equal(sh.Editors, []string{aID}) || len(sh.People) != 1 {
		t.Fatalf("sharing from My Files: %+v", sh)
	}
	got := detailsOf(t, b, holiday.ID)
	if got.Drive.Kind != "share" || got.Drive.SharedBy != "Alex" || got.Drive.Role != "editor" {
		t.Fatalf("Holiday for Sam: %+v", got)
	}
	mkdir(t, b, holiday.ID, "Day 2")
	expect(t, b.get("/api/nodes/"+root+"/children"), 404)
	if got := detailsOf(t, a, holiday.ID); !got.Node.Shared {
		t.Fatal("no people icon for the owner")
	}
}

func zipNames(t *testing.T, r response) ([]string, map[string]string) {
	t.Helper()
	body := r.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	contents := map[string]string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Method != zip.Store {
			t.Errorf("%s compressed with %d", f.Name, f.Method)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[f.Name] = string(b)
	}
	return names, contents
}

func TestFolderZip(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	root := rootOf(t, a, "personal")
	album := mkdir(t, a, root, "Zoë's album")
	sub := mkdir(t, a, album.ID, "Sub")
	mkdir(t, a, sub.ID, "Empty")
	upload(t, a, album.ID, "pic.jpg", []byte("JPEGDATA"), 100)
	upload(t, a, sub.ID, "deep.txt", []byte("deep text"), 100)
	gone := upload(t, a, album.ID, "gone.txt", []byte("trashed"), 100)
	expect(t, a.json("DELETE", "/api/nodes/"+gone.ID, nil), 204)
	file := upload(t, a, root, "loose.txt", []byte("loose"), 100)

	r := a.get("/api/nodes/" + album.ID + "/zip")
	expect(t, r, 200)
	h := r.Header()
	if h.Get("Content-Type") != "application/zip" || h.Get("Cache-Control") != "no-store" ||
		h.Get("Content-Disposition") != `attachment; filename*=utf-8''Zo%C3%AB%27s%20album.zip` {
		t.Fatalf("headers %v", h)
	}
	names, contents := zipNames(t, r)
	if strings.Join(names, "|") != "Sub/|Sub/Empty/|Sub/deep.txt|pic.jpg" || contents["Sub/deep.txt"] != "deep text" {
		t.Fatalf("zip %v %v", names, contents)
	}
	// A drive's top folder is named after the drive.
	r = a.get("/api/nodes/" + root + "/zip")
	expect(t, r, 200)
	if !strings.Contains(r.Header().Get("Content-Disposition"), `filename="My Files.zip"`) {
		t.Fatalf("top folder zip %q", r.Header().Get("Content-Disposition"))
	}
	expectCode(t, a.get("/api/nodes/"+file.ID+"/zip"), 400, "not_folder")
	expect(t, b.get("/api/nodes/"+album.ID+"/zip"), 404)
	expect(t, a.get("/api/nodes/"+gone.ID+"/zip"), 404)
}

func TestPublicLinks(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "Alex")
	b := env.signIn(t, "b@example.com", "Sam")
	root := rootOf(t, a, "personal")
	album := mkdir(t, a, root, "Album")
	sub := mkdir(t, a, album.ID, "Sub")
	pic := upload(t, a, album.ID, "pic.jpg", []byte("JPEGDATA"), 100)
	deep := upload(t, a, sub.ID, "deep.txt", []byte("deep text"), 100)
	gone := upload(t, a, album.ID, "gone.txt", []byte("trashed"), 100)
	expect(t, a.json("DELETE", "/api/nodes/"+gone.ID, nil), 204)
	outside := upload(t, a, root, "outside.txt", []byte("private"), 100)
	settings := "/api/nodes/" + album.ID + "/link"
	var tokens []string

	setLink := func(body any) linkInfo {
		t.Helper()
		r := a.json("PUT", settings, body)
		expect(t, r, 200)
		var l linkInfo
		r.decode(t, &l)
		return l
	}

	// Making a link.
	if r := a.get(settings); r.Code != 200 || strings.TrimSpace(r.Body.String()) != "null" {
		t.Fatalf("no link yet: %d %s", r.Code, r.Body)
	}
	expect(t, b.json("PUT", settings, map[string]any{}), 404)
	expectCode(t, a.json("PUT", "/api/nodes/"+root+"/link", map[string]any{}), 400, "drive_root")
	l := setLink(map[string]any{})
	if len(l.Token) != 22 || l.Expiry != "never" || l.ExpiresAt != nil || l.Password || l.Expired {
		t.Fatalf("new link %+v", l)
	}
	token := l.Token
	tokens = append(tokens, token)
	if got := sharingOf(t, a, album.ID); got.Link == nil || got.Link.Token != token {
		t.Fatalf("link in the dialog: %+v", got.Link)
	}
	if got := detailsOf(t, a, album.ID); !got.Node.Shared {
		t.Fatal("no people icon for a linked item")
	}

	// A visitor sees the linked folder and what is in it, nothing more.
	v := env.client(t)
	page := "/api/s/" + token
	r := v.get(page)
	expect(t, r, 200)
	if r.Header().Get("Cache-Control") != "no-store" || !strings.Contains(r.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("headers %v", r.Header())
	}
	var p struct {
		Node     struct{ ID, Name, Kind string }
		Path     []struct{ ID, Name string }
		Children []struct{ ID, Name, Kind string }
	}
	r.decode(t, &p)
	if p.Node.ID != album.ID || p.Node.Name != "Album" || len(p.Path) != 0 || len(p.Children) != 2 ||
		p.Children[0].Name != "Sub" || p.Children[1].Name != "pic.jpg" {
		t.Fatalf("link page %s", r.Body)
	}
	for _, leak := range []string{"Alex", "a@example.com", root, "driveId", "parentId", "sharedBy"} {
		if strings.Contains(r.Body.String(), leak) {
			t.Fatalf("link page shows %q: %s", leak, r.Body)
		}
	}
	r = v.get(page + "/nodes/" + deep.ID)
	expect(t, r, 200)
	p.Children = nil
	r.decode(t, &p)
	if p.Node.Name != "deep.txt" || len(p.Path) != 2 || p.Path[0].ID != album.ID || p.Path[1].ID != sub.ID || p.Children != nil {
		t.Fatalf("deep page %s", r.Body)
	}
	r = v.get(page + "/nodes/" + deep.ID + "/content")
	expect(t, r, 200)
	if r.Body.String() != "deep text" || !strings.HasPrefix(r.Header().Get("Content-Security-Policy"), "sandbox") ||
		r.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("content %q %v", r.Body, r.Header())
	}
	r = v.send("GET", page+"/nodes/"+pic.ID+"/content?download=1", "", nil, map[string]string{"Range": "bytes=0-3"})
	if r.Code != 206 || r.Body.String() != "JPEG" || !strings.HasPrefix(r.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("range %d %q %v", r.Code, r.Body, r.Header())
	}
	r = v.get(page + "/nodes/" + album.ID + "/zip")
	expect(t, r, 200)
	if names, _ := zipNames(t, r); strings.Join(names, "|") != "Sub/|Sub/deep.txt|pic.jpg" {
		t.Fatalf("zip %v", names)
	}
	if r.Header().Get("Content-Disposition") != `attachment; filename=Album.zip` {
		t.Fatalf("zip name %q", r.Header().Get("Content-Disposition"))
	}
	expectCode(t, v.get(page+"/nodes/"+pic.ID+"/zip"), 400, "not_folder")
	expectCode(t, v.get(page+"/nodes/"+sub.ID+"/content"), 404, "not_found")
	// IDs outside the linked folder, or in the trash, don't exist here.
	for _, id := range []string{outside.ID, root, gone.ID, "0190a6e0-0000-7000-8000-000000000000", "x"} {
		for _, suffix := range []string{"", "/content", "/zip"} {
			expectCode(t, v.get(page+"/nodes/"+id+suffix), 404, "not_found")
		}
	}
	// Visitors change nothing.
	for _, m := range []string{"PUT", "POST", "PATCH", "DELETE"} {
		if r := v.json(m, page+"/nodes/"+pic.ID, map[string]string{"name": "x"}); r.Code < 400 {
			t.Fatalf("%s by a visitor: %d", m, r.Code)
		}
	}

	// A link to a file shows just the file.
	fileLink := a.json("PUT", "/api/nodes/"+pic.ID+"/link", map[string]any{})
	expect(t, fileLink, 200)
	var fl linkInfo
	fileLink.decode(t, &fl)
	tokens = append(tokens, fl.Token)
	r = v.get("/api/s/" + fl.Token)
	expect(t, r, 200)
	p.Children = nil
	r.decode(t, &p)
	if p.Node.ID != pic.ID || p.Children != nil || len(p.Path) != 0 {
		t.Fatalf("file link page %s", r.Body)
	}
	expectCode(t, v.get("/api/s/"+fl.Token+"/nodes/"+album.ID), 404, "not_found")

	// Tokens that aren't a live link all look the same.
	flipped := "A" + token[1:]
	if token[0] == 'A' {
		flipped = "B" + token[1:]
	}
	for _, bad := range []string{"x", token[:21], token + "A", flipped, strings.Repeat("A", 22)} {
		expectCode(t, v.get("/api/s/"+bad), 404, "link_dead")
		expectCode(t, v.get("/api/s/"+bad+"/nodes/"+album.ID+"/content"), 404, "link_dead")
	}

	// Changing settings keeps the address. Expiry counts from now.
	l = setLink(map[string]string{"expiry": "7d"})
	if l.Token != token || l.Expiry != "7d" || l.ExpiresAt == nil || time.Until(*l.ExpiresAt) < 7*24*time.Hour-time.Minute || l.Expired {
		t.Fatalf("7 days %+v", l)
	}
	expectCode(t, a.json("PUT", settings, map[string]string{"expiry": "2d"}), 400, "bad_request")
	if _, err := env.conn.Exec(`UPDATE links SET expires_at = ? WHERE id = ?`, db.FormatTime(time.Now().Add(-time.Second)), l.ID); err != nil {
		t.Fatal(err)
	}
	expectCode(t, v.get(page), 404, "link_dead")
	if got := sharingOf(t, a, album.ID); got.Link == nil || !got.Link.Expired {
		t.Fatalf("expired link in the dialog: %+v", got.Link)
	}
	l = setLink(map[string]string{"expiry": "1d"})
	if time.Until(*l.ExpiresAt) < 24*time.Hour-time.Minute || l.Expired {
		t.Fatalf("1 day %+v", l)
	}
	expect(t, v.get(page), 200)
	if l = setLink(map[string]string{"expiry": "never"}); l.ExpiresAt != nil || l.Token != token {
		t.Fatalf("never %+v", l)
	}

	// A password: asked once, remembered in a cookie that only fits this
	// link and this password.
	expectCode(t, a.json("PUT", settings, map[string]string{"password": "short"}), 400, "link_password")
	expectCode(t, a.json("PUT", settings, map[string]string{"password": strings.Repeat("x", 257)}), 400, "link_password")
	if l = setLink(map[string]string{"password": "open sesame 1"}); !l.Password || l.Token != token {
		t.Fatalf("password %+v", l)
	}
	expectCode(t, v.get(page), 401, "link_locked")
	expectCode(t, v.get(page+"/nodes/"+deep.ID+"/content"), 401, "link_locked")
	expectCode(t, v.get(page+"/nodes/"+album.ID+"/zip"), 401, "link_locked")
	expectCode(t, v.json("POST", page+"/unlock", map[string]string{"password": "open sesame 2"}), 403, "wrong_password")
	r = v.json("POST", page+"/unlock", map[string]string{"password": "open sesame 1"})
	expect(t, r, 204)
	var grant string
	for _, c := range r.Result().Cookies() {
		if c.Name == "__Host-hd_link_"+l.ID {
			if !c.Secure || !c.HttpOnly || c.SameSite != 2 || c.Path != "/" || c.MaxAge != 12*3600 {
				t.Fatalf("cookie %+v", c)
			}
			grant = c.Value
		}
	}
	if grant == "" {
		t.Fatalf("no grant cookie: %v", r.Header())
	}
	withGrant := func(name, value string) response {
		return v.send("GET", page, "", nil, map[string]string{"Cookie": name + "=" + value})
	}
	expect(t, withGrant("__Host-hd_link_"+l.ID, grant), 200)
	expectCode(t, withGrant("__Host-hd_link_"+l.ID, grant[:len(grant)-1]+"A"), 401, "link_locked")
	expectCode(t, withGrant("__Host-hd_link_"+l.ID, ""), 401, "link_locked")
	expectCode(t, withGrant("__Host-hd_link_"+fl.ID, grant), 401, "link_locked")
	// A new password ends the grants given before.
	setLink(map[string]string{"password": "open sesame 2"})
	expectCode(t, withGrant("__Host-hd_link_"+l.ID, grant), 401, "link_locked")
	if l = setLink(map[string]string{"password": ""}); l.Password {
		t.Fatalf("password not removed: %+v", l)
	}
	expect(t, v.get(page), 200)
	expect(t, v.json("POST", page+"/unlock", map[string]string{"password": "anything"}), 204)

	// The link follows its item: a move keeps it, the trash pauses it.
	archive := mkdir(t, a, root, "Archive")
	expect(t, a.json("PATCH", "/api/nodes/"+album.ID, map[string]string{"parentId": archive.ID}), 200)
	expect(t, v.get(page), 200)
	expect(t, a.json("DELETE", "/api/nodes/"+archive.ID, nil), 204)
	expectCode(t, v.get(page), 404, "link_dead")
	expect(t, a.json("POST", "/api/nodes/"+archive.ID+"/restore", nil), 200)
	expect(t, v.get(page), 200)

	// Off is off for good; on again is a new address.
	expect(t, a.json("DELETE", settings, nil), 204)
	expect(t, a.json("DELETE", settings, nil), 204)
	expectCode(t, v.get(page), 404, "link_dead")
	l = setLink(map[string]any{})
	if l.Token == token {
		t.Fatal("the old address came back")
	}
	tokens = append(tokens, l.Token)
	expect(t, v.get("/api/s/"+l.Token), 200)

	// The page itself is the app, never cached.
	w := do(env.h, "GET", "/s/"+l.Token, nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("link page: %d %v", w.Code, w.Header())
	}

	// Tokens and passwords stay out of the logs and the audit log.
	logs := env.logs.String()
	if !strings.Contains(logs, "/api/s/…/nodes/") {
		t.Fatalf("link requests not logged: %s", logs)
	}
	events := auditEvents(t, env, "link.")
	for _, tok := range tokens {
		if strings.Contains(logs, tok) || strings.Contains(strings.Join(events, "\n"), tok) {
			t.Fatalf("token %s in the logs", tok)
		}
	}
	if strings.Contains(strings.Join(events, "\n"), "sesame") || strings.Contains(logs, "sesame") {
		t.Fatal("a link password was logged")
	}
	want := []string{
		"link.create " + album.ID,
		"link.create " + pic.ID,
		"link.change " + album.ID + " expiry=7d",
		"link.change " + album.ID + " expiry=1d",
		"link.change " + album.ID + " expiry=never",
		"link.change " + album.ID + " password=set",
		"link.change " + album.ID + " password=set",
		"link.change " + album.ID + " password=removed",
		"link.remove " + album.ID,
		"link.create " + album.ID,
	}
	if !slices.Equal(events, want) {
		t.Fatalf("audit:\n%s\nwant\n%s", strings.Join(events, "\n"), strings.Join(want, "\n"))
	}
}

func TestLinkLimits(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	f := upload(t, a, rootOf(t, a, "personal"), "f.txt", []byte("f"), 100)
	r := a.json("PUT", "/api/nodes/"+f.ID+"/link", map[string]string{"password": "open sesame 1"})
	expect(t, r, 200)
	var l linkInfo
	r.decode(t, &l)
	v := env.client(t)
	unlock := "/api/s/" + l.Token + "/unlock"

	// Wrong passwords per link and address.
	for range 5 {
		expectCode(t, v.json("POST", unlock, map[string]string{"password": "wrong guess"}), 403, "wrong_password")
	}
	r = v.json("POST", unlock, map[string]string{"password": "open sesame 1"})
	expectCode(t, r, 429, "rate_limited")
	if r.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}

	// Tokens that don't work, per address.
	for range 20 {
		expectCode(t, v.get("/api/s/"+strings.Repeat("A", 22)), 404, "link_dead")
	}
	expectCode(t, v.get("/api/s/"+l.Token), 429, "rate_limited")
}

func TestGate(t *testing.T) {
	g := newGate()
	if !g.enter("a", 2) || !g.enter("a", 2) || g.enter("a", 2) || !g.enter("b", 1) || g.enter("b", 1) {
		t.Fatal("gate let too many in")
	}
	g.leave("a")
	if !g.enter("a", 2) {
		t.Fatal("gate stayed shut")
	}
	g.leave("a")
	g.leave("a")
	g.leave("b")
	if len(g.n) != 0 {
		t.Fatalf("gate keeps %v", g.n)
	}
}

func TestLogPath(t *testing.T) {
	for in, want := range map[string]string{
		"/s/abc":                     "/s/…",
		"/s/abc/":                    "/s/…/",
		"/api/s/abc":                 "/api/s/…",
		"/api/s/abc/nodes/1/content": "/api/s/…/nodes/1/content",
		"/api/nodes/1":               "/api/nodes/1",
		"/share-target":              "/share-target",
		"/s":                         "/s",
	} {
		if got := logPath(in); got != want {
			t.Errorf("logPath(%q) = %q, want %q", in, got, want)
		}
	}
}
