package server

import (
	"strings"
	"testing"
)

type batchResult struct {
	Done    []string
	Skipped []struct{ ID, Code string }
}

func batch(t *testing.T, c *client, body map[string]any) (response, batchResult) {
	t.Helper()
	r := c.json("POST", "/api/nodes/batch", body)
	var res batchResult
	if r.Code == 200 {
		r.decode(t, &res)
	}
	return r, res
}

func skippedCodes(res batchResult) string {
	var codes []string
	for _, s := range res.Skipped {
		codes = append(codes, s.Code)
	}
	return strings.Join(codes, ",")
}

// TestBatchNodes checks that a selection request does no more than the
// single-item endpoints: every item is checked on its own, and what the
// user may not change is skipped with the same code.
func TestBatchNodes(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	aRoot, bRoot := rootOf(t, a, "personal"), rootOf(t, b, "personal")
	aFile := upload(t, a, aRoot, "a.txt", []byte("a"), 10)
	aDir := mkdir(t, a, aRoot, "Dir")
	bFile := upload(t, b, bRoot, "b.txt", []byte("b"), 10)

	// Validation.
	expectCode(t, a.json("POST", "/api/nodes/batch", map[string]any{"op": "explode", "ids": []string{aFile.ID}}), 400, "bad_request")
	expectCode(t, a.json("POST", "/api/nodes/batch", map[string]any{"op": "trash", "ids": []string{}}), 400, "bad_request")
	expectCode(t, a.json("POST", "/api/nodes/batch", map[string]any{"op": "move", "ids": []string{aFile.ID}}), 400, "bad_request")
	expectCode(t, a.json("POST", "/api/nodes/batch", map[string]any{"op": "star", "ids": []string{aFile.ID}}), 400, "bad_request")
	many := make([]string, maxBatch+1)
	for i := range many {
		many[i] = aFile.ID
	}
	expectCode(t, a.json("POST", "/api/nodes/batch", map[string]any{"op": "trash", "ids": many}), 400, "bad_request")
	expect(t, env.client(t).json("POST", "/api/nodes/batch", map[string]any{"op": "trash", "ids": []string{aFile.ID}}), 401)

	// B can't touch A's items through a selection; their own still works.
	for _, body := range []map[string]any{
		{"op": "trash", "ids": []string{aFile.ID, bFile.ID}},
		{"op": "star", "ids": []string{aFile.ID}, "starred": true},
		{"op": "move", "ids": []string{aFile.ID}, "parentId": bRoot},
		{"op": "move", "ids": []string{bFile.ID}, "parentId": aDir.ID},
		{"op": "restore", "ids": []string{aFile.ID}},
	} {
		r, res := batch(t, b, body)
		expect(t, r, 200)
		for _, s := range res.Skipped {
			if s.Code != "not_found" {
				t.Errorf("%v: skipped %s with %s", body, s.ID, s.Code)
			}
		}
		for _, id := range res.Done {
			if id == aFile.ID {
				t.Errorf("%v: B changed A's file", body)
			}
		}
	}
	expect(t, a.get("/api/nodes/"+aFile.ID), 200)
	expect(t, b.json("POST", "/api/nodes/"+bFile.ID+"/restore", nil), 200)

	// A viewer of a shared drive is skipped as read only.
	r := a.json("POST", "/api/drives", map[string]string{"name": "Club"})
	expect(t, r, 201)
	var club driveInfo
	r.decode(t, &club)
	expect(t, a.json("PUT", "/api/drives/"+club.ID+"/members/"+userID(t, b), map[string]string{"role": "viewer"}), 204)
	clubFile := upload(t, a, club.RootID, "rules.txt", []byte("rules"), 10)
	_, res := batch(t, b, map[string]any{"op": "trash", "ids": []string{clubFile.ID}})
	if len(res.Done) != 0 || skippedCodes(res) != "read_only" {
		t.Fatalf("viewer trash: %+v", res)
	}
	// Starring is the viewer's own business.
	_, res = batch(t, b, map[string]any{"op": "star", "ids": []string{clubFile.ID}, "starred": true})
	if len(res.Done) != 1 {
		t.Fatalf("viewer star: %+v", res)
	}

	// The owner moves, trashes and restores several at once.
	other := upload(t, a, aRoot, "c.txt", []byte("c"), 10)
	_, res = batch(t, a, map[string]any{"op": "move", "ids": []string{aFile.ID, other.ID, aDir.ID}, "parentId": aDir.ID})
	if len(res.Done) != 2 || skippedCodes(res) != "invalid_move" {
		t.Fatalf("move: %+v", res)
	}
	_, res = batch(t, a, map[string]any{"op": "trash", "ids": []string{aFile.ID, other.ID}})
	if len(res.Done) != 2 {
		t.Fatalf("trash: %+v", res)
	}
	_, res = batch(t, a, map[string]any{"op": "restore", "ids": []string{aFile.ID}})
	if len(res.Done) != 1 {
		t.Fatalf("restore: %+v", res)
	}

	// Deleting for good needs the password again and is audited like the
	// single delete.
	env.expireRecentAuth(t)
	expectCode(t, a.json("POST", "/api/nodes/batch", map[string]any{"op": "deleteForever", "ids": []string{other.ID}}), 403, "reauth_required")
	expect(t, a.json("POST", "/api/auth/reauth", map[string]string{"password": testPassword}), 204)
	_, res = batch(t, a, map[string]any{"op": "deleteForever", "ids": []string{other.ID, aFile.ID}})
	if len(res.Done) != 1 || res.Done[0] != other.ID || skippedCodes(res) != "not_trashed" {
		t.Fatalf("delete forever: %+v", res)
	}
	if got := env.auditEvents(t, "deleted_forever"); len(got) != 1 || got[0] != "user "+other.ID {
		t.Fatalf("audit %v", got)
	}
}

