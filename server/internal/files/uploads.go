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
}

// CreateUpload starts a chunked upload of a new file into a folder.
func (s *Service) CreateUpload(ctx context.Context, userID, parentID, name string, size int64) (Upload, error) {
	name, err := ValidateName(name)
	if err != nil {
		return Upload{}, err
	}
	if size < 0 || size > MaxFileSize {
		return Upload{}, ErrTooLarge
	}
	if _, err := s.editableFolder(ctx, s.db, userID, parentID); err != nil {
		return Upload{}, err
	}
	u := Upload{ID: db.NewID(), ParentID: parentID, Name: name, Size: size, ChunkSize: ChunkSize}
	if err := s.store.CreateUpload(u.ID); err != nil {
		return Upload{}, err
	}
	now := s.now()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO uploads (id, user_id, parent_id, name, size, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, userID, parentID, name, size, db.FormatTime(now), db.FormatTime(now.Add(uploadTimeout))); err != nil {
		s.store.RemoveUpload(u.ID)
		return Upload{}, err
	}
	return u, nil
}

// upload loads one of the user's own uploads that hasn't expired.
func (s *Service) upload(ctx context.Context, userID, id string) (Upload, error) {
	if !db.ValidID(id) {
		return Upload{}, ErrNotFound
	}
	u := Upload{ID: id, ChunkSize: ChunkSize}
	err := s.db.QueryRowContext(ctx,
		`SELECT parent_id, name, size, received FROM uploads WHERE id = ? AND user_id = ? AND expires_at > ?`,
		id, userID, s.stamp()).Scan(&u.ParentID, &u.Name, &u.Size, &u.Received)
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
	objectID, sum, err := s.store.FinishUpload(id, u.Size)
	if err != nil {
		return Node{}, err
	}
	nodeID, err := s.addFile(ctx, userID, u.ParentID, u.Name, objectID, sum, u.Size)
	if err != nil {
		// The object is unreferenced now; retention cleanup removes it.
		s.log.Warn("upload completed but file not added", "upload", id, "object", objectID, "error", err)
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

// addFile creates a file node with its first version.
func (s *Service) addFile(ctx context.Context, userID, parentID, name, objectID, sum string, size int64) (string, error) {
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
		`INSERT INTO nodes (id, drive_id, parent_id, kind, name, name_key, current_version_id, size, mime, created_by, created_at, modified_at)
		 VALUES (?, ?, ?, 'file', ?, ?, ?, ?, ?, ?, ?, ?)`,
		nodeID, parent.DriveID, parent.ID, name, nameKey(name), versionID, size, MimeOf(name), userID, ts, ts); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO versions (id, node_id, object_id, size, sha256, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		versionID, nodeID, objectID, size, sum, userID, ts); err != nil {
		return "", err
	}
	if err := s.touch(ctx, tx, parent.ID); err != nil {
		return "", err
	}
	return nodeID, tx.Commit()
}

// Open returns a live file's current contents for reading.
func (s *Service) Open(ctx context.Context, userID, nodeID string) (*os.File, Node, error) {
	it, err := s.lookup(ctx, s.db, userID, nodeID, false)
	if err != nil {
		return nil, Node{}, err
	}
	if it.IsFolder() || it.versionID == "" {
		return nil, Node{}, ErrNotFound
	}
	var objectID string
	if err := s.db.QueryRowContext(ctx, `SELECT object_id FROM versions WHERE id = ?`, it.versionID).Scan(&objectID); err != nil {
		return nil, Node{}, err
	}
	f, err := s.store.OpenObject(objectID)
	if err != nil {
		return nil, Node{}, err
	}
	return f, it.Node, nil
}
