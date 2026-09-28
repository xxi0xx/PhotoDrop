-- Short-lived login state only. Provider tokens/codes are never persisted.
CREATE TABLE oidc_transactions (
    state_hash TEXT PRIMARY KEY,
    nonce_hash TEXT NOT NULL,
    pkce_verifier TEXT NOT NULL,
    browser_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX oidc_transactions_expiry ON oidc_transactions(expires_at);
