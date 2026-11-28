package destinations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"photodrop/internal/secrets"
)

// LinkLifetime bounds a link or unlink from start to callback.
const LinkLifetime = 5 * time.Minute

const maxLinkTransactions = 512

// Pending is a stored link or unlink transaction. Only hashes of the state,
// browser secret and session token are kept, plus the short-lived PKCE
// verifier. Never an Immich session token or API key.
type Pending struct {
	StateHash, Purpose, Provider string
	HostID                       int64
	SessionHash, BrowserHash     string
	TargetID, AccountID          int64
	Verifier                     string
}

func (Pending) String() string     { return "pending destination link (redacted)" }
func (p Pending) GoString() string { return p.String() }

// SaveTransaction stores a pending link. Like OIDC login transactions, the
// table is bounded: expired rows are pruned first, this browser's previous
// flow is replaced, and at most 512 may be pending.
func SaveTransaction(ctx context.Context, db *sql.DB, p Pending) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `DELETE FROM destination_link_transactions WHERE state_hash IN
	    (SELECT state_hash FROM destination_link_transactions WHERE expires_at <= ? ORDER BY expires_at LIMIT 512) OR browser_hash = ?`, stamp, p.BrowserHash); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM destination_link_transactions").Scan(&count); err != nil {
		return err
	}
	if count >= maxLinkTransactions {
		return ErrLinkFailed
	}
	var account any
	if p.AccountID != 0 {
		account = p.AccountID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO destination_link_transactions
	    (state_hash, purpose, provider, host_id, session_hash, browser_hash, immich_target_id, account_id, pkce_verifier, created_at, expires_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, p.StateHash, p.Purpose, p.Provider, p.HostID, p.SessionHash, p.BrowserHash,
		p.TargetID, account, p.Verifier, stamp, stamp+int64(LinkLifetime.Seconds())); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeTransaction atomically removes and returns an unexpired transaction
// matching both the state and the browser secret. A replay finds nothing.
func ConsumeTransaction(ctx context.Context, db *sql.DB, stateHash, browserHash string) (Pending, error) {
	var p Pending
	var account sql.NullInt64
	err := db.QueryRowContext(ctx, `DELETE FROM destination_link_transactions
	    WHERE state_hash = ? AND browser_hash = ? AND expires_at > ?
	    RETURNING state_hash, purpose, provider, host_id, session_hash, browser_hash, immich_target_id, account_id, pkce_verifier`,
		stateHash, browserHash, time.Now().Unix()).Scan(&p.StateHash, &p.Purpose, &p.Provider, &p.HostID, &p.SessionHash, &p.BrowserHash, &p.TargetID, &account, &p.Verifier)
	if err != nil {
		return Pending{}, ErrLinkFailed
	}
	p.AccountID = account.Int64
	return p, nil
}

// CheckRemoteUser enforces invariant 6 before a key is minted: no other live
// account may hold this remote user on this target.
func CheckRemoteUser(ctx context.Context, db *sql.DB, targetID int64, remoteUserID string, except int64) error {
	var taken bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM destination_accounts
	    WHERE provider = 'immich' AND immich_target_id = ? AND remote_user_id = ? AND status <> 'revoked' AND id <> ?)`,
		targetID, remoteUserID, except).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return ErrInUse
	}
	return nil
}

func uniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// StoreLinked persists a verified credential, encrypted with AAD that binds it
// to its row, in one short transaction. accountID 0 inserts a new account (the
// row comes first, because the AAD names its id); otherwise it is a relink that
// replaces the credential of the host's existing account for the same remote
// user. A uniqueness conflict is ErrInUse.
func StoreLinked(ctx context.Context, db *sql.DB, ring *secrets.Keyring, hostID, targetID int64, targetKey string, accountID int64, l Linked) (int64, error) {
	if !ring.Configured() {
		return 0, ErrLinkSecretsKey
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	email := any(nil)
	if l.RemoteEmail != "" {
		email = l.RemoteEmail
	}
	if accountID == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO destination_accounts
		    (provider, host_id, immich_target_id, credential_source, remote_user_id, remote_email, remote_credential_id, status, created_at, updated_at, last_validated_at)
		    VALUES ('immich', ?, ?, 'linked', ?, ?, ?, 'active', ?, ?, ?) RETURNING id`,
			hostID, targetID, l.RemoteUserID, email, l.RemoteCredentialID, stamp, stamp, stamp).Scan(&accountID)
		if uniqueViolation(err) {
			return 0, ErrInUse
		}
		if err != nil {
			return 0, fmt.Errorf("store linked account: %w", err)
		}
	}
	aad, err := secrets.AAD(secrets.Binding{AccountID: accountID, Provider: "immich", TargetKey: targetKey, RemoteUserID: l.RemoteUserID, RemoteCredentialID: l.RemoteCredentialID})
	if err != nil {
		return 0, ErrLinkFailed
	}
	keyID, nonce, ciphertext, err := ring.Encrypt(aad, l.Secret)
	if err != nil {
		return 0, ErrLinkFailed
	}
	res, err := tx.ExecContext(ctx, `UPDATE destination_accounts SET remote_email = ?, remote_credential_id = ?,
	    credential_key_id = ?, credential_nonce = ?, credential_ciphertext = ?, status = 'active', status_reason = '',
	    updated_at = ?, last_validated_at = ?
	    WHERE id = ? AND host_id = ? AND immich_target_id = ? AND credential_source = 'linked' AND remote_user_id = ? AND status <> 'revoked'`,
		email, l.RemoteCredentialID, keyID, nonce, ciphertext, stamp, stamp, accountID, hostID, targetID, l.RemoteUserID)
	if uniqueViolation(err) {
		return 0, ErrInUse
	}
	if err != nil {
		return 0, fmt.Errorf("store linked credential: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return 0, ErrLinkFailed
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return accountID, nil
}

// Revoke marks a host's linked account revoked and drops its ciphertext. The
// row stays, so bindings and history remain readable.
func Revoke(ctx context.Context, db *sql.DB, hostID, accountID int64) error {
	res, err := db.ExecContext(ctx, `UPDATE destination_accounts SET status = 'revoked', status_reason = '',
	    credential_key_id = NULL, credential_nonce = NULL, credential_ciphertext = NULL, updated_at = ?
	    WHERE id = ? AND host_id = ? AND credential_source = 'linked' AND status <> 'revoked'`,
		time.Now().UTC().Format(time.RFC3339Nano), accountID, hostID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// Fixed reasons recorded when validation finds a stored credential unusable.
const (
	ReasonRejected   = "Immich rejected the linked API key; link the account again"
	ReasonKeyChanged = "The linked API key no longer matches; link the account again"
)

// MarkNeedsRelink records that a linked credential stopped working. It never
// deletes anything remotely and never changes config accounts.
func MarkNeedsRelink(ctx context.Context, db *sql.DB, accountID int64, reason string) error {
	_, err := db.ExecContext(ctx, `UPDATE destination_accounts SET status = 'needs_relink', status_reason = ?, updated_at = ?
	    WHERE id = ? AND credential_source = 'linked' AND status = 'active'`, reason, time.Now().UTC().Format(time.RFC3339Nano), accountID)
	return err
}

// MarkValidated records a successful validation time.
func MarkValidated(ctx context.Context, db *sql.DB, accountID int64) error {
	_, err := db.ExecContext(ctx, "UPDATE destination_accounts SET last_validated_at = ? WHERE id = ?", time.Now().UTC().Format(time.RFC3339Nano), accountID)
	return err
}

// Linked lists every live linked account, for background validation.
func LinkedAccounts(ctx context.Context, db *sql.DB) ([]Account, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+accountColumns+" FROM destination_accounts WHERE credential_source = 'linked' AND status = 'active' ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// errorsIsAny is a small helper for callers mapping outcomes to result codes.
func errorsIsAny(err error, targets ...error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}

// ResultCode maps a link outcome to the fixed code shown on the Account page.
func ResultCode(purpose string, err error) string {
	switch {
	case err == nil && purpose == "unlink":
		return "unlinked"
	case err == nil:
		return "linked"
	case errors.Is(err, ErrSubjectMismatch):
		return "link_subject_mismatch"
	case errors.Is(err, ErrWrongUser):
		return "link_wrong_user"
	case errors.Is(err, ErrInUse):
		return "link_in_use"
	case errorsIsAny(err, ErrLinkVersion):
		return "link_version"
	default:
		return "link_failed"
	}
}
