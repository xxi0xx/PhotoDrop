package database

import (
	"photodrop/migrations"
	"testing"
	"testing/fstest"
)

func TestPhase2AutoImportUpgrade(t *testing.T) {
	files := fstest.MapFS{}
	names, err := migrations.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name.Name() >= "010" {
			continue
		}
		b, err := migrations.Files.ReadFile(name.Name())
		if err != nil {
			t.Fatal(err)
		}
		files[name.Name()] = &fstest.MapFile{Data: b}
	}
	dir := t.TempDir()
	db, err := open(t.Context(), dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO events(id,public_id,name,enabled,created_at,updated_at) VALUES(1,'AAAAAAAAAAAAAAAAAAAAAAAA','Phase 2',1,'old','old');
 INSERT INTO immich_targets(id,key,base_url,created_at) VALUES(1,'home','https://photos.test','old');
 INSERT INTO immich_event_imports(id,event_id,target_id,album_name,album_marker,album_state,immich_album_id,created_at,updated_at) VALUES(1,1,1,'Album','marker','ready','remote-album','old','old');`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&before); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for range 2 {
		db = openTestDB(t, dir)
		var auto bool
		var album, marker, after string
		if err = db.QueryRow("SELECT auto_import,immich_album_id,album_marker FROM immich_event_imports WHERE id=1").Scan(&auto, &album, &marker); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=9 ORDER BY version)").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if auto || album != "remote-album" || marker != "marker" || before != after || migrationCount(t, db) != 10 {
			t.Fatal("upgrade changed existing binding/history")
		}
		if _, err = db.Exec("UPDATE immich_event_imports SET auto_import=2"); err == nil {
			t.Fatal("nonboolean accepted")
		}
		db.Close()
	}
}
