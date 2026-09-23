package database

import (
	"photodrop/migrations"
	"testing"
	"testing/fstest"
)

func TestGate6ContributorUpgrade(t *testing.T) {
	files := fstest.MapFS{}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "009" {
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
	if _, err := db.Exec(`INSERT INTO events(id,public_id,name,enabled,created_at,updated_at) VALUES(1,'AAAAAAAAAAAAAAAAAAAAAAAA','Existing Gate 6',1,'old','old');
 INSERT INTO upload_sessions(id,event_id,created_at,updated_at,expires_at) VALUES('old-session',1,'old','old','2030-01-01T00:00:00Z');
 INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,mime_type,size_bytes,status,created_at,completed_at,storage_backend_id) VALUES('old-asset',1,'old-session','photo.png','key','image/png',77,'ready','old','old',1);
 INSERT INTO immich_targets(id,key,base_url,created_at) VALUES(1,'home','https://photos.test','old');
 INSERT INTO immich_event_imports(id,event_id,target_id,album_name,album_marker,album_state,immich_album_id,created_at,updated_at) VALUES(1,1,1,'Album','marker','ready','remote-album','old','old');`); err != nil {
		t.Fatal(err)
	}
	var before string
	db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&before)
	db.Close()
	for range 2 {
		db = openTestDB(t, dir)
		var after, name, state, album string
		var size int
		var sessionName, assetName *string
		if err := db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=8 ORDER BY version)").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT e.name,a.status,a.size_bytes,u.contributor_name,a.contributor_name,i.immich_album_id FROM events e JOIN assets a ON a.event_id=e.id JOIN upload_sessions u ON u.id=a.upload_session_id JOIN immich_event_imports i ON i.event_id=e.id WHERE e.id=1").Scan(&name, &state, &size, &sessionName, &assetName, &album); err != nil {
			t.Fatal(err)
		}
		if migrationCount(t, db) != 9 || before != after || name != "Existing Gate 6" || state != "ready" || size != 77 || sessionName != nil || assetName != nil || album != "remote-album" {
			t.Fatal("history or existing data changed")
		}
		db.Close()
	}
}
