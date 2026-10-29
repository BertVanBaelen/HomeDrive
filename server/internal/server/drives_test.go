package server

import (
	"strings"
	"testing"
)

type driveInfo struct {
	ID, Kind, Name, RootID, Role string
}

func drivesOf(t *testing.T, c *client) []driveInfo {
	t.Helper()
	var drives []driveInfo
	r := c.get("/api/drives")
	expect(t, r, 200)
	r.decode(t, &drives)
	return drives
}

func driveNamed(t *testing.T, c *client, name string) (driveInfo, bool) {
	t.Helper()
	for _, d := range drivesOf(t, c) {
		if d.Name == name {
			return d, true
		}
	}
	return driveInfo{}, false
}

func userID(t *testing.T, c *client) string {
	t.Helper()
	var me struct{ ID string }
	c.get("/api/me").decode(t, &me)
	return me.ID
}

func expectCode(t *testing.T, r response, status int, code string) {
	t.Helper()
	if r.Code != status || r.errorCode() != code {
		t.Fatalf("got %d %s, want %d %s", r.Code, r.Body, status, code)
	}
}

func TestSharedDriveRoles(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A") // first user: manages Family
	b := env.signIn(t, "b@example.com", "B")
	aID, bID := userID(t, a), userID(t, b)

	r := a.json("POST", "/api/drives", map[string]string{"name": "  Holiday house "})
	expect(t, r, 201)
	var d driveInfo
	r.decode(t, &d)
	if d.Name != "Holiday house" || d.Kind != "shared" || d.Role != "manager" {
		t.Fatalf("created %+v", d)
	}
	members := "/api/drives/" + d.ID + "/members/"

	// Outsiders see nothing of it.
	if _, ok := driveNamed(t, b, "Holiday house"); ok {
		t.Fatal("B sees a drive they aren't in")
	}
	expect(t, b.get("/api/drives/"+d.ID+"/members"), 404)
	expect(t, b.get("/api/nodes/"+d.RootID+"/children"), 404)
	expect(t, b.json("PATCH", "/api/drives/"+d.ID, map[string]string{"name": "Mine"}), 404)
	expect(t, b.json("PUT", members+bID, map[string]string{"role": "manager"}), 404)

	// A viewer browses but can't change anything.
	expect(t, a.json("PUT", members+bID, map[string]string{"role": "viewer"}), 204)
	if got, _ := driveNamed(t, b, "Holiday house"); got.Role != "viewer" {
		t.Fatalf("B's role %+v", got)
	}
	expect(t, b.get("/api/nodes/"+d.RootID+"/children"), 200)
	expectCode(t, b.json("POST", "/api/folders", map[string]string{"parentId": d.RootID, "name": "x"}), 403, "read_only")
	expectCode(t, b.json("PATCH", "/api/drives/"+d.ID, map[string]string{"name": "Mine"}), 403, "read_only")
	expectCode(t, b.json("PUT", members+bID, map[string]string{"role": "manager"}), 403, "read_only")
	var list []struct{ UserID, Name, Role string }
	r = b.get("/api/drives/" + d.ID + "/members")
	expect(t, r, 200)
	r.decode(t, &list)
	if len(list) != 2 || list[0].UserID != aID || list[0].Role != "manager" || list[1].Role != "viewer" {
		t.Fatalf("members %+v", list)
	}

	// As an editor B adds things.
	expect(t, a.json("PUT", members+bID, map[string]string{"role": "editor"}), 204)
	mkdir(t, b, d.RootID, "Keys")
	expectCode(t, a.json("PUT", members+bID, map[string]string{"role": "owner"}), 400, "invalid_role")
	expect(t, a.json("PUT", members+"0190a6e0-0000-7000-8000-000000000000", map[string]string{"role": "editor"}), 404)

	// The last manager stays.
	expectCode(t, a.json("DELETE", members+aID, nil), 409, "last_manager")
	expectCode(t, a.json("PUT", members+aID, map[string]string{"role": "editor"}), 409, "last_manager")
	expect(t, a.json("PUT", members+bID, map[string]string{"role": "manager"}), 204)
	expect(t, a.json("PUT", members+aID, map[string]string{"role": "viewer"}), 204)
	expect(t, a.json("DELETE", members+aID, nil), 204) // leaving
	if _, ok := driveNamed(t, a, "Holiday house"); ok {
		t.Fatal("A still sees the drive after leaving")
	}
	expect(t, a.get("/api/nodes/"+d.RootID+"/children"), 404)

	// Names follow the rules.
	expectCode(t, b.json("PATCH", "/api/drives/"+d.ID, map[string]string{"name": " "}), 400, "invalid_name")
	expectCode(t, b.json("PATCH", "/api/drives/"+d.ID, map[string]string{"name": strings.Repeat("x", 61)}), 400, "invalid_name")
	expectCode(t, b.json("POST", "/api/drives", map[string]string{"name": "a/b"}), 400, "invalid_name")
	r = b.json("PATCH", "/api/drives/"+d.ID, map[string]string{"name": "Beach house"})
	expect(t, r, 200)
	r.decode(t, &d)
	if d.Name != "Beach house" {
		t.Fatalf("renamed %+v", d)
	}

	// Personal drives have no members and keep their name.
	personal := drivesOf(t, b)[0]
	expectCode(t, b.json("PATCH", "/api/drives/"+personal.ID, map[string]string{"name": "Stuff"}), 400, "personal_drive")
	expectCode(t, b.get("/api/drives/"+personal.ID+"/members"), 400, "personal_drive")
	expectCode(t, b.json("DELETE", "/api/drives/"+personal.ID, nil), 400, "personal_drive")
	expect(t, a.json("DELETE", "/api/drives/"+personal.ID, nil), 404)
}

