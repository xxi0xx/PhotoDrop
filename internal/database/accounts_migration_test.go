package database

import (
	"strconv"
	"testing"
	"testing/fstest"

	"photodrop/migrations"
)

// A 013 deployment with two targets, bindings in every album state and jobs
// upgrades to destination accounts: one config account (host 1) per target that
// has bindings, none for a target without, and every binding backfilled.
func TestDestinationAccountsUpgrade(t *testing.T) {
	files := fstest.MapFS{}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "014" {
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
	 INSERT INTO events(id,public_id,name,enabled,created_at,updated_at,owner_host_id) VALUES
	   (1,'AAAAAAAAAAAAAAAAAAAAAAAA','One',1,'old','old',1),(2,'BBBBBBBBBBBBBBBBBBBBBBBB','Two',1,'old','old',1),(3,'CCCCCCCCCCCCCCCCCCCCCCCC','Three',1,'old','old',1);
	 INSERT INTO immich_targets(id,key,base_url,created_at) VALUES(1,'home','https://immich.test','2026-01-01T00:00:00Z'),(2,'unused','https://other.test','2026-01-02T00:00:00Z');
	 INSERT INTO immich_event_imports(id,event_id,target_id,album_name,album_marker,album_state,immich_album_id,created_at,updated_at) VALUES
	   (1,1,1,'One','m-1','new',NULL,'old','old'),
	   (2,2,1,'Two','m-2','creating',NULL,'old','old'),
	   (3,3,1,'Three','m-3','ready','11111111-1111-4111-8111-111111111111','old','old');
	 INSERT INTO integration_jobs(event_import_id,status,mode,created_at) VALUES(3,'completed','new','old'),(2,'queued','new','old');`); err != nil {
		t.Fatal(err)
	}
	var history string
	db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations ORDER BY version)").Scan(&history)
	db.Close()

	for range 2 {
		db = openTestDB(t, dir)
		var after string
		var accounts, configHome, configUnused, unbound, jobs int
		db.QueryRow("SELECT group_concat(checksum||applied_at) FROM (SELECT checksum,applied_at FROM schema_migrations WHERE version<=13 ORDER BY version)").Scan(&after)
		db.QueryRow("SELECT count(*) FROM destination_accounts").Scan(&accounts)
		db.QueryRow("SELECT count(*) FROM destination_accounts WHERE immich_target_id=1 AND host_id=1 AND credential_source='config' AND status='active' AND credential_ciphertext IS NULL").Scan(&configHome)
		db.QueryRow("SELECT count(*) FROM destination_accounts WHERE immich_target_id=2").Scan(&configUnused)
		db.QueryRow("SELECT count(*) FROM immich_event_imports i LEFT JOIN destination_accounts a ON a.id=i.account_id WHERE a.immich_target_id IS NOT i.target_id").Scan(&unbound)
		db.QueryRow("SELECT count(*) FROM integration_jobs").Scan(&jobs)
		if after != history || migrationCount(t, db) != latestMigration || accounts != 1 || configHome != 1 || configUnused != 0 || unbound != 0 || jobs != 2 {
			t.Fatal("upgrade result", accounts, configHome, configUnused, unbound, jobs)
		}
		db.Close()
	}

	db = openTestDB(t, dir)
	exec := func(statement string) error { _, err := db.Exec(statement); return err }
	if err := exec(`INSERT INTO hosts(id,role,display_name,oidc_issuer,oidc_subject,created_at) VALUES(2,'host','Host two','https://idp.test/','two','now'),(3,'host','Host three','https://idp.test/','three','now')`); err != nil {
		t.Fatal(err)
	}
	linked := func(id, host, target int, user string) string {
		return `INSERT INTO destination_accounts(id,provider,host_id,immich_target_id,credential_source,remote_user_id,remote_credential_id,credential_key_id,credential_nonce,credential_ciphertext,status,created_at,updated_at)
		 VALUES(` + itoa(id) + `,'immich',` + itoa(host) + `,` + itoa(target) + `,'linked','` + user + `','cred','0123456789abcdef',x'00',x'00','active','now','now')`
	}
	if err := exec(linked(10, 2, 1, "user-2")); err != nil {
		t.Fatal("valid linked account rejected", err)
	}
	if err := exec(linked(11, 2, 2, "user-2-other-server")); err != nil {
		t.Fatal("one account per target per host must allow a second target", err)
	}
	rejects := map[string]string{
		"binding without account":            `INSERT INTO immich_event_imports(event_id,target_id,album_name,album_marker,created_at,updated_at) VALUES(1,2,'x','m-x','now','now')`,
		"binding account on other target":    `INSERT INTO immich_event_imports(event_id,target_id,album_name,album_marker,created_at,updated_at,account_id) VALUES(1,2,'x','m-y','now','now',1)`,
		"account change after album":         `UPDATE immich_event_imports SET account_id=10 WHERE id=3`,
		"account change while creating":      `UPDATE immich_event_imports SET account_id=10 WHERE id=2`,
		"binding target change":              `UPDATE immich_event_imports SET target_id=2, account_id=11 WHERE id=1`,
		"second live account per host":       linked(12, 2, 1, "user-2b"),
		"remote user linked twice":           linked(13, 3, 1, "user-2"),
		"second config account":              `INSERT INTO destination_accounts(provider,host_id,immich_target_id,credential_source,status,created_at,updated_at) VALUES('immich',3,1,'config','active','now','now')`,
		"config with ciphertext":             `INSERT INTO destination_accounts(provider,host_id,immich_target_id,credential_source,credential_key_id,credential_nonce,credential_ciphertext,status,created_at,updated_at) VALUES('immich',3,2,'config','k',x'00',x'00','active','now','now')`,
		"linked without remote user":         `INSERT INTO destination_accounts(provider,host_id,immich_target_id,credential_source,status,created_at,updated_at) VALUES('immich',3,2,'linked','active','now','now')`,
		"ciphertext without nonce":           `INSERT INTO destination_accounts(provider,host_id,immich_target_id,credential_source,remote_user_id,credential_key_id,credential_ciphertext,status,created_at,updated_at) VALUES('immich',3,2,'linked','u3','k',x'00','active','now','now')`,
		"immich account without target":      `INSERT INTO destination_accounts(provider,host_id,credential_source,remote_user_id,status,created_at,updated_at) VALUES('immich',3,'linked','u4','active','now','now')`,
		"unknown status":                     `INSERT INTO destination_accounts(provider,host_id,immich_target_id,credential_source,remote_user_id,status,created_at,updated_at) VALUES('immich',3,2,'linked','u5','broken','now','now')`,
		"unlink transaction without account": `INSERT INTO destination_link_transactions(state_hash,purpose,provider,host_id,session_hash,browser_hash,immich_target_id,pkce_verifier,created_at,expires_at) VALUES('s','unlink','immich',2,'a','b',1,'v',1,2)`,
	}
	for name, statement := range rejects {
		if err := exec(statement); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// A binding still in state 'new' may move to another account on its target.
	if err := exec(`UPDATE immich_event_imports SET account_id=10 WHERE id=1`); err != nil {
		t.Fatal("account change on a 'new' binding rejected", err)
	}
	// A revoked account frees its host/target slot and its remote user.
	if err := exec(`UPDATE destination_accounts SET status='revoked', credential_key_id=NULL, credential_nonce=NULL, credential_ciphertext=NULL WHERE id=10`); err != nil {
		t.Fatal(err)
	}
	if err := exec(linked(14, 2, 1, "user-2")); err != nil {
		t.Fatal("relink after revoke rejected", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
