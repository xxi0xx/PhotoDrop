-- Existing bindings remain manual-only. API server identity is unchanged.
ALTER TABLE immich_event_imports ADD COLUMN auto_import INTEGER NOT NULL DEFAULT 0 CHECK (auto_import IN (0,1));
CREATE INDEX immich_auto_bindings ON immich_event_imports(id) WHERE auto_import=1 AND album_state='ready';
