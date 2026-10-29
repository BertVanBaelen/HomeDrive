// Package files is the drive itself: drives, the folder tree, uploads,
// file contents, sharing and public links. Every operation takes the
// acting user's ID and checks access through drive membership and what was
// shared with them; a valid ID alone never grants anything. Visitors of a
// public link reach only the linked item and what is in it.
package files

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
	"github.com/BertVanBaelen/HomeDrive/server/internal/storage"
)

var (
	// ErrNotFound also covers items the user may not see, so that IDs of
	// other people's files can't be probed.
	ErrNotFound     = errors.New("not found")
	ErrReadOnly     = errors.New("no permission to change this drive")
	ErrInvalidName  = errors.New("invalid name")
	ErrNameTaken    = errors.New("name already used in this folder")
	ErrNotFolder    = errors.New("not a folder")
	ErrInvalidMove  = errors.New("can't move a folder into itself")
	ErrRoot         = errors.New("a drive's top folder can't be changed")
	ErrTooLarge     = errors.New("file too large")
	ErrBadOffset    = errors.New("upload offset mismatch")
	ErrIncomplete   = errors.New("upload incomplete")
	ErrUploadFailed = errors.New("upload failed")
	// ErrSharedItem is for changing the item that was shared with you
	// itself: only people in its drive may rename, move or trash it.
	ErrSharedItem = errors.New("only the drive's people can change a shared item itself")
	// ErrMoveOut is for moving something out of a folder shared with you.
	ErrMoveOut = errors.New("items in a shared folder can only move within it")
)

type Service struct {
	db    *sql.DB
	store *storage.Store
	log   *slog.Logger
	now   func() time.Time

	uploadMu    sync.Mutex
	uploadLocks map[string]*sync.Mutex
}

func New(conn *sql.DB, store *storage.Store, log *slog.Logger) *Service {
	return &Service{db: conn, store: store, log: log, now: time.Now, uploadLocks: map[string]*sync.Mutex{}}
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Drive is a drive with the user's role in it. For someone who reaches an
// item only through a share, it is the shared item instead: kind "share",
// named after it, with that item as its top, so nothing above it shows.
type Drive struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // personal | shared | share
	Name   string `json:"name"`
	RootID string `json:"rootId"`
	Role   string `json:"role"` // viewer | editor | manager
	// SharedBy is who shared the item, for kind "share".
	SharedBy string `json:"sharedBy,omitempty"`
	// CanShare is set on an item's drive when the user may share the item
	// and delete it forever: they can edit it on its drive, not only
	// through a share.
	CanShare bool `json:"canShare,omitempty"`
}

func (d Drive) CanEdit() bool { return d.Role == "editor" || d.Role == "manager" }

