// Package storage owns the data folder that holds file contents.
//
// The folder belongs to Homedrive alone. Contents are stored as immutable
// objects named by random IDs (see docs/architecture.md), and every access
// goes through an os.Root so nothing can reach outside the folder.
package storage

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	markerName = ".homedrive"
	format     = 1
)

var (
	// ErrWrongFolder means the data folder isn't this installation's, most
	// likely because a USB disk or NAS share isn't mounted.
	ErrWrongFolder = errors.New("data folder does not belong to this Homedrive installation")
	// ErrNotEmpty means a first start was pointed at a folder that already
	// holds other files.
	ErrNotEmpty = errors.New("data folder is not empty")
)

// housekeeping lists names that filesystems and NAS systems create by
// themselves; they don't make a fresh folder "non-empty".
var housekeeping = map[string]bool{
	"lost+found": true, ".DS_Store": true, "Thumbs.db": true, "desktop.ini": true, "@eaDir": true, "#recycle": true,
}

type marker struct {
	InstanceID string `json:"instance_id"`
	Format     int    `json:"format"`
}

type Store struct {
	root *os.Root
	path string
}

// Open checks that dir is this installation's data folder and returns a
// store rooted there.
//
// On first use (initialized false) it claims a missing or empty folder by
// writing a marker with instanceID. After that the marker must be present
// and match. That catches a USB disk or NAS share that isn't mounted: its
// mount point looks like an empty local folder, and writing there would
// silently split the drive in two.
func Open(dir, instanceID string, initialized bool) (*Store, error) {
	if err := ensureDir(dir, initialized); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening data folder %s: %w", dir, err)
	}
	s := &Store{root: root, path: dir}
	if err := s.init(instanceID, initialized); err != nil {
		root.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init(instanceID string, initialized bool) error {
	if err := s.checkMarker(instanceID, initialized); err != nil {
		return err
	}
	for _, sub := range []string{"objects", "uploads"} {
		if err := s.root.MkdirAll(sub, 0o750); err != nil {
			return fmt.Errorf("creating %s in data folder %s: %w", sub, s.path, err)
		}
	}
	return s.probeWritable()
}

func (s *Store) Close() error { return s.root.Close() }

func ensureDir(dir string, initialized bool) error {
	fi, err := os.Stat(dir)
	switch {
	case err == nil:
		if !fi.IsDir() {
			return fmt.Errorf("data folder %s is not a directory", dir)
		}
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("checking data folder %s: %w", dir, err)
	case initialized:
		return fmt.Errorf("%w: %s is missing. If it is on a USB disk or NAS share, check that it is mounted", ErrWrongFolder, dir)
	}
	// Only create the folder itself. A missing parent usually means a disk or
	// share that isn't mounted, and creating it would write to the system disk.
	parent := filepath.Dir(dir)
	if _, err := os.Stat(parent); err != nil {
		return fmt.Errorf("can't create data folder %s: %s is not available (is the disk or share mounted?): %w", dir, parent, err)
	}
	if err := os.Mkdir(dir, 0o750); err != nil {
		return fmt.Errorf("creating data folder %s: %w", dir, err)
	}
	return nil
}

func (s *Store) checkMarker(instanceID string, initialized bool) error {
	b, err := s.root.ReadFile(markerName)
	switch {
	case err == nil:
		var m marker
		if err := json.Unmarshal(b, &m); err != nil || m.InstanceID == "" {
			return fmt.Errorf("%w: the marker file %s in %s is damaged", ErrWrongFolder, markerName, s.path)
		}
		if m.InstanceID != instanceID {
			return fmt.Errorf("%w: %s was created by another installation (%s)", ErrWrongFolder, s.path, m.InstanceID)
		}
		if m.Format != format {
			return fmt.Errorf("data folder %s uses storage format %d, this version supports %d", s.path, m.Format, format)
		}
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("reading %s in data folder %s: %w", markerName, s.path, err)
	case initialized:
		return fmt.Errorf("%w: %s has no %s marker. If it is on a USB disk or NAS share, check that it is mounted", ErrWrongFolder, s.path, markerName)
	}

	// First use: only claim a folder that holds nothing of value.
	entries, err := fs.ReadDir(s.root.FS(), ".")
	if err != nil {
		return fmt.Errorf("listing data folder %s: %w", s.path, err)
	}
	for _, e := range entries {
		if !housekeeping[e.Name()] {
			return fmt.Errorf("%w: %s contains %q. Homedrive needs an empty folder of its own", ErrNotEmpty, s.path, e.Name())
		}
	}
	return s.writeMarker(instanceID)
}

func (s *Store) writeMarker(instanceID string) error {
	b, err := json.Marshal(marker{InstanceID: instanceID, Format: format})
	if err != nil {
		return err
	}
	tmp := markerName + ".tmp"
	if err := s.writeSynced(tmp, b, 0o640); err != nil {
		return fmt.Errorf("writing marker in data folder %s: %w", s.path, err)
	}
	if err := s.root.Rename(tmp, markerName); err != nil {
		return fmt.Errorf("writing marker in data folder %s: %w", s.path, err)
	}
	return nil
}

// probeWritable writes, syncs and removes a small file, so that a read-only
// or full share fails at startup rather than on the first upload.
func (s *Store) probeWritable() error {
	name := filepath.Join("uploads", ".probe-"+rand.Text())
	err := s.writeSynced(name, []byte("ok"), 0o600)
	if err == nil {
		err = s.root.Remove(name)
	}
	if err != nil {
		return fmt.Errorf("data folder %s failed a write test: %w", s.path, err)
	}
	return nil
}

func (s *Store) writeSynced(name string, data []byte, perm os.FileMode) error {
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	return errors.Join(werr, serr, f.Close())
}
