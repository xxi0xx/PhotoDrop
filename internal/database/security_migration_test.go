package database

import (
	"photodrop/migrations"
	"testing"
	"testing/fstest"
)

func TestGate4BackendPatchUpgradeExpiresLegacyGrants(t *testing.T) {
	files := fstest.MapFS{}
	for _, name := range []string{"001_init.sql", "002_events.sql", "003_admin_sessions.sql", "004_local_uploads.sql", "005_s3_storage.sql", "006_storage_backends.sql"} {
		b, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: b}
	}
	dir := t.TempDir()
	db, err := open(t.Context(), dir, files)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO events(id,public_id,name,enabled,created_at,updated_at) VALUES(1,'AAAAAAAAAAAAAAAAAAAAAAAA','legacy',1,'2020-01-01T00:00:00Z','2020-01-01T00:00:00Z');
 INSERT INTO upload_sessions(id,event_id,created_at,updated_at) VALUES('old-session',1,'2020-01-01T00:00:00Z','2020-01-01T00:00:00Z');
 INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,status,created_at,completed_at,mime_type,size_bytes,storage_backend_id) VALUES('old-asset',1,'old-session','a.png','old-key','ready','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z','image/png',68,1);`)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&before)
	db.Close()
	db = openTestDB(t, dir)
	var after, expiry, status string
	var size, backend int64
	var maxAssets, maxBytes *int64
	db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=6 ORDER BY version)").Scan(&after)
	if err := db.QueryRow("SELECT expires_at FROM upload_sessions WHERE id='old-session'").Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT status,size_bytes,storage_backend_id FROM assets WHERE id='old-asset'").Scan(&status, &size, &backend)
	db.QueryRow("SELECT max_assets,max_bytes FROM events WHERE id=1").Scan(&maxAssets, &maxBytes)
	if before != after || migrationCount(t, db) != 9 || expiry != "1970-01-01T00:00:00Z" || status != "ready" || size != 68 || backend != 1 || maxAssets != nil || maxBytes != nil {
		t.Fatal("upgrade changed ready assets/history or left legacy grant live")
	}
}
