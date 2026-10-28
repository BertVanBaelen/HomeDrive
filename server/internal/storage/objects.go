package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
)

// ErrBadID means an ID from the caller has the wrong form. Paths are only
// ever built from IDs that pass these checks.
var ErrBadID = errors.New("invalid storage id")

// Object IDs are 128 random bits in lower-case hex. Upload IDs are UUIDs
// handed out by the database layer.
func validObjectID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !isLowerHex(c) {
			return false
		}
	}
	return true
}

func validUploadID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !isLowerHex(c) {
			return false
		}
	}
	return true
}

func isLowerHex(c rune) bool { return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' }

func objectPath(id string) string { return path.Join("objects", id[:2], id) }

func uploadPath(id string) string { return path.Join("uploads", id) }

// CreateUpload starts an empty upload file.
func (s *Store) CreateUpload(id string) error {
	if !validUploadID(id) {
		return ErrBadID
	}
	f, err := s.root.OpenFile(uploadPath(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	return f.Close()
}

// WriteUpload appends r to the upload, which must currently be offset bytes
// long. It returns how many bytes were written. Partial writes are kept; the
// caller records the new length.
func (s *Store) WriteUpload(id string, offset int64, r io.Reader) (int64, error) {
	if !validUploadID(id) {
		return 0, ErrBadID
	}
	f, err := s.root.OpenFile(uploadPath(id), os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	// A previous chunk may have been cut off after the database recorded a
	// shorter length; drop whatever is beyond it.
	if err := f.Truncate(offset); err != nil {
		return 0, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if serr := f.Sync(); err == nil {
		err = serr
	}
	return n, err
}

// FinishUpload turns a complete upload into an immutable object and returns
// its ID and SHA-256. The upload file is gone afterwards.
func (s *Store) FinishUpload(id string, size int64) (objectID, sum string, err error) {
	if !validUploadID(id) {
		return "", "", ErrBadID
	}
	f, err := s.root.Open(uploadPath(id))
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	f.Close()
	if err != nil {
		return "", "", err
	}
	if n != size {
		return "", "", fmt.Errorf("upload %s has %d bytes, expected %d", id, n, size)
	}
	objectID = newObjectID()
	if err := s.root.MkdirAll(path.Dir(objectPath(objectID)), 0o750); err != nil {
		return "", "", err
	}
	if err := s.root.Rename(uploadPath(id), objectPath(objectID)); err != nil {
		return "", "", err
	}
	return objectID, hex.EncodeToString(h.Sum(nil)), nil
}

// PutObject stores data as a new object, for small contents written in one
// go (such as an empty file).
func (s *Store) PutObject(data []byte) (objectID, sum string, err error) {
	tmp := path.Join("uploads", ".put-"+rand.Text())
	if err := s.writeSynced(tmp, data, 0o640); err != nil {
		return "", "", err
	}
	objectID = newObjectID()
	if err := s.root.MkdirAll(path.Dir(objectPath(objectID)), 0o750); err != nil {
		s.root.Remove(tmp)
		return "", "", err
	}
	if err := s.root.Rename(tmp, objectPath(objectID)); err != nil {
		s.root.Remove(tmp)
		return "", "", err
	}
	h := sha256.Sum256(data)
	return objectID, hex.EncodeToString(h[:]), nil
}

// RemoveUpload deletes an upload's file. A missing file is not an error.
func (s *Store) RemoveUpload(id string) error {
	if !validUploadID(id) {
		return ErrBadID
	}
	err := s.root.Remove(uploadPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// RemoveObject deletes an object that no version uses any more. The backup
// keeps its own copy for the retention period. A missing object is not an
// error.
func (s *Store) RemoveObject(id string) error {
	if !validObjectID(id) {
		return ErrBadID
	}
	err := s.root.Remove(objectPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// HasObject reports whether an object exists.
func (s *Store) HasObject(id string) bool {
	if !validObjectID(id) {
		return false
	}
	_, err := s.root.Stat(objectPath(id))
	return err == nil
}

// CopyObject stores r under an existing object ID, as the backup does with
// objects from the data folder. It is written aside and renamed into place,
// so a half-copied object never looks complete.
func (s *Store) CopyObject(id string, r io.Reader) (int64, error) {
	if !validObjectID(id) {
		return 0, ErrBadID
	}
	tmp := path.Join("uploads", ".copy-"+rand.Text())
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = s.root.MkdirAll(path.Dir(objectPath(id)), 0o750)
	}
	if err == nil {
		err = s.root.Rename(tmp, objectPath(id))
	}
	if err != nil {
		s.root.Remove(tmp)
		return 0, err
	}
	return n, nil
}

// ListObjects returns the IDs of all objects with their sizes.
func (s *Store) ListObjects() (map[string]int64, error) {
	out := map[string]int64{}
	fsys := s.root.FS()
	dirs, err := fs.ReadDir(fsys, "objects")
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		entries, err := fs.ReadDir(fsys, path.Join("objects", d.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if validObjectID(e.Name()) {
				if info, err := e.Info(); err == nil {
					out[e.Name()] = info.Size()
				}
			}
		}
	}
	return out, nil
}

// OpenObject opens an object for reading.
func (s *Store) OpenObject(id string) (*os.File, error) {
	if !validObjectID(id) {
		return nil, ErrBadID
	}
	return s.root.Open(objectPath(id))
}

func newObjectID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
