package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BertVanBaelen/HomeDrive/server/internal/config"
)

const (
	publicHost    = "drive.example.com"
	secretContent = "TOP-SECRET-7f3a91"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	web := filepath.Join(dir, "web")
	for name, body := range map[string]string{
		"index.html":           "<!doctype html><title>Homedrive</title>",
		"sw.js":                "self.addEventListener('fetch', () => {})",
		"manifest.webmanifest": `{"name":"Homedrive"}`,
		"assets/app-abc123.js": "console.log(1)",
	} {
		p := filepath.Join(web, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	// A secret next to web_dir that must never be reachable.
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte(secretContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load([]string{
		"HOMEDRIVE_PUBLIC_URL=https://" + publicHost,
		"HOMEDRIVE_STATE_DIR=" + filepath.Join(dir, "state"),
		"HOMEDRIVE_DATA_PATH=" + filepath.Join(dir, "files"),
		"HOMEDRIVE_WEB_DIR=" + web,
		"HOMEDRIVE_SMTP_HOST=smtp.example.com",
		"HOMEDRIVE_SMTP_FROM=drive@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv.Handler()
}

func do(h http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = publicHost
	for k, v := range header {
		if k == "Host" {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	h := newTestServer(t)
	for _, tc := range []struct {
		method, path string
		header       map[string]string
	}{
		{"GET", "/", nil},
		{"GET", "/api/nothing", nil},
		{"GET", "/assets/missing.js", nil},
		{"GET", "/", map[string]string{"Host": "192.168.1.10:8080"}},
		{"POST", "/api/x", map[string]string{"Sec-Fetch-Site": "cross-site"}},
	} {
		w := do(h, tc.method, tc.path, tc.header)
		for _, name := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy",
			"Strict-Transport-Security", "X-Frame-Options", "Permissions-Policy", "Cross-Origin-Opener-Policy", "X-Robots-Tag"} {
			if w.Header().Get(name) == "" {
				t.Errorf("%s %s (status %d): missing %s", tc.method, tc.path, w.Code, name)
			}
		}
	}
	csp := do(h, "GET", "/", nil).Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP allows unsafe sources: %q", csp)
	}
}

func TestHostCheck(t *testing.T) {
	h := newTestServer(t)
	for host, want := range map[string]int{
		publicHost:              http.StatusOK,
		"DRIVE.example.com:443": http.StatusOK,
		"192.168.1.10:8080":     http.StatusMisdirectedRequest,
		"attacker.example":      http.StatusMisdirectedRequest,
	} {
		if w := do(h, "GET", "/", map[string]string{"Host": host}); w.Code != want {
			t.Errorf("Host %q: status %d, want %d", host, w.Code, want)
		}
	}
	// Watchdogs call the health check by IP address.
	if w := do(h, "GET", "/healthz", map[string]string{"Host": "172.30.33.4:8080"}); w.Code != http.StatusOK {
		t.Errorf("healthz by IP: status %d", w.Code)
	}
}

func TestCrossOriginWritesRejected(t *testing.T) {
	h := newTestServer(t)
	tests := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, http.StatusForbidden},
		{"old browser, foreign origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"same origin", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://" + publicHost}, http.StatusNotFound},
		{"non-browser client", nil, http.StatusNotFound},
	}
	for _, tt := range tests {
		w := do(h, "POST", "/api/anything", tt.header)
		if w.Code != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, w.Code, tt.want)
		}
	}
}

