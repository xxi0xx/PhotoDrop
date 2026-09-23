package database

import (
	"testing"
	"testing/fstest"

	"photodrop/migrations"
)

func TestGate5UpgradePreservesMediaAndHistory(t *testing.T) {
	files := fstest.MapFS{}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "008" {
			continue
		}
		data, err := migrations.Files.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = &fstest.MapFile{Data: data}
	}
	dir := t.TempDir()
	db, err := open(t.Context(), dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events(id,public_id,name,enabled,created_at,updated_at,max_assets,max_bytes) VALUES(1,'AAAAAAAAAAAAAAAAAAAAAAAA','Gate 5',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',20,1000000);
	 INSERT INTO upload_sessions(id,event_id,created_at,updated_at,expires_at) VALUES('session',1,'old','old','2030-01-01T00:00:00Z');
	 INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,mime_type,size_bytes,status,created_at,completed_at,storage_backend_id) VALUES('asset',1,'session','photo.png','key','image/png',77,'ready','old','old',1);`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&before); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for range 2 {
		db = openTestDB(t, dir)
		var after string
		var count, maxAssets, size, backend int
		var state string
		if err := db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=7 ORDER BY version)").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT max_assets FROM events WHERE id=1").Scan(&maxAssets); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT status,size_bytes,storage_backend_id FROM assets WHERE id='asset'").Scan(&state, &size, &backend); err != nil {
			t.Fatal(err)
		}
		if before != after || count != 9 || maxAssets != 20 || state != "ready" || size != 77 || backend != 1 {
			t.Fatal("migration changed Gate 5 data or history")
		}
		db.Close()
	}
}
