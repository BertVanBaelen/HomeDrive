package thumbs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// The cache holds, per version, in <state dir>/cache/thumbs/<last 2
// characters of the version>/ (the first ones of a UUIDv7 are a timestamp,
// the last ones random):
//
//	<version>-grid.webp, -list.webp, -preview.webp   images
//	<version>.json                                    a snippet
//	<version>.failed                                  nothing could be made
//	<version>-link.failed                             a saved link's image couldn't be fetched
//
// Work in progress happens in tmp/ and is renamed into place.
const (
	snippetSuffix    = ".json"
	failedSuffix     = ".failed"
	linkFailedSuffix = "-link.failed"
	// legacyLinkSuffix is how v2's saved links cached their provider's
	// image before this pipeline existed.
	legacyLinkSuffix = "-link"
)

func imageSuffix(size Size) string { return "-" + string(size) + ".webp" }

// linkRetry is how long a saved link whose image couldn't be fetched isn't
// asked for again; providers have bad moments.
const linkRetry = time.Hour

type cache struct{ dir string }

func newCache(stateDir string) cache {
	return cache{dir: filepath.Join(stateDir, "cache", "thumbs")}
}

// validVersion keeps cache paths to IDs Homedrive made.
func validVersion(id string) bool { return db.ValidID(id) }

// path is where a version's entry is. The version must be valid.
func (c cache) path(versionID, suffix string) string {
	if !validVersion(versionID) {
		return filepath.Join(c.dir, "invalid")
	}
	return filepath.Join(c.dir, versionID[len(versionID)-2:], versionID+suffix)
}

// legacyLink is where v2's saved links kept their provider's image before
// this pipeline: unresized, in a folder named after the first characters.
func (c cache) legacyLink(versionID string) string {
	if !validVersion(versionID) {
		return filepath.Join(c.dir, "invalid")
	}
	return filepath.Join(c.dir, versionID[:2], versionID+legacyLinkSuffix)
}

func (c cache) exists(versionID, suffix string) bool {
	return validVersion(versionID) && fileExists(c.path(versionID, suffix))
}

func (c cache) failed(versionID string) bool { return c.exists(versionID, failedSuffix) }

func (c cache) fail(versionID string) error { return c.mark(versionID, failedSuffix) }

func (c cache) linkFailed(versionID string) bool {
	info, err := os.Stat(c.path(versionID, linkFailedSuffix))
	return err == nil && time.Since(info.ModTime()) < linkRetry
}

func (c cache) mark(versionID, suffix string) error {
	if !validVersion(versionID) {
		return os.ErrInvalid
	}
	p := c.path(versionID, suffix)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, nil, 0o640)
}

// workDir makes a private folder for one job's temporary files.
func (c cache) workDir() (string, error) {
	tmp := filepath.Join(c.dir, "tmp")
	if err := os.MkdirAll(tmp, 0o750); err != nil {
		return "", err
	}
	return os.MkdirTemp(tmp, "job-")
}

// keep moves a finished file from a work folder into the cache.
func (c cache) keep(src, versionID, suffix string) error {
	dst := c.path(versionID, suffix)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// write stores bytes as a cache entry, replacing it in one step.
func (c cache) write(versionID, suffix string, b []byte) error {
	dir, err := c.workDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, "entry")
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return c.keep(tmp, versionID, suffix)
}

// Cleanup removes the entries of versions that are gone, saved links'
// failures that may be tried again, and work folders of jobs that were
// interrupted. It runs hourly with the other housekeeping.
func (s *Service) Cleanup(ctx context.Context) (int, error) {
	removed := 0
	tmp := filepath.Join(s.cache.dir, "tmp")
	if jobs, err := os.ReadDir(tmp); err == nil {
		for _, j := range jobs {
			if info, err := j.Info(); err == nil && time.Since(info.ModTime()) > jobTimeout*2 {
				if os.RemoveAll(filepath.Join(tmp, j.Name())) == nil {
					removed++
				}
			}
		}
	}
	dirs, err := os.ReadDir(s.cache.dir)
	if errors.Is(err, os.ErrNotExist) {
		return removed, nil
	}
	if err != nil {
		return removed, err
	}
	for _, d := range dirs {
		if !d.IsDir() || len(d.Name()) != 2 {
			continue
		}
		n, err := s.cleanDir(ctx, filepath.Join(s.cache.dir, d.Name()))
		removed += n
		if err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// cleanDir cleans one of the cache's two-character folders.
func (s *Service) cleanDir(ctx context.Context, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	byVersion := map[string][]string{}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		id := ""
		if len(name) >= idLength {
			id = name[:idLength]
		}
		if !validVersion(id) {
			// Not something the cache writes.
			continue
		}
		if _, ok := byVersion[id]; !ok {
			ids = append(ids, id)
		}
		byVersion[id] = append(byVersion[id], name)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	live, err := s.src.LiveVersions(ctx, ids)
	if err != nil {
		return 0, err
	}
	removed := 0
	for id, names := range byVersion {
		for _, name := range names {
			p := filepath.Join(dir, name)
			stale := !live[id]
			if !stale && name == id+linkFailedSuffix {
				if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) >= linkRetry {
					stale = true
				}
			}
			if stale && os.Remove(p) == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// idLength is the length of a version ID (a UUID).
const idLength = 36
