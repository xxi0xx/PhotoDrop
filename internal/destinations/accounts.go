package destinations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	SourceConfig = "config"
	SourceLinked = "linked"

	StatusActive      = "active"
	StatusNeedsRelink = "needs_relink"
	StatusRevoked     = "revoked"
)

// ErrNoAccount means the event's owner has no usable account on the chosen
// target. The server reports it as 422 immich_no_account.
var ErrNoAccount = errors.New("the event's host has no Immich account on this server")

// Account is one destination account row. Its ciphertext fields are filled only
// for linked accounts and never leave this process.
type Account struct {
	ID, HostID, TargetID                          int64
	Provider, Source, Status, StatusReason        string
	RemoteUserID, RemoteEmail, RemoteCredentialID string
	KeyID                                         string
	Nonce, Ciphertext                             []byte
	LastValidatedAt                               *time.Time
}

func (Account) String() string     { return "destination account (credentials redacted)" }
func (a Account) GoString() string { return a.String() }

// Public is the API view of an account. It never includes ciphertext, the key
// id or the remote credential id.
type Public struct {
	ID              int64      `json:"id"`
	Provider        string     `json:"provider"`
	Source          string     `json:"source"`
	RemoteEmail     string     `json:"remote_email,omitempty"`
	Status          string     `json:"status"`
	LastValidatedAt *time.Time `json:"last_validated_at,omitempty"`
}

func (a Account) Public() Public {
	return Public{ID: a.ID, Provider: a.Provider, Source: a.Source, RemoteEmail: a.RemoteEmail, Status: a.Status, LastValidatedAt: a.LastValidatedAt}
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const accountColumns = `id, host_id, immich_target_id, provider, credential_source, status, status_reason,
    COALESCE(remote_user_id,''), COALESCE(remote_email,''), COALESCE(remote_credential_id,''),
    COALESCE(credential_key_id,''), credential_nonce, credential_ciphertext, last_validated_at`

func scanAccount(row interface{ Scan(...any) error }) (Account, error) {
	var a Account
	var validated *string
	if err := row.Scan(&a.ID, &a.HostID, &a.TargetID, &a.Provider, &a.Source, &a.Status, &a.StatusReason,
		&a.RemoteUserID, &a.RemoteEmail, &a.RemoteCredentialID, &a.KeyID, &a.Nonce, &a.Ciphertext, &validated); err != nil {
		return Account{}, err
	}
	if validated != nil {
		if t, err := time.Parse(time.RFC3339Nano, *validated); err == nil {
			a.LastValidatedAt = &t
		}
	}
	return a, nil
}

// Get returns one account by id.
func Get(ctx context.Context, q querier, id int64) (Account, error) {
	a, err := scanAccount(q.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM destination_accounts WHERE id = ?", id))
	if err != nil {
		return Account{}, fmt.Errorf("get destination account: %w", err)
	}
	return a, nil
}

// ForEventOwner returns the event owner's live (non-revoked) account on an
// Immich target. Invariant 5 makes it unique; none is ErrNoAccount.
func ForEventOwner(ctx context.Context, q querier, eventID, targetID int64) (Account, error) {
	a, err := scanAccount(q.QueryRowContext(ctx, "SELECT "+accountColumns+` FROM destination_accounts
	    WHERE provider = 'immich' AND immich_target_id = ? AND status <> 'revoked'
	    AND host_id = (SELECT owner_host_id FROM events WHERE id = ?)`, targetID, eventID))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNoAccount
	}
	if err != nil {
		return Account{}, fmt.Errorf("find event owner account: %w", err)
	}
	return a, nil
}

// ForHost returns a host's live Immich accounts keyed by target id.
func ForHost(ctx context.Context, q querier, hostID int64) (map[int64]Account, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+accountColumns+" FROM destination_accounts WHERE provider = 'immich' AND host_id = ? AND status <> 'revoked'", hostID)
	if err != nil {
		return nil, fmt.Errorf("list host accounts: %w", err)
	}
	defer rows.Close()
	result := map[int64]Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("read host account: %w", err)
		}
		result[a.TargetID] = a
	}
	return result, rows.Err()
}

// ConfigHostID owns every config account: the original administrator.
const ConfigHostID int64 = 1

// ReconcileConfig applies the startup rules for a target's config account, in
// the caller's transaction:
//
//   - key configured, no config account: insert one owned by host 1, unless host
//     1 already has a live linked account there (invariant 5); then report
//     ignored and insert nothing;
//   - no key configured: insert nothing and leave an existing row unchanged.
func ReconcileConfig(ctx context.Context, tx *sql.Tx, targetID int64, keyConfigured bool) (ignored bool, err error) {
	if !keyConfigured {
		return false, nil
	}
	var config, linked bool
	if err := tx.QueryRowContext(ctx, `SELECT
	    EXISTS(SELECT 1 FROM destination_accounts WHERE immich_target_id = ? AND credential_source = 'config'),
	    EXISTS(SELECT 1 FROM destination_accounts WHERE immich_target_id = ? AND host_id = ? AND status <> 'revoked' AND credential_source = 'linked')`,
		targetID, targetID, ConfigHostID).Scan(&config, &linked); err != nil {
		return false, fmt.Errorf("inspect config account: %w", err)
	}
	if config {
		return false, nil
	}
	if linked {
		return true, nil
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO destination_accounts (provider, host_id, immich_target_id, credential_source, status, created_at, updated_at)
	    VALUES ('immich', ?, ?, 'config', 'active', ?, ?)`, ConfigHostID, targetID, stamp, stamp); err != nil {
		return false, fmt.Errorf("create config account: %w", err)
	}
	return false, nil
}
