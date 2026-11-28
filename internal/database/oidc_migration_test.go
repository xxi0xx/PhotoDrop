package database

import (
	"photodrop/migrations"
	"testing"
	"testing/fstest"
)

func TestPhase3OIDCUpgradePreservesPasswordAndSession(t *testing.T) {
	files := fstest.MapFS{}
	names, err := migrations.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name.Name() >= "011" {
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
	if _, err = db.Exec(`INSERT INTO admin_credential VALUES(1,'existing-hash'); INSERT INTO admin_sessions(token_hash,csrf_token,created_at,expires_at) VALUES('existing-token-hash','existing-csrf',1,9999999999)`); err != nil {
		t.Fatal(err)
	}
	var history string
	db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&history)
	db.Close()
	for range 2 {
		db = openTestDB(t, dir)
		var hash, after string
		var sessions int
		if err = db.QueryRow("SELECT password_hash FROM admin_credential").Scan(&hash); err != nil {
			t.Fatal(err)
		}
		// Migration 013 deliberately resets sessions; the password credential survives.
		if err = db.QueryRow("SELECT count(*) FROM host_sessions").Scan(&sessions); err != nil {
			t.Fatal(err)
		}
		db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=10 ORDER BY version)").Scan(&after)
		if hash != "existing-hash" || sessions != 0 || after != history || migrationCount(t, db) != latestMigration {
			t.Fatal("upgrade modified previous auth/history")
		}
		var count int
		if err = db.QueryRow("SELECT count(*) FROM oidc_transactions").Scan(&count); err != nil || count != 0 {
			t.Fatal(err)
		}
		db.Close()
	}
}
