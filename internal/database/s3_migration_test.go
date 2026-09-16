package database

import (
	"os"
	"path/filepath"
	"photodrop/migrations"
	"testing"
	"testing/fstest"
)

func TestGate3UpgradePreservesLocalAssets(t *testing.T) {
	dir := t.TempDir()
	files := testMigrations(t)
	for _, name := range []string{"002_events.sql", "003_admin_sessions.sql", "004_local_uploads.sql"} {
		b, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: b}
	}
	db, err := open(t.Context(), dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO events(public_id,name,enabled,created_at,updated_at) VALUES('AAAAAAAAAAAAAAAAAAAAAAAA','old event',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
 INSERT INTO upload_sessions(id,event_id,created_at,updated_at) VALUES('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
 INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,mime_type,size_bytes,status,created_at,completed_at) VALUES('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',1,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','original.png','e1_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','image/png',5,'ready','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	var before string
	db.QueryRow("SELECT group_concat(checksum || applied_at, ',') FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&before)
	os.Mkdir(filepath.Join(dir, "uploads"), 0700)
	path := filepath.Join(dir, "uploads", "e1_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	os.WriteFile(path, []byte("bytes"), 0600)
	db.Close()
	db = openTestDB(t, dir)
	var after, provider, target, name, status, key string
	var size, expected int64
	if err := db.QueryRow("SELECT storage_provider,storage_target,original_filename,status,storage_key,size_bytes,expected_size_bytes FROM assets").Scan(&provider, &target, &name, &status, &key, &size, &expected); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT group_concat(checksum || applied_at, ',') FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=4 ORDER BY version)").Scan(&after)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "bytes" || before != after || provider != "local" || target != "" || name != "original.png" || status != "ready" || size != 5 || expected != 0 || key != "e1_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || migrationCount(t, db) != 8 {
		t.Fatal("Gate 3 data or history changed")
	}
	db.Close()
	db = openTestDB(t, dir)
	if migrationCount(t, db) != 8 {
		t.Fatal("migration reapplied")
	}
}
