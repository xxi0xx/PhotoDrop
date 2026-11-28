// Package destinations owns destination accounts: where an event's photos go,
// and the stored, encrypted credentials for linked accounts.
package destinations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"photodrop/internal/secrets"
)

// RotateCounts reports what a rotation did. It never includes key material.
type RotateCounts struct {
	Linked  int // linked accounts that hold a stored credential
	Rotated int // re-encrypted under the active key by this run
	Current int // already under the active key
	Changed int // changed by something else during the run; left as they were
}

type storedCredential struct {
	id                    int64
	keyID                 string
	nonce, ciphertext     []byte
	binding               secrets.Binding
	targetKey, remoteUser sql.NullString
}

// Rotate re-encrypts every stored linked credential under the active key, one
// short transaction per row. It first checks that every row decrypts with a
// loaded key and changes nothing if any does not.
func Rotate(ctx context.Context, db *sql.DB, ring *secrets.Keyring) (RotateCounts, error) {
	var counts RotateCounts
	if !ring.Configured() {
		return counts, secrets.ErrNoKey
	}
	rows, err := db.QueryContext(ctx, `SELECT a.id, a.provider, t.key, a.remote_user_id, a.remote_credential_id,
	    a.credential_key_id, a.credential_nonce, a.credential_ciphertext
	    FROM destination_accounts a LEFT JOIN immich_targets t ON t.id = a.immich_target_id
	    WHERE a.credential_source = 'linked' AND a.credential_ciphertext IS NOT NULL ORDER BY a.id`)
	if err != nil {
		return counts, fmt.Errorf("read destination accounts: %w", err)
	}
	var stored []storedCredential
	for rows.Next() {
		var c storedCredential
		var credentialID sql.NullString
		if err := rows.Scan(&c.id, &c.binding.Provider, &c.targetKey, &c.remoteUser, &credentialID, &c.keyID, &c.nonce, &c.ciphertext); err != nil {
			rows.Close()
			return counts, fmt.Errorf("read destination account: %w", err)
		}
		c.binding.AccountID = c.id
		c.binding.TargetKey, c.binding.RemoteUserID, c.binding.RemoteCredentialID = c.targetKey.String, c.remoteUser.String, credentialID.String
		stored = append(stored, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return counts, fmt.Errorf("read destination accounts: %w", err)
	}
	counts.Linked = len(stored)
	// Check everything before changing anything.
	for _, c := range stored {
		aad, err := secrets.AAD(c.binding)
		if err != nil {
			return counts, fmt.Errorf("account %d: %w; nothing was changed", c.id, err)
		}
		plaintext, err := ring.Decrypt(c.keyID, c.nonce, c.ciphertext, aad)
		clear(plaintext)
		if err != nil {
			return counts, fmt.Errorf("account %d: %w; nothing was changed", c.id, err)
		}
	}
	for _, c := range stored {
		if c.keyID == ring.ActiveKeyID() {
			counts.Current++
			continue
		}
		changed, err := rotateOne(ctx, db, ring, c)
		if err != nil {
			return counts, fmt.Errorf("account %d: %w", c.id, err)
		}
		if changed {
			counts.Rotated++
		} else {
			counts.Changed++
		}
	}
	return counts, nil
}

func rotateOne(ctx context.Context, db *sql.DB, ring *secrets.Keyring, c storedCredential) (bool, error) {
	aad, err := secrets.AAD(c.binding)
	if err != nil {
		return false, err
	}
	plaintext, err := ring.Decrypt(c.keyID, c.nonce, c.ciphertext, aad)
	if err != nil {
		return false, err
	}
	keyID, nonce, ciphertext, err := ring.Encrypt(aad, plaintext)
	clear(plaintext)
	if err != nil {
		return false, err
	}
	// The row must still hold exactly what was read; a concurrent relink or
	// unlink wins and this run leaves that row alone.
	res, err := db.ExecContext(ctx, `UPDATE destination_accounts SET credential_key_id = ?, credential_nonce = ?, credential_ciphertext = ?
	    WHERE id = ? AND credential_key_id = ? AND credential_nonce = ? AND credential_ciphertext = ?`,
		keyID, nonce, ciphertext, c.id, c.keyID, c.nonce, c.ciphertext)
	if err != nil {
		return false, fmt.Errorf("store rotated credential: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store rotated credential: %w", err)
	}
	return n == 1, nil
}

// ErrRotateUsage is returned for unexpected command-line arguments.
var ErrRotateUsage = errors.New("usage: photodrop rotate-secrets")
