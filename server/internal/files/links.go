package files

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"time"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/auth"
	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// Public links (docs/architecture.md §4, "Public links"). One view-only
// link per item, managed by whoever may share the item. Visitors reach the
// linked item and what is in it, nothing else.

var (
	ErrInvalidExpiry = errors.New("expiry must be never, 7d or 1d")
	ErrLinkPassword  = errors.New("a link password has 8 to 256 characters")
)

// Link password length, in characters.
const (
	MinLinkPassword = 8
	MaxLinkPassword = 256
)

// ValidLinkPassword checks a new link password's length.
func ValidLinkPassword(pw string) error {
	if n := utf8.RuneCountInString(pw); n < MinLinkPassword || n > MaxLinkPassword {
		return ErrLinkPassword
	}
	return nil
}

// expiries maps the choices to how long a link then works.
var expiries = map[string]time.Duration{"never": 0, "7d": 7 * 24 * time.Hour, "1d": 24 * time.Hour}

func ValidExpiry(e string) error {
	if _, ok := expiries[e]; !ok {
		return ErrInvalidExpiry
	}
	return nil
}

// Link is a public link's settings, for the people who manage it.
type Link struct {
	ID string `json:"id"`
	// Token makes the address: /s/<token>.
	Token     string     `json:"token"`
	Expiry    string     `json:"expiry"` // never | 7d | 1d
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Expired   bool       `json:"expired,omitempty"`
	Password  bool       `json:"password"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (s *Service) link(ctx context.Context, q querier, nodeID string) (*Link, error) {
	var l Link
	var expires sql.NullString
	var created, hash string
	err := q.QueryRowContext(ctx, `SELECT id, token, expiry, expires_at, password_hash, created_at FROM links WHERE node_id = ?`,
		nodeID).Scan(&l.ID, &l.Token, &l.Expiry, &expires, &hash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if expires.Valid {
		t, _ := db.ParseTime(expires.String)
		l.ExpiresAt = &t
		l.Expired = !s.now().Before(t)
	}
	l.Password = hash != ""
	l.CreatedAt, _ = db.ParseTime(created)
	return &l, nil
}

// Link returns an item's link, or nil when it has none, for those who may
// share the item.
func (s *Service) Link(ctx context.Context, userID, nodeID string) (*Link, error) {
	it, err := s.sharer(ctx, s.db, userID, nodeID)
	if err != nil {
		return nil, err
	}
	return s.link(ctx, s.db, it.ID)
}

// LinkSettings changes a link. Nil fields stay as they are.
type LinkSettings struct {
	// Expiry is counted from now.
	Expiry *string
	// PasswordHash replaces the password; empty removes it.
	PasswordHash *string
}

// LinkChange says what SetLink or RemoveLink did, for the audit log.
type LinkChange string

const (
	LinkCreated LinkChange = "link.create"
	LinkChanged LinkChange = "link.change"
	LinkRemoved LinkChange = "link.remove"
	LinkSame    LinkChange = ""
)

// SetLink makes an item's link, or changes its settings. A new link has a
// new address; a changed one keeps it.
func (s *Service) SetLink(ctx context.Context, userID, nodeID string, set LinkSettings) (Link, LinkChange, error) {
	if set.Expiry != nil {
		if err := ValidExpiry(*set.Expiry); err != nil {
			return Link{}, LinkSame, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Link{}, LinkSame, err
	}
	defer tx.Rollback()
	it, err := s.sharer(ctx, tx, userID, nodeID)
	if err != nil {
		return Link{}, LinkSame, err
	}
	now := s.now()
	stamp := db.FormatTime(now)
	var expiresAt any
	if set.Expiry != nil && expiries[*set.Expiry] > 0 {
		expiresAt = db.FormatTime(now.Add(expiries[*set.Expiry]))
	}
	current, err := s.link(ctx, tx, it.ID)
	if err != nil {
		return Link{}, LinkSame, err
	}
	change := LinkChanged
	if current == nil {
		change = LinkCreated
		expiry, hash := "never", ""
		if set.Expiry != nil {
			expiry = *set.Expiry
		}
		if set.PasswordHash != nil {
			hash = *set.PasswordHash
		}
		token, tokenHash := auth.NewLinkToken()
		if _, err := tx.ExecContext(ctx, `INSERT INTO links
			(id, node_id, token, token_hash, expiry, expires_at, password_hash, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			db.NewID(), it.ID, token, tokenHash, expiry, expiresAt, hash, userID, stamp, stamp); err != nil {
			return Link{}, LinkSame, err
		}
	} else {
		if set.Expiry == nil && set.PasswordHash == nil {
			return *current, LinkSame, nil
		}
		if set.Expiry != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE links SET expiry = ?, expires_at = ?, updated_at = ? WHERE id = ?`,
				*set.Expiry, expiresAt, stamp, current.ID); err != nil {
				return Link{}, LinkSame, err
			}
		}
		if set.PasswordHash != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE links SET password_hash = ?, updated_at = ? WHERE id = ?`,
				*set.PasswordHash, stamp, current.ID); err != nil {
				return Link{}, LinkSame, err
			}
		}
	}
	l, err := s.link(ctx, tx, it.ID)
	if err != nil {
		return Link{}, LinkSame, err
	}
	return *l, change, tx.Commit()
}