func TestDeleteSharedDrive(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	r := a.json("POST", "/api/drives", map[string]string{"name": "Club"})
	var d driveInfo
	r.decode(t, &d)
	expect(t, a.json("PUT", "/api/drives/"+d.ID+"/members/"+userID(t, b), map[string]string{"role": "editor"}), 204)

	f := upload(t, b, d.RootID, "minutes.txt", []byte("notes"), 100)
	expectCode(t, b.json("DELETE", "/api/drives/"+d.ID, nil), 403, "read_only")
	expectCode(t, a.json("DELETE", "/api/drives/"+d.ID, nil), 409, "drive_not_empty")
	// Something in the trash still counts.
	expect(t, a.json("DELETE", "/api/nodes/"+f.ID, nil), 204)
	expectCode(t, a.json("DELETE", "/api/drives/"+d.ID, nil), 409, "drive_not_empty")
	expect(t, a.json("DELETE", "/api/nodes/"+f.ID+"?permanent=true", nil), 204)
	// An upload that never finished doesn't keep the drive alive.
	expect(t, b.json("POST", "/api/uploads", map[string]any{"parentId": d.RootID, "name": "late.txt", "size": 4}), 201)

	expect(t, a.json("DELETE", "/api/drives/"+d.ID, nil), 204)
	for _, c := range []*client{a, b} {
		if _, ok := driveNamed(t, c, "Club"); ok {
			t.Fatal("deleted drive still listed")
		}
	}
	expect(t, a.get("/api/nodes/"+d.RootID), 404)
	var n int
	if err := env.conn.QueryRow(`SELECT count(*) FROM drive_members WHERE drive_id = ?`, d.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("members left: %d %v", n, err)
	}
}

