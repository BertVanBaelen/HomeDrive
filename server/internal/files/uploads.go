package files

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

const (
	// MaxChunk stays under Cloudflare's 100 MB request body limit.
	MaxChunk = 32 << 20
	// ChunkSize is what clients are asked to send; smaller chunks give
	// smoother progress and lose less on a dropped connection.
	ChunkSize = 8 << 20
	// MaxFileSize is a sanity limit; Homedrive is for documents.
	MaxFileSize   = 50 << 30
	uploadTimeout = 24 * time.Hour
)

type Upload struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Received  int64  `json:"received"`
	ChunkSize int64  `json:"chunkSize"`
	// Replaces is the file the upload becomes a new version of.
	Replaces string `json:"replaces,omitempty"`

	description string
}

// Conflict says what an upload does when its name is taken by a file in
// the folder: keep both (the new one gets a number), replace the file with
// a new version, or ask (refuse, naming the file, before any bytes). Folders
// never ask: the upload is kept next to them.
type Conflict string

const (
	ConflictKeep    Conflict = ""
	ConflictAsk     Conflict = "ask"
	ConflictReplace Conflict = "replace"
)

// ErrInvalidConflict is for a conflict choice that doesn't exist.
var ErrInvalidConflict = errors.New("invalid conflict choice")

// CreateUpload starts a chunked upload of a new file into a folder, with
// the description it gets once complete.
func (s *Service) CreateUpload(ctx context.Context, userID, parentID, name string, size int64, description string, conflict Conflict) (Upload, error) {
	name, err := ValidateName(name)
	if err != nil {
		return Upload{}, err
	}
	if description, err = ValidateDescription(description); err != nil {
		return Upload{}, err
	}
	if size < 0 || size > MaxFileSize {
		return Upload{}, ErrTooLarge
	}
	if conflict != ConflictKeep && conflict != ConflictAsk && conflict != ConflictReplace {
		return Upload{}, ErrInvalidConflict
	}
	if _, err := s.editableFolder(ctx, s.db, userID, parentID); err != nil {
		return Upload{}, err
	}
	var replaces string
	if conflict != ConflictKeep {
		if replaces, err = s.resolveConflict(ctx, userID, parentID, name, conflict); err != nil {
			return Upload{}, err
		}
	}
	if err := s.checkSpace(size); err != nil {
		return Upload{}, err
	}
	u := Upload{ID: db.NewID(), ParentID: parentID, Name: name, Size: size, ChunkSize: ChunkSize, Replaces: replaces}
	if err := s.store.CreateUpload(u.ID); err != nil {
		return Upload{}, err
	}
	now := s.now()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO uploads (id, user_id, parent_id, name, size, description, replace_node_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, nullif(?, ''), ?, ?)`,
		u.ID, userID, parentID, name, size, description, replaces, db.FormatTime(now), db.FormatTime(now.Add(uploadTimeout))); err != nil {
		s.store.RemoveUpload(u.ID)
		return Upload{}, err
	}
	return u, nil
}

// resolveConflict handles a taken name: it returns the file to replace, ""
// to add a new file, or NameTakenError to ask.
func (s *Service) resolveConflict(ctx context.Context, userID, parentID, name string, conflict Conflict) (string, error) {
	var id, kind string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, kind FROM nodes WHERE parent_id = ? AND name_key = ? AND trashed_at IS NULL`, parentID, nameKey(name)).Scan(&id, &kind)
	if errors.Is(err, sql.ErrNoRows) || err == nil && kind == "folder" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if conflict == ConflictAsk {
		existing, err := s.node(ctx, userID, id)
		if err != nil {
			return "", err
		}
		return "", &NameTakenError{Existing: existing}
	}
	return id, s.idle(ctx, s.db, id, userID, true)
}

// upload loads one of the user's own uploads that hasn't expired.
func (s *Service) upload(ctx context.Context, userID, id string) (Upload, error) {
	if !db.ValidID(id) {
		return Upload{}, ErrNotFound
	}
	u := Upload{ID: id, ChunkSize: ChunkSize}
	err := s.db.QueryRowContext(ctx,
		`SELECT parent_id, name, size, received, description, coalesce(replace_node_id, '') FROM uploads WHERE id = ? AND user_id = ? AND expires_at > ?`,
		id, userID, s.stamp()).Scan(&u.ParentID, &u.Name, &u.Size, &u.Received, &u.description, &u.Replaces)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	return u, err
}

