package server

import (
	"regexp"
	"testing"
)

// lastLink returns the token of the most recent password link mailed to
// addr.
func (f *fakeMail) lastLink(t *testing.T, addr string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].To == addr {
			if m := regexp.MustCompile(`/welcome#(\S+)`).FindStringSubmatch(f.sent[i].Body); m != nil {
				return m[1]
			}
		}
	}
	t.Fatalf("no link mailed to %s", addr)
	return ""
}

// expireRecentAuth makes every session look like the password was entered
// long ago.
func (e *testEnv) expireRecentAuth(t *testing.T) {
	t.Helper()
	if _, err := e.conn.Exec(`UPDATE sessions SET recent_auth_at = '2000-01-01T00:00:00.000Z'`); err != nil {
		t.Fatal(err)
	}
}

type userInfo struct {
	ID, Email, Name, Role, Status string
}

func TestInviteAndWelcome(t *testing.T) {
	env := newTestEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")

	r := admin.json("POST", "/api/admin/users", map[string]string{
		"email": "Lotte@Example.com", "name": "Lotte", "role": "member", "locale": "nl"})
	expect(t, r, 201)
	var created struct{ User userInfo }
	r.decode(t, &created)
	if created.User.Status != "invited" || created.User.Email != "lotte@example.com" {
		t.Fatalf("created = %+v", created)
	}
	// Same address again is refused.
	if r := admin.json("POST", "/api/admin/users", map[string]string{"email": "lotte@example.com", "name": "L", "role": "member"}); r.Code != 409 {
		t.Fatalf("duplicate invite: %d", r.Code)
	}

	// An invited user can't sign in yet.
	anon := env.client(t)
	if r := anon.json("POST", "/api/auth/login", map[string]string{"email": "lotte@example.com", "password": ""}); r.Code != 401 {
		t.Fatalf("login before accepting: %d", r.Code)
	}

	token := env.mail.lastLink(t, "lotte@example.com")
	var link struct{ Email, Name, Purpose string }
	r = anon.json("POST", "/api/welcome/check", map[string]string{"token": token})
	expect(t, r, 200)
	r.decode(t, &link)
	if link.Email != "lotte@example.com" || link.Purpose != "invite" {
		t.Fatalf("link = %+v", link)
	}
	expect(t, anon.json("POST", "/api/welcome/check", map[string]string{"token": "nope"}), 403)
	if r := anon.json("POST", "/api/welcome/finish", map[string]string{"token": token, "password": "short"}); r.errorCode() != "password_too_short" {
		t.Fatalf("weak password: %s", r.Body)
	}
	expect(t, anon.json("POST", "/api/welcome/finish", map[string]string{"token": token, "password": "lotte's own secret"}), 200)
	expect(t, anon.get("/api/me"), 200)
	// The link works once.
	expect(t, env.client(t).json("POST", "/api/welcome/finish", map[string]string{"token": token, "password": "another secret pw"}), 403)
	// Lotte has her own drive and the family drive.
	var drives []struct{ Kind string }
	anon.get("/api/drives").decode(t, &drives)
	if len(drives) != 2 {
		t.Fatalf("drives = %+v", drives)
	}

	var users []userInfo
	admin.get("/api/admin/users").decode(t, &users)
	if len(users) != 2 || users[1].Name != "Lotte" || users[1].Status != "active" {
		t.Fatalf("users = %+v", users)
	}

	// Members can't manage users.
	if r := anon.get("/api/admin/users"); r.Code != 403 {
		t.Fatalf("member listing users: %d", r.Code)
	}
	if r := anon.json("POST", "/api/admin/users", map[string]string{"email": "x@example.com", "name": "X", "role": "admin"}); r.Code != 403 {
		t.Fatalf("member inviting: %d", r.Code)
	}
}

func TestAdminActionsNeedRecentPassword(t *testing.T) {
	env := newTestEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	env.expireRecentAuth(t)

	invite := map[string]string{"email": "c@example.com", "name": "C", "role": "member"}
	if r := admin.json("POST", "/api/admin/users", invite); r.errorCode() != "reauth_required" {
		t.Fatalf("stale session: %d %s", r.Code, r.Body)
	}
	if r := admin.json("POST", "/api/auth/reauth", map[string]string{"password": "wrong password!"}); r.errorCode() != "wrong_password" {
		t.Fatalf("wrong reauth: %d %s", r.Code, r.Body)
	}
	expect(t, admin.json("POST", "/api/auth/reauth", map[string]string{"password": testPassword}), 204)
	expect(t, admin.json("POST", "/api/admin/users", invite), 201)
}

