package files

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

// Categories split My Files on the Storage page, in the order of its bar.
var Categories = []string{"documents", "spreadsheets", "pdfs", "images", "video", "audio", "archives", "other"}

// category is the Storage page's category for a file name. Code files and
// unknown extensions are "other".
func category(name string) string {
	t, _ := lookupType(name)
	switch t.kind {
	case "doc", "text":
		return "documents"
	case "sheet":
		return "spreadsheets"
	case "pdf":
		return "pdfs"
	case "image":
		return "images"
	case "video", "audio":
		return t.kind
	case "archive":
		return "archives"
	}
	return "other"
}

// Usage is what the files of a drive take on the data disk, in bytes.
type Usage struct {
	// Files are the current contents of files that aren't in the trash.
	Files int64 `json:"files"`
	// Trash is what is in the trash, directly or inside a trashed folder.
	Trash int64 `json:"trash"`
	// OldVersions are contents that newer ones replaced.
	OldVersions int64 `json:"oldVersions"`
}

func (u Usage) total() int64 { return u.Files + u.Trash + u.OldVersions }

// Drive filters: SQL over drives d, with the user as ?1.
const (
	ownDrive = `d.owner_user_id = ?1`
	// editableDrives are the drives where the user may delete for good,
	// as for emptying the trash.
	editableDrives = `(d.owner_user_id = ?1 OR d.id IN (
		SELECT drive_id FROM drive_members WHERE user_id = ?1 AND role IN ('editor', 'manager')))`
	allDrives = `1`
)

// liveTree starts a query with the CTE live(id, drive_id): the nodes on
// the drives that filter selects, without what is in the trash, directly
// or through a folder.
func liveTree(filter string) string {
	return `WITH RECURSIVE live(id, drive_id) AS (
		SELECT d.root_id, d.id FROM drives d WHERE ` + filter + `
		UNION ALL
		SELECT n.id, live.drive_id FROM nodes n JOIN live ON n.parent_id = live.id
		WHERE n.trashed_at IS NULL
	) `
}

// usage measures the drives that filter selects. Drives without files
// are left out.
func (s *Service) usage(ctx context.Context, filter string, args ...any) (map[string]Usage, error) {
	found := map[string]*Usage{}
	get := func(id string) *Usage {
		if found[id] == nil {
			found[id] = &Usage{}
		}
		return found[id]
	}
	for _, q := range []struct {
		query string
		set   func(u *Usage, n int64)
	}{
		{liveTree(filter) + `SELECT live.drive_id, sum(n.size) FROM live JOIN nodes n ON n.id = live.id
			WHERE n.kind = 'file' GROUP BY live.drive_id`,
			func(u *Usage, n int64) { u.Files = n }},
		// Every file, in the trash or not: the live ones are taken off
		// below. Counting this way, what is inside a trashed folder counts
		// once, however many folders above it are trashed too.
		{`SELECT n.drive_id, sum(n.size) FROM nodes n JOIN drives d ON d.id = n.drive_id
			WHERE n.kind = 'file' AND ` + filter + ` GROUP BY n.drive_id`,
			func(u *Usage, n int64) { u.Trash = n }},
		{`SELECT n.drive_id, sum(v.size) FROM versions v
			JOIN nodes n ON n.id = v.node_id JOIN drives d ON d.id = n.drive_id
			WHERE v.superseded_at IS NOT NULL AND ` + filter + ` GROUP BY n.drive_id`,
			func(u *Usage, n int64) { u.OldVersions = n }},
	} {
		err := s.sums(ctx, q.query, args, func(id string, n int64) { q.set(get(id), n) })
		if err != nil {
			return nil, err
		}
	}
	out := make(map[string]Usage, len(found))
	for id, u := range found {
		u.Trash -= u.Files
		out[id] = *u
	}
	return out, nil
}

// sums runs a query for (id, number) rows.
func (s *Service) sums(ctx context.Context, query string, args []any, each func(id string, n int64)) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return err
		}
		each(id, n)
	}
	return rows.Err()
}

// largestCount is how many files the Storage page lists as the largest.
const largestCount = 10

// Storage is a user's Storage page.
type Storage struct {
	MyFiles MyFilesUsage `json:"myFiles"`
	// Largest are the largest files on the drives the user can edit, not
	// in the trash.
	Largest []Located `json:"largest"`
	// Trash and OldVersions are on every drive the user can edit: what
	// emptying the trash and deleting old versions would free.
	Trash       int64 `json:"trash"`
	OldVersions int64 `json:"oldVersions"`
}

// MyFilesUsage is the user's own drive, with its files by category.
type MyFilesUsage struct {
	Usage
	Types map[string]int64 `json:"types"`
}

