-- The Keystone v3 user and project named by ID instead of by name and
-- domain (ADR 0113). Every existing row names both by name, which reads
-- as empty through COALESCE in selectColumns.
ALTER TABLE bookmarks ADD COLUMN user_id TEXT;
ALTER TABLE bookmarks ADD COLUMN project_id TEXT;
