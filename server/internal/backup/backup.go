// Package backup copies Homedrive to backup_path every night: a consistent
// database snapshot plus the file objects, which are immutable, so each run
// only copies what is new (docs/architecture.md §8). Export turns a
// backup back into a normal folder tree.
package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/config"
	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
	"github.com/BertVanBaelen/HomeDrive/server/internal/storage"
)

// ErrRunning means a backup is already in progress.
var ErrRunning = errors.New("a backup is already running")

const (
	dailySnapshots   = 14
	monthlySnapshots = 12
	statusKey        = "backup_status"
	initializedKey   = "backup_path_initialized"
	retentionFile    = "retention.json"
	snapshotPrefix   = "homedrive-"
	snapshotExt      = ".db"
)

type Service struct {
	db         *sql.DB
	data       *storage.Store
	path       string
	at         config.TimeOfDay
	retention  time.Duration
	instanceID string
	log        *slog.Logger
	now        func() time.Time
	running    atomic.Bool
}

// New returns the backup service, or nil when backup_path is empty.
func New(cfg *config.Config, conn *sql.DB, data *storage.Store, instanceID string, log *slog.Logger) *Service {
	if cfg.BackupPath == "" {
		return nil
	}
	return &Service{
		db: conn, data: data, path: cfg.BackupPath, at: cfg.BackupTime,
		retention:  time.Duration(cfg.BackupRetentionDays) * 24 * time.Hour,
		instanceID: instanceID, log: log, now: time.Now,
	}
}

