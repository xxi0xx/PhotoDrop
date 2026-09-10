CREATE TABLE events (
    id INTEGER PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE CHECK (length(public_id) = 24),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 4000),
    event_date TEXT,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TRIGGER events_public_id_immutable
BEFORE UPDATE OF public_id ON events
WHEN NEW.public_id <> OLD.public_id
BEGIN
    SELECT RAISE(ABORT, 'public event identifiers are immutable');
END;
