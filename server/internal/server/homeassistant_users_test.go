package server

import (
	"context"
	"strings"
	"testing"
)

// Home Assistant users: accounts an admin adds with a Home Assistant
// username, without a Homedrive password or an invitation.

type haUserInfo struct {
	ID, Email, Name, Role, Status, HAUsername string
	HAOnly                                    bool
}

func addHAUser(t *testing.T, admin *client, body map[string]any) response {
	t.Helper()
	return admin.json("POST", "/api/admin/homeassistant/users", body)
}

// addAnna adds Anna as a Home Assistant user.
func addAnna(t *testing.T, admin *client) haUserInfo {
	t.Helper()
	r := addHAUser(t, admin, map[string]any{"email": "anna@example.com", "name": "Anna", "haUsername": "anna"})
	expect(t, r, 201)
	var created struct{ User haUserInfo }
	r.decode(t, &created)
	return created.User
}

// haSignIn logs in through Home Assistant and the emailed code.
func (e *testEnv) haSignIn(t *testing.T, username, password, email string) *client {
	t.Helper()
	c := e.client(t)
	r := haLogin(c, username, password)
	expect(t, r, 200)
	var ch struct{ Challenge string }
	r.decode(t, &ch)
	expect(t, c.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": e.mail.lastCode(t, email)}), 200)
	return c
}

func (e *testEnv) userByEmail(t *testing.T, admin *client, email string) haUserInfo {
	t.Helper()
	var users []haUserInfo
	admin.get("/api/admin/users").decode(t, &users)
	for _, u := range users {
		if u.Email == email {
			return u
		}
	}
	t.Fatalf("no user %s", email)
	return haUserInfo{}
}

func TestAddHomeAssistantUser(t *testing.T) {
	env, _ := newHomeAssistantEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	member := env.signIn(t, "bob@example.com", "Bob")
	r := admin.json("POST", "/api/drives", map[string]string{"name": "Holiday"})
	expect(t, r, 201)
	var holiday driveInfo
	r.decode(t, &holiday)

	body := map[string]any{"email": " Anna@Example.com ", "name": " Anna ", "haUsername": " Anna ", "locale": "nl",
		"driveIds": []string{holiday.ID}}
	expectCode(t, addHAUser(t, member, body), 403, "forbidden")
	expectCode(t, addHAUser(t, env.client(t), body), 401, "unauthenticated")

	// Only ever a member.
	body["role"] = "admin"
	expectCode(t, addHAUser(t, admin, body), 409, "ha_only_admin")
	body["role"] = "member"

	// Like any admin action, it needs a recent password.
	env.expireRecentAuth(t)
	expectCode(t, addHAUser(t, admin, body), 403, "reauth_required")
	expect(t, admin.json("POST", "/api/auth/reauth", map[string]string{"password": testPassword}), 204)

	sent := len(env.mail.sent)
	r = addHAUser(t, admin, body)
	expect(t, r, 201)
	var created struct{ User haUserInfo }
	r.decode(t, &created)
	anna := created.User
	if anna.Email != "anna@example.com" || anna.Name != "Anna" || anna.HAUsername != "anna" || anna.Role != "member" ||
		anna.Status != "active" || !anna.HAOnly {
		t.Fatalf("created %+v", anna)
	}
	// No invitation or password email.
	if len(env.mail.sent) != sent {
		t.Errorf("%d emails sent", len(env.mail.sent)-sent)
	}
	if got := env.auditEvents(t, "homeassistant_user_added"); len(got) != 1 || got[0] != "user "+anna.ID+" anna" {
		t.Errorf("audit = %q", got)
	}

	// Email addresses and Home Assistant usernames are taken once.
	expectCode(t, addHAUser(t, admin, map[string]any{"email": "ANNA@example.com", "name": "A", "haUsername": "other"}), 409, "email_taken")
	expectCode(t, addHAUser(t, admin, map[string]any{"email": "bob@example.com", "name": "B", "haUsername": "bob"}), 409, "email_taken")
	expectCode(t, addHAUser(t, admin, map[string]any{"email": "carol@example.com", "name": "Carol", "haUsername": "ANNA "}), 409, "ha_username_taken")
	expectCode(t, addHAUser(t, admin, map[string]any{"email": "carol@example.com", "name": "Carol", "haUsername": ""}), 400, "invalid_ha_username")
	expectCode(t, addHAUser(t, admin, map[string]any{"email": "carol", "name": "Carol", "haUsername": "carol"}), 400, "invalid_email")
	expectCode(t, addHAUser(t, admin, map[string]any{"email": "carol@example.com", "name": " ", "haUsername": "carol"}), 400, "invalid_name")
	expectCode(t, addHAUser(t, admin, map[string]any{"email": "carol@example.com", "name": "Carol", "haUsername": strings.Repeat("c", 101)}), 400, "invalid_ha_username")
	expectCode(t, addHAUser(t, admin, map[string]any{"email": "carol@example.com", "name": "Carol", "haUsername": "carol",
		"driveIds": []string{"nope"}}), 404, "not_found")
	// Each refusal was rolled back whole: the address is still free.
	expect(t, addHAUser(t, admin, map[string]any{"email": "carol@example.com", "name": "Carol", "haUsername": "carol"}), 201)

	// Anna logs in with Home Assistant and has her own drive and the one
	// picked for her, not the default Family drive.
	c := env.haSignIn(t, "anna", "anna's ha password", "anna@example.com")
	var names []string
	for _, d := range drivesOf(t, c) {
		names = append(names, d.Kind+":"+d.Name)
	}
	if strings.Join(names, ",") != "personal:My Files,shared:Holiday" {
		t.Errorf("drives = %q", names)
	}
	var me struct {
		Role, Locale string
		HAOnly       bool
	}
	c.get("/api/me").decode(t, &me)
	if me.Role != "member" || me.Locale != "nl" || !me.HAOnly {
		t.Errorf("me = %+v", me)
	}
}

func TestHomeAssistantUserLogin(t *testing.T) {
	env, sv := newHomeAssistantEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	anna := addAnna(t, admin)

	// Without a Homedrive password there is no email and password login,
	// whatever is typed.
	for _, pw := range []string{"", "anna's ha password", testPassword} {
		expectCode(t, env.client(t).json("POST", "/api/auth/login", map[string]string{"email": "anna@example.com", "password": pw}),
			401, "invalid_credentials")
	}

	expectCode(t, haLogin(env.client(t), "anna", "wrong"), 401, "ha_invalid_credentials")
	c := env.haSignIn(t, " ANNA", "anna's ha password", "anna@example.com")
	var me struct {
		ID     string
		HAOnly bool
	}
	c.get("/api/me").decode(t, &me)
	if me.ID != anna.ID || !me.HAOnly {
		t.Fatalf("me = %+v", me)
	}
	if u := env.userByEmail(t, admin, "anna@example.com"); u.Status != "active" || !u.HAOnly {
		t.Errorf("admin sees %+v", u)
	}

	// Disabled: signed out, and the wrong-password answer after the
	// Supervisor was asked.
	expect(t, admin.json("PATCH", "/api/admin/users/"+anna.ID, map[string]bool{"disabled": true}), 200)
	expect(t, c.get("/api/me"), 401)
	calls := sv.callCount()
	expectCode(t, haLogin(env.client(t), "anna", "anna's ha password"), 401, "ha_invalid_credentials")
	if sv.callCount() != calls+1 {
		t.Error("the Supervisor wasn't asked for a disabled account")
	}
	expect(t, admin.json("PATCH", "/api/admin/users/"+anna.ID, map[string]bool{"disabled": false}), 200)

	// An invitation that isn't accepted yet still can't log in this way.
	r := admin.json("POST", "/api/admin/users", map[string]string{"email": "bob@example.com", "name": "Bob", "role": "member"})
	expect(t, r, 201)
	var invited struct{ User haUserInfo }
	r.decode(t, &invited)
	if invited.User.Status != "invited" || invited.User.HAOnly {
		t.Fatalf("invited = %+v", invited.User)
	}
	expect(t, admin.json("PATCH", "/api/admin/users/"+invited.User.ID, map[string]string{"haUsername": "bob"}), 200)
	expectCode(t, haLogin(env.client(t), "bob", "bob's ha password"), 401, "ha_invalid_credentials")

	env.haSignIn(t, "anna", "anna's ha password", "anna@example.com")
	if strings.Contains(env.logs.String(), "ha password") {
		t.Error("a Home Assistant password is in the log")
	}
}

func TestHomeAssistantUserReauth(t *testing.T) {
	env, sv := newHomeAssistantEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	addAnna(t, admin)
	anna := env.haSignIn(t, "anna", "anna's ha password", "anna@example.com")

	env.expireRecentAuth(t)
	expectCode(t, anna.json("POST", "/api/me/devices", map[string]string{"name": "iPhone"}), 403, "reauth_required")

	reauth := func(body map[string]string) response {
		return anna.json("POST", "/api/auth/reauth", body)
	}
	expectCode(t, reauth(map[string]string{"password": "wrong"}), 401, "ha_invalid_credentials")
	expectCode(t, reauth(map[string]string{"password": ""}), 401, "ha_invalid_credentials")
	// Always her own Home Assistant user, whatever the request names.
	expectCode(t, reauth(map[string]string{"username": "bob", "password": "bob's ha password"}), 401, "ha_invalid_credentials")
	sv.mu.Lock()
	asked := sv.asked
	sv.mu.Unlock()
	if asked != "anna" {
		t.Errorf("the Supervisor was asked about %q", asked)
	}
	expectCode(t, anna.json("POST", "/api/me/devices", map[string]string{"name": "iPhone"}), 403, "reauth_required")

	expect(t, reauth(map[string]string{"password": "anna's ha password"}), 204)
	expect(t, anna.json("POST", "/api/me/devices", map[string]string{"name": "iPhone"}), 201)

	got := strings.Join(env.auditEvents(t, "reauth.homeassistant"), "\n")
	if want := "user failed anna\nuser failed anna\nuser failed anna\nuser anna"; got != want {
		t.Errorf("audit log:\n%s\nwant:\n%s", got, want)
	}

	// A Homedrive password is no use: there is none.
	env.expireRecentAuth(t)
	expectCode(t, reauth(map[string]string{"password": testPassword}), 401, "ha_invalid_credentials")

	// Home Assistant can't check passwords while it restarts.
	sv.mu.Lock()
	sv.down = true
	sv.mu.Unlock()
	expectCode(t, reauth(map[string]string{"password": "anna's ha password"}), 503, "homeassistant_unavailable")
}

func TestHomeAssistantUserReauthRateLimit(t *testing.T) {
	env, sv := newHomeAssistantEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	addAnna(t, admin)
	anna := env.haSignIn(t, "anna", "anna's ha password", "anna@example.com")
	// The limit is the Home Assistant login's, per Home Assistant username.
	for range 10 {
		expectCode(t, anna.json("POST", "/api/auth/reauth", map[string]string{"password": "guess"}), 401, "ha_invalid_credentials")
	}
	calls := sv.callCount()
	expectCode(t, anna.json("POST", "/api/auth/reauth", map[string]string{"password": "anna's ha password"}), 429, "rate_limited")
	expectCode(t, haLogin(env.client(t), "anna", "anna's ha password"), 429, "rate_limited")
	if sv.callCount() != calls {
		t.Error("the Supervisor was asked for a blocked username")
	}
}

func TestHomeAssistantUserLimits(t *testing.T) {
	env, _ := newHomeAssistantEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	info := addAnna(t, admin)
	anna := env.haSignIn(t, "anna", "anna's ha password", "anna@example.com")

	// Not an admin until there is a password.
	expectCode(t, admin.json("PATCH", "/api/admin/users/"+info.ID, map[string]string{"role": "admin"}), 409, "ha_only_admin")
	expectCode(t, admin.json("PATCH", "/api/admin/users/"+info.ID, map[string]any{"name": "Anna B", "role": "admin"}), 409, "ha_only_admin")
	if u := env.userByEmail(t, admin, "anna@example.com"); u.Role != "member" || u.Name != "Anna" {
		t.Errorf("partly changed: %+v", u)
	}
	expect(t, admin.json("PATCH", "/api/admin/users/"+info.ID, map[string]string{"role": "member"}), 200)
	// Nor can the database be made to.
	if _, err := env.conn.Exec(`UPDATE users SET role = 'admin' WHERE id = ?`, info.ID); err == nil {
		t.Error("the database made a Home Assistant user admin")
	}

	// Unlinking would leave no way to log in.
	expectCode(t, admin.json("PATCH", "/api/admin/users/"+info.ID, map[string]string{"haUsername": " "}), 409, "ha_only_unlink")
	expectCode(t, anna.json("DELETE", "/api/me/homeassistant", nil), 409, "ha_only_unlink")
	var link struct{ Username string }
	if anna.get("/api/me/homeassistant").decode(t, &link); link.Username != "anna" {
		t.Errorf("linked to %q", link.Username)
	}
	// Another Home Assistant user is fine, if it is free.
	expectCode(t, admin.json("PATCH", "/api/admin/users/"+info.ID, map[string]string{"haUsername": "admin"}), 200, "")
	expect(t, admin.json("PATCH", "/api/admin/users/"+info.ID, map[string]string{"haUsername": "anna"}), 200)

	// There is no current password to change; the emailed link sets one.
	expectCode(t, anna.json("POST", "/api/me/password", map[string]string{"current": "", "password": "a brand new password"}), 409, "no_password")
}

// Choosing a password through an emailed link makes a Home Assistant user a
// normal account; it stays linked. They may ask for the link themselves
// while signed in, or an admin sends it.
func TestHomeAssistantUserChoosesPassword(t *testing.T) {
	env, _ := newHomeAssistantEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	bob := env.signIn(t, "bob@example.com", "Bob")
	info := addAnna(t, admin)
	anna := env.haSignIn(t, "anna", "anna's ha password", "anna@example.com")

	expectCode(t, bob.json("POST", "/api/me/password-link", nil), 409, "has_password")
	expectCode(t, env.client(t).json("POST", "/api/me/password-link", nil), 401, "unauthenticated")
	env.expireRecentAuth(t)
	expectCode(t, anna.json("POST", "/api/me/password-link", nil), 403, "reauth_required")
	expect(t, anna.json("POST", "/api/auth/reauth", map[string]string{"password": "anna's ha password"}), 204)
	expect(t, anna.json("POST", "/api/me/password-link", nil), 204)
	env.mail.mu.Lock()
	last := env.mail.sent[len(env.mail.sent)-1]
	env.mail.mu.Unlock()
	if last.To != "anna@example.com" || last.Subject != "Choose a password for Homedrive" {
		t.Errorf("mail %q to %s", last.Subject, last.To)
	}
	selfToken := env.mail.lastLink(t, "anna@example.com")

	// The admin's link is a reset too, never an invitation; it replaces hers.
	expect(t, admin.json("POST", "/api/auth/reauth", map[string]string{"password": testPassword}), 204)
	expect(t, admin.json("POST", "/api/admin/users/"+info.ID+"/link", nil), 204)
	token := env.mail.lastLink(t, "anna@example.com")
	expect(t, env.client(t).json("POST", "/api/welcome/check", map[string]string{"token": selfToken}), 403)
	var link struct{ Email, Purpose string }
	env.client(t).json("POST", "/api/welcome/check", map[string]string{"token": token}).decode(t, &link)
	if link.Purpose != "reset" || link.Email != "anna@example.com" {
		t.Fatalf("link = %+v", link)
	}

	fresh := env.client(t)
	r := fresh.json("POST", "/api/welcome/finish", map[string]string{"token": token, "password": "anna's own password"})
	expect(t, r, 200)
	var me struct{ HAOnly bool }
	if r.decode(t, &me); me.HAOnly {
		t.Error("still without a password after choosing one")
	}
	expect(t, anna.get("/api/me"), 401)
	if u := env.userByEmail(t, admin, "anna@example.com"); u.HAOnly || u.Status != "active" || u.HAUsername != "anna" {
		t.Errorf("after choosing a password: %+v", u)
	}

	// Both ways in work now, and she can be an admin or unlink.
	expect(t, env.client(t).json("POST", "/api/auth/login", map[string]string{"email": "anna@example.com", "password": "anna's own password"}), 200)
	expect(t, haLogin(env.client(t), "anna", "anna's ha password"), 200)
	env.expireRecentAuth(t)
	expectCode(t, fresh.json("POST", "/api/auth/reauth", map[string]string{"password": "anna's ha password"}), 403, "wrong_password")
	expect(t, fresh.json("POST", "/api/auth/reauth", map[string]string{"password": "anna's own password"}), 204)
	expect(t, admin.json("POST", "/api/auth/reauth", map[string]string{"password": testPassword}), 204)
	expect(t, admin.json("PATCH", "/api/admin/users/"+info.ID, map[string]string{"role": "admin"}), 200)
	expect(t, fresh.json("DELETE", "/api/me/homeassistant", nil), 204)
}

// With the add-on option turned off again, a Home Assistant user can't
// confirm anything or log in; an admin's password link is the way back.
func TestHomeAssistantUserWithLoginOff(t *testing.T) {
	env := newTestEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	ctx := context.Background()
	info, err := env.accounts.AddHAUser(ctx, "anna@example.com", "Anna", "anna", "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A session from when the option was on.
	token, err := env.accounts.CreateSession(ctx, info.ID, "test", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	anna := env.client(t)
	anna.cookie = token
	expect(t, anna.get("/api/me"), 200)
	expectCode(t, env.client(t).json("POST", "/api/auth/login", map[string]string{"email": "anna@example.com", "password": ""}),
		401, "invalid_credentials")

	expect(t, admin.json("POST", "/api/admin/users/"+info.ID+"/link", nil), 204)
	link := env.mail.lastLink(t, "anna@example.com")
	env.expireRecentAuth(t)
	expectCode(t, anna.json("POST", "/api/auth/reauth", map[string]string{"password": "anna's ha password"}), 409, "homeassistant_off")

	expect(t, env.client(t).json("POST", "/api/welcome/finish", map[string]string{"token": link, "password": "anna's own password"}), 200)
	expect(t, env.client(t).json("POST", "/api/auth/login", map[string]string{"email": "anna@example.com", "password": "anna's own password"}), 200)
}