type Node struct {
	ID         string     `json:"id"`
	DriveID    string     `json:"driveId"`
	ParentID   string     `json:"parentId,omitempty"`
	Kind       string     `json:"kind"` // folder | file
	Type       string     `json:"type"` // folder, text, sheet, doc, pdf, image, video, audio, archive, code
	Name       string     `json:"name"`
	Size       int64      `json:"size"`
	Mime       string     `json:"mime,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ModifiedAt time.Time  `json:"modifiedAt"`
	TrashedAt  *time.Time `json:"trashedAt,omitempty"`
	ChildCount *int       `json:"childCount,omitempty"`
	// Starred is per user: whether the user asking starred it.
	Starred bool `json:"starred"`
	// Shared is set for drive members when the item is shared with people
	// or has a link.
	Shared bool `json:"shared,omitempty"`
	// Preview is a folder's first few items, for its thumbnail.
	Preview []Mini `json:"preview,omitempty"`

	versionID string
}

// Mini is an item shown inside a folder's thumbnail.
type Mini struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Type string `json:"type"`
	Name string `json:"name"`
}

// Located is a node listed outside its folder (trash, starred, recent,
// search results), with where it lives.
type Located struct {
	Node
	Drive Drive   `json:"drive"`
	Path  []Crumb `json:"path"`
}

func (n Node) IsFolder() bool { return n.Kind == "folder" }

// Crumb is one step of the path from a drive's top folder to an item.
type Crumb struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

const nodeColumns = `n.id, n.drive_id, coalesce(n.parent_id, ''), n.kind, n.name, n.size, n.mime,
	n.created_at, n.modified_at, n.trashed_at, coalesce(n.current_version_id, '')`

func scanNode(row interface{ Scan(...any) error }, extra ...any) (Node, error) {
	var n Node
	var created, modified string
	var trashed sql.NullString
	dest := append([]any{&n.ID, &n.DriveID, &n.ParentID, &n.Kind, &n.Name, &n.Size, &n.Mime,
		&created, &modified, &trashed, &n.versionID}, extra...)
	if err := row.Scan(dest...); err != nil {
		return Node{}, err
	}
	n.CreatedAt, _ = db.ParseTime(created)
	n.ModifiedAt, _ = db.ParseTime(modified)
	if trashed.Valid {
		t, _ := db.ParseTime(trashed.String)
		n.TrashedAt = &t
	}
	n.Type = TypeOf(n)
	return n, nil
}

// Drives lists the drives the user can open: their own first, then shared.
func (s *Service) Drives(ctx context.Context, userID string) ([]Drive, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.kind, d.name, d.root_id, CASE WHEN d.owner_user_id = ?1 THEN 'manager' ELSE m.role END
		FROM drives d LEFT JOIN drive_members m ON m.drive_id = d.id AND m.user_id = ?1
		WHERE d.owner_user_id = ?1 OR m.user_id IS NOT NULL
		ORDER BY d.kind = 'shared', lower(d.name), d.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Drive
	for rows.Next() {
		var d Drive
		if err := rows.Scan(&d.ID, &d.Kind, &d.Name, &d.RootID, &d.Role); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// drive returns a drive with the user's role in it, or ErrNotFound.
func (s *Service) drive(ctx context.Context, q querier, userID, driveID string) (Drive, error) {
	var d Drive
	var role sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT d.id, d.kind, d.name, d.root_id, CASE WHEN d.owner_user_id = ?1 THEN 'manager' ELSE m.role END
		FROM drives d LEFT JOIN drive_members m ON m.drive_id = d.id AND m.user_id = ?1
		WHERE d.id = ?2`, userID, driveID).Scan(&d.ID, &d.Kind, &d.Name, &d.RootID, &role)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !role.Valid {
		return Drive{}, ErrNotFound
	}
	d.Role = role.String
	return d, err
}

// item is a node the user may see, with its drive and the path to it.
type item struct {
	Node
	drive Drive
	path  []Crumb // from the top (drive or shared item) down to the parent
	// live is false when the node or one of its ancestors is in the trash.
	live bool
	// member is set when the user is in the node's drive, and driveEdit
	// when they may change it as an editor or manager there.
	member, driveEdit bool
	// editTop is where the user's right to change things starts: the
	// drive's top folder for its editors, else the highest item shared
	// with them to edit, else empty.
	editTop string
	// above holds the IDs of all the node's folders, the drive's top first.
	above []string
}

// isRoot reports whether the node is the top of what the user sees: a
// drive's top folder, or the item shared with them.
func (it item) isRoot() bool { return it.ID == it.drive.RootID }

// canEdit reports whether the user may change the node's contents: add to
// a folder, or save a file.
func (it item) canEdit() bool { return it.editTop != "" }

// within reports whether the node is id or inside it.
func (it item) within(id string) bool { return it.ID == id || slices.Contains(it.above, id) }

// changeable checks that the user may rename, move or trash the node
// itself, which takes the right to change the folder it is in.
func (it item) changeable() error {
	switch {
	case it.member && it.isRoot():
		return ErrRoot
	case it.editTop == "":
		return ErrReadOnly
	case it.editTop == it.ID:
		return ErrSharedItem
	}
	return nil
}

var roleRank = map[string]int{"": 0, "viewer": 1, "editor": 2, "manager": 3}

