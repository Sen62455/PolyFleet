package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/store/migrations"
	_ "modernc.org/sqlite"
)

const migrationDisableForeignKeys = "-- hyfleet:foreign-keys-off"

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	dsn := sqliteDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// The Server receives heartbeats, Agent results, maintenance work, and UI reads
	// concurrently. A one-connection pool lets any stalled read starve every node.
	// Connection-level DSN options keep the SQLite policy consistent across this
	// deliberately small pool while WAL allows reads beside the short write jobs.
	maxConnections := 4
	if path == ":memory:" {
		maxConnections = 1
	}
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections)
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure sqlite: %w", err)
		}
	}
	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func sqliteDSN(path string) string {
	if path == ":memory:" {
		return path
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator +
		"_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL&_synchronous=NORMAL&_txlock=immediate"
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists int
		err := s.db.QueryRowContext(ctx,
			"SELECT 1 FROM schema_migrations WHERE version = ?", name,
		).Scan(&exists)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := s.applyMigration(
			ctx, name, string(body), bytes.Contains(body, []byte(migrationDisableForeignKeys)),
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, name, body string, disableForeignKeys bool) error {
	restoreForeignKeys := func() error { return nil }
	if disableForeignKeys {
		if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return fmt.Errorf("disable foreign keys for migration %s: %w", name, err)
		}
		restoreForeignKeys = func() error {
			if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
				return fmt.Errorf("restore foreign keys after migration %s: %w", name, err)
			}
			return nil
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(fmt.Errorf("begin migration %s: %w", name, err), restoreForeignKeys())
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return errors.Join(cause, restoreForeignKeys())
	}
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return rollback(fmt.Errorf("apply migration %s: %w", name, err))
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
		name, time.Now().UTC().UnixMilli(),
	); err != nil {
		return rollback(fmt.Errorf("record migration %s: %w", name, err))
	}
	if disableForeignKeys {
		rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return rollback(fmt.Errorf("check foreign keys after migration %s: %w", name, err))
		}
		if rows.Next() {
			var table, parent string
			var rowID sql.NullInt64
			var foreignKeyID int
			scanErr := rows.Scan(&table, &rowID, &parent, &foreignKeyID)
			_ = rows.Close()
			if scanErr != nil {
				return rollback(fmt.Errorf("read foreign key violation after migration %s: %w", name, scanErr))
			}
			return rollback(fmt.Errorf(
				"foreign key violation after migration %s: table=%s rowid=%v parent=%s constraint=%d",
				name, table, rowID, parent, foreignKeyID,
			))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return rollback(fmt.Errorf("check foreign keys after migration %s: %w", name, err))
		}
		if err := rows.Close(); err != nil {
			return rollback(fmt.Errorf("close foreign key check after migration %s: %w", name, err))
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(fmt.Errorf("commit migration %s: %w", name, err), restoreForeignKeys())
	}
	return restoreForeignKeys()
}
