package database

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/fs"
	"strings"
)

func migrate(ctx context.Context, db *sql.DB, files fs.FS) error {
	names, err := fs.Glob(files, "*.sql") // Glob returns lexical order.
	if err != nil || len(names) == 0 {
		return fmt.Errorf("no readable SQL migrations")
	}
	for i, name := range names {
		if !strings.HasPrefix(name, fmt.Sprintf("%03d_", i+1)) {
			return fmt.Errorf("migration %q must have contiguous NNN_ numbering starting at 001", name)
		}
	}
	// The SQLite DSN uses BEGIN IMMEDIATE. Concurrent startup processes wait for
	// this write lock before reading history, preventing double application.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')").Scan(&exists); err != nil {
		return fmt.Errorf("inspect migration history: %w", err)
	}
	type appliedMigration struct {
		name, checksum string
	}
	applied := make(map[int]appliedMigration)
	if exists {
		rows, err := tx.QueryContext(ctx, "SELECT version, name, checksum FROM schema_migrations ORDER BY version")
		if err != nil {
			return fmt.Errorf("read migration history: %w", err)
		}
		for rows.Next() {
			var version int
			var entry appliedMigration
			if err := rows.Scan(&version, &entry.name, &entry.checksum); err != nil {
				rows.Close()
				return fmt.Errorf("read applied migration: %w", err)
			}
			if version != len(applied)+1 || version > len(names) || entry.name != names[version-1] {
				rows.Close()
				return fmt.Errorf("migration history does not match this binary at version %d; missing migrations or an older binary", version)
			}
			applied[version] = entry
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("read migration history: %w", err)
		}
	}
	for i, name := range names {
		script, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		// A Windows checkout must have the same checksum as a Linux build.
		script = bytes.ReplaceAll(script, []byte("\r\n"), []byte("\n"))
		checksum := fmt.Sprintf("%x", sha256.Sum256(script))
		if old, ok := applied[i+1]; ok {
			if old.checksum != checksum {
				return fmt.Errorf("migration %s changed after application; restore it and add a new migration", name)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, checksum) VALUES (?, ?, ?)", i+1, name, checksum); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}
