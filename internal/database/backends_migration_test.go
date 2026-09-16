package database

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"photodrop/internal/config"
	"photodrop/internal/storage"
	"photodrop/migrations"
)

func TestGate4BackendUpgrade(t *testing.T) {
	for _, withS3 := range []bool{false, true} {
		t.Run(map[bool]string{false: "local only", true: "historical S3 and retired keys"}[withS3], func(t *testing.T) {
			dir := t.TempDir()
			files := fstest.MapFS{}
			for _, name := range []string{"001_init.sql", "002_events.sql", "003_admin_sessions.sql", "004_local_uploads.sql", "005_s3_storage.sql"} {
				data, err := migrations.Files.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				files[name] = &fstest.MapFile{Data: data}
			}
			db, err := open(t.Context(), dir, files)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`INSERT INTO events(id,public_id,name,enabled,created_at,updated_at) VALUES(1,'AAAAAAAAAAAAAAAAAAAAAAAA','legacy',1,'old','old');
   INSERT INTO upload_sessions(id,event_id,created_at,updated_at) VALUES('session',1,'old','old');
   INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,status,created_at) VALUES('local-asset',1,'session','old.png','local-key','pending','old');`)
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{StorageProvider: "local", S3Backends: map[string]config.S3{}}
			s3 := config.S3{Bucket: "legacy-bucket", Endpoint: "https://legacy.test", Region: "auto", Prefix: "old/", PathStyle: true, AccessKeyID: "runtime-id", SecretAccessKey: "runtime-secret", PresignTTL: time.Minute}
			fingerprint := storage.NewS3(s3).Target()
			if withS3 {
				_, err = db.Exec(`INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,status,created_at,storage_provider,storage_target) VALUES('s3-asset',1,'session','old.jpg','s3-key','pending','old','s3',?);`, fingerprint)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("INSERT INTO s3_cleanup(storage_key,storage_target,event_id,asset_id,checked_at) VALUES('retired-key',?,1,'retired-asset','old')", fingerprint); err != nil {
					t.Fatal(err)
				}
			}
			var before string
			if err := db.QueryRow("SELECT group_concat(checksum || applied_at,',') FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&before); err != nil {
				t.Fatal(err)
			}
			db.Close()
			db = openTestDB(t, dir)
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			if withS3 {
				if _, err := storage.Reconcile(t.Context(), db, cfg, logger); err == nil || !strings.Contains(err.Error(), "original S3 configuration") {
					t.Fatal("guessed unknown legacy target", err)
				}
				wrong := s3
				wrong.Bucket = "wrong-bucket"
				cfg.S3Backends["legacy-r2"] = wrong
				if _, err := storage.Reconcile(t.Context(), db, cfg, logger); err == nil {
					t.Fatal("mapped wrong historical destination")
				}
				var count int
				db.QueryRow("SELECT count(*) FROM storage_backends").Scan(&count)
				if count != 1 {
					t.Fatal("failed resolution committed partial registration")
				}
				cfg.S3Backends["legacy-r2"] = s3
				cfg.S3Backends["ambiguous-alias"] = s3
				if _, err := storage.Reconcile(t.Context(), db, cfg, logger); err == nil {
					t.Fatal("guessed ambiguous legacy alias")
				}
				delete(cfg.S3Backends, "ambiguous-alias")
			}
			registry, err := storage.Reconcile(t.Context(), db, cfg, logger)
			if err != nil {
				t.Fatal(err)
			}
			var localID int64
			if err := db.QueryRow("SELECT storage_backend_id FROM assets WHERE id='local-asset'").Scan(&localID); err != nil || localID != 1 {
				t.Fatal("local association", err)
			}
			if withS3 {
				var id, retiredID int64
				db.QueryRow("SELECT storage_backend_id FROM assets WHERE id='s3-asset'").Scan(&id)
				db.QueryRow("SELECT storage_backend_id FROM s3_cleanup").Scan(&retiredID)
				backend, err := registry.Resolve(id)
				if err != nil || backend.Key != "legacy-r2" || retiredID != id {
					t.Fatal("incorrect legacy association", err)
				}
				if _, err := db.Exec("UPDATE assets SET storage_backend_id=1 WHERE id='s3-asset'"); err == nil {
					t.Fatal("allowed reassociation")
				}
				if _, err := db.Exec("UPDATE s3_cleanup SET storage_backend_id=1"); err == nil {
					t.Fatal("allowed retired-key reassociation")
				}
			}
			var after string
			db.QueryRow("SELECT group_concat(checksum || applied_at,',') FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=5 ORDER BY version)").Scan(&after)
			if before != after || migrationCount(t, db) != 8 {
				t.Fatal("changed old migration history")
			}
			db.Close()
			db = openTestDB(t, dir)
			// Once bound, credentials can be absent without blocking unrelated startup.
			cfg.S3Backends = nil
			if _, err := storage.Reconcile(t.Context(), db, cfg, logger); err != nil {
				t.Fatal("restart requires historical credentials", err)
			}
			if strings.Contains(logs.String(), "runtime-secret") {
				t.Fatal("legacy migration logged credentials")
			}
		})
	}
}
