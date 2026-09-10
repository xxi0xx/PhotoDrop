-- One instance credential, not a user account system. A password change
-- atomically replaces this bcrypt hash and revokes existing sessions.
CREATE TABLE admin_credential (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash TEXT NOT NULL
);

CREATE TABLE admin_sessions (
    token_hash TEXT PRIMARY KEY,
    csrf_token TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX admin_sessions_expiry ON admin_sessions (expires_at);
