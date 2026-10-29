package files

import (
	"archive/zip"
	"context"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// Download as .zip (docs/architecture.md §4). The zip is streamed without
// compression: photos and documents are mostly compressed already, and a
// small server stays responsive.

// Archive is a folder to download as a .zip.
type Archive struct {
	// Name is the download's file name.
	Name string
	root string
}

// Archive prepares a folder the user can open for download.
func (s *Service) Archive(ctx context.Context, userID, nodeID string) (Archive, error) {
	it, err := s.lookup(ctx, s.db, userID, nodeID, false)
	if err != nil {
		return Archive{}, err
	}
	if !it.IsFolder() {
		return Archive{}, ErrNotFolder
	}
	name := it.Name
	if name == "" {
		name = it.drive.Name // a drive's top folder
	}
	return Archive{Name: name + ".zip", root: it.ID}, nil
}

// LinkArchive prepares a folder on a link page for download.
func (s *Service) LinkArchive(ctx context.Context, v Visit, nodeID string) (Archive, error) {
	n, _, err := s.linked(ctx, v, nodeID)
	if err != nil {
		return Archive{}, err
	}
	if !n.IsFolder() {
		return Archive{}, ErrNotFolder
	}
	return Archive{Name: n.Name + ".zip", root: n.ID}, nil
}

type zipEntry struct {
	id, parent, name, object string
	folder                   bool
	modified                 time.Time
	path                     string
}

// WriteZip writes what is in the folder, leaving out the trash, to w.
// Entry names are relative to the folder; empty folders are included.
func (s *Service) WriteZip(ctx context.Context, w io.Writer, a Archive) error {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE t(id, parent_id, kind, name, modified_at, version_id) AS (
			SELECT id, parent_id, kind, name, modified_at, current_version_id
			FROM nodes WHERE parent_id = ? AND trashed_at IS NULL
			UNION ALL
			SELECT n.id, n.parent_id, n.kind, n.name, n.modified_at, n.current_version_id
			FROM nodes n JOIN t ON n.parent_id = t.id WHERE n.trashed_at IS NULL
		)
		SELECT t.id, t.parent_id, t.kind = 'folder', t.name, t.modified_at, coalesce(v.object_id, '')
		FROM t LEFT JOIN versions v ON v.id = t.version_id`, a.root)
	if err != nil {
		return err
	}
	byID := map[string]*zipEntry{}
	var entries []*zipEntry
	for rows.Next() {
		var e zipEntry
		var modified string
		if err := rows.Scan(&e.id, &e.parent, &e.folder, &e.name, &modified, &e.object); err != nil {
			rows.Close()
			return err
		}
		e.modified, _ = db.ParseTime(modified)
		byID[e.id] = &e
		entries = append(entries, &e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var pathOf func(e *zipEntry) string
	pathOf = func(e *zipEntry) string {
		if e.path == "" {
			e.path = zipSegment(e.name)
			if p, ok := byID[e.parent]; ok {
				e.path = pathOf(p) + "/" + e.path
			}
		}
		return e.path
	}
	for _, e := range entries {
		pathOf(e)
	}
	slices.SortFunc(entries, func(x, y *zipEntry) int { return strings.Compare(x.path, y.path) })

	zw := zip.NewWriter(w)
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		fh := &zip.FileHeader{Name: e.path, Method: zip.Store, Modified: e.modified.In(time.Local)}
		if e.folder {
			fh.Name += "/"
			if _, err := zw.CreateHeader(fh); err != nil {
				return err
			}
			continue
		}
		if e.object == "" {
			continue
		}
		if err := s.addToZip(zw, fh, e.object); err != nil {
			return err
		}
	}
	return zw.Close()
}

func (s *Service) addToZip(zw *zip.Writer, fh *zip.FileHeader, objectID string) error {
	f, err := s.store.OpenObject(objectID)
	if err != nil {
		return err
	}
	defer f.Close()
	fw, err := zw.CreateHeader(fh)
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, f)
	return err
}

// zipSegment makes a name safe as one step of an entry's path, so nothing
// can be extracted outside the folder it is unpacked into. Names from the
// tree already are (see ValidateName); this holds even if one isn't.
func zipSegment(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == utf8.RuneError || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	return name
}
