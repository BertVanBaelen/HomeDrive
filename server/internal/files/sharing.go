package files

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// Sharing with people (docs/architecture.md §4, "Sharing with people").
// Whoever can edit an item on its drive may share it; the people it is
// shared with can't share it further. Lookups in files.go apply shares.

var (
	ErrInvalidAccess = errors.New("access must be view or edit")
	// ErrHasAccess is for sharing with someone who can already edit
	// everything in the drive, the sharer included.
	ErrHasAccess = errors.New("they can already edit everything in this drive")
)

func validAccess(a string) bool { return a == "view" || a == "edit" }

// Share is a person an item is shared with.
type Share struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Access   string `json:"access"` // view | edit
	Disabled bool   `json:"disabled,omitempty"`
	// From is the folder the share was made on, when it isn't the item.
	From *Crumb `json:"from,omitempty"`
}

// Sharing is everything the Share dialog shows about an item.
type Sharing struct {
	Drive Drive `json:"drive"`
	// Members counts the people in a shared drive who aren't disabled.
	Members int `json:"members"`
	// Editors are the people who can already edit everything in the drive:
	// sharing with them adds nothing.
	Editors []string `json:"editors"`
	// People are those the item itself is shared with; Inherited those
	// who have it through a folder it is in.
	People    []Share `json:"people"`
	Inherited []Share `json:"inherited"`
	Link      *Link   `json:"link"`
}

// sharer loads an item for someone who wants to share it or manage its
// link: they must be able to edit it on its drive. Drive top folders can't
// be shared; their drive's members are the way to do that.
func (s *Service) sharer(ctx context.Context, q querier, userID, nodeID string) (item, error) {
	it, err := s.lookup(ctx, q, userID, nodeID, false)
	if err != nil {
		return item{}, err
	}
	if !it.driveEdit {
		return item{}, ErrReadOnly
	}
	if it.ParentID == "" {
		return item{}, ErrRoot
	}
	return it, nil
}

// Sharing returns who has access to an item and its link.
func (s *Service) Sharing(ctx context.Context, userID, nodeID string) (Sharing, error) {
	it, err := s.sharer(ctx, s.db, userID, nodeID)
	if err != nil {
		return Sharing{}, err
	}
	out := Sharing{Drive: it.drive, Editors: []string{}, People: []Share{}, Inherited: []Share{}}
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM drive_members m JOIN users u ON u.id = m.user_id
		WHERE m.drive_id = ? AND u.disabled_at IS NULL`, it.DriveID).Scan(&out.Members); err != nil {
		return Sharing{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT owner_user_id FROM drives WHERE id = ?1 AND owner_user_id IS NOT NULL
		UNION
		SELECT user_id FROM drive_members WHERE drive_id = ?1 AND role IN ('editor', 'manager')`, it.DriveID)
	if err != nil {
		return Sharing{}, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Sharing{}, err
		}
		out.Editors = append(out.Editors, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Sharing{}, err
	}

	rows, err = s.db.QueryContext(ctx, `
		WITH RECURSIVE up(id, parent_id, name, depth) AS (
			SELECT id, parent_id, name, 0 FROM nodes WHERE id = ?
			UNION ALL
			SELECT n.id, n.parent_id, n.name, up.depth + 1 FROM nodes n JOIN up ON n.id = up.parent_id
		)
		SELECT up.depth, up.id, up.name, u.id, u.name, u.email, ps.access, u.disabled_at IS NOT NULL
		FROM up JOIN people_shares ps ON ps.node_id = up.id JOIN users u ON u.id = ps.user_id
		ORDER BY up.depth, lower(u.name), u.email`, it.ID)
	if err != nil {
		return Sharing{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var depth int
		var from Crumb
		var sh Share
		if err := rows.Scan(&depth, &from.ID, &from.Name, &sh.UserID, &sh.Name, &sh.Email, &sh.Access, &sh.Disabled); err != nil {
			return Sharing{}, err
		}
		if depth == 0 {
			out.People = append(out.People, sh)
		} else {
			sh.From = &from
			out.Inherited = append(out.Inherited, sh)
		}
	}
	if err := rows.Err(); err != nil {
		return Sharing{}, err
	}
	if out.Link, err = s.link(ctx, s.db, it.ID); err != nil {
		return Sharing{}, err
	}
	return out, nil
}

