package server

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

type storagePage struct {
	MyFiles struct {
		Files, Trash, OldVersions int64
		Types                     map[string]int64
	}
	Largest []struct {
		node
		Drive struct{ Kind, Name string }
	}
	Trash, OldVersions int64
	RetentionDays      int
}

func storageOf(t *testing.T, c *client) storagePage {
	t.Helper()
	r := c.get("/api/storage")
	expect(t, r, 200)
	var st storagePage
	r.decode(t, &st)
	return st
}

func filled(n int) []byte { return []byte(strings.Repeat("x", n)) }

// supersede makes the contents of the file from an old version of the
// file to, as if to had been saved over them, and returns their object.
func supersede(t *testing.T, env *testEnv, from, to string) string {
	t.Helper()
	var object string
	if err := env.conn.QueryRow(`SELECT object_id FROM versions WHERE node_id = ?`, from).Scan(&object); err != nil {
		t.Fatal(err)
	}
	if _, err := env.conn.Exec(`UPDATE versions SET node_id = ?, superseded_at = ? WHERE node_id = ?`,
		to, db.FormatTime(time.Now()), from); err != nil {
		t.Fatal(err)
	}
	if _, err := env.conn.Exec(`DELETE FROM nodes WHERE id = ?`, from); err != nil {
		t.Fatal(err)
	}
	return object
}

func objectExists(t *testing.T, env *testEnv, id string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(env.data, "objects", id[:2], id))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestStoragePage(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A") // manages Family
	b := env.signIn(t, "b@example.com", "B")
	c := env.signIn(t, "c@example.com", "C")
	aID := userID(t, a)
	root, family := rootOf(t, a, "personal"), rootOf(t, a, "shared")

	photo := upload(t, a, root, "photo.jpg", filled(300), 1000)
	upload(t, a, root, "notes.txt", filled(100), 1000)
	upload(t, a, root, "data.json", filled(50), 1000)
	upload(t, a, root, "README", filled(20), 1000)
	upload(t, a, root, "budget.csv", filled(40), 1000)
	// What is in a trashed folder counts once, also when it was trashed
	// itself before.
	old := mkdir(t, a, root, "Old")
	upload(t, a, old.ID, "film.mp4", filled(1000), 1000)
	sub := upload(t, a, old.ID, "sub.txt", filled(10), 1000)
	expect(t, a.json("DELETE", "/api/nodes/"+sub.ID, nil), 204)
	expect(t, a.json("DELETE", "/api/nodes/"+old.ID, nil), 204)

	upload(t, a, family, "family.zip", filled(500), 1000)
	upload(t, b, family, "b.pdf", filled(700), 1000)
	gone := upload(t, b, family, "gone.txt", filled(30), 1000)
	expect(t, b.json("DELETE", "/api/nodes/"+gone.ID, nil), 204)

	// A drive A may only view, and a file shared with A to edit, aren't
	// A's to clean up.
	r := c.json("POST", "/api/drives", map[string]string{"name": "Attic"})
	expect(t, r, 201)
	var attic driveInfo
	r.decode(t, &attic)
	expect(t, c.json("PUT", "/api/drives/"+attic.ID+"/members/"+aID, map[string]string{"role": "viewer"}), 204)
	huge := upload(t, c, attic.RootID, "huge.bin", filled(2000), 1000)
	song := upload(t, c, rootOf(t, c, "personal"), "song.mp3", filled(1500), 1000)
	shareWith(t, c, song.ID, aID, "edit")

	ownOld := supersede(t, env, upload(t, a, root, "photo-old.jpg", filled(80), 1000).ID, photo.ID)
	atticOld := supersede(t, env, upload(t, c, attic.RootID, "huge-old.bin", filled(60), 1000).ID, huge.ID)

	st := storageOf(t, a)
	if st.MyFiles.Files != 510 || st.MyFiles.Trash != 1010 || st.MyFiles.OldVersions != 80 {
		t.Fatalf("My Files = %+v", st.MyFiles)
	}
	want := map[string]int64{"documents": 100, "spreadsheets": 40, "pdfs": 0, "images": 300,
		"video": 0, "audio": 0, "archives": 0, "other": 70}
	if !maps.Equal(st.MyFiles.Types, want) {
		t.Fatalf("types = %v", st.MyFiles.Types)
	}
	if st.Trash != 1040 || st.OldVersions != 80 || st.RetentionDays != 30 {
		t.Fatalf("trash %d, old versions %d, retention %d", st.Trash, st.OldVersions, st.RetentionDays)
	}
	if got := nodeNames(st.Largest); got != "b.pdf|family.zip|photo.jpg|notes.txt|data.json|budget.csv|README" {
		t.Fatalf("largest = %s", got)
	}
	if d := st.Largest[0].Drive; d.Kind != "shared" || d.Name != "Family" {
		t.Fatalf("largest file's drive = %+v", d)
	}

	var meter struct {
		Used int64
		Disk *struct{ Total uint64 }
	}
	a.get("/api/storage/meter").decode(t, &meter)
	if meter.Used != 510 || meter.Disk != nil {
		t.Fatalf("member's meter = %+v", meter)
	}

	// Deleting old versions takes a recent password, and only touches
	// the drives A can edit.
	env.expireRecentAuth(t)
	if r := a.json("DELETE", "/api/old-versions", nil); r.errorCode() != "reauth_required" {
		t.Fatalf("stale session: %d %s", r.Code, r.Body)
	}
	expect(t, a.json("POST", "/api/auth/reauth", map[string]string{"password": testPassword}), 204)
	r = a.json("DELETE", "/api/old-versions", nil)
	expect(t, r, 200)
	var res struct{ Deleted, Freed int64 }
	r.decode(t, &res)
	if res.Deleted != 1 || res.Freed != 80 {
		t.Fatalf("deleted %+v", res)
	}
	if objectExists(t, env, ownOld) || !objectExists(t, env, atticOld) {
		t.Fatal("wrong old versions deleted")
	}
	expect(t, a.get("/api/nodes/"+photo.ID+"/content"), 200)
	if ev := auditEvents(t, env, "versions."); len(ev) != 1 || ev[0] != "versions.delete 1 80" {
		t.Fatalf("audit = %v", ev)
	}

	// Emptying the trash frees what the page said.
	r = a.json("DELETE", "/api/trash", nil)
	expect(t, r, 200)
	st = storageOf(t, a)
	if st.Trash != 0 || st.OldVersions != 0 || st.MyFiles.Trash != 0 || st.MyFiles.Files != 510 {
		t.Fatalf("after cleaning up: %+v", st)
	}
	if st := storageOf(t, c); st.OldVersions != 60 {
		t.Fatalf("C's old versions = %d", st.OldVersions)
	}
}

