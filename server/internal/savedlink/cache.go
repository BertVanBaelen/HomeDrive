package savedlink

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// retryAfter is how long a thumbnail that couldn't be fetched isn't asked
// for again.
const retryAfter = time.Hour

// Thumbs caches link thumbnails in <state dir>/cache/thumbs, by the
// version of the .url file, like the thumbnails of other files. The cache
// isn't backed up and may be deleted at any time.
type Thumbs struct{ dir string }

func NewThumbs(stateDir string) *Thumbs {
	return &Thumbs{dir: filepath.Join(stateDir, "cache", "thumbs")}
}

func (c *Thumbs) path(versionID, suffix string) (string, error) {
	if !db.ValidID(versionID) {
		return "", os.ErrNotExist
	}
	return filepath.Join(c.dir, versionID[:2], versionID+"-link"+suffix), nil
}

// Get returns a cached thumbnail.
func (c *Thumbs) Get(versionID string) ([]byte, error) {
	p, err := c.path(versionID, "")
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// Put stores a thumbnail, replacing the file in one step.
func (c *Thumbs) Put(versionID string, b []byte) error {
	p, err := c.path(versionID, "")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	os.Remove(p + ".failed")
	return os.Rename(tmp, p)
}

// Fail remembers that a version's thumbnail couldn't be fetched.
func (c *Thumbs) Fail(versionID string) error {
	p, err := c.path(versionID, ".failed")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, nil, 0o640)
}

// Failed reports whether fetching a version's thumbnail failed recently.
func (c *Thumbs) Failed(versionID string) bool {
	p, err := c.path(versionID, ".failed")
	if err != nil {
		return true
	}
	info, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) || err == nil && time.Since(info.ModTime()) > retryAfter {
		return false
	}
	return true
}
