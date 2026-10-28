package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const id = "0123456789abcdef0123456789abcdef"

func mustOpen(t *testing.T, dir, instanceID string, initialized bool) *Store {
	t.Helper()
	s, err := Open(dir, instanceID, initialized)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFirstStartClaimsMissingFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "homedrive")
	mustOpen(t, dir, id, false)

	for _, name := range []string{markerName, "objects", "uploads"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s not created: %v", name, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "uploads"))
	if len(entries) != 0 {
		t.Errorf("write probe left files behind: %v", entries)
	}

	// Later starts accept the folder.
	mustOpen(t, dir, id, true)
}

func TestFirstStartToleratesHousekeepingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "@eaDir"), 0o750); err != nil {
		t.Fatal(err)
	}
	mustOpen(t, dir, id, false)
}

func TestFirstStartRefusesNonEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "taxes.pdf"), []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, id, false); !errors.Is(err, ErrNotEmpty) {
		t.Errorf("err = %v, want ErrNotEmpty", err)
	}
	if _, err := os.Stat(filepath.Join(dir, markerName)); !errors.Is(err, os.ErrNotExist) {
		t.Error("marker written into a folder that was refused")
	}
}

func TestFirstStartRefusesMissingParent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unmounted-share", "homedrive")
	if _, err := Open(dir, id, false); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(filepath.Dir(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("parent folder was created")
	}
}

func TestUnmountedShareIsDetected(t *testing.T) {
	// Initialized before, but the folder is now an empty mount point.
	if _, err := Open(t.TempDir(), id, true); !errors.Is(err, ErrWrongFolder) {
		t.Errorf("empty folder: err = %v, want ErrWrongFolder", err)
	}
	// Initialized before, but the folder is gone.
	if _, err := Open(filepath.Join(t.TempDir(), "gone"), id, true); !errors.Is(err, ErrWrongFolder) {
		t.Errorf("missing folder: err = %v, want ErrWrongFolder", err)
	}
}

func TestOtherInstallationIsRefused(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, id, false)
	s.Close()
	for _, initialized := range []bool{false, true} {
		if _, err := Open(dir, "ffffffffffffffffffffffffffffffff", initialized); !errors.Is(err, ErrWrongFolder) {
			t.Errorf("initialized=%v: err = %v, want ErrWrongFolder", initialized, err)
		}
	}
}

func TestInterruptedFirstStartResumes(t *testing.T) {
	// The marker was written but the database flag wasn't saved: the next
	// first start must accept its own marker.
	dir := t.TempDir()
	s := mustOpen(t, dir, id, false)
	s.Close()
	mustOpen(t, dir, id, false)
}

func TestDamagedMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, markerName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, id, true); !errors.Is(err, ErrWrongFolder) {
		t.Errorf("err = %v, want ErrWrongFolder", err)
	}
}