// ShareChange says what SetShare or RemoveShare did, for the audit log.
type ShareChange string

const (
	ShareAdded   ShareChange = "share.add"
	ShareChanged ShareChange = "share.change"
	ShareRemoved ShareChange = "share.remove"
	ShareSame    ShareChange = "" // nothing changed
)

// SetShare shares an item with a person or changes their access. The
// person needs an account that isn't disabled.
func (s *Service) SetShare(ctx context.Context, actorID, nodeID, personID, access string) (ShareChange, error) {
	if !validAccess(access) {
		return ShareSame, ErrInvalidAccess
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ShareSame, err
	}
	defer tx.Rollback()
	it, err := s.sharer(ctx, tx, actorID, nodeID)
	if err != nil {
		return ShareSame, err
	}
	var current sql.NullString
	var disabled, editor bool
	err = tx.QueryRowContext(ctx, `
		SELECT ps.access, u.disabled_at IS NOT NULL,
			EXISTS (SELECT 1 FROM drives WHERE id = ?2 AND owner_user_id = u.id)
			OR EXISTS (SELECT 1 FROM drive_members WHERE drive_id = ?2 AND user_id = u.id AND role IN ('editor', 'manager'))
		FROM users u LEFT JOIN people_shares ps ON ps.user_id = u.id AND ps.node_id = ?1
		WHERE u.id = ?3`, it.ID, it.DriveID, personID).Scan(&current, &disabled, &editor)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !current.Valid && disabled {
		return ShareSame, ErrNotFound
	}
	if err != nil {
		return ShareSame, err
	}
	if editor || personID == actorID {
		return ShareSame, ErrHasAccess
	}
	if current.String == access {
		return ShareSame, nil
	}
	change := ShareAdded
	if current.Valid {
		change = ShareChanged
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO people_shares (node_id, user_id, access, created_by, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (node_id, user_id) DO UPDATE SET access = excluded.access`,
		it.ID, personID, access, actorID, s.stamp()); err != nil {
		return ShareSame, err
	}
	return change, tx.Commit()
}

// RemoveShare stops sharing an item with a person.
func (s *Service) RemoveShare(ctx context.Context, actorID, nodeID, personID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	it, err := s.sharer(ctx, tx, actorID, nodeID)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM people_shares WHERE node_id = ? AND user_id = ?`, it.ID, personID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// SharedItem is an item shared with the user, for Shared with me.
type SharedItem struct {
	Located
	SharedBy string    `json:"sharedBy"`
	SharedAt time.Time `json:"sharedAt"`
	Access   string    `json:"access"` // view | edit
}

// SharedWithMe lists what was shared with the user, newest first. Items in
// the trash are left out until they are restored.
func (s *Service) SharedWithMe(ctx context.Context, userID string) ([]SharedItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ps.node_id, coalesce(b.name, ''), ps.created_at, ps.access
		FROM people_shares ps
		JOIN users u ON u.id = ps.user_id AND u.disabled_at IS NULL
		LEFT JOIN users b ON b.id = ps.created_by
		WHERE ps.user_id = ?
		ORDER BY ps.created_at DESC
		LIMIT 500`, userID)
	if err != nil {
		return nil, err
	}
	type row struct{ id, by, at, access string }
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.by, &r.at, &r.access); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := []SharedItem{}
	for _, r := range list {
		it, err := s.lookup(ctx, s.db, userID, r.id, false)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if it.IsFolder() {
			c, err := s.childCount(ctx, s.db, it.ID)
			if err != nil {
				return nil, err
			}
			it.ChildCount = &c
		}
		at, _ := db.ParseTime(r.at)
		out = append(out, SharedItem{
			Located:  Located{Node: it.Node, Drive: it.drive, Path: nonNil(it.path)},
			SharedBy: r.by, SharedAt: at, Access: r.access,
		})
	}
	nodes := make([]Node, len(out))
	for i := range out {
		nodes[i] = out[i].Node
	}
	if err := s.decorate(ctx, userID, nodes); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Node = nodes[i]
	}
	return out, nil
}
