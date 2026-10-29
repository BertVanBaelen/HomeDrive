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
	ErrWrongFolder = errors.New("folder does not belong to this Homedrive installation")
	// ErrNotEmpty means a first start was pointed at a folder that already
	// holds other files.
	ErrNotEmpty = errors.New("folder is not empty")
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
	root  *os.Root
	path  string
	label string // "data folder" or "backup folder", for messages
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
	return open(dir, "data folder", instanceID, initialized)
}

// OpenBackup does the same for backup_path, which holds a copy of the
// objects with the same layout, plus database snapshots in db/.
func OpenBackup(dir, instanceID string, initialized bool) (*Store, error) {
	s, err := open(dir, "backup folder", instanceID, initialized)
	if err != nil {
		return nil, err
	}
	if err := s.root.MkdirAll("db", 0o750); err != nil {
		s.Close()
		return nil, fmt.Errorf("creating db in backup folder %s: %w", dir, err)
	}
	return s, nil
}

// Path is the folder's location on disk.
func (s *Store) Path() string { return s.path }

// FreeSpace is how many bytes can still be written to the folder's disk
// by this process.
func (s *Store) FreeSpace() (uint64, error) {
	_, free, err := s.DiskUsage()
	return free, err
}

func open(dir, label, instanceID string, initialized bool) (*Store, error) {
	if err := ensureDir(dir, label, initialized); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening %s %s: %w", label, dir, err)
	}
	s := &Store{root: root, path: dir, label: label}
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
			return fmt.Errorf("creating %s in %s %s: %w", sub, s.label, s.path, err)
		}
	}
	return s.probeWritable()
}

func (s *Store) Close() error { return s.root.Close() }

func ensureDir(dir, label string, initialized bool) error {
	fi, err := os.Stat(dir)
	switch {
	case err == nil:
		if !fi.IsDir() {
			return fmt.Errorf("%s %s is not a directory", label, dir)
		}
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("checking %s %s: %w", label, dir, err)
	case initialized:
		return fmt.Errorf("%w: %s is missing. If it is on a USB disk or NAS share, check that it is mounted", ErrWrongFolder, dir)
	}
	// Only create the folder itself. A missing parent usually means a disk or
	// share that isn't mounted, and creating it would write to the system disk.
	parent := filepath.Dir(dir)
	if _, err := os.Stat(parent); err != nil {
		return fmt.Errorf("can't create %s %s: %s is not available (is the disk or share mounted?): %w", label, dir, parent, err)
	}
	if err := os.Mkdir(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s %s: %w", label, dir, err)
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
			return fmt.Errorf("%s %s uses storage format %d, this version supports %d", s.label, s.path, m.Format, format)
		}
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("reading %s in %s %s: %w", markerName, s.label, s.path, err)
	case initialized:
		return fmt.Errorf("%w: %s has no %s marker. If it is on a USB disk or NAS share, check that it is mounted", ErrWrongFolder, s.path, markerName)
	}

	// First use: only claim a folder that holds nothing of value.
	entries, err := fs.ReadDir(s.root.FS(), ".")
	if err != nil {
		return fmt.Errorf("listing %s %s: %w", s.label, s.path, err)
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
		return fmt.Errorf("writing marker in %s %s: %w", s.label, s.path, err)
	}
	if err := s.root.Rename(tmp, markerName); err != nil {
		return fmt.Errorf("writing marker in %s %s: %w", s.label, s.path, err)
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
		return fmt.Errorf("%s %s failed a write test: %w", s.label, s.path, err)
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