// UploadStatus tells a client where to resume.
func (s *Service) UploadStatus(ctx context.Context, userID, id string) (Upload, error) {
	return s.upload(ctx, userID, id)
}

// lockUpload serialises chunk writes to one upload.
func (s *Service) lockUpload(id string) func() {
	s.uploadMu.Lock()
	m, ok := s.uploadLocks[id]
	if !ok {
		m = &sync.Mutex{}
		s.uploadLocks[id] = m
	}
	s.uploadMu.Unlock()
	m.Lock()
	return m.Unlock
}

func (s *Service) forgetUploadLock(id string) {
	s.uploadMu.Lock()
	delete(s.uploadLocks, id)
	s.uploadMu.Unlock()
}

// WriteChunk appends length bytes from r at offset, which must be exactly
// what was received so far. It returns the new received count.
func (s *Service) WriteChunk(ctx context.Context, userID, id string, offset, length int64, r io.Reader) (int64, error) {
	unlock := s.lockUpload(id)
	defer unlock()
	u, err := s.upload(ctx, userID, id)
	if err != nil {
		return 0, err
	}
	if offset != u.Received {
		return u.Received, ErrBadOffset
	}
	if length < 0 || length > MaxChunk || offset+length > u.Size {
		return u.Received, ErrTooLarge
	}
	n, werr := s.store.WriteUpload(id, offset, io.LimitReader(r, length))
	received := offset + n
	if _, err := s.db.ExecContext(ctx, `UPDATE uploads SET received = ? WHERE id = ?`, received, id); err != nil {
		return u.Received, err
	}
	if werr != nil || n != length {
		return received, errors.Join(ErrUploadFailed, werr)
	}
	return received, nil
}

// CompleteUpload turns a fully received upload into a file. If the name is
// taken by now, a number is added.
func (s *Service) CompleteUpload(ctx context.Context, userID, id string) (Node, error) {
	unlock := s.lockUpload(id)
	defer unlock()
	u, err := s.upload(ctx, userID, id)
	if err != nil {
		return Node{}, err
	}
	if u.Received != u.Size {
		return Node{}, ErrIncomplete
	}
	// Fail before building the object if the folder went away meanwhile.
	if _, err := s.editableFolder(ctx, s.db, userID, u.ParentID); err != nil {
		return Node{}, err
	}
	target := s.replaceTarget(ctx, userID, u)
	if target != "" {
		// Checked before the upload is consumed, so it can be retried.
		if err := s.idle(ctx, s.db, target, userID, true); err != nil {
			return Node{}, err
		}
	}
	objectID, sum, err := s.store.FinishUpload(id, u.Size)
	if err != nil {
		return Node{}, err
	}
	nodeID := target
	if target != "" {
		_, err = s.swap(ctx, userID, target, stored{objectID, sum, u.Size, ""}, func(q querier, it item) error {
			return s.idle(ctx, q, it.ID, userID, true)
		})
	} else {
		nodeID, err = s.addFile(ctx, userID, u.ParentID, u.Name, u.description, objectID, sum, u.Size)
	}
	if err != nil {
		// The object is unreferenced now; retention cleanup removes it.
		s.log.Warn("upload completed but file not added", "upload", id, "object", objectID, "error", err)
		if target != "" {
			// The bytes are gone with the failed swap, so it can't be retried.
			s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id)
			s.forgetUploadLock(id)
		}
		return Node{}, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id); err != nil {
		s.log.Warn("removing finished upload", "upload", id, "error", err)
	}
	s.forgetUploadLock(id)
	return s.node(ctx, userID, nodeID)
}

// CancelUpload drops an upload and its bytes.
func (s *Service) CancelUpload(ctx context.Context, userID, id string) error {
	unlock := s.lockUpload(id)
	defer unlock()
	if _, err := s.upload(ctx, userID, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id); err != nil {
		return err
	}
	s.forgetUploadLock(id)
	return s.store.RemoveUpload(id)
}

