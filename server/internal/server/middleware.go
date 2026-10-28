package server

import (
	"context"
	"net/http"
	"net/netip"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/config"
)

// contentSecurityPolicy allows only Homedrive's own scripts, styles and
// fonts. File contents served to the browser get a stricter sandbox policy
// of their own.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' blob: data:; media-src 'self' blob:; font-src 'self'; connect-src 'self'; " +
	"worker-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'none'; " +
	"form-action 'self'; frame-ancestors 'none'"

const permissionsPolicy = "accelerometer=(), bluetooth=(), browsing-topics=(), camera=(), " +
	"display-capture=(), geolocation=(), gyroscope=(), hid=(), magnetometer=(), microphone=(), " +
	"midi=(), payment=(), serial=(), usb=()"

func securityHeaders(cfg *config.Config) http.Header {
	h := http.Header{}
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", permissionsPolicy)
	// A private drive has nothing for search engines.
	h.Set("X-Robots-Tag", "noindex, nofollow")
	if cfg.HTTPS() {
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
	return h
}

// setHeaders adds the security headers before the handler runs, so handlers
// can still override them (for example the sandbox policy on file contents).
func (s *Server) setHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		for k, v := range s.headers {
			h[k] = slices.Clone(v)
		}
		next.ServeHTTP(w, r)
	})
}

// requireHost rejects requests that don't name the public host. That stops
// DNS rebinding and direct LAN access that bypasses Cloudflare. The health
// check is exempt because watchdogs call it by IP address.
func (s *Server) requireHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && !s.cfg.MatchesHost(r.Host) {
			s.log.Debug("wrong host", "host", r.Host, "ip", ClientIP(r.Context()))
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type ctxKey int

const clientIPKey ctxKey = iota

// ClientIP is the browser's address as resolved by the trusted-proxy rules.
func ClientIP(ctx context.Context) netip.Addr {
	ip, _ := ctx.Value(clientIPKey).(netip.Addr)
	return ip
}

func (s *Server) withClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.proxies.resolve(r)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey, ip)))
	})
}

// clientIPResolver finds the browser's address behind cloudflared.
// Forwarding headers are only believed when the direct peer is a trusted
// proxy; anyone else could set them to dodge rate limits.
type clientIPResolver struct{ trusted []netip.Prefix }

func (c clientIPResolver) resolve(r *http.Request) netip.Addr {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	addr := peer.Addr().Unmap()
	if !c.isTrusted(addr) {
		return addr
	}
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		if ip, err := netip.ParseAddr(strings.TrimSpace(v)); err == nil {
			return ip.Unmap()
		}
	}
	// X-Forwarded-For is "client, proxy1, proxy2". Walking from the right,
	// the first address that isn't a trusted proxy is the one the last
	// trusted proxy saw; anything further left could be forged.
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		addr = ip.Unmap()
		if !c.isTrusted(addr) {
			break
		}
	}
	return addr
}

func (c clientIPResolver) isTrusted(ip netip.Addr) bool {
	for _, p := range c.trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// statusRecorder remembers the response status for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 && code >= 200 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// logRequests logs method, path and status. Query strings are left out on
// purpose: they may carry tokens.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		level := s.log.Info
		if r.URL.Path == "/healthz" {
			level = s.log.Debug
		}
		level("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration", time.Since(start).Round(time.Millisecond), "ip", ClientIP(r.Context()))
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic while serving request", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
