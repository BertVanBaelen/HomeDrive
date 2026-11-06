package files

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// Editing: one person at a time changes a file's contents, under a lock,
// and every save is a version. See docs/architecture.md "Editors, versions
// and edit locks".

// LockTTL is how long an edit lock lasts without being renewed; editors
// renew every 30 seconds.
const LockTTL = 2 * time.Minute

var (
	ErrLocked       = errors.New("someone else is editing this file")
	ErrLockedSelf   = errors.New("you are editing this file somewhere else")
	ErrLockLost     = errors.New("the edit lock is gone")
	ErrEditConflict = errors.New("the file was changed by someone else")
	ErrNotEditable  = errors.New("this file can't be edited here")
)

// The errors below say more than their code. The server adds Extra to the
// answer, for the app to show who it was.

// LockedError is ErrLocked with the name of the person editing.
type LockedError struct{ Holder string }

func (e *LockedError) Error() string         { return ErrLocked.Error() }
func (e *LockedError) Is(target error) bool  { return target == ErrLocked }
func (e *LockedError) Extra() map[string]any { return map[string]any{"holder": e.Holder} }

// ConflictError is ErrEditConflict with who saved the newer version, and when.
type ConflictError struct {
	By string
	At time.Time
}

func (e *ConflictError) Error() string        { return ErrEditConflict.Error() }
func (e *ConflictError) Is(target error) bool { return target == ErrEditConflict }
func (e *ConflictError) Extra() map[string]any {
	return map[string]any{"by": e.By, "at": e.At.UTC().Format(time.RFC3339)}
}

// NameTakenError is ErrNameTaken with the file that has the name, for an
// upload that asked.
type NameTakenError struct{ Existing Node }

func (e *NameTakenError) Error() string         { return ErrNameTaken.Error() }
func (e *NameTakenError) Is(target error) bool  { return target == ErrNameTaken }
func (e *NameTakenError) Extra() map[string]any { return map[string]any{"existing": e.Existing} }

// lock is a file's edit lock. Only the hash of its secret ID is stored.
type lock struct {
	userID, holder, hash string
	live                 bool
}

func hashLock(lockID string) string {
	sum := sha256.Sum256([]byte(lockID))
	return hex.EncodeToString(sum[:])
}

