ALTER TABLE upload_sessions ADD COLUMN contributor_name TEXT
    CHECK (contributor_name IS NULL OR length(contributor_name) BETWEEN 1 AND 100);
ALTER TABLE assets ADD COLUMN contributor_name TEXT
    CHECK (contributor_name IS NULL OR length(contributor_name) BETWEEN 1 AND 100);
