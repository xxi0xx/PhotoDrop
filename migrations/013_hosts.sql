-- Hosts own events. Host 1 is the existing administrator: the password credential
-- in admin_credential stays its password, and its first OIDC admin login claims it.
CREATE TABLE hosts (
    id INTEGER PRIMARY KEY,
    role TEXT NOT NULL CHECK (role IN ('admin','host')),
    display_name TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 200),
    email TEXT CHECK (email IS NULL OR length(email) <= 320),
    oidc_issuer TEXT,
    oidc_subject TEXT,
    password_login INTEGER NOT NULL DEFAULT 0 CHECK (password_login IN (0,1)),
    disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0,1)),
    created_at TEXT NOT NULL,
    last_login_at TEXT,
    UNIQUE (oidc_issuer, oidc_subject),
    CHECK ((oidc_issuer IS NULL) = (oidc_subject IS NULL))
);
CREATE UNIQUE INDEX hosts_single_password ON hosts(password_login) WHERE password_login = 1;
INSERT INTO hosts (id, role, display_name, password_login, created_at)
    VALUES (1, 'admin', 'Administrator', 1, strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- Sessions now name their host. Everyone signs in once after upgrade.
CREATE TABLE host_sessions (
    token_hash TEXT PRIMARY KEY,
    host_id INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX host_sessions_expiry ON host_sessions(expires_at);
CREATE INDEX host_sessions_host ON host_sessions(host_id);
DROP TABLE admin_sessions;

-- SQLite requires a NULL default for an added REFERENCES column while
-- foreign_keys is on, so NOT NULL is enforced by triggers.
ALTER TABLE events ADD COLUMN owner_host_id INTEGER REFERENCES hosts(id);
UPDATE events SET owner_host_id = 1;
CREATE INDEX events_owner ON events(owner_host_id);
CREATE TRIGGER events_owner_insert BEFORE INSERT ON events WHEN NEW.owner_host_id IS NULL
BEGIN SELECT RAISE(ABORT, 'events require an owner host'); END;
CREATE TRIGGER events_owner_update BEFORE UPDATE OF owner_host_id ON events WHEN NEW.owner_host_id IS NULL
BEGIN SELECT RAISE(ABORT, 'events require an owner host'); END;
