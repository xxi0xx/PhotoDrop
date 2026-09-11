-- A deletion that cannot finish stays closed and can be retried safely.
ALTER TABLE events ADD COLUMN deleting INTEGER NOT NULL DEFAULT 0 CHECK (deleting IN (0, 1));

CREATE TABLE upload_sessions (
    id TEXT PRIMARY KEY NOT NULL,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (id, event_id)
);
CREATE INDEX upload_sessions_event ON upload_sessions(event_id);

CREATE TABLE assets (
    id TEXT PRIMARY KEY NOT NULL,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    upload_session_id TEXT NOT NULL,
    original_filename TEXT NOT NULL CHECK (length(original_filename) BETWEEN 1 AND 255),
    storage_key TEXT NOT NULL UNIQUE,
    mime_type TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    status TEXT NOT NULL CHECK (status IN ('pending', 'ready')),
    created_at TEXT NOT NULL,
    completed_at TEXT,
    FOREIGN KEY (upload_session_id, event_id) REFERENCES upload_sessions(id, event_id) ON DELETE CASCADE,
    CHECK (status <> 'ready' OR (size_bytes > 0 AND mime_type <> '' AND completed_at IS NOT NULL))
);
CREATE INDEX assets_event_status ON assets(event_id, status);
CREATE INDEX assets_session ON assets(upload_session_id);
CREATE INDEX assets_pending_cleanup ON assets(created_at) WHERE status = 'pending';
