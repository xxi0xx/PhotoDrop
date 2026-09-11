package database

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"photodrop/migrations"
)

func openTestDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func openGate1DB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := open(t.Context(), dir, testMigrations(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func migrationCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func testMigrations(t *testing.T) fstest.MapFS {
	t.Helper()
	initial, err := migrations.Files.ReadFile("001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{"001_init.sql": &fstest.MapFile{Data: initial}}
}

func TestInitializationAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested # & data")
	db := openTestDB(t, dir)
	if info, err := os.Stat(filepath.Join(dir, "photodrop.db")); err != nil || info.Size() == 0 {
		t.Fatalf("persistent database not created: %v", err)
	}
	var tables int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table'").Scan(&tables); err != nil || tables != 6 {
		t.Fatalf("expected six migration/domain tables, got %d: %v", tables, err)
	}
	var before string
	if err := db.QueryRow("SELECT applied_at FROM schema_migrations WHERE version = 1").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openTestDB(t, dir)
	var after string
	if err := db.QueryRow("SELECT applied_at FROM schema_migrations WHERE version = 1").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if count := migrationCount(t, db); count != 4 || before != after {
		t.Fatalf("migration reapplied: count=%d before=%s after=%s", count, before, after)
	}
}

func TestMigrationExecutionAndRollback(t *testing.T) {
	db := openGate1DB(t, t.TempDir())
	files := testMigrations(t)
	files["002_probe.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE migration_probe (value TEXT); INSERT INTO migration_probe VALUES ('persisted');")}
	if err := migrate(t.Context(), db, files); err != nil {
		t.Fatal(err)
	}
	// Repeating a migration containing CREATE TABLE and INSERT would fail or
	// duplicate data if startup did not correctly skip committed migrations.
	if err := migrate(t.Context(), db, files); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := db.QueryRow("SELECT value FROM migration_probe").Scan(&value); err != nil || value != "persisted" || migrationCount(t, db) != 2 {
		t.Fatalf("migration data missing: %q, %v", value, err)
	}
	files["003_broken.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE rollback_probe (id INTEGER); INSERT INTO nonexistent VALUES (1);")}
	if err := migrate(t.Context(), db, files); err == nil || !strings.Contains(err.Error(), "003_broken.sql") {
		t.Fatalf("expected useful migration failure, got %v", err)
	}
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name = 'rollback_probe')").Scan(&exists); err != nil || exists || migrationCount(t, db) != 2 {
		t.Fatalf("failed migration was not rolled back: exists=%v, err=%v", exists, err)
	}
	files["003_broken.sql"].Data = []byte("CREATE TABLE rollback_probe (id INTEGER);")
	if err := migrate(t.Context(), db, files); err != nil || migrationCount(t, db) != 3 {
		t.Fatalf("cannot recover after failed migration: %v", err)
	}
}

func TestMigrationHistoryProtection(t *testing.T) {
	db := openGate1DB(t, t.TempDir())
	files := testMigrations(t)
	files["001_init.sql"].Data = bytes.ReplaceAll(files["001_init.sql"].Data, []byte("\n"), []byte("\r\n"))
	if err := migrate(t.Context(), db, files); err != nil {
		t.Fatalf("line endings changed migration identity: %v", err)
	}
	files["001_init.sql"].Data = append(files["001_init.sql"].Data, []byte("-- modified\n")...)
	if err := migrate(t.Context(), db, files); err == nil || !strings.Contains(err.Error(), "changed after application") {
		t.Fatalf("changed migration accepted: %v", err)
	}
	files = testMigrations(t)
	files["003_gap.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
	if err := migrate(t.Context(), db, files); err == nil {
		t.Fatal("accepted gap in migration numbering")
	}
	delete(files, "003_gap.sql")
	files["002_probe.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
	if err := migrate(t.Context(), db, files); err != nil {
		t.Fatal(err)
	}
	delete(files, "002_probe.sql")
	if err := migrate(t.Context(), db, files); err == nil || !strings.Contains(err.Error(), "older binary") {
		t.Fatalf("accepted a database newer than binary: %v", err)
	}
}

func TestConcurrentInitialization(t *testing.T) {
	dir := t.TempDir()
	start := make(chan struct{})
	errs := make(chan error, 4)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			<-start
			db, err := Open(t.Context(), dir)
			if err == nil {
				err = db.Close()
			}
			errs <- err
		})
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count := migrationCount(t, openTestDB(t, dir)); count != 4 {
		t.Fatalf("concurrent initialization applied %d migrations", count)
	}
}

func TestGate1Upgrade(t *testing.T) {
	dir := t.TempDir()
	db := openGate1DB(t, dir)
	var checksum, applied string
	if err := db.QueryRow("SELECT checksum, applied_at FROM schema_migrations WHERE version = 1").Scan(&checksum, &applied); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db = openTestDB(t, dir)
	var afterChecksum, afterApplied string
	if err := db.QueryRow("SELECT checksum, applied_at FROM schema_migrations WHERE version = 1").Scan(&afterChecksum, &afterApplied); err != nil {
		t.Fatal(err)
	}
	if migrationCount(t, db) != 4 || checksum != afterChecksum || applied != afterApplied {
		t.Fatal("Gate 1 history was changed during upgrade")
	}
	for _, table := range []string{"events", "admin_credential", "admin_sessions", "upload_sessions", "assets"} {
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name = ? AND type = 'table')", table).Scan(&exists); err != nil || !exists {
			t.Fatalf("missing %s: %v", table, err)
		}
	}
}

func TestGate2UpgradePreservesEventsAndSessions(t *testing.T) {
	dir := t.TempDir()
	files := testMigrations(t)
	for _, name := range []string{"002_events.sql", "003_admin_sessions.sql"} {
		content, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: content}
	}
	db, err := open(t.Context(), dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events(public_id, name, enabled, created_at, updated_at) VALUES ('AAAAAAAAAAAAAAAAAAAAAAAA', 'Existing event', 1, '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z'); INSERT INTO admin_credential(id, password_hash) VALUES (1, 'test hash'); INSERT INTO admin_sessions(token_hash, csrf_token, created_at, expires_at) VALUES ('test token digest', 'test csrf', 1, 2);`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.QueryRow("SELECT group_concat(checksum || applied_at, ',') FROM (SELECT checksum, applied_at FROM schema_migrations ORDER BY version)").Scan(&before); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db = openTestDB(t, dir)
	var after, name string
	var deleting bool
	var sessions int
	if err := db.QueryRow("SELECT group_concat(checksum || applied_at, ',') FROM (SELECT checksum, applied_at FROM schema_migrations WHERE version <= 3 ORDER BY version)").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT name, deleting FROM events WHERE id = 1").Scan(&name, &deleting); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM admin_sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if after != before || name != "Existing event" || deleting || sessions != 1 || migrationCount(t, db) != 4 {
		t.Fatal("Gate 2 data/history changed during upgrade")
	}
	db.Close()
	db = openTestDB(t, dir)
	if migrationCount(t, db) != 4 {
		t.Fatal("restart reapplied migration")
	}
}

func TestInitializationErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), file); err == nil || !strings.Contains(err.Error(), "data directory") {
		t.Fatalf("expected data directory error, got %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "photodrop.db"), []byte("not SQLite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), dir); err == nil {
		t.Fatal("accepted a corrupt database")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Open(ctx, t.TempDir()); err == nil {
		t.Fatal("ignored canceled startup")
	}
}