// lockOf reads the file's lock, or nil when it has none. A lock that ran
// out is returned too, with live false.
func (s *Service) lockOf(ctx context.Context, q querier, nodeID string) (*lock, error) {
	var l lock
	var expires string
	err := q.QueryRowContext(ctx, `SELECT l.user_id, u.name, l.lock_hash, l.expires_at
		FROM edit_locks l JOIN users u ON u.id = l.user_id WHERE l.node_id = ?`, nodeID).Scan(&l.userID, &l.holder, &l.hash, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, _ := db.ParseTime(expires)
	l.live = t.After(s.now())
	return &l, nil
}

// idle is nil when nobody else is editing the file right now. The user's
// own lock stops them too, unless allowSelf: an upload by the person
// editing is theirs to make (their editor then finds the file changed).
func (s *Service) idle(ctx context.Context, q querier, nodeID, userID string, allowSelf bool) error {
	l, err := s.lockOf(ctx, q, nodeID)
	switch {
	case err != nil:
		return err
	case l == nil || !l.live:
		return nil
	case l.userID != userID:
		return &LockedError{l.holder}
	case allowSelf:
		return nil
	}
	return ErrLockedSelf
}

// checkLock is nil when lockID is the lock the user holds on the file. It
// holds even after the lock ran out, until someone else takes the file.
func (s *Service) checkLock(ctx context.Context, q querier, nodeID, userID, lockID string) error {
	l, err := s.lockOf(ctx, q, nodeID)
	if err != nil {
		return err
	}
	if l == nil || l.userID != userID || subtle.ConstantTimeCompare([]byte(l.hash), []byte(hashLock(lockID))) != 1 {
		return ErrLockLost
	}
	return nil
}

func (s *Service) extendLock(ctx context.Context, q querier, nodeID string) error {
	_, err := q.ExecContext(ctx, `UPDATE edit_locks SET expires_at = ? WHERE node_id = ?`, db.FormatTime(s.now().Add(LockTTL)), nodeID)
	return err
}

// editableFile loads a live file the user may change. With needEditor the
// app must also have an editor for it.
func (s *Service) editableFile(ctx context.Context, q querier, userID, nodeID string, needEditor bool) (item, error) {
	it, err := s.lookup(ctx, q, userID, nodeID, false)
	switch {
	case err != nil:
		return item{}, err
	case it.IsFolder():
		return item{}, ErrNotEditable
	case !it.canEdit():
		return item{}, ErrReadOnly
	case needEditor && it.Editor == "":
		return item{}, ErrNotEditable
	}
	return it, nil
}

// Holder is the person who has a file locked.
type Holder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// LockStatus says who is editing a file and whether that is the user.
type LockStatus struct {
	Holder Holder `json:"holder"`
	Mine   bool   `json:"mine"`
}

// LockStatus tells the file's editors who is editing it, or nil if nobody.
func (s *Service) LockStatus(ctx context.Context, userID, nodeID string) (*LockStatus, error) {
	it, err := s.editableFile(ctx, s.db, userID, nodeID, true)
	if err != nil {
		return nil, err
	}
	l, err := s.lockOf(ctx, s.db, it.ID)
	if err != nil || l == nil || !l.live {
		return nil, err
	}
	return &LockStatus{Holder{l.userID, l.holder}, l.userID == userID}, nil
}

// Lock takes the edit lock and returns its ID, the secret the saves must
// carry. Someone else's lock refuses (LockedError); so does one of the
// user's own, unless takeover: then they move the editing here, and
// tookOver says so.
func (s *Service) Lock(ctx context.Context, userID, nodeID string, takeover bool) (lockID string, tookOver bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	it, err := s.editableFile(ctx, tx, userID, nodeID, true)
	if err != nil {
		return "", false, err
	}
	if l, err := s.lockOf(ctx, tx, it.ID); err != nil {
		return "", false, err
	} else if l != nil && l.live {
		switch {
		case l.userID != userID:
			return "", false, &LockedError{l.holder}
		case !takeover:
			return "", false, ErrLockedSelf
		}
		tookOver = true
	}
	lockID = rand.Text()
	if _, err := tx.ExecContext(ctx, `INSERT INTO edit_locks (node_id, user_id, lock_hash, expires_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (node_id) DO UPDATE SET user_id = excluded.user_id, lock_hash = excluded.lock_hash, expires_at = excluded.expires_at`,
		it.ID, userID, hashLock(lockID), db.FormatTime(s.now().Add(LockTTL))); err != nil {
		return "", false, err
	}
	return lockID, tookOver, tx.Commit()
}

// RenewLock keeps the lock going, also for a holder whose lock just ran
// out, as long as nobody else took the file.
func (s *Service) RenewLock(ctx context.Context, userID, nodeID, lockID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	it, err := s.editableFile(ctx, tx, userID, nodeID, true)
	if err != nil {
		return err
	}
	if err := s.checkLock(ctx, tx, it.ID, userID, lockID); err != nil {
		return err
	}
	if err := s.extendLock(ctx, tx, it.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// Unlock releases the lock. It does nothing when the lock isn't the user's
// any more.
func (s *Service) Unlock(ctx context.Context, userID, nodeID, lockID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM edit_locks WHERE node_id = ? AND user_id = ? AND lock_hash = ?`,
		nodeID, userID, hashLock(lockID))
	return err
}

// stored is a new content in the object store, ready to become a version.
type stored struct {
	objectID, sum string
	size          int64
	// lockHash is the editing session that saved it: its previous save is
	// replaced, not kept as a version of its own.
	lockHash string
}

// swap makes a stored object the file's current version. check runs inside
// the transaction, after the access check, and says whether the change may
// go ahead. If anything fails the object is removed.
func (s *Service) swap(ctx context.Context, userID, nodeID string, o stored, check func(q querier, it item) error) (Node, error) {
	committed := false
	defer func() {
		if !committed {
			s.removeObjects([]string{o.objectID})
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	it, err := s.editableFile(ctx, tx, userID, nodeID, false)
	if err != nil {
		return Node{}, err
	}
	if err := check(tx, it); err != nil {
		return Node{}, err
	}
	versionID, drop, err := s.setVersion(ctx, tx, it, userID, o)
	if err != nil {
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, err
	}
	committed = true
	s.removeObjects(drop)
	s.saved(it.Name, versionID)
	return s.node(ctx, userID, nodeID)
}

// setVersion records o as the file's current version. The version it
// replaces stays as history, and goes (with its object, returned) when it
// was saved by the same editing session.
func (s *Service) setVersion(ctx context.Context, tx *sql.Tx, it item, userID string, o stored) (string, []string, error) {
	var prevID, prevObject string
	var prevLock sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, object_id, edit_lock FROM versions WHERE id = ?`, it.versionID).Scan(&prevID, &prevObject, &prevLock)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	versionID, ts := db.NewID(), s.stamp()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO versions (id, node_id, object_id, size, sha256, created_by, created_at, edit_lock) VALUES (?, ?, ?, ?, ?, ?, ?, nullif(?, ''))`,
		versionID, it.ID, o.objectID, o.size, o.sum, userID, ts, o.lockHash); err != nil {
		return "", nil, err
	}
	var drop []string
	switch {
	case prevID == "":
		err = nil
	case o.lockHash != "" && prevLock.String == o.lockHash:
		_, err = tx.ExecContext(ctx, `DELETE FROM versions WHERE id = ?`, prevID)
		drop = []string{prevObject}
	default:
		_, err = tx.ExecContext(ctx, `UPDATE versions SET superseded_at = ? WHERE id = ?`, ts, prevID)
	}
	if err != nil {
		return "", nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET current_version_id = ?, size = ?, modified_at = ? WHERE id = ?`,
		versionID, o.size, ts, it.ID); err != nil {
		return "", nil, err
	}
	if it.Type == "link" {
		if err := indexLink(ctx, tx, it.ID, s.linkTarget(it.Name, o.objectID, o.size)); err != nil {
			return "", nil, err
		}
	}
	return versionID, drop, nil
}

// Edit saves new contents of a file the user holds the lock on. kind is
// "text" (Markdown and plain text) or "sheet", and must match the file.
// ifMatch is the version the user started from; if someone else saved since,
// that is ErrEditConflict. build makes the new bytes, and runs only once the
// user is known to be allowed; cur reads the current contents for it.
func (s *Service) Edit(ctx context.Context, userID, nodeID, lockID, ifMatch, kind string,
	build func(cur func() ([]byte, error)) ([]byte, error)) (Node, error) {
	it, err := s.editableFile(ctx, s.db, userID, nodeID, true)
	if err != nil {
		return Node{}, err
	}
	limit := int64(MaxTextFile)
	if kind == "sheet" {
		limit = MaxSheetFile
	}
	if (it.Editor == "sheet") != (kind == "sheet") {
		return Node{}, ErrNotEditable
	}
	check := func(q querier, it item) error {
		if err := s.checkLock(ctx, q, it.ID, userID, lockID); err != nil {
			return err
		}
		if ifMatch != it.versionID {
			c := ConflictError{}
			var at string
			q.QueryRowContext(ctx, `SELECT coalesce(u.name, ''), v.created_at FROM versions v
				LEFT JOIN users u ON u.id = v.created_by WHERE v.id = ?`, it.versionID).Scan(&c.By, &at)
			c.At, _ = db.ParseTime(at)
			return &c
		}
		return s.extendLock(ctx, q, it.ID)
	}
	if err := check(s.db, it); err != nil {
		return Node{}, err
	}
	data, err := build(func() ([]byte, error) {
		f, err := s.openVersion(ctx, it.Node)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, limit))
	})
	if err != nil {
		return Node{}, err
	}
	if int64(len(data)) > limit {
		return Node{}, ErrTooLarge
	}
	if err := s.checkSpace(int64(len(data))); err != nil {
		return Node{}, err
	}
	objectID, sum, err := s.store.PutObject(data)
	if err != nil {
		return Node{}, err
	}
	return s.swap(ctx, userID, nodeID, stored{objectID, sum, int64(len(data)), hashLock(lockID)}, check)
}

