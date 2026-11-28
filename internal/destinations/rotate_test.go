package destinations

import (
	"bytes"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	"photodrop/internal/database"
	"photodrop/internal/secrets"
)

func key(fill byte) []byte { return bytes.Repeat([]byte{fill}, 32) }

func seeded(t *testing.T, ring *secrets.Keyring, n int) *sql.DB {
	t.Helper()
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO immich_targets(id,key,base_url,created_at) VALUES(1,'home','https://immich.test','old');
	    INSERT INTO destination_accounts(id,provider,host_id,immich_target_id,credential_source,status,created_at,updated_at) VALUES(100,'immich',1,1,'config','active','now','now')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		store(t, db, ring, int64(i), "plain-secret-"+string(rune('a'+i)))
	}
	return db
}

func bindingFor(id int64) secrets.Binding {
	return secrets.Binding{AccountID: id, Provider: "immich", TargetKey: "home", RemoteUserID: "user-" + strconv.FormatInt(id, 10), RemoteCredentialID: "credential"}
}

func store(t *testing.T, db *sql.DB, ring *secrets.Keyring, id int64, plaintext string) {
	t.Helper()
	aad, _ := secrets.AAD(bindingFor(id))
	keyID, nonce, ciphertext, err := ring.Encrypt(aad, []byte(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	// One host per linked account: a host has at most one live account per target.
	if _, err := db.Exec(`INSERT OR IGNORE INTO hosts(id,role,display_name,oidc_issuer,oidc_subject,created_at) VALUES(?,'host','Host','https://idp.test/',?,'now')`, 1000+id, "host-"+strconv.FormatInt(id, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO destination_accounts(id,provider,host_id,immich_target_id,credential_source,remote_user_id,remote_credential_id,credential_key_id,credential_nonce,credential_ciphertext,status,created_at,updated_at)
	    VALUES(?,'immich',?,1,'linked',?,'credential',?,?,?,'active','now','now')`, id, 1000+id, "user-"+strconv.FormatInt(id, 10), keyID, nonce, ciphertext); err != nil {
		t.Fatal(err)
	}
}

func readAll(t *testing.T, db *sql.DB, ring *secrets.Keyring) map[int64]string {
	t.Helper()
	rows, err := db.Query("SELECT id, credential_key_id, credential_nonce, credential_ciphertext FROM destination_accounts WHERE credential_source='linked'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var keyID string
		var nonce, ciphertext []byte
		rows.Scan(&id, &keyID, &nonce, &ciphertext)
		aad, _ := secrets.AAD(bindingFor(id))
		plaintext, err := ring.Decrypt(keyID, nonce, ciphertext, aad)
		if err != nil {
			t.Fatal("row unreadable after rotation", id, err)
		}
		out[id] = keyID + ":" + string(plaintext)
	}
	return out
}

func TestRotateWithZeroRowsAndWithoutAKey(t *testing.T) {
	ring, _ := secrets.FromKeys(key(1))
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if counts, err := Rotate(t.Context(), db, ring); err != nil || counts != (RotateCounts{}) {
		t.Fatal("zero rows", counts, err)
	}
	if _, err := Rotate(t.Context(), db, &secrets.Keyring{}); !errors.Is(err, secrets.ErrNoKey) {
		t.Fatal("rotation without an active key", err)
	}
}

func TestRotateReencryptsUnderTheActiveKey(t *testing.T) {
	old, _ := secrets.FromKeys(key(1))
	db := seeded(t, old, 3)
	ring, _ := secrets.FromKeys(key(2), key(1))
	counts, err := Rotate(t.Context(), db, ring)
	if err != nil || counts != (RotateCounts{Linked: 3, Rotated: 3}) {
		t.Fatal(counts, err)
	}
	after := readAll(t, db, ring)
	for id, value := range after {
		if !strings.HasPrefix(value, ring.ActiveKeyID()+":plain-secret-") {
			t.Fatal("row not under the active key", id, value)
		}
	}
	// With the old key removed everything still decrypts, and a second run is a no-op.
	onlyNew, _ := secrets.FromKeys(key(2))
	if counts, err := Rotate(t.Context(), db, onlyNew); err != nil || counts != (RotateCounts{Linked: 3, Current: 3}) {
		t.Fatal("second run", counts, err)
	}
	var config int
	db.QueryRow("SELECT count(*) FROM destination_accounts WHERE credential_source='config' AND credential_ciphertext IS NULL").Scan(&config)
	if config != 1 {
		t.Fatal("config account touched")
	}
}

func TestRotateRefusesWithoutChangingAnything(t *testing.T) {
	old, _ := secrets.FromKeys(key(1))
	stranger, _ := secrets.FromKeys(key(3))
	snapshot := func(db *sql.DB) string {
		var out string
		db.QueryRow("SELECT group_concat(id || credential_key_id || hex(credential_nonce) || hex(credential_ciphertext), ',') FROM (SELECT * FROM destination_accounts ORDER BY id)").Scan(&out)
		return out
	}
	for name, setup := range map[string]func(*sql.DB){
		"row under an unloaded key": func(db *sql.DB) { store(t, db, stranger, 9, "other") },
		"tampered ciphertext": func(db *sql.DB) {
			db.Exec("UPDATE destination_accounts SET credential_ciphertext = randomblob(30) WHERE id = 2")
		},
		"row moved to another account": func(db *sql.DB) {
			db.Exec("UPDATE destination_accounts SET remote_user_id = 'someone-else' WHERE id = 1")
		},
		"binding with a separator": func(db *sql.DB) { db.Exec("UPDATE destination_accounts SET remote_credential_id = 'a|b' WHERE id = 1") },
	} {
		t.Run(name, func(t *testing.T) {
			db := seeded(t, old, 2)
			setup(db)
			before := snapshot(db)
			ring, _ := secrets.FromKeys(key(2), key(1))
			if _, err := Rotate(t.Context(), db, ring); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
				t.Fatal("rotation did not refuse", err)
			}
			if snapshot(db) != before {
				t.Fatal("a refused rotation changed rows")
			}
		})
	}
}

func TestRotateLeavesRowsChangedDuringTheRun(t *testing.T) {
	old, _ := secrets.FromKeys(key(1))
	db := seeded(t, old, 1)
	ring, _ := secrets.FromKeys(key(2), key(1))
	var c storedCredential
	db.QueryRow("SELECT id, credential_key_id, credential_nonce, credential_ciphertext FROM destination_accounts WHERE id = 1").Scan(&c.id, &c.keyID, &c.nonce, &c.ciphertext)
	c.binding = bindingFor(1)
	// Simulate a relink between the read and the write.
	db.Exec("DELETE FROM destination_accounts WHERE id = 1")
	store(t, db, ring, 1, "relinked")
	changed, err := rotateOne(t.Context(), db, ring, c)
	if err != nil || changed {
		t.Fatal("stale rotation overwrote a relinked row", changed, err)
	}
	if got := readAll(t, db, ring)[1]; !strings.HasSuffix(got, ":relinked") {
		t.Fatal("relinked credential lost", got)
	}
}
