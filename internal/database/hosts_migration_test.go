package database

import (
	"strings"
	"testing"
	"testing/fstest"

	"photodrop/migrations"
)

// A 012 deployment upgrades to hosts: host 1 is the administrator and owns every
// existing event, sessions reset, and the password credential survives.
func TestMultiHostUpgradeAssignsEventsToHostOne(t *testing.T) {
	files := fstest.MapFS{}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "013" {
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
	if _, err := db.Exec(`
	 INSERT INTO admin_credential(id,password_hash) VALUES(1,'existing-hash');
	 INSERT INTO admin_sessions(token_hash,csrf_token,created_at,expires_at) VALUES('existing-token-hash','existing-csrf',1,9999999999);
	 INSERT INTO events(id,public_id,name,enabled,created_at,updated_at) VALUES
	   (1,'AAAAAAAAAAAAAAAAAAAAAAAA','First',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'),
	   (2,'BBBBBBBBBBBBBBBBBBBBBBBB','Second',0,'2026-01-02T00:00:00Z','2026-01-02T00:00:00Z');
	 INSERT INTO immich_targets(id,key,base_url,created_at) VALUES(1,'home','https://immich.test','2026-01-01T00:00:00Z');
	 INSERT INTO immich_event_imports(event_id,target_id,album_name,album_marker,album_state,immich_album_id,created_at,updated_at)
	   VALUES(1,1,'First','marker-1','ready','11111111-1111-4111-8111-111111111111','old','old');`); err != nil {
		t.Fatal(err)
	}
	var history string
	if err := db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&history); err != nil {
		t.Fatal(err)
	}
	db.Close()

	for range 2 { // The second open proves the migration is not reapplied.
		db = openTestDB(t, dir)
		var after, role, name, hash string
		var password, disabled, owners, unowned, sessions, oldSessions, bindings int
		var issuer, subject *string
		if err := db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=12 ORDER BY version)").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT role,display_name,password_login,disabled,oidc_issuer,oidc_subject FROM hosts WHERE id=1").Scan(&role, &name, &password, &disabled, &issuer, &subject); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT count(*) FROM events WHERE owner_host_id=1").Scan(&owners); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT count(*) FROM events WHERE owner_host_id IS NULL").Scan(&unowned); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT count(*) FROM host_sessions").Scan(&sessions); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='admin_sessions'").Scan(&oldSessions); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT password_hash FROM admin_credential WHERE id=1").Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT count(*) FROM immich_event_imports WHERE album_state='ready'").Scan(&bindings); err != nil {
			t.Fatal(err)
		}
		if after != history || migrationCount(t, db) != latestMigration || role != "admin" || name != "Administrator" || password != 1 || disabled != 0 || issuer != nil || subject != nil {
			t.Fatal("host 1 or history is wrong after upgrade")
		}
		if owners != 2 || unowned != 0 || sessions != 0 || oldSessions != 0 || hash != "existing-hash" || bindings != 1 {
			t.Fatal("events, sessions, credential or bindings changed unexpectedly")
		}
		db.Close()
	}

	db = openTestDB(t, dir)
	now := "'2026-11-27T00:00:00Z'"
	rejects := map[string]string{
		"event without owner":     "INSERT INTO events(public_id,name,enabled,created_at,updated_at) VALUES('CCCCCCCCCCCCCCCCCCCCCCCC','x',1," + now + "," + now + ")",
		"owner cleared":           "UPDATE events SET owner_host_id=NULL WHERE id=1",
		"unknown owner":           "INSERT INTO events(public_id,name,enabled,created_at,updated_at,owner_host_id) VALUES('DDDDDDDDDDDDDDDDDDDDDDDD','x',1," + now + "," + now + ",99)",
		"second password host":    "INSERT INTO hosts(role,display_name,password_login,created_at) VALUES('admin','Second',1," + now + ")",
		"issuer without subject":  "INSERT INTO hosts(role,display_name,oidc_issuer,created_at) VALUES('host','Half','https://idp.test/'," + now + ")",
		"unknown role":            "INSERT INTO hosts(role,display_name,created_at) VALUES('owner','Role'," + now + ")",
		"empty display name":      "INSERT INTO hosts(role,display_name,created_at) VALUES('host',''," + now + ")",
		"session for no host":     "INSERT INTO host_sessions(token_hash,host_id,csrf_token,created_at,expires_at) VALUES('t',99,'c',1,2)",
		"duplicate OIDC identity": "INSERT INTO hosts(role,display_name,oidc_issuer,oidc_subject,created_at) VALUES('host','A','https://idp.test/','same'," + now + "),('host','B','https://idp.test/','same'," + now + ")",
	}
	for name, statement := range rejects {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("%s was accepted", name)
		} else if strings.HasPrefix(name, "event without") && !strings.Contains(err.Error(), "events require an owner host") {
			t.Fatalf("%s failed for the wrong reason: %v", name, err)
		}
	}
	if _, err := db.Exec("INSERT INTO hosts(id,role,display_name,oidc_issuer,oidc_subject,created_at) VALUES(2,'host','Guest host','https://idp.test/','host-2'," + now + ")"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO events(public_id,name,enabled,created_at,updated_at,owner_host_id) VALUES('EEEEEEEEEEEEEEEEEEEEEEEE','Host event',1," + now + "," + now + ",2)"); err != nil {
		t.Fatal("event owned by a second host was rejected:", err)
	}
	if _, err := db.Exec("INSERT INTO host_sessions(token_hash,host_id,csrf_token,created_at,expires_at) VALUES('t2',2,'c',1,2)"); err != nil {
		t.Fatal(err)
	}
}