// CreateFile makes a new file with the given contents in a folder, takes
// its edit lock for the user and returns both. Only types the app can edit
// are accepted, and a taken name gets a number.
func (s *Service) CreateFile(ctx context.Context, userID, parentID, name string, content []byte) (Node, string, error) {
	name, err := ValidateName(name)
	if err != nil {
		return Node{}, "", err
	}
	if EditorOf(name, int64(len(content))) == "" {
		return Node{}, "", ErrNotEditable
	}
	if _, err := s.editableFolder(ctx, s.db, userID, parentID); err != nil {
		return Node{}, "", err
	}
	if err := s.checkSpace(int64(len(content))); err != nil {
		return Node{}, "", err
	}
	objectID, sum, err := s.store.PutObject(content)
	if err != nil {
		return Node{}, "", err
	}
	nodeID, err := s.addFile(ctx, userID, parentID, name, "", objectID, sum, int64(len(content)))
	if err != nil {
		s.removeObjects([]string{objectID})
		return Node{}, "", err
	}
	lockID, _, err := s.Lock(ctx, userID, nodeID, false)
	if err != nil {
		return Node{}, "", err
	}
	n, err := s.node(ctx, userID, nodeID)
	return n, lockID, err
}

// Version is one saved content of a file.
type Version struct {
	ID        string    `json:"id"`
	Current   bool      `json:"current"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
	By        string    `json:"by"`
}

// Versions lists a file's saved contents, newest first. Only the people
// who can edit the file see them.
func (s *Service) Versions(ctx context.Context, userID, nodeID string) ([]Version, error) {
	it, err := s.editableFile(ctx, s.db, userID, nodeID, false)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT v.id, v.size, v.created_at, coalesce(u.name, '')
		FROM versions v LEFT JOIN users u ON u.id = v.created_by
		WHERE v.node_id = ? ORDER BY v.created_at DESC, v.id DESC`, it.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		var created string
		if err := rows.Scan(&v.ID, &v.Size, &created, &v.By); err != nil {
			return nil, err
		}
		v.CreatedAt, _ = db.ParseTime(created)
		v.Current = v.ID == it.versionID
		out = append(out, v)
	}
	return out, rows.Err()
}

