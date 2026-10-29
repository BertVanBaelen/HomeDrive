// Package files is the drive itself: drives, the folder tree, uploads and
// file contents. Every operation takes the acting user's ID and checks
// access through drive membership; a valid ID alone never grants anything.
package files

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
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

type Drive struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // personal | shared
	Name   string `json:"name"`
	RootID string `json:"rootId"`
	Role   string `json:"role"` // viewer | editor | manager
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
	path  []Crumb // from the drive's top folder down to the parent
	// live is false when the node or one of its ancestors is in the trash.
	live bool
}

func (it item) isRoot() bool { return it.ParentID == "" }

// lookup loads a node with access checks. Unless trashedOK, items that are
// in the trash (themselves or through a folder) are reported as missing.
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
	if err != nil {
		return item{}, err
	}
	it := item{Node: n, drive: d, live: n.TrashedAt == nil}
	rows, err := q.QueryContext(ctx, `
		WITH RECURSIVE up(id, parent_id, name, trashed, depth) AS (
			SELECT id, parent_id, name, trashed_at IS NOT NULL, 0 FROM nodes WHERE id = ?
			UNION ALL
			SELECT n.id, n.parent_id, n.name, n.trashed_at IS NOT NULL, up.depth + 1
			FROM nodes n JOIN up ON n.id = up.parent_id
		)
		SELECT id, name, trashed FROM up WHERE depth > 0 ORDER BY depth DESC`, n.ID)
	if err != nil {
		return item{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Crumb
		var trashed bool
		if err := rows.Scan(&c.ID, &c.Name, &trashed); err != nil {
			return item{}, err
		}
		it.path = append(it.path, c)
		if trashed {
			it.live = false
		}
	}
	if err := rows.Err(); err != nil {
		return item{}, err
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
	if err := s.markStars(ctx, userID, nodes); err != nil {
		return Details{}, err
	}
	if err := s.addPreviews(ctx, nodes); err != nil {
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
