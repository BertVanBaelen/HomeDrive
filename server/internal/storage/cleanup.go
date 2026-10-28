package storage

import (
	"errors"
	"io/fs"
	"path"
	"time"
)

// ObjectsOlderThan lists objects last written before t. The cleanup job
// compares them with the database to find orphans; the age check keeps it
// away from an object an upload is just adding.
func (s *Store) ObjectsOlderThan(t time.Time) ([]string, error) {
	var out []string
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
			if !validObjectID(e.Name()) {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Before(t) {
				out = append(out, e.Name())
			}
		}
	}
	return out, nil
}

// UploadFiles lists what is in the uploads folder and when it last changed:
// chunked uploads (named by their ID) and temporary files of interrupted
// writes.
func (s *Store) UploadFiles() (map[string]time.Time, error) {
	entries, err := fs.ReadDir(s.root.FS(), "uploads")
	if err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			out[e.Name()] = info.ModTime()
		}
	}
	return out, nil
}

// RemoveUploadFile deletes a file from the uploads folder by name. Only
// plain names are accepted, never paths.
func (s *Store) RemoveUploadFile(name string) error {
	if name == "" || name != path.Base(name) || name == "." || name == ".." {
		return ErrBadID
	}
	err := s.root.Remove(path.Join("uploads", name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
