package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// Summary describes what an export wrote.
type Summary struct {
	Snapshot string
	Folders  int
	Files    int
	Bytes    int64
	// Missing lists files whose contents weren't in the backup, for
	// example files deleted longer ago than the retention period when an
	// old snapshot is exported.
	Missing []string
}

// Export rebuilds a normal folder tree in target from a backup folder,
// without a running Homedrive: one folder per drive, then the folders and
// files as they were at the snapshot. Items in the trash are left out.
// snapshot names a file in <backup>/db; empty means the newest.
func Export(backupDir, target, snapshot string) (Summary, error) {
	var sum Summary
	names, err := Snapshots(backupDir)
	if err != nil {
		return sum, fmt.Errorf("reading snapshots in %s: %w", backupDir, err)
	}
	if snapshot == "" {
		if len(names) == 0 {
			return sum, fmt.Errorf("no snapshots in %s", filepath.Join(backupDir, "db"))
		}
		snapshot = names[len(names)-1]
	}
	snapshot = filepath.Base(snapshot)
	sum.Snapshot = snapshot
	if err := emptyTarget(target); err != nil {
		return sum, err
	}

	// Work on a copy: opening the snapshot itself would modify it.
	tmpDir, err := os.MkdirTemp("", "homedrive-export-")
	if err != nil {
		return sum, err
	}
	defer os.RemoveAll(tmpDir)
	work := filepath.Join(tmpDir, "snapshot.db")
	if err := copyFile(filepath.Join(backupDir, "db", snapshot), work, time.Time{}); err != nil {
		return sum, fmt.Errorf("reading snapshot: %w", err)
	}
	ctx := context.Background()
	conn, err := db.Open(ctx, work)
	if err != nil {
		return sum, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `SELECT d.kind, d.name, coalesce(u.name, ''), d.root_id
		FROM drives d LEFT JOIN users u ON u.id = d.owner_user_id ORDER BY d.kind = 'personal', u.name`)
	if err != nil {
		return sum, err
	}
	type drive struct{ kind, name, owner, root string }
	var drives []drive
	for rows.Next() {
		var d drive
		if err := rows.Scan(&d.kind, &d.name, &d.owner, &d.root); err != nil {
			rows.Close()
			return sum, err
		}
		drives = append(drives, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return sum, err
	}

	e := exporter{conn: conn, objects: filepath.Join(backupDir, "objects"), sum: &sum}
	used := uniqueNames{}
	for _, d := range drives {
		label := d.name
		if d.kind == "personal" {
			label = "My Files - " + d.owner
		}
		dir := filepath.Join(target, used.take(safeName(label)))
		if err := e.folder(ctx, d.root, dir, label); err != nil {
			return sum, err
		}
	}
	return sum, nil
}

type exporter struct {
	conn    *sql.DB
	objects string
	sum     *Summary
}

func (e *exporter) folder(ctx context.Context, id, dir, display string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	e.sum.Folders++
	rows, err := e.conn.QueryContext(ctx, `SELECT n.id, n.kind, n.name, n.modified_at, coalesce(v.object_id, '')
		FROM nodes n LEFT JOIN versions v ON v.id = n.current_version_id
		WHERE n.parent_id = ? AND n.trashed_at IS NULL ORDER BY n.kind DESC, n.name_key`, id)
	if err != nil {
		return err
	}
	type child struct{ id, kind, name, modified, object string }
	var kids []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.id, &c.kind, &c.name, &c.modified, &c.object); err != nil {
			rows.Close()
			return err
		}
		kids = append(kids, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	used := uniqueNames{}
	for _, c := range kids {
		path := filepath.Join(dir, used.take(safeName(c.name)))
		shown := display + "/" + c.name
		if c.kind == "folder" {
			if err := e.folder(ctx, c.id, path, shown); err != nil {
				return err
			}
			continue
		}
		modified, _ := db.ParseTime(c.modified)
		src := filepath.Join(e.objects, objectDir(c.object), c.object)
		info, err := os.Stat(src)
		if c.object == "" || err != nil {
			e.sum.Missing = append(e.sum.Missing, shown)
			continue
		}
		if err := copyFile(src, path, modified); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		e.sum.Files++
		e.sum.Bytes += info.Size()
	}
	return nil
}

func objectDir(id string) string {
	if len(id) < 2 {
		return "_"
	}
	return id[:2]
}

// emptyTarget accepts a missing or empty folder only, so an export never
// mixes with or overwrites other files.
func emptyTarget(target string) error {
	entries, err := os.ReadDir(target)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(target, 0o750)
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("target %s is not empty", target)
	}
	return nil
}

func copyFile(src, dst string, modified time.Time) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && !modified.IsZero() {
		err = os.Chtimes(dst, modified, modified)
	}
	return err
}

// safeName makes a Homedrive name usable on any filesystem the export may
// go to (Windows, exFAT, SMB): no reserved characters or names, no
// trailing dots or spaces.
func safeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	s := strings.TrimRight(b.String(), ". ")
	if s == "" {
		s = "_"
	}
	base := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		s = "_" + s
	}
	return s
}

// uniqueNames hands out names unique within one folder, ignoring case, because
// target filesystems often ignore case where Homedrive's names may differ
// only by a character that safeName replaced.
type uniqueNames map[string]bool

func (n uniqueNames) take(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	candidate := name
	for i := 2; n[strings.ToLower(candidate)]; i++ {
		candidate = fmt.Sprintf("%s (%d)%s", base, i, ext)
	}
	n[strings.ToLower(candidate)] = true
	return candidate
}
