package files

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// ProvisionUser gives a new user their personal drive and membership of the
// family drive, creating that on first use. It runs in the caller's
// transaction.
func ProvisionUser(ctx context.Context, tx *sql.Tx, userID string, now time.Time) error {
	if _, err := createDrive(ctx, tx, "personal", "My Files", userID, now); err != nil {
		return err
	}
	var family string
	err := tx.QueryRowContext(ctx, `SELECT id FROM drives WHERE kind = 'shared' ORDER BY created_at LIMIT 1`).Scan(&family)
	if err == sql.ErrNoRows {
		family, err = createDrive(ctx, tx, "shared", "Family", "", now)
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO drive_members (drive_id, user_id, role) VALUES (?, ?, 'editor')`, family, userID)
	return err
}

func createDrive(ctx context.Context, tx *sql.Tx, kind, name, ownerID string, now time.Time) (string, error) {
	id, root, ts := db.NewID(), db.NewID(), db.FormatTime(now)
	var owner any
	if ownerID != "" {
		owner = ownerID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO drives (id, kind, name, owner_user_id, root_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, kind, name, owner, root, ts); err != nil {
		return "", err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO nodes (id, drive_id, parent_id, kind, name, name_key, created_by, created_at, modified_at)
		 VALUES (?, ?, NULL, 'folder', '', '', ?, ?, ?)`, root, id, owner, ts, ts)
	return id, err
}

type SortKey string

const (
	SortName     SortKey = "name"
	SortModified SortKey = "modified"
	SortSize     SortKey = "size"
)

// Children lists a live folder's contents, folders first.
func (s *Service) Children(ctx context.Context, userID, folderID string, sortBy SortKey, desc bool) ([]Node, error) {
	it, err := s.lookup(ctx, s.db, userID, folderID, false)
	if err != nil {
		return nil, err
	}
	if !it.IsFolder() {
		return nil, ErrNotFolder
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeColumns+`,
		CASE n.kind WHEN 'folder' THEN (SELECT count(*) FROM nodes c WHERE c.parent_id = n.id AND c.trashed_at IS NULL) END
		FROM nodes n WHERE n.parent_id = ? AND n.trashed_at IS NULL`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		var count sql.NullInt64
		n, err := scanNode(rows, &count)
		if err != nil {
			return nil, err
		}
		if count.Valid {
			c := int(count.Int64)
			n.ChildCount = &c
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	SortNodes(out, sortBy, desc)
	if err := s.markStars(ctx, userID, out); err != nil {
		return nil, err
	}
	return out, s.addPreviews(ctx, out)
}

// SortNodes orders folders before files, then by the key. Names compare
// naturally ("IMG 2" before "IMG 10") and ignoring case.
func SortNodes(nodes []Node, by SortKey, desc bool) {
	slices.SortStableFunc(nodes, func(a, b Node) int {
		if a.IsFolder() != b.IsFolder() {
			if a.IsFolder() {
				return -1
			}
			return 1
		}
		var c int
		switch by {
		case SortModified:
			c = a.ModifiedAt.Compare(b.ModifiedAt)
		case SortSize:
			c = cmp.Compare(a.Size, b.Size)
		}
		if c == 0 {
			c = naturalCompare(a.Name, b.Name)
		}
		if desc {
			c = -c
		}
		return c
	})
}

func naturalCompare(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		ra, sizeA := utf8.DecodeRuneInString(a)
		rb, sizeB := utf8.DecodeRuneInString(b)
		if unicode.IsDigit(ra) && unicode.IsDigit(rb) {
			na, restA := leadingDigits(a)
			nb, restB := leadingDigits(b)
			// Compare by value: strip zeros, then length, then digits.
			ta, tb := strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if c := cmp.Compare(len(ta), len(tb)); c != 0 {
				return c
			}
			if c := strings.Compare(ta, tb); c != 0 {
				return c
			}
			a, b = restA, restB
			continue
		}
		if ra != rb {
			return cmp.Compare(ra, rb)
		}
		a, b = a[sizeA:], b[sizeB:]
	}
	return cmp.Compare(len(a), len(b))
}

func leadingDigits(s string) (digits, rest string) {
	i := strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) })
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i:]
}