// TestZipSelection checks downloading a selection: the address is single
// use, only for the same user and session, and access is checked again
// when the download starts.
func TestZipSelection(t *testing.T) {
	env := newTestEnv(t)
	a := env.signIn(t, "a@example.com", "A")
	b := env.signIn(t, "b@example.com", "B")
	aRoot := rootOf(t, a, "personal")
	album := mkdir(t, a, aRoot, "Album")
	upload(t, a, album.ID, "pic.jpg", []byte("JPEG"), 100)
	one := upload(t, a, aRoot, "note.txt", []byte("one"), 100)
	sub := mkdir(t, a, aRoot, "Sub")
	two := upload(t, a, sub.ID, "note.txt", []byte("two"), 100)

	create := func(c *client, ids ...string) string {
		t.Helper()
		r := c.json("POST", "/api/zips", map[string]any{"ids": ids})
		expect(t, r, 201)
		var res struct{ URL string }
		r.decode(t, &res)
		if !strings.HasPrefix(res.URL, "/api/zips/") {
			t.Fatalf("url %q", res.URL)
		}
		return res.URL
	}

	url := create(a, album.ID, one.ID, two.ID)
	expect(t, b.get(url), 404)
	// The address was spent by B's attempt.
	expect(t, a.get(url), 404)

	url = create(a, album.ID, one.ID, two.ID, one.ID)
	r := a.get(url)
	expect(t, r, 200)
	if !strings.Contains(r.Header().Get("Content-Disposition"), "Homedrive.zip") {
		t.Fatalf("headers %v", r.Header())
	}
	names, contents := zipNames(t, r)
	if strings.Join(names, "|") != "Album/|Album/pic.jpg|note.txt|note (2).txt" ||
		contents["note.txt"] != "one" || contents["note (2).txt"] != "two" {
		t.Fatalf("zip %v %v", names, contents)
	}
	expect(t, a.get(url), 404)

	// Another session of the same user can't use it.
	url = create(a, one.ID)
	other := env.client(t)
	var ch struct{ Challenge string }
	r = other.json("POST", "/api/auth/login", map[string]string{"email": "a@example.com", "password": testPassword})
	r.decode(t, &ch)
	expect(t, other.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": env.mail.lastCode(t, "a@example.com")}), 200)
	expect(t, other.get(url), 404)

	// Access is checked when the address is made and again when it is used.
	expect(t, b.json("POST", "/api/zips", map[string]any{"ids": []string{one.ID}}), 404)
	url = create(a, one.ID)
	expect(t, a.json("DELETE", "/api/nodes/"+one.ID, nil), 204)
	expect(t, a.get(url), 404)

	// Bad requests and tokens.
	expectCode(t, a.json("POST", "/api/zips", map[string]any{"ids": []string{}}), 400, "bad_request")
	expect(t, a.get("/api/zips/not-a-token"), 404)
	expect(t, a.get("/api/zips/AAAAAAAAAAAAAAAAAAAAAA"), 404)

	// At most a few waiting addresses per user.
	for range zipTicketsPerUser {
		create(a, album.ID)
	}
	expect(t, a.json("POST", "/api/zips", map[string]any{"ids": []string{album.ID}}), 429)

	if got := logPath("/api/zips/AAAAAAAAAAAAAAAAAAAAAA"); got != "/api/zips/…" {
		t.Errorf("logPath = %q", got)
	}
}
