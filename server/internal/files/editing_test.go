package files

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func save(s *Service, uid string, n Node, lockID, text string) (Node, error) {
	return s.Edit(context.Background(), uid, n.ID, lockID, n.Version, "text",
		func(func() ([]byte, error)) ([]byte, error) { return []byte(text), nil })
}

func mustSave(t *testing.T, s *Service, uid string, n Node, lockID, text string) Node {
	t.Helper()
	n, err := save(s, uid, n, lockID, text)
	if err != nil {
		t.Fatalf("saving %q: %v", text, err)
	}
	return n
}

func TestEditing(t *testing.T) {
	s, uid, root, _ := newService(t)
	ctx := context.Background()
	n, lock, err := s.CreateFile(ctx, uid, root, "n.md", nil)
	if err != nil || n.Editor != "markdown" || !n.CanEdit {
		t.Fatalf("new file = %+v, %v", n, err)
	}
	if _, _, err := s.CreateFile(ctx, uid, root, "x.png", nil); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("an image: %v", err)
	}

	// Saves of one editing session replace each other; history keeps the
	// content from before the session.
	n1 := mustSave(t, s, uid, n, lock, "one")
	n2 := mustSave(t, s, uid, n1, lock, "two")
	versions := func() []Version {
		vs, err := s.Versions(ctx, uid, n.ID)
		if err != nil {
			t.Fatal(err)
		}
		return vs
	}
	if vs := versions(); len(vs) != 2 || !vs[0].Current || vs[0].ID != n2.Version || vs[1].Size != 0 {
		t.Fatalf("versions = %+v", vs)
	}
	if _, err := save(s, uid, n1, lock, "late"); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("a save from an old version: %v", err)
	}

	// Taking over moves the lock; the old one is dead.
	if _, _, err := s.Lock(ctx, uid, n.ID, false); !errors.Is(err, ErrLockedSelf) {
		t.Fatalf("locking again: %v", err)
	}
	lock2, tookOver, err := s.Lock(ctx, uid, n.ID, true)
	if err != nil || !tookOver {
		t.Fatalf("takeover: %v %v", tookOver, err)
	}
	if _, err := save(s, uid, n2, lock, "x"); !errors.Is(err, ErrLockLost) {
		t.Fatalf("the old lock: %v", err)
	}
	n3 := mustSave(t, s, uid, n2, lock2, "three")
	if len(versions()) != 3 {
		t.Fatalf("a new session keeps the previous content: %+v", versions())
	}

	// A lock that ran out still serves its holder until someone takes the file.
	later := time.Now().Add(LockTTL + time.Second)
	s.now = func() time.Time { return later }
	if st, _ := s.LockStatus(ctx, uid, n.ID); st != nil {
		t.Fatalf("expired lock shows: %+v", st)
	}
	n4 := mustSave(t, s, uid, n3, lock2, "four")

	// Restoring needs the file free, keeps the history and shares no object.
	old := versions()[1].ID
	if _, err := s.RestoreVersion(ctx, uid, n.ID, old); !errors.Is(err, ErrLockedSelf) {
		t.Fatalf("restore while editing: %v", err)
	}
	if err := s.Unlock(ctx, uid, n.ID, lock2); err != nil {
		t.Fatal(err)
	}
	r, err := s.RestoreVersion(ctx, uid, n.ID, old)
	if err != nil || r.Size != 3 || len(versions()) != 4 {
		t.Fatalf("restored %+v, %v, %d versions", r, err, len(versions()))
	}
	if same, _ := s.RestoreVersion(ctx, uid, n.ID, r.Version); same.Version != r.Version {
		t.Fatal("restoring the current version made a new one")
	}
	f, got, err := s.OpenOldVersion(ctx, uid, n.ID, old)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if b, _ := io.ReadAll(f); string(b) != "two" || got.ModifiedAt.Equal(n4.ModifiedAt) {
		t.Fatalf("old version = %q, %v", b, got.ModifiedAt)
	}
}

func TestVersionCleanup(t *testing.T) {
	s, uid, root, _ := newService(t)
	ctx := context.Background()
	n, lock, _ := s.CreateFile(ctx, uid, root, "n.txt", []byte("zero"))
	mustSave(t, s, uid, n, lock, "one")
	s.Unlock(ctx, uid, n.ID, lock)
	var replaced, current string
	s.db.QueryRow(`SELECT object_id FROM versions WHERE node_id = ? AND superseded_at IS NOT NULL`, n.ID).Scan(&replaced)
	s.db.QueryRow(`SELECT v.object_id FROM versions v JOIN nodes n ON n.current_version_id = v.id WHERE n.id = ?`, n.ID).Scan(&current)
	if !s.store.HasObject(replaced) {
		t.Fatal("the replaced content is missing")
	}

	// Past the retention the replaced version goes, with its object; the
	// lock of a closed session goes after a day.
	s.SetVersionRetention(time.Hour)
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if res, _ := s.Cleanup(ctx); res.Versions != 1 || res.Locks != 0 {
		t.Fatalf("after 2h: %+v", res)
	}
	s.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	s.Lock(ctx, uid, n.ID, false)
	s.now = func() time.Time { return time.Now().Add(50 * time.Hour) }
	if res, _ := s.Cleanup(ctx); res.Locks != 1 {
		t.Fatalf("after 50h: %+v", res)
	}
	if s.store.HasObject(replaced) || !s.store.HasObject(current) {
		t.Fatal("wrong objects removed")
	}
}

func TestReplaceUpload(t *testing.T) {
	s, uid, root, _ := newService(t)
	ctx := context.Background()
	orig := s.mustUpload(t, uid, root, "a.txt", "one")

	_, err := s.CreateUpload(ctx, uid, root, "A.txt", 3, "", ConflictAsk)
	var taken *NameTakenError
	if !errors.As(err, &taken) || taken.Existing.ID != orig.ID {
		t.Fatalf("ask: %v", err)
	}
	if _, err := s.CreateUpload(ctx, uid, root, "a.txt", 3, "", Conflict("overwrite")); !errors.Is(err, ErrInvalidConflict) {
		t.Fatalf("bad conflict: %v", err)
	}

	// A lock stops a replacement, except the user's own.
	lock, _, _ := s.Lock(ctx, uid, orig.ID, false)
	up, err := s.CreateUpload(ctx, uid, root, "a.txt", 3, "", ConflictReplace)
	if err != nil || up.Replaces != orig.ID {
		t.Fatalf("replace: %+v %v", up, err)
	}
	s.WriteChunk(ctx, uid, up.ID, 0, 3, strings.NewReader("two"))
	n, err := s.CompleteUpload(ctx, uid, up.ID)
	if err != nil || n.ID != orig.ID || n.Size != 3 {
		t.Fatalf("completed %+v, %v", n, err)
	}
	if _, err := save(s, uid, orig, lock, "late"); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("the editor after a replacement: %v", err)
	}

	// Renamed meanwhile: the upload becomes a new file instead.
	up, _ = s.CreateUpload(ctx, uid, root, "a.txt", 3, "", ConflictReplace)
	s.WriteChunk(ctx, uid, up.ID, 0, 3, strings.NewReader("six"))
	s.Rename(ctx, uid, orig.ID, "b.txt")
	if n, err := s.CompleteUpload(ctx, uid, up.ID); err != nil || n.ID == orig.ID || n.Name != "a.txt" {
		t.Fatalf("after rename: %+v, %v", n, err)
	}
}
