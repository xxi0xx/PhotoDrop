-- Destination accounts: whose Immich library an event's albums and photos go to.
-- A 'config' account uses an API key from the environment (never stored here); a
-- 'linked' account stores its key only as AES-256-GCM ciphertext.
CREATE TABLE destination_accounts (
    id INTEGER PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('immich')),
    host_id INTEGER NOT NULL REFERENCES hosts(id),
    immich_target_id INTEGER REFERENCES immich_targets(id),
    credential_source TEXT NOT NULL CHECK (credential_source IN ('config','linked')),
    remote_user_id TEXT,
    remote_email TEXT,
    remote_credential_id TEXT,
    credential_key_id TEXT,
    credential_nonce BLOB,
    credential_ciphertext BLOB,
    status TEXT NOT NULL CHECK (status IN ('active','needs_relink','revoked')),
    status_reason TEXT NOT NULL DEFAULT '' CHECK (length(status_reason) <= 200),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_validated_at TEXT,
    CHECK (provider <> 'immich' OR immich_target_id IS NOT NULL),
    CHECK (credential_source = 'config' OR remote_user_id IS NOT NULL),
    CHECK ((credential_ciphertext IS NULL) = (credential_nonce IS NULL)),
    CHECK ((credential_ciphertext IS NULL) = (credential_key_id IS NULL)),
    CHECK (credential_source = 'linked' OR credential_ciphertext IS NULL)
);
CREATE UNIQUE INDEX destination_accounts_remote_user
    ON destination_accounts(provider, immich_target_id, remote_user_id)
    WHERE remote_user_id IS NOT NULL AND status <> 'revoked';
CREATE UNIQUE INDEX destination_accounts_host_target
    ON destination_accounts(provider, immich_target_id, host_id)
    WHERE status <> 'revoked';
CREATE UNIQUE INDEX destination_accounts_config_target
    ON destination_accounts(immich_target_id) WHERE credential_source = 'config';

-- A target row does not prove a key was ever configured, and SQL can't see the
-- environment. Create config accounts (host 1) only where existing bindings need
-- one for the backfill below. Reconcile adds the rest from configured keys.
INSERT INTO destination_accounts (provider, host_id, immich_target_id, credential_source, status, created_at, updated_at)
    SELECT 'immich', 1, t.id, 'config', 'active', t.created_at, t.created_at FROM immich_targets t
    WHERE EXISTS (SELECT 1 FROM immich_event_imports b WHERE b.target_id = t.id);

ALTER TABLE immich_event_imports ADD COLUMN account_id INTEGER REFERENCES destination_accounts(id);
UPDATE immich_event_imports SET account_id = (
    SELECT a.id FROM destination_accounts a
    WHERE a.immich_target_id = immich_event_imports.target_id AND a.credential_source = 'config');
CREATE INDEX immich_event_imports_account ON immich_event_imports(account_id);
CREATE TRIGGER immich_binding_account_insert BEFORE INSERT ON immich_event_imports
WHEN NEW.account_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM destination_accounts a WHERE a.id = NEW.account_id AND a.immich_target_id = NEW.target_id)
BEGIN SELECT RAISE(ABORT, 'binding account must exist on the binding target'); END;
CREATE TRIGGER immich_binding_account_update BEFORE UPDATE OF account_id, target_id ON immich_event_imports
WHEN NEW.account_id IS NOT OLD.account_id AND OLD.album_state <> 'new'
  OR NEW.target_id IS NOT OLD.target_id
  OR NOT EXISTS (SELECT 1 FROM destination_accounts a WHERE a.id = NEW.account_id AND a.immich_target_id = NEW.target_id)
BEGIN SELECT RAISE(ABORT, 'binding account is immutable once an album exists'); END;

-- Short-lived link/unlink state. Never stores Immich session tokens or API keys.
CREATE TABLE destination_link_transactions (
    state_hash TEXT PRIMARY KEY,
    purpose TEXT NOT NULL CHECK (purpose IN ('link','unlink')),
    provider TEXT NOT NULL CHECK (provider IN ('immich')),
    host_id INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    session_hash TEXT NOT NULL,
    browser_hash TEXT NOT NULL,
    immich_target_id INTEGER NOT NULL REFERENCES immich_targets(id),
    account_id INTEGER REFERENCES destination_accounts(id),
    pkce_verifier TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    CHECK (purpose = 'link' OR account_id IS NOT NULL)
);
CREATE INDEX destination_link_transactions_expiry ON destination_link_transactions(expires_at);