// editableFolder loads a live folder the user may add to.
func (s *Service) editableFolder(ctx context.Context, q querier, userID, folderID string) (item, error) {
	it, err := s.lookup(ctx, q, userID, folderID, false)
	if err != nil {
		return item{}, err
	}
	if !it.IsFolder() {
		return item{}, ErrNotFolder
	}
	if !it.drive.CanEdit() {
		return item{}, ErrReadOnly
	}
	return it, nil
}

// editableItem loads a live, non-root node the user may change.
func (s *Service) editableItem(ctx context.Context, q querier, userID, nodeID string) (item, error) {
	it, err := s.lookup(ctx, q, userID, nodeID, false)
	if err != nil {
		return item{}, err
	}
	if it.isRoot() {
		return item{}, ErrRoot
	}
	if !it.drive.CanEdit() {
		return item{}, ErrReadOnly
	}
	return it, nil
}

func (s *Service) CreateFolder(ctx context.Context, userID, parentID, name string) (Node, error) {
	name, err := ValidateName(name)
	if err != nil {
		return Node{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	parent, err := s.editableFolder(ctx, tx, userID, parentID)
	if err != nil {
		return Node{}, err
	}
	id, ts := db.NewID(), s.stamp()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO nodes (id, drive_id, parent_id, kind, name, name_key, created_by, created_at, modified_at)
		 VALUES (?, ?, ?, 'folder', ?, ?, ?, ?, ?)`,
		id, parent.DriveID, parent.ID, name, nameKey(name), userID, ts, ts)
	if db.IsUniqueViolation(err) {
		return Node{}, ErrNameTaken
	}
	if err != nil {
		return Node{}, err
	}
	if err := s.touch(ctx, tx, parent.ID); err != nil {
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, err
	}
	return s.node(ctx, userID, id)
}

// touch marks a folder as modified because its contents changed.
func (s *Service) touch(ctx context.Context, q querier, folderID string) error {
	_, err := q.ExecContext(ctx, `UPDATE nodes SET modified_at = ? WHERE id = ?`, s.stamp(), folderID)
	return err
}

// node reads a node without access checks, for returning results of
// operations that already checked.
func (s *Service) node(ctx context.Context, userID, id string) (Node, error) {
	n, err := scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes n WHERE n.id = ?`, id))
	if err != nil {
		return Node{}, err
	}
	if n.IsFolder() {
		c, err := s.childCount(ctx, s.db, id)
		if err != nil {
			return Node{}, err
		}
		n.ChildCount = &c
	}
	nodes := []Node{n}
	if err := s.markStars(ctx, userID, nodes); err != nil {
		return Node{}, err
	}
	err = s.addPreviews(ctx, nodes)
	return nodes[0], err
}