func TestHouseholdStorage(t *testing.T) {
	env := newTestEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin") // manages Family
	mia := env.signIn(t, "mia@example.com", "Mia")
	zoe := env.signIn(t, "zoe@example.com", "Zoe")
	upload(t, mia, rootOf(t, mia, "personal"), "a.txt", filled(100), 1000)
	trashed := upload(t, mia, rootOf(t, mia, "personal"), "b.txt", filled(10), 1000)
	expect(t, mia.json("DELETE", "/api/nodes/"+trashed.ID, nil), 204)
	upload(t, zoe, rootOf(t, zoe, "personal"), "z.txt", filled(5), 1000)
	upload(t, admin, rootOf(t, admin, "shared"), "f.txt", filled(50), 1000)
	if _, err := env.conn.Exec(`UPDATE users SET disabled_at = ? WHERE email = 'zoe@example.com'`, db.FormatTime(time.Now())); err != nil {
		t.Fatal(err)
	}

	expectCode(t, mia.get("/api/admin/storage"), 403, "forbidden")

	r := admin.get("/api/admin/storage")
	expect(t, r, 200)
	var h struct {
		Disk *struct {
			Total, Free, Used  uint64
			Homedrive, Reserve int64
		}
		People []struct {
			Name                      string
			Disabled                  bool
			Files, Trash, OldVersions int64
		}
		Drives []struct {
			Name                      string
			Members                   int
			Files, Trash, OldVersions int64
		}
	}
	r.decode(t, &h)
	if h.Disk == nil || h.Disk.Total == 0 || h.Disk.Used+h.Disk.Free != h.Disk.Total ||
		h.Disk.Homedrive != 165 || h.Disk.Reserve != 512_000_000 {
		t.Fatalf("disk = %+v", h.Disk)
	}
	if len(h.People) != 3 || h.People[0].Name != "Mia" || h.People[0].Files != 100 || h.People[0].Trash != 10 ||
		h.People[1].Name != "Zoe" || !h.People[1].Disabled || h.People[2].Name != "Admin" || h.People[2].Files != 0 {
		t.Fatalf("people = %+v", h.People)
	}
	if len(h.Drives) != 1 || h.Drives[0].Name != "Family" || h.Drives[0].Members != 3 || h.Drives[0].Files != 50 {
		t.Fatalf("drives = %+v", h.Drives)
	}
	// Sizes only: no file names.
	if strings.Contains(r.Body.String(), ".txt") {
		t.Fatalf("file names in %s", r.Body)
	}

	// The admins' meter is the data disk.
	var meter struct {
		Used int64
		Disk *struct{ Total uint64 }
	}
	admin.get("/api/storage/meter").decode(t, &meter)
	if meter.Used != 0 || meter.Disk == nil || meter.Disk.Total == 0 {
		t.Fatalf("admin's meter = %+v", meter)
	}
}
