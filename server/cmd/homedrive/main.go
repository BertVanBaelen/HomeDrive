// Command homedrive runs the Homedrive server.
//
//	homedrive              serve (configuration from HOMEDRIVE_* / the options file)
//	homedrive healthcheck  exit 0 if the local server answers /healthz
//	homedrive version      print the version
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/accounts"
	"github.com/BertVanBaelen/HomeDrive/server/internal/config"
	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
	"github.com/BertVanBaelen/HomeDrive/server/internal/mail"
	"github.com/BertVanBaelen/HomeDrive/server/internal/server"
	"github.com/BertVanBaelen/HomeDrive/server/internal/storage"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "version":
			fmt.Println(version)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q (commands: healthcheck, version)\n", os.Args[1])
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		slog.Error("homedrive stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Environ())
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}
	log.Info("starting homedrive", append([]any{"version", version}, cfg.LogAttrs()...)...)
	if !cfg.SMTP.Configured() {
		log.Warn("SMTP is not configured: login codes will be written to this log. Only acceptable for local development.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.StateDir, 0o750); err != nil {
		return fmt.Errorf("creating state_dir: %w", err)
	}
	conn, err := db.Open(ctx, filepath.Join(cfg.StateDir, "homedrive.db"))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		return err
	}

	instanceID, err := db.InstanceID(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading instance ID: %w", err)
	}
	initialized, _, err := db.Meta(ctx, conn, "data_path_initialized")
	if err != nil {
		return err
	}
	store, err := storage.Open(cfg.DataPath, instanceID, initialized == "1")
	if err != nil {
		return err
	}
	defer store.Close()
	if initialized != "1" {
		if err := db.SetMeta(ctx, conn, "data_path_initialized", "1"); err != nil {
			return err
		}
		log.Info("data folder initialized", "path", cfg.DataPath, "instance_id", instanceID)
	}

	acc := accounts.New(conn, mail.New(cfg.SMTP, log), log, cfg.Origin())
	link, err := acc.StartSetup(ctx)
	if err != nil {
		return err
	}
	if link != "" {
		// The link is the only way in on a new installation, so it goes to
		// the log at a level that is always shown.
		log.Warn("no accounts yet: open this link within 1 hour to create the admin account (restart for a new link)", "link", link)
	} else if cfg.AdminRecovery {
		if link, err = acc.StartRecovery(ctx); err != nil {
			return err
		}
		log.Warn("admin_recovery is on: open this link within 1 hour to set a new admin password, then turn admin_recovery off again", "link", link)
	}

	srv, err := server.New(cfg, log, acc, files.New(conn, store, log))
	if err != nil {
		return err
	}
	defer srv.Close()
	return srv.Run(ctx)
}

// healthcheck calls /healthz on the local listener. It reads only
// HOMEDRIVE_LISTEN so it works even when the rest of the configuration is
// broken.
func healthcheck() int {
	listen := os.Getenv("HOMEDRIVE_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = errors.New(resp.Status)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	return 0
}
