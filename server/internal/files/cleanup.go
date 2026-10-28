package files

import (
	"context"
	"errors"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// ErrDiskFull means the data folder's disk is too full for the upload.
var ErrDiskFull = errors.New("not enough free space")

// diskReserve stays free on the data disk: the database, the backup's
// temporary files and the system need room too.
const diskReserve = 512 << 20

// leftoverAge protects files that are being written right now: a chunk or
// object younger than this is never treated as a leftover.
const leftoverAge = time.Hour

// checkSpace refuses an upload that would eat into the reserve. If the
// free space can't be read, the upload goes ahead.
func (s *Service) checkSpace(size int64) error {
	free, err := s.store.FreeSpace()
	if err != nil {
		s.log.Warn("reading free disk space", "error", err)
		return nil
	}
	if free < uint64(size)+diskReserve {
		s.log.Warn("upload refused: data disk nearly full", "free", free, "size", size)
		return ErrDiskFull
	}
	return nil
}

// CleanupResult counts what one cleanup removed.
type CleanupResult struct {
	Uploads, TempFiles, Objects int
}

// Cleanup removes leftovers: uploads that expired unfinished, temporary
// files of interrupted writes, and objects no version uses (after a crash
// between storing a file and recording it). It is safe to run at any time.
func (s *Service) Cleanup(ctx context.Context) (CleanupResult, error) {
	var res CleanupResult
	now := s.now()

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM uploads WHERE expires_at <= ?`, db.FormatTime(now))
	if err != nil {
		return res, err
	}
	var expired []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return res, err
		}
		expired = append(expired, id)
	}
	rows.Close()
	for _, id := range expired {
		unlock := s.lockUpload(id)
		_, err := s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id)
		if err == nil {
			err = s.store.RemoveUpload(id)
		}
		unlock()
		s.forgetUploadLock(id)
		if err != nil {
			return res, err
		}
		res.Uploads++
	}

	// Files in uploads/ that no upload owns any more.
	live := map[string]bool{}
	idRows, err := s.db.QueryContext(ctx, `SELECT id FROM uploads`)
	if err != nil {
		return res, err
	}
	for idRows.Next() {
		var id string
		if err := idRows.Scan(&id); err != nil {
			idRows.Close()
			return res, err
		}
		live[id] = true
	}
	idRows.Close()
	files, err := s.store.UploadFiles()
	if err != nil {
		return res, err
	}
	for name, modified := range files {
		if !live[name] && now.Sub(modified) > leftoverAge {
			if err := s.store.RemoveUploadFile(name); err != nil {
				return res, err
			}
			res.TempFiles++
		}
	}

	// Objects nothing refers to.
	candidates, err := s.store.ObjectsOlderThan(now.Add(-leftoverAge))
	if err != nil {
		return res, err
	}
	for _, id := range candidates {
		var used bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM versions WHERE object_id = ?)`, id).Scan(&used); err != nil {
			return res, err
		}
		if !used {
			if err := s.store.RemoveObject(id); err != nil {
				return res, err
			}
			res.Objects++
		}
	}
	return res, nil
}