func TestAdminManagesDrives(t *testing.T) {
	env := newTestEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	adminID, bID := userID(t, admin), userID(t, b)

	r := a.json("POST", "/api/drives", map[string]string{"name": "Secret club"})
	var club driveInfo
	r.decode(t, &club)
	upload(t, a, club.RootID, "plans.txt", []byte("12345"), 100)

	expect(t, a.get("/api/admin/drives"), 403)
	expect(t, a.json("PUT", "/api/admin/drives/"+club.ID+"/members/"+bID, map[string]string{"role": "viewer"}), 403)

	type adminDrive struct {
		ID, Name      string
		Size          int64
		JoinByDefault bool
		Members       []struct{ UserID, Role string }
	}
	var list []adminDrive
	r = admin.get("/api/admin/drives")
	expect(t, r, 200)
	r.decode(t, &list)
	if len(list) != 2 || list[0].Name != "Family" || !list[0].JoinByDefault || len(list[0].Members) != 3 ||
		list[1].Name != "Secret club" || list[1].Size != 5 || list[1].JoinByDefault || len(list[1].Members) != 1 {
		t.Fatalf("admin drives %+v", list)
	}
	if strings.Contains(r.Body.String(), "plans.txt") {
		t.Fatal("admin list shows file names")
	}

	// The list doesn't open the drive: only membership does.
	expect(t, admin.get("/api/nodes/"+club.RootID+"/children"), 404)
	expect(t, admin.json("PUT", "/api/admin/drives/"+club.ID+"/members/"+bID, map[string]string{"role": "manager"}), 204)
	expect(t, b.get("/api/nodes/"+club.RootID+"/children"), 200)
	expect(t, admin.json("PUT", "/api/admin/drives/"+club.ID+"/members/"+adminID, map[string]string{"role": "viewer"}), 204)
	expect(t, admin.get("/api/nodes/"+club.RootID+"/children"), 200)
	expect(t, admin.json("DELETE", "/api/admin/drives/"+club.ID+"/members/"+adminID, nil), 204)
	// Admins keep a manager in the drive too.
	aID := userID(t, a)
	expect(t, admin.json("DELETE", "/api/admin/drives/"+club.ID+"/members/"+aID, nil), 204)
	expectCode(t, admin.json("DELETE", "/api/admin/drives/"+club.ID+"/members/"+bID, nil), 409, "last_manager")
	// Personal drives aren't on the list and can't be changed.
	personal := drivesOf(t, a)[0]
	expect(t, admin.json("PUT", "/api/admin/drives/"+personal.ID+"/members/"+bID, map[string]string{"role": "viewer"}), 404)

	var events []string
	rows, err := env.conn.Query(`SELECT event || ' ' || detail FROM audit_log WHERE event LIKE 'drive.%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var e string
		rows.Scan(&e)
		events = append(events, e)
	}
	rows.Close()
	want := []string{
		"drive.create " + club.ID,
		"drive.member " + club.ID + " add " + bID + " manager admin",
		"drive.member " + club.ID + " add " + adminID + " viewer admin",
		"drive.member " + club.ID + " remove " + adminID + " admin",
		"drive.member " + club.ID + " remove " + aID + " admin",
	}
	if strings.Join(events, "\n") != strings.Join(want, "\n") {
		t.Fatalf("audit:\n%s\nwant:\n%s", strings.Join(events, "\n"), strings.Join(want, "\n"))
	}

	// Join by default decides which drives an invitation picks without a choice.
	expect(t, admin.json("PATCH", "/api/admin/drives/"+club.ID, map[string]any{"joinByDefault": true}), 204)
	expect(t, admin.json("PATCH", "/api/admin/drives/"+club.ID, map[string]any{}), 400)
	expect(t, admin.json("POST", "/api/admin/users", map[string]any{"email": "c@example.com", "name": "C", "role": "member"}), 201)
	var family string
	for _, d := range list {
		if d.Name == "Family" {
			family = d.ID
		}
	}
	expect(t, admin.json("POST", "/api/admin/users", map[string]any{
		"email": "d@example.com", "name": "D", "role": "member", "driveIds": []string{}}), 201)
	expect(t, admin.json("POST", "/api/admin/users", map[string]any{
		"email": "e@example.com", "name": "E", "role": "member", "driveIds": []string{club.ID}}), 201)
	expect(t, admin.json("POST", "/api/admin/users", map[string]any{
		"email": "f@example.com", "name": "F", "role": "member", "driveIds": []string{personal.ID}}), 404)
	membership := func(email string) string {
		var ids []string
		rows, err := env.conn.Query(`SELECT m.drive_id || ':' || m.role FROM drive_members m JOIN users u ON u.id = m.user_id
			WHERE u.email = ? ORDER BY m.drive_id`, email)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			rows.Scan(&s)
			ids = append(ids, s)
		}
		return strings.Join(ids, ",")
	}
	if got, want := membership("c@example.com"), family+":editor,"+club.ID+":editor"; got != want {
		t.Errorf("default invite joined %q, want %q", got, want)
	}
	if got := membership("d@example.com"); got != "" {
		t.Errorf("invite without drives joined %q", got)
	}
	if got := membership("e@example.com"); got != club.ID+":editor" {
		t.Errorf("invite to club joined %q", got)
	}
	var exists bool
	env.conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE email = 'f@example.com')`).Scan(&exists)
	if exists {
		t.Error("failed invite left an account")
	}

	var people []struct{ ID, Name, Email string }
	r = b.get("/api/people")
	expect(t, r, 200)
	r.decode(t, &people)
	if len(people) != 6 {
		t.Errorf("people %+v", people)
	}
}
