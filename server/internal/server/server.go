// Package server is Homedrive's HTTP front: security middleware, the JSON
// API and the static PWA.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/accounts"
	"github.com/BertVanBaelen/HomeDrive/server/internal/backup"
	"github.com/BertVanBaelen/HomeDrive/server/internal/config"
	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
	"github.com/BertVanBaelen/HomeDrive/server/internal/ratelimit"
)

type Server struct {
	cfg      *config.Config
	log      *slog.Logger
	proxies  clientIPResolver
	headers  http.Header
	web      *staticHandler
	accounts *accounts.Service
	files    *files.Service
	backup   *backup.Service // nil when backup_path is empty

	// ipLimit counts sign-in requests per client address; accountLimit
	// counts failed passwords per account.
	ipLimit      *ratelimit.Limiter
	accountLimit *ratelimit.Limiter
}

// Deps are the services the HTTP layer serves.
type Deps struct {
	Accounts *accounts.Service
	Files    *files.Service
	Backup   *backup.Service
}

func New(cfg *config.Config, log *slog.Logger, deps Deps) (*Server, error) {
	web, err := newStaticHandler(cfg.WebDir)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:          cfg,
		log:          log,
		proxies:      clientIPResolver{trusted: cfg.TrustedProxies},
		headers:      securityHeaders(cfg),
		web:          web,
		accounts:     deps.Accounts,
		files:        deps.Files,
		backup:       deps.Backup,
		ipLimit:      ratelimit.New(30, 15*time.Minute),
		accountLimit: ratelimit.New(10, time.Hour),
	}, nil
}

// Handler returns the complete handler chain. The outermost middleware runs
// first: panics are caught around everything, the client IP is known before
// logging, and security headers are on every response, including rejections.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	s.authRoutes(mux)
	s.accountRoutes(mux)
	s.deviceRoutes(mux)
	mux.HandleFunc("GET /api/admin/backup", s.admin(s.handleBackupStatus))
	mux.HandleFunc("POST /api/admin/backup", s.admin(s.handleBackupNow))
	s.fileRoutes(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found")
	})
	// Android delivers shared files with a POST to the manifest's
	// share_target. The service worker normally intercepts it; this only runs
	// when it isn't active yet, and explains that on the save page.
	mux.HandleFunc("POST /share-target", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/save?status=no-worker", http.StatusSeeOther)
	})
	mux.Handle("/", s.web)

	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.log.Warn("cross-origin request rejected", "method", r.Method, "path", r.URL.Path,
			"origin", r.Header.Get("Origin"), "ip", ClientIP(r.Context()))
		writeError(w, http.StatusForbidden, "cross_origin")
	}))

	var h http.Handler = mux
	h = csrf.Handler(h)
	h = s.requireHost(h)
	h = s.setHeaders(h)
	h = s.logRequests(h)
	h = s.withClientIP(h)
	h = s.recoverPanics(h)
	return h
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Upload and download routes extend these with
		// http.ResponseController; everything else is small.
		ReadTimeout:    time.Minute,
		WriteTimeout:   2 * time.Minute,
		IdleTimeout:    2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
		ErrorLog:       slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	s.log.Info("listening", "addr", ln.Addr().String(), "public_url", s.cfg.Origin())

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	s.log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// Close releases the web directory. Call it after Run returns.
func (s *Server) Close() error { return s.web.Close() }

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte("ok\n"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError sends {"error": code}. Codes are stable identifiers the
// frontend translates; they never contain internal details.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