func (s *Service) Rename(ctx context.Context, userID, nodeID, name string) (Node, error) {
	name, err := ValidateName(name)
	if err != nil {
		return Node{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	it, err := s.editableItem(ctx, tx, userID, nodeID)
	if err != nil {
		return Node{}, err
	}
	if name != it.Name {
		_, err = tx.ExecContext(ctx, `UPDATE nodes SET name = ?, name_key = ?, modified_at = ?,
			mime = CASE kind WHEN 'file' THEN ? ELSE mime END WHERE id = ?`,
			name, nameKey(name), s.stamp(), MimeOf(name), it.ID)
		if db.IsUniqueViolation(err) {
			return Node{}, ErrNameTaken
		}
		if err != nil {
			return Node{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Node{}, err
	}
	return s.node(ctx, userID, it.ID)
}

// Move puts a node into another folder, possibly on another drive the user
// can edit. A folder can't move into itself or its own subfolders.
func (s *Service) Move(ctx context.Context, userID, nodeID, targetID string) (Node, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	it, err := s.editableItem(ctx, tx, userID, nodeID)
	if err != nil {
		return Node{}, err
	}
	target, err := s.editableFolder(ctx, tx, userID, targetID)
	if err != nil {
		return Node{}, err
	}
	if target.ID == it.ParentID {
		return it.Node, nil
	}
	if target.ID == it.ID || slices.ContainsFunc(target.path, func(c Crumb) bool { return c.ID == it.ID }) {
		return Node{}, ErrInvalidMove
	}
	if target.DriveID != it.DriveID {
		if _, err := tx.ExecContext(ctx, `
			WITH RECURSIVE sub(id) AS (
				SELECT ? UNION ALL SELECT n.id FROM nodes n JOIN sub ON n.parent_id = sub.id
			)
			UPDATE nodes SET drive_id = ? WHERE id IN (SELECT id FROM sub)`, it.ID, target.DriveID); err != nil {
			return Node{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE nodes SET parent_id = ? WHERE id = ?`, target.ID, it.ID)
	if db.IsUniqueViolation(err) {
		return Node{}, ErrNameTaken
	}
	if err != nil {
		return Node{}, err
	}
	if err := s.touch(ctx, tx, it.ParentID); err != nil {
		return Node{}, err
	}
	if err := s.touch(ctx, tx, target.ID); err != nil {
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, err
	}
	return s.node(ctx, userID, it.ID)
}

// Trash moves a node to the trash. A folder's contents go with it without
// being marked themselves, so restoring the folder brings them back as they
// were.
func (s *Service) Trash(ctx context.Context, userID, nodeID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	it, err := s.editableItem(ctx, tx, userID, nodeID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET trashed_at = ?, trashed_by = ? WHERE id = ?`,
		s.stamp(), userID, it.ID); err != nil {
		return err
	}
	if err := s.touch(ctx, tx, it.ParentID); err != nil {
		return err
	}
	return tx.Commit()
}

// TrashList lists what was put in the trash on the drives the user can see,
// most recent first.
func (s *Service) TrashList(ctx context.Context, userID string) ([]Located, error) {
	return s.locate(ctx, userID, `n.trashed_at IS NOT NULL`, nil, `n.trashed_at DESC`, 1000, true)
}

// Restore takes a node out of the trash. If its folder is gone (in the
// trash itself), it goes to the top of its drive; if its name is taken by
// now, it gets a number added.
func (s *Service) Restore(ctx context.Context, userID, nodeID string) (Node, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	it, err := s.lookup(ctx, tx, userID, nodeID, true)
	if err != nil {
		return Node{}, err
	}
	if it.TrashedAt == nil {
		return Node{}, ErrNotFound
	}
	if !it.drive.CanEdit() {
		return Node{}, ErrReadOnly
	}
	parent := it.ParentID
	if _, err := s.lookup(ctx, tx, userID, parent, false); err != nil {
		parent = it.drive.RootID
	}
	name, err := uniqueName(ctx, tx, parent, it.Name)
	if err != nil {
		return Node{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE nodes SET trashed_at = NULL, trashed_by = NULL, parent_id = ?, name = ?, name_key = ? WHERE id = ?`,
		parent, name, nameKey(name), it.ID); err != nil {
		return Node{}, err
	}
	if err := s.touch(ctx, tx, parent); err != nil {
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, err
	}
	return s.node(ctx, userID, it.ID)
}

// uniqueName returns name, or "name (2).ext", "name (3).ext"… if it is
// taken in the folder.
func uniqueName(ctx context.Context, q querier, parentID, name string) (string, error) {
	ext := path.Ext(name)
	if ext == name {
		ext = "" // ".bashrc" has no extension
	}
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		var taken bool
		if err := q.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM nodes WHERE parent_id = ? AND name_key = ? AND trashed_at IS NULL)`,
			parentID, nameKey(candidate)).Scan(&taken); err != nil {
			return "", err
		}
		if !taken && len(candidate) <= 255 {
			return candidate, nil
		}
	}
	return "", ErrNameTaken
}