// Storage gives the user's Storage page.
func (s *Service) Storage(ctx context.Context, userID string) (Storage, error) {
	st := Storage{MyFiles: MyFilesUsage{Types: map[string]int64{}}}
	use, err := s.usage(ctx, editableDrives, userID)
	if err != nil {
		return Storage{}, err
	}
	var own string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM drives WHERE owner_user_id = ?`, userID).Scan(&own)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Storage{}, err
	}
	st.MyFiles.Usage = use[own]
	for _, u := range use {
		st.Trash += u.Trash
		st.OldVersions += u.OldVersions
	}

	for _, c := range Categories {
		st.MyFiles.Types[c] = 0
	}
	rows, err := s.db.QueryContext(ctx, liveTree(ownDrive)+`
		SELECT n.name, n.size FROM live JOIN nodes n ON n.id = live.id
		WHERE n.kind = 'file' AND n.size > 0`, userID)
	if err != nil {
		return Storage{}, err
	}
	for rows.Next() {
		var name string
		var size int64
		if err := rows.Scan(&name, &size); err != nil {
			rows.Close()
			return Storage{}, err
		}
		st.MyFiles.Types[category(name)] += size
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Storage{}, err
	}

	var ids []string
	err = s.sums(ctx, liveTree(editableDrives)+`
		SELECT n.id, n.size FROM live JOIN nodes n ON n.id = live.id
		WHERE n.kind = 'file' AND n.size > 0
		ORDER BY n.size DESC, n.name_key LIMIT ?2`, []any{userID, largestCount},
		func(id string, _ int64) { ids = append(ids, id) })
	if err != nil {
		return Storage{}, err
	}
	if st.Largest, err = s.located(ctx, userID, ids, largestCount, false); err != nil {
		return Storage{}, err
	}
	return st, nil
}

// MyFilesSize is what the user's own files take, without the trash and
// old versions: the storage meter.
func (s *Service) MyFilesSize(ctx context.Context, userID string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, liveTree(ownDrive)+`
		SELECT coalesce(sum(n.size), 0) FROM live JOIN nodes n ON n.id = live.id
		WHERE n.kind = 'file'`, userID).Scan(&n)
	return n, err
}

// Disk is the disk under the data folder, in bytes.
type Disk struct {
	Total uint64 `json:"total"`
	// Free is what Homedrive can still write.
	Free uint64 `json:"free"`
	Used uint64 `json:"used"`
	// Homedrive is what all versions of all files take, trash included.
	Homedrive int64 `json:"homedrive"`
	// Reserve is kept free: uploads that would eat into it are refused.
	Reserve int64 `json:"reserve"`
}

// Disk reads the data disk. It is nil when the system can't tell.
func (s *Service) Disk(ctx context.Context) (*Disk, error) {
	total, free, err := s.store.DiskUsage()
	if err != nil {
		s.log.Warn("reading data disk usage", "error", err)
		return nil, nil
	}
	d := &Disk{Total: total, Free: free, Used: total - min(free, total), Reserve: diskReserve}
	if err := s.db.QueryRowContext(ctx, `SELECT coalesce(sum(size), 0) FROM versions`).Scan(&d.Homedrive); err != nil {
		return nil, err
	}
	return d, nil
}

// HouseholdStorage is the admins' part of the Storage page. It has sizes
// only: admins don't see anyone's file names this way.
type HouseholdStorage struct {
	Disk   *Disk         `json:"disk"`
	People []PersonUsage `json:"people"`
	Drives []DriveUsage  `json:"drives"`
}

// PersonUsage is someone's My Files.
type PersonUsage struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Disabled bool   `json:"disabled,omitempty"`
	Usage
}

// DriveUsage is a shared drive.
type DriveUsage struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Members int    `json:"members"`
	Usage
}

// HouseholdStorage measures the data disk, everyone's My Files and every
// shared drive, the largest first.
func (s *Service) HouseholdStorage(ctx context.Context) (HouseholdStorage, error) {
	h := HouseholdStorage{People: []PersonUsage{}, Drives: []DriveUsage{}}
	var err error
	if h.Disk, err = s.Disk(ctx); err != nil {
		return HouseholdStorage{}, err
	}
	use, err := s.usage(ctx, allDrives)
	if err != nil {
		return HouseholdStorage{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, coalesce(u.id, ''), coalesce(u.name, d.name), u.disabled_at IS NOT NULL,
			(SELECT count(*) FROM drive_members m WHERE m.drive_id = d.id)
		FROM drives d LEFT JOIN users u ON u.id = d.owner_user_id`)
	if err != nil {
		return HouseholdStorage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var driveID, userID, name string
		var disabled bool
		var members int
		if err := rows.Scan(&driveID, &userID, &name, &disabled, &members); err != nil {
			return HouseholdStorage{}, err
		}
		if userID != "" {
			h.People = append(h.People, PersonUsage{ID: userID, Name: name, Disabled: disabled, Usage: use[driveID]})
		} else {
			h.Drives = append(h.Drives, DriveUsage{ID: driveID, Name: name, Members: members, Usage: use[driveID]})
		}
	}
	if err := rows.Err(); err != nil {
		return HouseholdStorage{}, err
	}
	slices.SortFunc(h.People, func(a, b PersonUsage) int {
		return largestFirst(a.Usage, b.Usage, a.Name, b.Name)
	})
	slices.SortFunc(h.Drives, func(a, b DriveUsage) int {
		return largestFirst(a.Usage, b.Usage, a.Name, b.Name)
	})
	return h, nil
}

func largestFirst(a, b Usage, aName, bName string) int {
	return cmp.Or(cmp.Compare(b.total(), a.total()), cmp.Compare(strings.ToLower(aName), strings.ToLower(bName)))
}

// DeleteOldVersions deletes, on the drives the user can edit, the
// contents that newer ones replaced. It returns how many versions went
// and the bytes that freed. The backup keeps them for the retention
// period.
func (s *Service) DeleteOldVersions(ctx context.Context, userID string) (int, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT v.id, v.object_id, v.size FROM versions v
		JOIN nodes n ON n.id = v.node_id JOIN drives d ON d.id = n.drive_id
		WHERE v.superseded_at IS NOT NULL AND `+editableDrives, userID)
	if err != nil {
		return 0, 0, err
	}
	var ids, objects []string
	var freed int64
	for rows.Next() {
		var id, object string
		var size int64
		if err := rows.Scan(&id, &object, &size); err != nil {
			rows.Close()
			return 0, 0, err
		}
		ids, objects, freed = append(ids, id), append(objects, object), freed+size
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM versions WHERE id IN (SELECT value FROM json_each(?))`, string(list)); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	s.removeObjects(objects)
	return len(ids), freed, nil
}
