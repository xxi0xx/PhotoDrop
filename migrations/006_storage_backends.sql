CREATE TABLE storage_backends (
    id INTEGER PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL CHECK (type IN ('local', 's3')),
    endpoint TEXT NOT NULL DEFAULT '',
    bucket TEXT NOT NULL DEFAULT '',
    region TEXT NOT NULL DEFAULT '',
    path_style INTEGER NOT NULL DEFAULT 0 CHECK (path_style IN (0, 1)),
    prefix TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
INSERT INTO storage_backends(id,key,type,created_at)
VALUES(1,'local-default','local',strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- Gate 4 S3 fingerprints cannot be reversed into addressing metadata. Startup
-- binds NULL S3 associations transactionally using explicitly configured keys.
ALTER TABLE assets ADD COLUMN storage_backend_id INTEGER REFERENCES storage_backends(id);
UPDATE assets SET storage_backend_id=1 WHERE storage_provider='local';
CREATE INDEX assets_backend ON assets(storage_backend_id);
ALTER TABLE s3_cleanup ADD COLUMN storage_backend_id INTEGER REFERENCES storage_backends(id);
CREATE INDEX s3_cleanup_backend ON s3_cleanup(storage_backend_id);

CREATE TRIGGER assets_backend_required BEFORE INSERT ON assets
WHEN NEW.storage_backend_id IS NULL
BEGIN SELECT RAISE(ABORT,'asset storage backend is required'); END;
CREATE TRIGGER assets_backend_immutable BEFORE UPDATE OF storage_backend_id ON assets
WHEN OLD.storage_backend_id IS NOT NULL AND NEW.storage_backend_id IS NOT OLD.storage_backend_id
BEGIN SELECT RAISE(ABORT,'asset storage backend is immutable'); END;
CREATE TRIGGER cleanup_backend_required BEFORE INSERT ON s3_cleanup
WHEN NEW.storage_backend_id IS NULL
BEGIN SELECT RAISE(ABORT,'cleanup storage backend is required'); END;
CREATE TRIGGER cleanup_backend_immutable BEFORE UPDATE OF storage_backend_id ON s3_cleanup
WHEN OLD.storage_backend_id IS NOT NULL AND NEW.storage_backend_id IS NOT OLD.storage_backend_id
BEGIN SELECT RAISE(ABORT,'cleanup storage backend is immutable'); END;
CREATE TRIGGER storage_backend_immutable BEFORE UPDATE ON storage_backends
BEGIN SELECT RAISE(ABORT,'storage backend is immutable; choose a new backend key'); END;
CREATE TRIGGER storage_backend_retained BEFORE DELETE ON storage_backends
BEGIN SELECT RAISE(ABORT,'storage backend identity must be retained'); END;
