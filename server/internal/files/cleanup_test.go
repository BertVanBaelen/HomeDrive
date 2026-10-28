package files

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
	"github.com/BertVanBaelen/HomeDrive/server/internal/storage"
)

// newService returns a files service with one user and their drives.
func newService(t testing.TB) (*Service, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	conn, err := db.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	instance, _ := db.InstanceID(ctx, conn)
	data := filepath.Join(dir, "data")
	store, err := storage.Open(data, instance, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	uid := db.NewID()
	tx, _ := conn.Begin()
	if _, err := tx.Exec(`INSERT INTO users (id, email, name, password_hash, role, created_at) VALUES (?, 'a@example.com', 'A', '', 'admin', ?)`,
		uid, db.FormatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionUser(ctx, tx, uid, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s := New(conn, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	drives, _ := s.Drives(ctx, uid)
	return s, uid, drives[0].RootID, data
}

func age(t testing.TB, path string) {
	t.Helper()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestCleanup(t *testing.T) {
	s, uid, root, data := newService(t)
	ctx := context.Background()

	// An upload that was never finished and has expired.
	up, err := s.CreateUpload(ctx, uid, root, "half.bin", 10)
	if err != nil {
		t.Fatal(err)
	}
	s.WriteChunk(ctx, uid, up.ID, 0, 5, strings.NewReader("01234"))
	s.db.Exec(`UPDATE uploads SET expires_at = '2000-01-01T00:00:00.000Z' WHERE id = ?`, up.ID)
	// An upload still going on, and one just started without a row yet.
	live, _ := s.CreateUpload(ctx, uid, root, "live.bin", 10)
	os.WriteFile(filepath.Join(data, "uploads", ".put-fresh"), []byte("x"), 0o640)
	// An old temporary file from an interrupted write.
	tmp := filepath.Join(data, "uploads", ".copy-old")
	os.WriteFile(tmp, []byte("x"), 0o640)
	age(t, tmp)
	// An object nothing refers to, and one a file uses; both old.
	orphan, _, _ := s.store.PutObject([]byte("orphan"))
	age(t, filepath.Join(data, "objects", orphan[:2], orphan))
	kept := s.mustUpload(t, uid, root, "kept.txt", "kept")
	var keptObject string
	s.db.QueryRow(`SELECT object_id FROM versions WHERE node_id = ?`, kept.ID).Scan(&keptObject)
	age(t, filepath.Join(data, "objects", keptObject[:2], keptObject))

	res, err := s.Cleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Uploads != 1 || res.TempFiles != 1 || res.Objects != 1 {
		t.Fatalf("result = %+v", res)
	}
	if _, err := s.UploadStatus(ctx, uid, live.ID); err != nil {
		t.Fatal("live upload removed")
	}
	if _, err := os.Stat(filepath.Join(data, "uploads", ".put-fresh")); err != nil {
		t.Fatal("fresh temp file removed")
	}
	if !s.store.HasObject(keptObject) || s.store.HasObject(orphan) {
		t.Fatal("wrong objects removed")
	}
}

func (s *Service) mustUpload(t *testing.T, uid, parent, name, content string) Node {
	t.Helper()
	ctx := context.Background()
	up, err := s.CreateUpload(ctx, uid, parent, name, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteChunk(ctx, uid, up.ID, 0, int64(len(content)), strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	n, err := s.CompleteUpload(ctx, uid, up.ID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
