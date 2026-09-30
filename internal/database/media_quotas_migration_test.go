package database

import (
	"fmt"
	"photodrop/migrations"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPhase4MediaQuotaUpgrade(t *testing.T) {
	files := fstest.MapFS{}
	names, err := migrations.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name.Name() >= "012" {
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
	if _, err = db.Exec(`INSERT INTO events(id,public_id,name,enabled,created_at,updated_at,max_assets,max_bytes) VALUES(1,'AAAAAAAAAAAAAAAAAAAAAAAA','Phase 4',1,'old','old',20,123456789);
 INSERT INTO upload_sessions(id,event_id,created_at,updated_at,expires_at) VALUES('session',1,'old','old','2030-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	kinds := []string{"image/jpeg", "image/png", "image/webp", "image/gif", "image/heic", "image/heif", "video/mp4", "video/quicktime", "application/octet-stream", ""}
	for i, kind := range kinds {
		for _, status := range []string{"ready", "pending"} {
			if kind == "" && status == "ready" {
				continue
			}
			if _, err = db.Exec(`INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,mime_type,size_bytes,status,created_at,storage_backend_id,expected_mime_type,expected_size_bytes,completed_at) VALUES(?,1,'session','misleading.mov',?,?,77,?,'old',1,?,77,'old')`, fmt.Sprint(i, status), fmt.Sprint(i, status), kind, status, kind); err != nil {
				t.Fatal(err)
			}
		}
	}
	var history string
	if err = db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&history); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for range 2 {
		db = openTestDB(t, dir)
		var after string
		var count, bytes, unlimited int
		if err = db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=11 ORDER BY version)").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(`SELECT max_assets,max_bytes,(max_photos IS NULL AND max_videos IS NULL AND max_photo_file_bytes IS NULL AND max_video_file_bytes IS NULL AND max_photo_storage_bytes IS NULL AND max_video_storage_bytes IS NULL) FROM events`).Scan(&count, &bytes, &unlimited); err != nil || count != 20 || bytes != 123456789 || unlimited != 1 || after != history || migrationCount(t, db) != 12 {
			t.Fatal("changed old quotas/history", err)
		}
		for i, kind := range kinds {
			for _, status := range []string{"ready", "pending"} {
				if kind == "" && status == "ready" {
					continue
				}
				var class *string
				if err = db.QueryRow("SELECT media_class FROM assets WHERE id=?", fmt.Sprint(i, status)).Scan(&class); err != nil {
					t.Fatal(err)
				}
				if i >= 8 {
					if class != nil {
						t.Fatal("guessed unknown MIME")
					}
				} else {
					expected := "photo"
					if strings.HasPrefix(kind, "video/") {
						expected = "video"
					}
					if class == nil || *class != expected {
						t.Fatal(kind, class)
					}
				}
			}
		}
		// The existing event index covers the narrowed per-class aggregate; no extra index needed.
		rows, err := db.Query("EXPLAIN QUERY PLAN SELECT count(*),sum(expected_size_bytes) FROM assets WHERE event_id=1 AND media_class='photo'")
		if err != nil {
			t.Fatal(err)
		}
		indexed := false
		for rows.Next() {
			var a, b, c int
			var detail string
			if err = rows.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			indexed = indexed || strings.Contains(detail, "USING INDEX assets_event")
		}
		rows.Close()
		if !indexed {
			t.Fatal("quota aggregate lost event index")
		}
		if _, err = db.Exec("UPDATE assets SET media_class='audio'"); err == nil {
			t.Fatal("accepted invalid class")
		}
		db.Close()
	}
}