// lookup loads a node with access checks. Unless trashedOK, items that are
// in the trash (themselves or through a folder) are reported as missing.
//
// Access is the user's role in the drive, raised by what was shared with
// them: the node itself or a folder it is in. A share counts only while
// the shared item and its folders are out of the trash, and only for
// enabled users.
func (s *Service) lookup(ctx context.Context, q querier, userID, nodeID string, trashedOK bool) (item, error) {
	if !db.ValidID(nodeID) {
		return item{}, ErrNotFound
	}
	n, err := scanNode(q.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes n WHERE n.id = ?`, nodeID))
	if errors.Is(err, sql.ErrNoRows) {
		return item{}, ErrNotFound
	}
	if err != nil {
		return item{}, err
	}
	d, err := s.drive(ctx, q, userID, n.DriveID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return item{}, err
	}
	type step struct {
		Crumb
		trashed bool
		access  string
		by      string
	}
	rows, err := q.QueryContext(ctx, `
		WITH RECURSIVE up(id, parent_id, name, trashed, depth) AS (
			SELECT id, parent_id, name, trashed_at IS NOT NULL, 0 FROM nodes WHERE id = ?1
			UNION ALL
			SELECT n.id, n.parent_id, n.name, n.trashed_at IS NOT NULL, up.depth + 1
			FROM nodes n JOIN up ON n.id = up.parent_id
		)
		SELECT up.id, up.name, up.trashed, coalesce(ps.access, ''), coalesce(u.name, '')
		FROM up
		LEFT JOIN people_shares ps ON ps.node_id = up.id AND ps.user_id = ?2
			AND EXISTS (SELECT 1 FROM users WHERE id = ?2 AND disabled_at IS NULL)
		LEFT JOIN users u ON u.id = ps.created_by
		ORDER BY up.depth DESC`, n.ID, userID)
	if err != nil {
		return item{}, err
	}
	defer rows.Close()
	var chain []step
	for rows.Next() {
		var st step
		if err := rows.Scan(&st.ID, &st.Name, &st.trashed, &st.access, &st.by); err != nil {
			return item{}, err
		}
		chain = append(chain, st)
	}
	if err := rows.Err(); err != nil {
		return item{}, err
	}
	if len(chain) == 0 {
		return item{}, ErrNotFound
	}

	// The highest live shares, for viewing and for editing.
	viewTop, editTop := -1, -1
	live := true
	for i, st := range chain {
		if st.trashed {
			live = false
		}
		if !live || st.access == "" {
			continue
		}
		if viewTop < 0 {
			viewTop = i
		}
		if editTop < 0 && st.access == "edit" {
			editTop = i
		}
	}
	member := d.Role != ""
	if !member && viewTop < 0 {
		return item{}, ErrNotFound
	}
	role := d.Role
	if editTop >= 0 && roleRank[role] < roleRank["editor"] {
		role = "editor"
	} else if viewTop >= 0 && role == "" {
		role = "viewer"
	}

	it := item{Node: n, live: live, member: member}
	for _, st := range chain[:len(chain)-1] {
		it.above = append(it.above, st.ID)
	}
	top := 0
	if member {
		it.drive = d
	} else {
		top = viewTop
		sh := chain[viewTop]
		it.drive = Drive{ID: n.DriveID, Kind: "share", Name: sh.Name, RootID: sh.ID, SharedBy: sh.by}
	}
	it.drive.Role = role
	for _, st := range chain[top : len(chain)-1] {
		it.path = append(it.path, st.Crumb)
	}
	switch {
	case member && d.CanEdit():
		it.editTop = chain[0].ID
		it.driveEdit = true
		it.drive.CanShare = true
	case editTop >= 0:
		it.editTop = chain[editTop].ID
	}
	if !it.live && !trashedOK {
		return item{}, ErrNotFound
	}
	return it, nil
}

// Details is a node with what the UI needs around it.
type Details struct {
	Node  Node    `json:"node"`
	Drive Drive   `json:"drive"`
	Path  []Crumb `json:"path"`
}

// Get returns a live node, its drive and its path.
func (s *Service) Get(ctx context.Context, userID, nodeID string) (Details, error) {
	it, err := s.lookup(ctx, s.db, userID, nodeID, false)
	if err != nil {
		return Details{}, err
	}
	if it.IsFolder() {
		n, err := s.childCount(ctx, s.db, it.ID)
		if err != nil {
			return Details{}, err
		}
		it.ChildCount = &n
	}
	nodes := []Node{it.Node}
	if err := s.decorate(ctx, userID, nodes); err != nil {
		return Details{}, err
	}
	return Details{Node: nodes[0], Drive: it.drive, Path: nonNil(it.path)}, nil
}

func (s *Service) childCount(ctx context.Context, q querier, id string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE parent_id = ? AND trashed_at IS NULL`, id).Scan(&n)
	return n, err
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ValidateName trims a file or folder name and checks it. Names are data,
// never paths, so the rules only keep them sane to display and download:
// no slashes, no control characters, at most 255 bytes.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) ||
		strings.ContainsAny(name, `/\`) || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalidName
	}
	return name, nil
}

func nameKey(name string) string { return strings.ToLower(name) }

func (s *Service) stamp() string { return db.FormatTime(s.now()) }
