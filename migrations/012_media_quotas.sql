ALTER TABLE events ADD COLUMN max_photos INTEGER CHECK (max_photos IS NULL OR (max_photos >= 1 AND max_photos <= 1000000));
ALTER TABLE events ADD COLUMN max_videos INTEGER CHECK (max_videos IS NULL OR (max_videos >= 1 AND max_videos <= 1000000));
ALTER TABLE events ADD COLUMN max_photo_file_bytes INTEGER CHECK (max_photo_file_bytes IS NULL OR (max_photo_file_bytes >= 1 AND max_photo_file_bytes <= 1125899906842624));
ALTER TABLE events ADD COLUMN max_video_file_bytes INTEGER CHECK (max_video_file_bytes IS NULL OR (max_video_file_bytes >= 1 AND max_video_file_bytes <= 1125899906842624));
ALTER TABLE events ADD COLUMN max_photo_storage_bytes INTEGER CHECK (max_photo_storage_bytes IS NULL OR (max_photo_storage_bytes >= 1 AND max_photo_storage_bytes <= 1125899906842624));
ALTER TABLE events ADD COLUMN max_video_storage_bytes INTEGER CHECK (max_video_storage_bytes IS NULL OR (max_video_storage_bytes >= 1 AND max_video_storage_bytes <= 1125899906842624));
ALTER TABLE assets ADD COLUMN media_class TEXT CHECK (media_class IS NULL OR media_class IN ('photo','video'));

-- Final MIME for ready rows; only unambiguous declared MIME for pending rows.
-- Never infer historical policy class from filenames.
UPDATE assets SET media_class = CASE
    WHEN (CASE WHEN status='ready' THEN mime_type ELSE expected_mime_type END) IN ('image/jpeg','image/png','image/webp','image/gif','image/heic','image/heif') THEN 'photo'
    WHEN (CASE WHEN status='ready' THEN mime_type ELSE expected_mime_type END) IN ('video/mp4','video/quicktime') THEN 'video'
    ELSE NULL END;
-- Existing assets(event_id) index bounds event and typed quota scans. Each
-- reservation needs both totals, so no additional class index is necessary.