func TestDisableAndReset(t *testing.T) {
	env := newTestEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	member := env.signIn(t, "m@example.com", "Member")
	var users []userInfo
	admin.get("/api/admin/users").decode(t, &users)
	var adminID, memberID string
	for _, u := range users {
		if u.Role == "admin" {
			adminID = u.ID
		} else {
			memberID = u.ID
		}
	}

	// No locking yourself out.
	if r := admin.json("PATCH", "/api/admin/users/"+adminID, map[string]any{"disabled": true}); r.errorCode() != "own_account" {
		t.Fatalf("disable self: %s", r.Body)
	}
	if r := admin.json("PATCH", "/api/admin/users/"+adminID, map[string]any{"role": "member"}); r.errorCode() != "own_account" {
		t.Fatalf("demote self: %s", r.Body)
	}

	// A password reset link: the old password keeps working until it's used,
	// then every session of that user ends.
	expect(t, admin.json("POST", "/api/admin/users/"+memberID+"/link", nil), 204)
	token := env.mail.lastLink(t, "m@example.com")
	var link struct{ Purpose string }
	env.client(t).json("POST", "/api/welcome/check", map[string]string{"token": token}).decode(t, &link)
	if link.Purpose != "reset" {
		t.Fatalf("purpose = %q", link.Purpose)
	}
	expect(t, member.get("/api/me"), 200)
	fresh := env.client(t)
	expect(t, fresh.json("POST", "/api/welcome/finish", map[string]string{"token": token, "password": "a brand new password"}), 200)
	expect(t, member.get("/api/me"), 401)
	expect(t, fresh.get("/api/me"), 200)

	// Disabling signs the user out at once and blocks sign-in.
	expect(t, admin.json("PATCH", "/api/admin/users/"+memberID, map[string]any{"disabled": true}), 200)
	expect(t, fresh.get("/api/me"), 401)
	if r := env.client(t).json("POST", "/api/auth/login", map[string]string{"email": "m@example.com", "password": "a brand new password"}); r.Code != 401 {
		t.Fatalf("disabled login: %d", r.Code)
	}
	if r := admin.json("POST", "/api/admin/users/"+memberID+"/link", nil); r.errorCode() != "user_disabled" {
		t.Fatalf("link for disabled user: %s", r.Body)
	}
	expect(t, admin.json("PATCH", "/api/admin/users/"+memberID, map[string]any{"disabled": false, "role": "admin"}), 200)
	// With two admins, one may step down.
	expect(t, admin.json("PATCH", "/api/admin/users/"+memberID, map[string]any{"name": "Promoted"}), 200)
}

func TestOwnAccount(t *testing.T) {
	env := newTestEnv(t)
	phone := env.signIn(t, "a@example.com", "A")
	// A second sign-in of the same user, on another device.
	laptop := env.client(t)
	r := laptop.json("POST", "/api/auth/login", map[string]string{"email": "a@example.com", "password": testPassword})
	var ch struct{ Challenge string }
	r.decode(t, &ch)
	expect(t, laptop.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": env.mail.lastCode(t, "a@example.com")}), 200)

	var sessions []struct {
		ID      string
		Current bool
	}
	laptop.get("/api/me/sessions").decode(t, &sessions)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v", sessions)
	}
	var other string
	for _, s := range sessions {
		if !s.Current {
			other = s.ID
		}
	}
	expect(t, laptop.json("DELETE", "/api/me/sessions/"+other, nil), 204)
	expect(t, phone.get("/api/me"), 401)
	expect(t, laptop.json("DELETE", "/api/me/sessions/"+other, nil), 404)

	// Language and name.
	r = laptop.json("PATCH", "/api/me", map[string]string{"locale": "nl", "name": " Anna "})
	expect(t, r, 200)
	var me struct{ Name, Locale string }
	r.decode(t, &me)
	if me.Locale != "nl" || me.Name != "Anna" {
		t.Fatalf("me = %+v", me)
	}

	// Changing the password needs the current one and ends other sessions.
	phone = env.client(t)
	r = phone.json("POST", "/api/auth/login", map[string]string{"email": "a@example.com", "password": testPassword})
	r.decode(t, &ch)
	expect(t, phone.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": env.mail.lastCode(t, "a@example.com")}), 200)
	if r := laptop.json("POST", "/api/me/password", map[string]string{"current": "not it at all", "password": "new and long enough"}); r.errorCode() != "wrong_password" {
		t.Fatalf("wrong current password: %s", r.Body)
	}
	if r := laptop.json("POST", "/api/me/password", map[string]string{"current": testPassword, "password": "password1234"}); r.errorCode() != "password_common" {
		t.Fatalf("common new password: %s", r.Body)
	}
	expect(t, laptop.json("POST", "/api/me/password", map[string]string{"current": testPassword, "password": "new and long enough"}), 204)
	expect(t, laptop.get("/api/me"), 200)
	expect(t, phone.get("/api/me"), 401)
	if r := env.client(t).json("POST", "/api/auth/login", map[string]string{"email": "a@example.com", "password": testPassword}); r.Code != 401 {
		t.Fatalf("old password still works: %d", r.Code)
	}
}
