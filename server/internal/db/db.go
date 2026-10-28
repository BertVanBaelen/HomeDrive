// Package db opens Homedrive's SQLite database and keeps its schema current.
package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Open opens the database at path, creating it if needed. The database must
// live on a local disk: SQLite's locking is not reliable on SMB or NFS shares.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)&_txlock=immediate"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}
	return conn, nil
}

type migration struct {
	version int
	name    string
	sql     string
}

// Migrate applies the embedded migrations that haven't run yet, each in its
// own transaction.
func Migrate(ctx context.Context, conn *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	) STRICT`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	var current int
	if err := conn.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	if latest := migrations[len(migrations)-1].version; current > latest {
		return fmt.Errorf("database schema version %d is newer than this Homedrive version supports (%d); downgrading is not supported", current, latest)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := apply(ctx, conn, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

func apply(ctx context.Context, conn *sql.DB, m migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
		m.version); err != nil {
		return err
	}
	return tx.Commit()
}

// loadMigrations reads migrations/NNNN_name.sql in version order and checks
// that versions run 1, 2, 3… without gaps.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil {
			return nil, fmt.Errorf("migration %s: name must start with a number and an underscore", e.Name())
		}
		b, err := migrationFiles.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: e.Name(), sql: string(b)})
	}
	slices.SortFunc(out, func(a, b migration) int { return a.version - b.version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %s: expected version %d", m.name, i+1)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no migrations embedded")
	}
	return out, nil
}

// Meta returns a value from the meta table and whether it was set.
func Meta(ctx context.Context, conn *sql.DB, key string) (string, bool, error) {
	var v string
	err := conn.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func SetMeta(ctx context.Context, conn *sql.DB, key, value string) error {
	_, err := conn.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// InstanceID returns this installation's random ID, creating it on first
// use. The data and backup folders carry it in their marker files.
func InstanceID(ctx context.Context, conn *sql.DB) (string, error) {
	b := make([]byte, 16)
	rand.Read(b)
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES ('instance_id', ?) ON CONFLICT (key) DO NOTHING`,
		hex.EncodeToString(b)); err != nil {
		return "", err
	}
	id, _, err := Meta(ctx, conn, "instance_id")
	return id, err
}