// RemoveLink switches an item's link off. Its address stops working for
// good: switching it on again gives a new one.
func (s *Service) RemoveLink(ctx context.Context, userID, nodeID string) (LinkChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LinkSame, err
	}
	defer tx.Rollback()
	it, err := s.sharer(ctx, tx, userID, nodeID)
	if err != nil {
		return LinkSame, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM links WHERE node_id = ?`, it.ID)
	if err != nil {
		return LinkSame, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return LinkSame, nil
	}
	return LinkRemoved, tx.Commit()
}

// LinkGrantKey returns the key that signs the cookies remembering link
// passwords, made on first use and kept in the database.
func (s *Service) LinkGrantKey(ctx context.Context) ([]byte, error) {
	b := make([]byte, 32)
	rand.Read(b)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES ('link_grant_key', ?) ON CONFLICT (key) DO NOTHING`,
		hex.EncodeToString(b)); err != nil {
		return nil, err
	}
	v, _, err := db.Meta(ctx, s.db, "link_grant_key")
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(v)
	if err != nil || len(key) < 32 {
		return nil, errors.New("meta.link_grant_key is damaged")
	}
	return key, nil
}

// Visit is a working link as a visitor reaches it.
type Visit struct {
	LinkID string
	NodeID string
	// PasswordHash is empty when the link has no password.
	PasswordHash string
}

