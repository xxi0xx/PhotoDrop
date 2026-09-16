-- Secrets remain in runtime configuration. A key is a durable server identity.
CREATE TABLE immich_targets (
    id INTEGER PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE immich_event_imports (
    id INTEGER PRIMARY KEY,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    target_id INTEGER NOT NULL REFERENCES immich_targets(id),
    album_name TEXT NOT NULL,
    album_marker TEXT NOT NULL UNIQUE,
    album_state TEXT NOT NULL DEFAULT 'new' CHECK (album_state IN ('new','creating','ready')),
    immich_album_id TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (event_id, target_id)
);

CREATE TABLE immich_asset_imports (
    event_import_id INTEGER NOT NULL REFERENCES immich_event_imports(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    immich_asset_id TEXT,
    duplicate INTEGER NOT NULL DEFAULT 0 CHECK (duplicate IN (0,1)),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','importing','imported','duplicate','failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    imported_at TEXT,
    PRIMARY KEY (event_import_id, asset_id)
);

CREATE TABLE integration_jobs (
    id INTEGER PRIMARY KEY,
    event_import_id INTEGER NOT NULL REFERENCES immich_event_imports(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','completed','failed','cancelled')),
    mode TEXT NOT NULL CHECK (mode IN ('new','retry')),
    cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK (cancel_requested IN (0,1)),
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX integration_jobs_active ON integration_jobs(event_import_id) WHERE status IN ('queued','running');
CREATE INDEX integration_jobs_queue ON integration_jobs(status,id);
