ALTER TABLE events ADD COLUMN max_assets INTEGER CHECK (max_assets BETWEEN 1 AND 1000000);
ALTER TABLE events ADD COLUMN max_bytes INTEGER CHECK (max_bytes BETWEEN 1 AND 1125899906842624);

-- Legacy grants expire deterministically; ready media and pending finalization
-- remain intact. No challenge tokens, secrets, or guest IP addresses are stored.
ALTER TABLE upload_sessions ADD COLUMN expires_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00Z';
ALTER TABLE upload_sessions ADD COLUMN max_assets INTEGER NOT NULL DEFAULT 100 CHECK (max_assets > 0);
ALTER TABLE upload_sessions ADD COLUMN max_bytes INTEGER NOT NULL DEFAULT 5368709120 CHECK (max_bytes > 0);
ALTER TABLE upload_sessions ADD COLUMN challenge_verified_at TEXT;
CREATE INDEX upload_sessions_expiry ON upload_sessions(expires_at);

-- Old local pending requests did not record their length. Reserve the largest
-- supported file allowance until owned-media cleanup can safely reclaim them.
UPDATE assets SET expected_size_bytes=1073741824
WHERE status='pending' AND expected_size_bytes=0 AND storage_backend_id=1;