// addFile creates a file node with its first version. A saved link also
// gets its address recorded.
func (s *Service) addFile(ctx context.Context, userID, parentID, name, description, objectID, sum string, size int64) (string, error) {
	link := s.linkTarget(name, objectID, size)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	parent, err := s.editableFolder(ctx, tx, userID, parentID)
	if err != nil {
		return "", err
	}
	name, err = uniqueName(ctx, tx, parent.ID, name)
	if err != nil {
		return "", err
	}
	nodeID, versionID, ts := db.NewID(), db.NewID(), s.stamp()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO nodes (id, drive_id, parent_id, kind, name, name_key, current_version_id, size, mime, created_by, created_at, modified_at,
			description, description_key)
		 VALUES (?, ?, ?, 'file', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nodeID, parent.DriveID, parent.ID, name, nameKey(name), versionID, size, MimeOf(name), userID, ts, ts,
		description, descriptionKey(description)); err != nil {
		return "", err
	}
	if link != nil {
		if err := indexLink(ctx, tx, nodeID, link); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO versions (id, node_id, object_id, size, sha256, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		versionID, nodeID, objectID, size, sum, userID, ts); err != nil {
		return "", err
	}
	if err := s.touch(ctx, tx, parent.ID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.saved(name, versionID)
	return nodeID, nil
}

// Open returns a live file's current contents for reading.
func (s *Service) Open(ctx context.Context, userID, nodeID string) (*os.File, Node, error) {
	it, err := s.lookup(ctx, s.db, userID, nodeID, false)
	if err != nil {
		return nil, Node{}, err
	}
	f, err := s.openVersion(ctx, it.Node)
	if err != nil {
		return nil, Node{}, err
	}
	return f, it.Node, nil
}

// InboxName is the folder at the top of My Files where uploads from
// devices (the iPhone Shortcut) land.
const InboxName = "Inbox"

// MaxInboxUpload is the most one device upload may carry: it is a single
// request, and Cloudflare refuses bodies over 100 MB.
const MaxInboxUpload = 95 << 20

// UploadToInbox stores a file sent in one request into the user's Inbox,
// creating the folder when needed. A taken name gets a number added. The
// description is text shared with the file; it is cut to the maximum
// length rather than refused.
func (s *Service) UploadToInbox(ctx context.Context, userID, name, description string, r io.Reader) (Node, error) {
	name, err := ValidateName(name)
	if err != nil {
		return Node{}, err
	}
	description = ClipDescription(description)
	// The size isn't known up front; assume the largest allowed.
	if err := s.checkSpace(MaxInboxUpload); err != nil {
		return Node{}, err
	}
	inbox, err := s.inbox(ctx, userID)
	if err != nil {
		return Node{}, err
	}
	id := db.NewID()
	if err := s.store.CreateUpload(id); err != nil {
		return Node{}, err
	}
	n, err := s.store.WriteUpload(id, 0, io.LimitReader(r, MaxInboxUpload+1))
	if err == nil && n > MaxInboxUpload {
		err = ErrTooLarge
	}
	if err != nil {
		s.store.RemoveUpload(id)
		return Node{}, err
	}
	objectID, sum, err := s.store.FinishUpload(id, n)
	if err != nil {
		s.store.RemoveUpload(id)
		return Node{}, err
	}
	nodeID, err := s.addFile(ctx, userID, inbox, name, description, objectID, sum, n)
	if err != nil {
		s.removeObjects([]string{objectID})
		return Node{}, err
	}
	return s.node(ctx, userID, nodeID)
}

// inbox finds or creates the Inbox folder of the user's personal drive.
func (s *Service) inbox(ctx context.Context, userID string) (string, error) {
	var root string
	if err := s.db.QueryRowContext(ctx,
		`SELECT root_id FROM drives WHERE owner_user_id = ? AND kind = 'personal'`, userID).Scan(&root); err != nil {
		return "", err
	}
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM nodes WHERE parent_id = ? AND name_key = ? AND kind = 'folder' AND trashed_at IS NULL`,
		root, nameKey(InboxName)).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	n, err := s.CreateFolder(ctx, userID, root, InboxName)
	if errors.Is(err, ErrNameTaken) {
		// A file is called Inbox; put the upload at the top instead.
		return root, nil
	}
	return n.ID, err
}
