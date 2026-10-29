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

// A v1 database has one shared Family drive with every user as editor.
// After the upgrade Family is the default drive and the admins manage it.
func TestSharedDrivesMigration(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, filepath.Join(t.TempDir(), "homedrive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	// The schema as v1 left it: migrations 1–4.
	if _, err := conn.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:4] {
		if err := apply(ctx, conn, m); err != nil {
			t.Fatal(err)
		}
	}
	const at = "2025-01-01T00:00:00.000Z"
	if _, err := conn.ExecContext(ctx, `BEGIN;
		INSERT INTO users (id, email, name, password_hash, role, locale, created_at) VALUES
			('admin', 'admin@example.com', 'Admin', 'x', 'admin', 'nl', '`+at+`'),
			('kid', 'kid@example.com', 'Kid', 'x', 'member', 'en', '`+at+`');
		INSERT INTO nodes (id, drive_id, kind, name, name_key, created_at, modified_at) VALUES
			('family-root', 'family', 'folder', '', '', '`+at+`', '`+at+`'),
			('admin-root', 'admin-files', 'folder', '', '', '`+at+`', '`+at+`');
		INSERT INTO drives (id, kind, name, owner_user_id, root_id, created_at) VALUES
			('family', 'shared', 'Family', NULL, 'family-root', '`+at+`'),
			('admin-files', 'personal', 'My Files', 'admin', 'admin-root', '`+at+`');
		INSERT INTO drive_members (drive_id, user_id, role) VALUES
			('family', 'admin', 'editor'), ('family', 'kid', 'editor');
		COMMIT;`); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	var def int
	var name string
	if err := conn.QueryRowContext(ctx, `SELECT join_by_default, name FROM drives WHERE id = 'family'`).Scan(&def, &name); err != nil || def != 1 || name != "Familie" {
		t.Errorf("Family join_by_default = %d, name %q (%v), want 1, Familie", def, name, err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT join_by_default FROM drives WHERE id = 'admin-files'`).Scan(&def); err != nil || def != 0 {
		t.Errorf("personal join_by_default = %d (%v), want 0", def, err)
	}
	for user, want := range map[string]string{"admin": "manager", "kid": "editor"} {
		var role string
		if err := conn.QueryRowContext(ctx, `SELECT role FROM drive_members WHERE drive_id = 'family' AND user_id = ?`, user).Scan(&role); err != nil || role != want {
			t.Errorf("%s is %q (%v), want %s", user, role, err, want)
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
