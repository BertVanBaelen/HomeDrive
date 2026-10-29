package files

import (
	"context"
	"database/sql"
	"errors"
)

// ErrNotTrashed means only items in the trash can be deleted for good.
var ErrNotTrashed = errors.New("item is not in the trash")

// DeleteForever removes an item that is in the trash, with everything in
// it. Its contents leave the data folder; the backup keeps a copy for the
// retention period.
func (s *Service) DeleteForever(ctx context.Context, userID, nodeID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	it, err := s.lookup(ctx, tx, userID, nodeID, true)
	if err != nil {
		return err
	}
	if it.TrashedAt == nil {
		return ErrNotTrashed
	}
	// Deleting for good takes being an editor of the drive: what was
	// shared with someone can't be lost by them.
	if !it.driveEdit {
		return ErrReadOnly
	}
	objects, err := s.purge(ctx, tx, []string{it.ID})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.removeObjects(objects)
	return nil
}

// EmptyTrash deletes for good everything in the trash on the drives the
// user can change. It returns how many items were removed.
func (s *Service) EmptyTrash(ctx context.Context, userID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT n.id FROM nodes n
		JOIN drives d ON d.id = n.drive_id
		LEFT JOIN drive_members m ON m.drive_id = d.id AND m.user_id = ?1
		WHERE n.trashed_at IS NOT NULL
		  AND (d.owner_user_id = ?1 OR m.role IN ('editor', 'manager'))`, userID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	objects, err := s.purge(ctx, tx, ids)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.removeObjects(objects)
	return len(ids), nil
}

// purge deletes nodes with their subtrees inside tx and returns the objects
// their versions used.
func (s *Service) purge(ctx context.Context, tx *sql.Tx, ids []string) ([]string, error) {
	var objects []string
	for _, id := range ids {
		const subtree = `WITH RECURSIVE sub(id) AS (
			SELECT ? UNION ALL SELECT n.id FROM nodes n JOIN sub ON n.parent_id = sub.id
		)`
		rows, err := tx.QueryContext(ctx, subtree+`
			SELECT v.object_id FROM versions v WHERE v.node_id IN (SELECT id FROM sub)`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o string
			if err := rows.Scan(&o); err != nil {
				rows.Close()
				return nil, err
			}
			objects = append(objects, o)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// Uploads still heading into a deleted folder go too; their bytes
		// are swept by the upload cleanup.
		if _, err := tx.ExecContext(ctx, subtree+`
			DELETE FROM uploads WHERE parent_id IN (SELECT id FROM sub)`, id); err != nil {
			return nil, err
		}
		// One statement, so the parent links are only checked once the
		// whole subtree is gone. Versions, stars and recents cascade.
		if _, err := tx.ExecContext(ctx, subtree+`
			DELETE FROM nodes WHERE id IN (SELECT id FROM sub)`, id); err != nil {
			return nil, err
		}
	}
	return objects, nil
}

// removeObjects deletes object files after the database no longer refers
// to them. A failure only leaves an orphan for the cleanup job.
func (s *Service) removeObjects(objects []string) {
	for _, o := range objects {
		if err := s.store.RemoveObject(o); err != nil {
			s.log.Warn("removing object", "object", o, "error", err)
		}
	}
}
