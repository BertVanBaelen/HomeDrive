package backup

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/accounts"
	"github.com/BertVanBaelen/HomeDrive/server/internal/config"
	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
	hdmail "github.com/BertVanBaelen/HomeDrive/server/internal/mail"
	"github.com/BertVanBaelen/HomeDrive/server/internal/storage"
)

type nopMail struct{}

func (nopMail) Send(context.Context, hdmail.Message) error { return nil }

type env struct {
	backup  *Service
	files   *files.Service
	userID  string
	rootID  string
	family  string
	dir     string
	backups string
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn, err := db.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	instance, _ := db.InstanceID(ctx, conn)
	store, err := storage.Open(filepath.Join(dir, "data"), instance, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	acc := accounts.New(conn, nopMail{}, log, "https://drive.example.com")
	u, err := acc.CreateUser(ctx, "anna@example.com", "Anna", "tulips in the rain", "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	fs := files.New(conn, store, log)
	drives, _ := fs.Drives(ctx, u.ID)
	cfg := &config.Config{BackupPath: filepath.Join(dir, "backup"), BackupTime: config.TimeOfDay{Hour: 3},
		BackupRetentionDays: 30}
	return &env{
		backup: New(cfg, conn, store, instance, log), files: fs, userID: u.ID,
		rootID: drives[0].RootID, family: drives[1].RootID, dir: dir, backups: cfg.BackupPath,
	}
}

func (e *env) put(t *testing.T, parent, name, content string) files.Node {
	t.Helper()
	ctx := context.Background()
	up, err := e.files.CreateUpload(ctx, e.userID, parent, name, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.files.WriteChunk(ctx, e.userID, up.ID, 0, int64(len(content)), strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	n, err := e.files.CompleteUpload(ctx, e.userID, up.ID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBackupAndExport(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	docs, err := e.files.CreateFolder(ctx, e.userID, e.rootID, "Documents")
	if err != nil {
		t.Fatal(err)
	}
	e.put(t, docs.ID, "Tax: 2026?.pdf", "%PDF tax")
	e.put(t, e.family, "Shopping list.md", "- milk")
	old := e.put(t, e.rootID, "old.txt", "old")
	if err := e.files.Trash(ctx, e.userID, old.ID); err != nil {
		t.Fatal(err)
	}

	if err := e.backup.Run(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := e.backup.Status(ctx)
	if err != nil || st.LastError != "" || st.Copied != 3 || st.Objects != 3 || st.Snapshots != 1 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	// A second run copies nothing new.
	if err := e.backup.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.backup.Status(ctx); st.Copied != 0 {
		t.Fatalf("second run copied %d", st.Copied)
	}

	// Export: one folder per drive, trash left out, names made safe.
	target := filepath.Join(e.dir, "restore")
	sum, err := Export(e.backups, target, "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Files != 2 || len(sum.Missing) != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	got, err := os.ReadFile(filepath.Join(target, "My Files - Anna", "Documents", "Tax_ 2026_.pdf"))
	if err != nil || string(got) != "%PDF tax" {
		t.Fatalf("exported tax file: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, "Family", "Shopping list.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "My Files - Anna", "old.txt")); err == nil {
		t.Fatal("trashed file exported")
	}
	// Never into a folder that has something in it.
	if _, err := Export(e.backups, target, ""); err == nil {
		t.Fatal("export into a non-empty folder")
	}
}

func TestRetention(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	n := e.put(t, e.rootID, "gone.txt", "soon gone")
	if err := e.backup.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.files.Trash(ctx, e.userID, n.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.files.DeleteForever(ctx, e.userID, n.ID); err != nil {
		t.Fatal(err)
	}
	// Deleted from the drive, still in the backup during retention...
	if err := e.backup.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.backup.Status(ctx); st.Objects != 1 || st.Removed != 0 {
		t.Fatalf("within retention: %+v", st)
	}
	// ...and removed once it has passed.
	e.backup.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if err := e.backup.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.backup.Status(ctx); st.Objects != 0 || st.Removed != 1 {
		t.Fatalf("after retention: %+v", st)
	}
}

func TestBackupFolderOfAnotherInstallation(t *testing.T) {
	e := setup(t)
	if err := os.MkdirAll(e.backups, 0o750); err != nil {
		t.Fatal(err)
	}
	marker := []byte(`{"instance_id":"someone-else","format":1}`)
	if err := os.WriteFile(filepath.Join(e.backups, ".homedrive"), marker, 0o640); err != nil {
		t.Fatal(err)
	}
	err := e.backup.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "another installation") {
		t.Fatalf("err = %v", err)
	}
	if st, _ := e.backup.Status(context.Background()); st.LastError == "" {
		t.Fatal("failure not recorded in status")
	}
}

func TestPruneSnapshots(t *testing.T) {
	e := setup(t)
	dir := filepath.Join(e.backups, "db")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 3, 0, 0, 0, time.UTC)
	// 60 nightly snapshots.
	for d := 0; d < 60; d++ {
		name := snapshotPrefix + now.AddDate(0, 0, -d).Format("20060102-150405") + snapshotExt
		os.WriteFile(filepath.Join(dir, name), nil, 0o640)
	}
	kept, err := e.backup.pruneSnapshots(now)
	if err != nil {
		t.Fatal(err)
	}
	names, _ := Snapshots(e.backups)
	if kept != len(names) {
		t.Fatalf("kept %d, have %d", kept, len(names))
	}
	// The 14 newest (Oct 15-28) plus the oldest of each month: Aug 30 (the
	// oldest there is), Sep 1 and Oct 1.
	var b bytes.Buffer
	for _, n := range names {
		b.WriteString(n[len(snapshotPrefix):len(snapshotPrefix)+8] + " ")
	}
	if len(names) != 17 || !strings.HasPrefix(b.String(), "20260830 20260901 20261001 20261015 ") {
		t.Fatalf("kept %s", b.String())
	}
}
