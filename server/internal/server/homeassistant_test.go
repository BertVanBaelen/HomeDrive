package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeSupervisor answers POST /auth like the Home Assistant Supervisor:
// 200 for a right username and password, 401 otherwise.
type fakeSupervisor struct {
	mu    sync.Mutex
	users map[string]string // username → password
	calls int
	down  bool
}

func (f *fakeSupervisor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if r.Method != http.MethodPost || r.URL.Path != "/auth" || r.Header.Get("X-Supervisor-Token") != "sv-token" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if f.down {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body struct{ Username, Password string }
	if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	// Home Assistant compares usernames ignoring case.
	if pw, ok := f.users[strings.ToLower(body.Username)]; !ok || pw != body.Password {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Write([]byte(`{"result":"ok","data":{}}`))
}

func (f *fakeSupervisor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// newHomeAssistantEnv is a test server running as the add-on with
// homeassistant_login on, and the Supervisor it asks.
func newHomeAssistantEnv(t *testing.T) (*testEnv, *fakeSupervisor) {
	t.Helper()
	sv := &fakeSupervisor{users: map[string]string{"anna": "anna's ha password", "bob": "bob's ha password"}}
	ts := httptest.NewServer(sv)
	t.Cleanup(ts.Close)
	return newTestEnv(t, "HOMEDRIVE_HOMEASSISTANT_LOGIN=true", "HOMEDRIVE_SUPERVISOR_URL="+ts.URL,
		"SUPERVISOR_TOKEN=sv-token"), sv
}

func (e *testEnv) auditEvents(t *testing.T, event string) []string {
	t.Helper()
	rows, err := e.conn.Query(`SELECT coalesce(user_id, ''), detail FROM audit_log WHERE event = ? ORDER BY id`, event)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var user, detail string
		if err := rows.Scan(&user, &detail); err != nil {
			t.Fatal(err)
		}
		if user != "" {
			user = "user "
		}
		out = append(out, user+detail)
	}
	return out
}

func haLogin(c *client, username, password string) response {
	c.t.Helper()
	return c.json("POST", "/api/auth/homeassistant", map[string]string{"username": username, "password": password})
}

func TestHomeAssistantLoginOffByDefault(t *testing.T) {
	env := newTestEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	r := env.client(t).get("/api/auth/options")
	expect(t, r, 200)
	if strings.TrimSpace(r.Body.String()) != `{"homeAssistant":false}` {
		t.Errorf("options = %s", r.Body)
	}
	expect(t, haLogin(env.client(t), "anna", "pw"), 404)
	expect(t, admin.get("/api/me/homeassistant"), 404)
	expect(t, admin.json("PUT", "/api/me/homeassistant", map[string]string{"username": "anna", "password": "pw"}), 404)
	var me struct{ ID string }
	admin.get("/api/me").decode(t, &me)
	expect(t, admin.json("PATCH", "/api/admin/users/"+me.ID, map[string]string{"haUsername": "admin"}), 400)
}

func TestHomeAssistantLogin(t *testing.T) {
	env, sv := newHomeAssistantEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	env.signIn(t, "anna@example.com", "Anna")
	var users []struct{ ID, Email, HAUsername string }
	admin.get("/api/admin/users").decode(t, &users)
	var annaID string
	for _, u := range users {
		if u.Email == "anna@example.com" {
			annaID = u.ID
		}
	}

	r := env.client(t).get("/api/auth/options")
	if strings.TrimSpace(r.Body.String()) != `{"homeAssistant":true}` {
		t.Errorf("options = %s", r.Body)
	}

	// Not linked yet: the same answer as a wrong password.
	anon := env.client(t)
	r = haLogin(anon, "anna", "anna's ha password")
	expectCode(t, r, 401, "ha_invalid_credentials")

	// The admin links her; the username is stored lower case.
	r = admin.json("PATCH", "/api/admin/users/"+annaID, map[string]string{"haUsername": " Anna "})
	expect(t, r, 200)
	var info struct{ HAUsername string }
	r.decode(t, &info)
	if info.HAUsername != "anna" {
		t.Errorf("haUsername = %q", info.HAUsername)
	}

	expectCode(t, haLogin(anon, "anna", "wrong"), 401, "ha_invalid_credentials")
	expectCode(t, haLogin(anon, "nobody", "anna's ha password"), 401, "ha_invalid_credentials")
	expectCode(t, haLogin(anon, "anna", ""), 401, "ha_invalid_credentials")

	// The right password leads to the email code, as a normal login does.
	calls := sv.callCount()
	r = haLogin(anon, "ANNA", "anna's ha password")
	expect(t, r, 200)
	if sv.callCount() != calls+1 {
		t.Errorf("Supervisor asked %d times", sv.callCount()-calls)
	}
	var ch struct{ Challenge, Email string }
	r.decode(t, &ch)
	if ch.Email != "a…@example.com" {
		t.Errorf("email = %q", ch.Email)
	}
	expect(t, anon.get("/api/me"), 401)
	expect(t, anon.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": "00000000"}), 401)
	expect(t, anon.json("POST", "/api/auth/verify", map[string]string{"challenge": ch.Challenge, "code": env.mail.lastCode(t, "anna@example.com")}), 200)
	var me struct{ Email string }
	anon.get("/api/me").decode(t, &me)
	if me.Email != "anna@example.com" {
		t.Errorf("signed in as %q", me.Email)
	}

	// Email and Homedrive password keep working.
	r = env.client(t).json("POST", "/api/auth/login", map[string]string{"email": "anna@example.com", "password": testPassword})
	expect(t, r, 200)

	// A disabled account gets the wrong-password answer, after the
	// Supervisor was asked like for anyone else.
	expect(t, admin.json("PATCH", "/api/admin/users/"+annaID, map[string]bool{"disabled": true}), 200)
	calls = sv.callCount()
	expectCode(t, haLogin(env.client(t), "anna", "anna's ha password"), 401, "ha_invalid_credentials")
	if sv.callCount() != calls+1 {
		t.Error("the Supervisor wasn't asked for a disabled account")
	}

	// So does someone who hasn't accepted their invitation yet.
	r = admin.json("POST", "/api/admin/users", map[string]string{"email": "bob@example.com", "name": "Bob", "role": "member"})
	expect(t, r, 201)
	var invited struct{ User struct{ ID string } }
	r.decode(t, &invited)
	expect(t, admin.json("PATCH", "/api/admin/users/"+invited.User.ID, map[string]string{"haUsername": "bob"}), 200)
	expectCode(t, haLogin(env.client(t), "bob", "bob's ha password"), 401, "ha_invalid_credentials")

	got := strings.Join(env.auditEvents(t, "login.homeassistant"), "\n")
	want := "failed anna\nfailed anna\nfailed nobody\nfailed anna\nuser anna\nfailed anna\nfailed bob"
	if got != want {
		t.Errorf("audit log:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(env.logs.String(), "ha password") {
		t.Error("a Home Assistant password is in the log")
	}

	// Home Assistant can't check passwords while it restarts.
	sv.mu.Lock()
	sv.down = true
	sv.mu.Unlock()
	expectCode(t, haLogin(env.client(t), "bob", "bob's ha password"), 503, "homeassistant_unavailable")
}

func TestHomeAssistantLoginRateLimit(t *testing.T) {
	env, sv := newHomeAssistantEnv(t)
	anon := env.client(t)
	for range 10 {
		expectCode(t, haLogin(anon, "anna", "guess"), 401, "ha_invalid_credentials")
	}
	calls := sv.callCount()
	expectCode(t, haLogin(anon, "Anna", "anna's ha password"), 429, "rate_limited")
	if sv.callCount() != calls {
		t.Error("the Supervisor was asked for a blocked account")
	}
	// Another account isn't blocked by it.
	expectCode(t, haLogin(anon, "bob", "guess"), 401, "ha_invalid_credentials")
	// The client address is: 30 sign-in requests per 15 minutes.
	for range 18 {
		haLogin(anon, "bob", "guess")
	}
	expectCode(t, haLogin(anon, "carol", "guess"), 429, "rate_limited")
}

func TestLinkHomeAssistantAccount(t *testing.T) {
	env, _ := newHomeAssistantEnv(t)
	admin := env.signInAs(t, "admin@example.com", "Admin", "admin")
	bob := env.signIn(t, "bob@example.com", "Bob")

	var link struct{ Username string }
	r := bob.get("/api/me/homeassistant")
	expect(t, r, 200)
	if r.decode(t, &link); link.Username != "" {
		t.Errorf("linked from the start: %q", link.Username)
	}

	// The Home Assistant password proves the username is Bob's.
	put := func(c *client, username, password string) response {
		return c.json("PUT", "/api/me/homeassistant", map[string]string{"username": username, "password": password})
	}
	expectCode(t, put(bob, "bob", "wrong"), 401, "ha_invalid_credentials")
	expectCode(t, put(bob, "", "x"), 400, "invalid_ha_username")
	expect(t, put(bob, "Bob", "bob's ha password"), 200)
	if bob.get("/api/me/homeassistant").decode(t, &link); link.Username != "bob" {
		t.Errorf("username = %q", link.Username)
	}
	if got := env.auditEvents(t, "homeassistant_linked"); len(got) != 1 || got[0] != "user bob" {
		t.Errorf("audit = %q", got)
	}

	// A username belongs to one account.
	var me struct{ ID string }
	admin.get("/api/me").decode(t, &me)
	expectCode(t, admin.json("PATCH", "/api/admin/users/"+me.ID, map[string]string{"haUsername": "BOB"}), 409, "ha_username_taken")

	// Bob logs in with Home Assistant, then unlinks.
	anon := env.client(t)
	expect(t, haLogin(anon, "bob", "bob's ha password"), 200)
	expect(t, bob.json("DELETE", "/api/me/homeassistant", nil), 204)
	if bob.get("/api/me/homeassistant").decode(t, &link); link.Username != "" {
		t.Errorf("still linked: %q", link.Username)
	}
	expectCode(t, haLogin(anon, "bob", "bob's ha password"), 401, "ha_invalid_credentials")
	// Now the admin can take it, and unlink it again with "".
	expect(t, admin.json("PATCH", "/api/admin/users/"+me.ID, map[string]string{"haUsername": "bob"}), 200)
	r = admin.json("PATCH", "/api/admin/users/"+me.ID, map[string]string{"haUsername": ""})
	expect(t, r, 200)
	var info struct{ HAUsername string }
	if r.decode(t, &info); info.HAUsername != "" {
		t.Errorf("not unlinked: %q", info.HAUsername)
	}

	// Linking needs a recent Homedrive password.
	env.expireRecentAuth(t)
	expectCode(t, put(bob, "bob", "bob's ha password"), 403, "reauth_required")
}
