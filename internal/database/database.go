// Package database initializes the persistent SQLite database.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"photodrop/migrations"

	_ "modernc.org/sqlite"
)

// Open creates the data directory and migrates the database before returning it.
// The caller owns the returned database and must close it after HTTP shutdown.
func Open(ctx context.Context, dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory %q: %w", dataDir, err)
	}
	path, err := filepath.Abs(filepath.Join(dataDir, "photodrop.db"))
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	_, statErr := os.Stat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("inspect database file %q: %w", path, statErr)
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath // Windows drive letters need file:///C:/... form.
	}
	params := url.Values{
		"_pragma": {"busy_timeout(5000)", "foreign_keys(1)"},
		"_txlock": {"immediate"},
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: params.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	// One connection is sufficient for this foundation and serializes writes.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database %q (data directory must be writable): %w", path, err)
	}
	// Restrict new files without opening/closing a separate file descriptor:
	// closing one can release another SQLite connection's POSIX file locks.
	if os.IsNotExist(statErr) {
		if err := os.Chmod(path, 0o600); err != nil {
			db.Close()
			return nil, fmt.Errorf("set database permissions: %w", err)
		}
	}
	if err := migrate(ctx, db, migrations.Files); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database %q: %w", path, err)
	}
	return db, nil
}