// Status is what admins see.
type Status struct {
	Enabled       bool       `json:"enabled"`
	Time          string     `json:"time,omitempty"`
	RetentionDays int        `json:"retentionDays,omitempty"`
	Running       bool       `json:"running"`
	NextRun       *time.Time `json:"nextRun,omitempty"`
	LastRun       *time.Time `json:"lastRun,omitempty"`
	LastSuccess   *time.Time `json:"lastSuccess,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
	DurationSec   float64    `json:"durationSec,omitempty"`
	Copied        int        `json:"copied"`  // objects copied in the last run
	Removed       int        `json:"removed"` // objects past retention removed
	Snapshots     int        `json:"snapshots"`
	Objects       int        `json:"objects"`
	Bytes         int64      `json:"bytes"`
}

// Status reports the last run and the next one.
func (s *Service) Status(ctx context.Context) (Status, error) {
	if s == nil {
		return Status{}, nil
	}
	var st Status
	if v, ok, err := db.Meta(ctx, s.db, statusKey); err != nil {
		return Status{}, err
	} else if ok {
		json.Unmarshal([]byte(v), &st)
	}
	st.Enabled = true
	st.Time = s.at.String()
	st.RetentionDays = int(s.retention.Hours() / 24)
	st.Running = s.running.Load()
	next := s.nextRun(s.now())
	st.NextRun = &next
	return st, nil
}

// Schedule runs the backup every day at backup_time until ctx ends.
func (s *Service) Schedule(ctx context.Context) {
	for {
		next := s.nextRun(s.now())
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := s.Run(ctx); err != nil && !errors.Is(err, ErrRunning) && ctx.Err() == nil {
			s.log.Error("backup failed", "error", err)
		}
	}
}

func (s *Service) nextRun(now time.Time) time.Time {
	now = now.Local()
	t := time.Date(now.Year(), now.Month(), now.Day(), s.at.Hour, s.at.Minute, 0, 0, time.Local)
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// Start runs a backup in the background (the admin's "Back up now").
func (s *Service) Start() error {
	if s.running.Load() {
		return ErrRunning
	}
	go func() {
		if err := s.Run(context.Background()); err != nil && !errors.Is(err, ErrRunning) {
			s.log.Error("backup failed", "error", err)
		}
	}()
	return nil
}

// Run makes one backup and records how it went.
func (s *Service) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return ErrRunning
	}
	defer s.running.Store(false)

	start := s.now()
	var st Status
	if v, ok, _ := db.Meta(ctx, s.db, statusKey); ok {
		json.Unmarshal([]byte(v), &st)
	}
	s.log.Info("backup started", "path", s.path)
	res, err := s.run(ctx, start)
	st.LastRun = &start
	st.DurationSec = s.now().Sub(start).Round(time.Millisecond).Seconds()
	if err != nil {
		st.LastError = err.Error()
	} else {
		st.LastError = ""
		st.LastSuccess = &start
		st.Copied, st.Removed = res.copied, res.removed
		st.Snapshots, st.Objects, st.Bytes = res.snapshots, res.objects, res.bytes
		s.log.Info("backup finished", "copied", res.copied, "removed", res.removed,
			"snapshots", res.snapshots, "objects", res.objects, "bytes", res.bytes, "duration", st.DurationSec)
	}
	if b, merr := json.Marshal(st); merr == nil {
		if serr := db.SetMeta(ctx, s.db, statusKey, string(b)); serr != nil {
			s.log.Warn("saving backup status", "error", serr)
		}
	}
	return err
}

type result struct {
	copied, removed, snapshots, objects int
	bytes                               int64
}

func (s *Service) run(ctx context.Context, start time.Time) (result, error) {
	var res result
	initialized, _, err := db.Meta(ctx, s.db, initializedKey)
	if err != nil {
		return res, err
	}
	// The backup folder carries the instance marker too: an unmounted disk
	// or another installation's folder stops the backup instead of mixing.
	store, err := storage.OpenBackup(s.path, s.instanceID, initialized == "1")
	if err != nil {
		return res, err
	}
	defer store.Close()
	if initialized != "1" {
		if err := db.SetMeta(ctx, s.db, initializedKey, "1"); err != nil {
			return res, err
		}
	}

	if err := s.snapshot(ctx, start); err != nil {
		return res, fmt.Errorf("database snapshot: %w", err)
	}

	referenced, err := s.referencedObjects(ctx)
	if err != nil {
		return res, err
	}
	have, err := store.ListObjects()
	if err != nil {
		return res, fmt.Errorf("listing backup objects: %w", err)
	}
	for id := range referenced {
		if _, ok := have[id]; ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		n, err := s.copyObject(store, id)
		if err != nil {
			return res, fmt.Errorf("copying object %s: %w", id, err)
		}
		have[id] = n
		res.copied++
	}

	removed, err := s.applyRetention(store, have, referenced, start)
	if err != nil {
		return res, fmt.Errorf("retention: %w", err)
	}
	res.removed = removed
	for _, size := range have {
		res.objects++
		res.bytes += size
	}

	snaps, err := s.pruneSnapshots(start)
	if err != nil {
		return res, fmt.Errorf("pruning snapshots: %w", err)
	}
	res.snapshots = snaps
	return res, nil
}

// snapshot writes a consistent copy of the database with VACUUM INTO,
// first under a temporary name.
func (s *Service) snapshot(ctx context.Context, start time.Time) error {
	dir := filepath.Join(s.path, "db")
	final := filepath.Join(dir, snapshotPrefix+start.UTC().Format("20060102-150405")+snapshotExt)
	tmp := final + ".tmp"
	os.Remove(tmp)
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := syncFile(tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, final)
}

func syncFile(name string) error {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	serr := f.Sync()
	return errors.Join(serr, f.Close())
}

// referencedObjects is every object any version uses, current or older.
func (s *Service) referencedObjects(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT object_id FROM versions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Service) copyObject(store *storage.Store, id string) (int64, error) {
	f, err := s.data.OpenObject(id)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return store.CopyObject(id, f)
}

// applyRetention removes backup objects that nothing has used for longer
// than backup_retention_days. When an object stopped being used is kept in
// retention.json in the backup folder.
func (s *Service) applyRetention(store *storage.Store, have map[string]int64, referenced map[string]bool, now time.Time) (int, error) {
	file := filepath.Join(store.Path(), retentionFile)
	since := map[string]time.Time{}
	if b, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(b, &since); err != nil {
			s.log.Warn("ignoring damaged retention state", "file", file, "error", err)
			since = map[string]time.Time{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	removed := 0
	for id := range have {
		if referenced[id] {
			delete(since, id)
			continue
		}
		first, ok := since[id]
		if !ok {
			since[id] = now
			continue
		}
		if now.Sub(first) >= s.retention {
			if err := store.RemoveObject(id); err != nil {
				return removed, err
			}
			delete(since, id)
			delete(have, id)
			removed++
		}
	}
	for id := range since {
		if _, ok := have[id]; !ok {
			delete(since, id)
		}
	}
	b, err := json.MarshalIndent(since, "", "  ")
	if err != nil {
		return removed, err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return removed, err
	}
	return removed, os.Rename(tmp, file)
}

// pruneSnapshots keeps the newest daily snapshots and the first snapshot
// of each recent month, and returns how many remain.
func (s *Service) pruneSnapshots(now time.Time) (int, error) {
	names, err := Snapshots(s.path)
	if err != nil {
		return 0, err
	}
	keep := map[string]bool{}
	for i := len(names) - 1; i >= 0 && i >= len(names)-dailySnapshots; i-- {
		keep[names[i]] = true
	}
	months := map[string]bool{}
	cutoff := now.AddDate(0, -monthlySnapshots, 0).UTC().Format("200601")
	for _, n := range names { // oldest first, so the first of each month wins
		month := strings.TrimPrefix(n, snapshotPrefix)[:6]
		if month >= cutoff && !months[month] {
			months[month] = true
			keep[n] = true
		}
	}
	for _, n := range names {
		if !keep[n] {
			if err := os.Remove(filepath.Join(s.path, "db", n)); err != nil {
				return 0, err
			}
		}
	}
	return len(keep), nil
}

// Snapshots lists the snapshot files in a backup folder, oldest first.
func Snapshots(backupDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(backupDir, "db"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, snapshotPrefix) && strings.HasSuffix(n, snapshotExt) &&
			len(n) == len(snapshotPrefix)+len("20060102-150405")+len(snapshotExt) {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names, nil
}