// savedVersion is a version's stored object.
type savedVersion struct {
	objectID  string
	size      int64
	createdAt time.Time
}

// versionOf finds one of the file's versions, for the people who can edit it.
func (s *Service) versionOf(ctx context.Context, userID, nodeID, versionID string) (item, savedVersion, error) {
	if !db.ValidID(versionID) {
		return item{}, savedVersion{}, ErrNotFound
	}
	it, err := s.editableFile(ctx, s.db, userID, nodeID, false)
	if err != nil {
		return item{}, savedVersion{}, err
	}
	var v savedVersion
	var created string
	err = s.db.QueryRowContext(ctx, `SELECT object_id, size, created_at FROM versions WHERE id = ? AND node_id = ?`,
		versionID, it.ID).Scan(&v.objectID, &v.size, &created)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	v.createdAt, _ = db.ParseTime(created)
	return it, v, err
}

// OpenOldVersion opens one saved content of a file for reading. The node
// is the file as it was then: its modified time is when that was saved.
func (s *Service) OpenOldVersion(ctx context.Context, userID, nodeID, versionID string) (*os.File, Node, error) {
	it, v, err := s.versionOf(ctx, userID, nodeID, versionID)
	if err != nil {
		return nil, Node{}, err
	}
	f, err := s.store.OpenObject(v.objectID)
	it.ModifiedAt = v.createdAt
	return f, it.Node, err
}

// RestoreVersion makes an old content current again, as a new version: the
// history stays. It is refused while anyone is editing the file.
func (s *Service) RestoreVersion(ctx context.Context, userID, nodeID, versionID string) (Node, error) {
	it, v, err := s.versionOf(ctx, userID, nodeID, versionID)
	if err != nil {
		return Node{}, err
	}
	if err := s.idle(ctx, s.db, it.ID, userID, false); err != nil {
		return Node{}, err
	}
	if versionID == it.versionID {
		return s.node(ctx, userID, it.ID)
	}
	if err := s.checkSpace(v.size); err != nil {
		return Node{}, err
	}
	newObject, sum, err := s.copyObject(v.objectID)
	if err != nil {
		return Node{}, err
	}
	return s.swap(ctx, userID, it.ID, stored{newObject, sum, v.size, ""}, func(q querier, it item) error {
		return s.idle(ctx, q, it.ID, userID, false)
	})
}

// copyObject stores a copy of an object under a new ID, so versions never
// share one and deleting one can't harm another.
func (s *Service) copyObject(objectID string) (newID, sum string, err error) {
	f, err := s.store.OpenObject(objectID)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	upload := db.NewID()
	if err := s.store.CreateUpload(upload); err != nil {
		return "", "", err
	}
	size, err := s.store.WriteUpload(upload, 0, f)
	if err == nil {
		newID, sum, err = s.store.FinishUpload(upload, size)
	}
	if err != nil {
		s.store.RemoveUpload(upload)
	}
	return newID, sum, err
}

// replaceTarget is the file an upload replaces, or "" when it adds a new
// one: it wasn't asked to replace, or the file was deleted, moved or renamed
// since, or the user may no longer change it. Adding is the safe fallback:
// nothing is overwritten.
func (s *Service) replaceTarget(ctx context.Context, userID string, u Upload) string {
	if u.Replaces == "" {
		return ""
	}
	it, err := s.editableFile(ctx, s.db, userID, u.Replaces, false)
	if err != nil || it.ParentID != u.ParentID || nameKey(it.Name) != nameKey(u.Name) {
		return ""
	}
	return it.ID
}
