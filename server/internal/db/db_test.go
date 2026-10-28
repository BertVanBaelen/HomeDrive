package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateAndInstanceID(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "homedrive.db")
	conn, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	for range 2 { // migrating twice must be a no-op
		if err := Migrate(ctx, conn); err != nil {
			t.Fatal(err)
		}
	}

	id, err := InstanceID(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Errorf("instance ID %q, want 32 hex characters", id)
	}
	again, err := InstanceID(ctx, conn)
	if err != nil || again != id {
		t.Errorf("InstanceID changed: %q then %q (%v)", id, again, err)
	}

	var mode string
	if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q (%v), want wal", mode, err)
	}
	var fk int
	if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d (%v), want 1", fk, err)
	}
}

func TestMeta(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, filepath.Join(t.TempDir(), "homedrive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := Meta(ctx, conn, "missing"); ok || err != nil {
		t.Errorf("Meta(missing) ok=%v err=%v", ok, err)
	}
	for _, v := range []string{"1", "2"} {
		if err := SetMeta(ctx, conn, "k", v); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := Meta(ctx, conn, "k"); got != v || !ok || err != nil {
			t.Errorf("Meta(k) = %q, %v, %v; want %q", got, ok, err, v)
		}
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, filepath.Join(t.TempDir(), "homedrive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO schema_migrations VALUES (9999, 'future')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, conn); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("Migrate on a newer schema: %v", err)
	}
}