func TestStaticFiles(t *testing.T) {
	h := newTestServer(t)

	tests := []struct {
		path, wantType, wantCache string
		wantCode                  int
		wantBody                  string
	}{
		{"/", "text/html; charset=utf-8", "no-cache", 200, "<title>Homedrive"},
		{"/save", "text/html; charset=utf-8", "no-cache", 200, "<title>Homedrive"}, // app route
		{"/sw.js", "text/javascript; charset=utf-8", "no-cache", 200, "addEventListener"},
		{"/manifest.webmanifest", "application/manifest+json", "no-cache", 200, "Homedrive"},
		{"/assets/app-abc123.js", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", 200, "console"},
		{"/assets/missing.js", "", "", 404, ""},
		{"/favicon.ico", "", "", 404, ""},
		{"/assets/", "", "", 404, ""}, // no directory listing
		{"/assets", "", "", 404, ""},
	}
	for _, tt := range tests {
		w := do(h, "GET", tt.path, nil)
		if w.Code != tt.wantCode {
			t.Errorf("GET %s: status %d, want %d", tt.path, w.Code, tt.wantCode)
			continue
		}
		if tt.wantCode != 200 {
			continue
		}
		if got := w.Header().Get("Content-Type"); got != tt.wantType {
			t.Errorf("GET %s: Content-Type %q, want %q", tt.path, got, tt.wantType)
		}
		if got := w.Header().Get("Cache-Control"); got != tt.wantCache {
			t.Errorf("GET %s: Cache-Control %q, want %q", tt.path, got, tt.wantCache)
		}
		if !strings.Contains(w.Body.String(), tt.wantBody) {
			t.Errorf("GET %s: body %q lacks %q", tt.path, w.Body.String(), tt.wantBody)
		}
	}

	if w := do(h, "DELETE", "/", nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /: status %d", w.Code)
	}
}

func TestNoTraversalOutOfWebDir(t *testing.T) {
	h := newTestServer(t)
	for _, p := range []string{"/../secret.txt", "/..%2fsecret.txt", "/assets/../../secret.txt", "/%2e%2e/secret.txt", `/..\secret.txt`} {
		w := do(h, "GET", p, nil)
		if strings.Contains(w.Body.String(), secretContent) {
			t.Errorf("GET %s (status %d) served a file outside web_dir", p, w.Code)
		}
	}
}

func TestAPIUnknownRouteIsJSON(t *testing.T) {
	h := newTestServer(t)
	w := do(h, "GET", "/api/does-not-exist", nil)
	if w.Code != http.StatusNotFound || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status %d, type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("API responses must not be cached")
	}
	if strings.TrimSpace(w.Body.String()) != `{"error":"not_found"}` {
		t.Errorf("body %q", w.Body.String())
	}
}

func TestShareTargetWithoutServiceWorker(t *testing.T) {
	h := newTestServer(t)
	w := do(h, "POST", "/share-target", map[string]string{"Sec-Fetch-Site": "none"})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/save?status=no-worker" {
		t.Errorf("status %d, Location %q", w.Code, w.Header().Get("Location"))
	}
}

func TestClientIP(t *testing.T) {
	c := clientIPResolver{trusted: []netip.Prefix{
		netip.MustParsePrefix("172.30.32.0/23"),
		netip.MustParsePrefix("10.0.0.0/8"),
	}}
	tests := []struct {
		name, remote string
		header       map[string]string
		want         string
	}{
		{"direct client", "203.0.113.7:5000", nil, "203.0.113.7"},
		{"direct client can't spoof", "203.0.113.7:5000", map[string]string{"CF-Connecting-IP": "1.2.3.4", "X-Forwarded-For": "1.2.3.4"}, "203.0.113.7"},
		{"cloudflared", "172.30.33.4:5000", map[string]string{"CF-Connecting-IP": "198.51.100.9"}, "198.51.100.9"},
		{"cloudflared, bad header", "172.30.33.4:5000", map[string]string{"CF-Connecting-IP": "nonsense"}, "172.30.33.4"},
		{"forwarded chain", "10.0.0.2:5000", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.9, 10.0.0.5"}, "198.51.100.9"},
		{"forwarded garbage", "10.0.0.2:5000", map[string]string{"X-Forwarded-For": "x, 10.0.0.5"}, "10.0.0.5"},
		{"ipv6 client", "[2001:db8::1]:5000", nil, "2001:db8::1"},
		{"ipv4-mapped peer", "[::ffff:172.30.33.4]:5000", map[string]string{"CF-Connecting-IP": "198.51.100.9"}, "198.51.100.9"},
	}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tt.remote
		for k, v := range tt.header {
			r.Header.Set(k, v)
		}
		if got := c.resolve(r); got.String() != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestMissingWebDir(t *testing.T) {
	if _, err := newStaticHandler(t.TempDir()); err == nil || !strings.Contains(err.Error(), "index.html") {
		t.Errorf("err = %v, want a hint about index.html", err)
	}
}
