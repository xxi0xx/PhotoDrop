ALTER TABLE assets ADD COLUMN storage_provider TEXT NOT NULL DEFAULT 'local' CHECK (storage_provider IN ('local', 's3'));
ALTER TABLE assets ADD COLUMN storage_target TEXT NOT NULL DEFAULT '';
ALTER TABLE assets ADD COLUMN expected_size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (expected_size_bytes >= 0);
ALTER TABLE assets ADD COLUMN expected_mime_type TEXT NOT NULL DEFAULT '';
ALTER TABLE assets ADD COLUMN authorized_until TEXT;
ALTER TABLE assets ADD COLUMN client_request_id TEXT;
CREATE UNIQUE INDEX assets_client_request ON assets(upload_session_id, client_request_id) WHERE client_request_id IS NOT NULL;

-- A PUT URL cannot be revoked. Keep retired keys independently of event rows
-- so startup can remove objects recreated by late/replayed PUTs after deletion.
-- Keys are never reused. This table stores no URLs or credentials.
CREATE TABLE s3_cleanup (
    storage_key TEXT PRIMARY KEY NOT NULL,
    storage_target TEXT NOT NULL,
    event_id INTEGER NOT NULL,
    asset_id TEXT NOT NULL,
    checked_at TEXT NOT NULL
);
CREATE INDEX s3_cleanup_checked ON s3_cleanup(checked_at);