// Visit finds the link for a token. Unknown, expired and switched-off
// links and links to items in the trash all give ErrNotFound.
func (s *Service) Visit(ctx context.Context, token string) (Visit, error) {
	if !auth.ValidLinkToken(token) {
		return Visit{}, ErrNotFound
	}
	var v Visit
	var expires sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, node_id, expires_at, password_hash FROM links WHERE token_hash = ?`,
		auth.HashToken(token)).Scan(&v.LinkID, &v.NodeID, &expires, &v.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Visit{}, ErrNotFound
	}
	if err != nil {
		return Visit{}, err
	}
	if expires.Valid {
		if t, err := db.ParseTime(expires.String); err != nil || !s.now().Before(t) {
			return Visit{}, ErrNotFound
		}
	}
	if _, _, err := s.linked(ctx, v, v.NodeID); err != nil {
		return Visit{}, err
	}
	return v, nil
}

// linked loads a node for a link visitor: the linked item or something in
// it, with it and every folder above it out of the trash. The path runs
// from the linked item down to the node's parent.
func (s *Service) linked(ctx context.Context, v Visit, nodeID string) (Node, []Crumb, error) {
	if !db.ValidID(nodeID) {
		return Node{}, nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE up(id, parent_id, name, trashed, depth) AS (
			SELECT id, parent_id, name, trashed_at IS NOT NULL, 0 FROM nodes WHERE id = ?
			UNION ALL
			SELECT n.id, n.parent_id, n.name, n.trashed_at IS NOT NULL, up.depth + 1
			FROM nodes n JOIN up ON n.id = up.parent_id
		)
		SELECT id, name, trashed FROM up ORDER BY depth DESC`, nodeID)
	if err != nil {
		return Node{}, nil, err
	}
	defer rows.Close()
	var path []Crumb
	inside := false
	for rows.Next() {
		var c Crumb
		var trashed bool
		if err := rows.Scan(&c.ID, &c.Name, &trashed); err != nil {
			return Node{}, nil, err
		}
		if trashed {
			return Node{}, nil, ErrNotFound
		}
		if c.ID == v.NodeID {
			inside = true
		}
		if inside {
			path = append(path, c)
		}
	}
	if err := rows.Err(); err != nil {
		return Node{}, nil, err
	}
	if !inside {
		return Node{}, nil, ErrNotFound
	}
	rows.Close()
	n, err := scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes n WHERE n.id = ?`, nodeID))
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, nil, ErrNotFound
	}
	if err != nil {
		return Node{}, nil, err
	}
	return n, path[:len(path)-1], nil
}

// PublicNode is what link visitors see of an item: nothing about its
// drive, its folders above the linked item or the people involved.
type PublicNode struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Type       string    `json:"type"`
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	Mime       string    `json:"mime,omitempty"`
	ModifiedAt time.Time `json:"modifiedAt"`
	ChildCount *int      `json:"childCount,omitempty"`
	Preview    []Mini    `json:"preview,omitempty"`
}

func publicNode(n Node) PublicNode {
	return PublicNode{ID: n.ID, Kind: n.Kind, Type: n.Type, Name: n.Name, Size: n.Size, Mime: n.Mime,
		ModifiedAt: n.ModifiedAt, ChildCount: n.ChildCount, Preview: n.Preview}
}

// LinkPage is an item on a link page, with a folder's contents.
type LinkPage struct {
	Node PublicNode `json:"node"`
	// Path runs from the linked item down to the node's parent.
	Path     []Crumb      `json:"path"`
	Children []PublicNode `json:"children,omitempty"`
}

// LinkPage returns the linked item, or an item in it, for a visitor.
func (s *Service) LinkPage(ctx context.Context, v Visit, nodeID string) (LinkPage, error) {
	n, path, err := s.linked(ctx, v, nodeID)
	if err != nil {
		return LinkPage{}, err
	}
	page := LinkPage{Path: nonNil(path)}
	if n.IsFolder() {
		kids, err := s.children(ctx, n.ID)
		if err != nil {
			return LinkPage{}, err
		}
		SortNodes(kids, SortName, false)
		c := len(kids)
		n.ChildCount = &c
		all := append([]Node{n}, kids...)
		if err := s.addPreviews(ctx, all); err != nil {
			return LinkPage{}, err
		}
		n = all[0]
		page.Children = make([]PublicNode, len(kids))
		for i, k := range all[1:] {
			page.Children[i] = publicNode(k)
		}
	}
	page.Node = publicNode(n)
	return page, nil
}

// LinkOpen returns a file's contents for a link visitor.
func (s *Service) LinkOpen(ctx context.Context, v Visit, nodeID string) (*os.File, Node, error) {
	n, _, err := s.linked(ctx, v, nodeID)
	if err != nil {
		return nil, Node{}, err
	}
	f, err := s.openVersion(ctx, n)
	return f, n, err
}

// openVersion opens a file's current version.
func (s *Service) openVersion(ctx context.Context, n Node) (*os.File, error) {
	if n.IsFolder() || n.versionID == "" {
		return nil, ErrNotFound
	}
	var objectID string
	if err := s.db.QueryRowContext(ctx, `SELECT object_id FROM versions WHERE id = ?`, n.versionID).Scan(&objectID); err != nil {
		return nil, err
	}
	return s.store.OpenObject(objectID)
}
