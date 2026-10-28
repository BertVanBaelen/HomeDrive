package files

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxQuery limits search terms.
const MaxQuery = 100

// locate lists nodes across every drive the user can see. where and order
// are SQL over the nodes table aliased n; where may use ?2, ?3… for args
// (?1 is the user). Items in the trash, directly or through a folder, are
// skipped unless trashedOK.
func (s *Service) locate(ctx context.Context, userID, where string, args []any, order string, limit int, trashedOK bool) ([]Located, error) {
	// Candidates may turn out to be inside a trashed folder; fetch extra.
	fetch := limit
	if !trashedOK {
		fetch = limit * 3
	}
	rows, err := s.db.QueryContext(ctx, `SELECT n.id FROM nodes n
		JOIN drives d ON d.id = n.drive_id
		LEFT JOIN drive_members m ON m.drive_id = d.id AND m.user_id = ?1
		WHERE (d.owner_user_id = ?1 OR m.user_id IS NOT NULL) AND `+where+`
		ORDER BY `+order+` LIMIT `+strconv.Itoa(fetch), append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := []Located{}
	for _, id := range ids {
		if len(out) == limit {
			break
		}
		it, err := s.lookup(ctx, s.db, userID, id, trashedOK)
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
		out = append(out, Located{Node: it.Node, Drive: it.drive, Path: nonNil(it.path)})
	}
	stars, err := s.starSet(ctx, userID)
	if err != nil {
		return nil, err
	}
	nodes := make([]Node, len(out))
	for i := range out {
		out[i].Starred = stars[out[i].ID]
		nodes[i] = out[i].Node
	}
	if err := s.addPreviews(ctx, nodes); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Preview = nodes[i].Preview
	}
	return out, nil
}

func (s *Service) starSet(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id FROM stars WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = true
	}
	return set, rows.Err()
}

// previewSize is how many items a folder thumbnail shows (a 2×2 grid).
const previewSize = 4

// addPreviews gives every folder among nodes its first items, folders
// first, then by name, in one query.
func (s *Service) addPreviews(ctx context.Context, nodes []Node) error {
	var ids []string
	for _, n := range nodes {
		if n.IsFolder() {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT parent_id, id, kind, name FROM (
			SELECT parent_id, id, kind, name,
				row_number() OVER (PARTITION BY parent_id ORDER BY kind = 'file', name_key) AS rn
			FROM nodes
			WHERE trashed_at IS NULL AND parent_id IN (SELECT value FROM json_each(?))
		) WHERE rn <= ? ORDER BY parent_id, rn`, string(list), previewSize)
	if err != nil {
		return err
	}
	defer rows.Close()
	previews := map[string][]Mini{}
	for rows.Next() {
		var parent string
		var m Mini
		if err := rows.Scan(&parent, &m.ID, &m.Kind, &m.Name); err != nil {
			return err
		}
		m.Type = TypeOf(Node{Kind: m.Kind, Name: m.Name})
		previews[parent] = append(previews[parent], m)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range nodes {
		nodes[i].Preview = previews[nodes[i].ID]
	}
	return nil
}

// markStars sets Starred on nodes for the user.
func (s *Service) markStars(ctx context.Context, userID string, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}
	set, err := s.starSet(ctx, userID)
	if err != nil {
		return err
	}
	for i := range nodes {
		nodes[i].Starred = set[nodes[i].ID]
	}
	return nil
}

// SetStar stars or unstars a node for the user. Stars are personal, so
// viewers may star too.
func (s *Service) SetStar(ctx context.Context, userID, nodeID string, on bool) (Node, error) {
	it, err := s.lookup(ctx, s.db, userID, nodeID, false)
	if err != nil {
		return Node{}, err
	}
	if it.isRoot() {
		return Node{}, ErrRoot
	}
	if on {
		_, err = s.db.ExecContext(ctx, `INSERT INTO stars (user_id, node_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, userID, it.ID)
	} else {
		_, err = s.db.ExecContext(ctx, `DELETE FROM stars WHERE user_id = ? AND node_id = ?`, userID, it.ID)
	}
	if err != nil {
		return Node{}, err
	}
	return s.node(ctx, userID, it.ID)
}

// Starred lists the user's starred items, folders first.
func (s *Service) Starred(ctx context.Context, userID string) ([]Located, error) {
	return s.locate(ctx, userID,
		`n.trashed_at IS NULL AND n.id IN (SELECT node_id FROM stars WHERE user_id = ?1)`, nil,
		`n.kind = 'file', n.name_key`, 500, false)
}

// Recent lists files on the user's drives by last change, newest first.
// On the family drive that includes what others added.
func (s *Service) Recent(ctx context.Context, userID string) ([]Located, error) {
	return s.locate(ctx, userID, `n.kind = 'file' AND n.trashed_at IS NULL`, nil, `n.modified_at DESC`, 50, false)
}

// Search finds items whose name contains q, ignoring case.
func (s *Service) Search(ctx context.Context, userID, q string) ([]Located, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []Located{}, nil
	}
	if utf8.RuneCountInString(q) > MaxQuery {
		return nil, ErrInvalidName
	}
	pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(nameKey(q)) + "%"
	return s.locate(ctx, userID,
		`n.parent_id IS NOT NULL AND n.trashed_at IS NULL AND n.name_key LIKE ?2 ESCAPE '\'`, []any{pattern},
		`n.kind = 'file', n.name_key`, 100, false)
}
