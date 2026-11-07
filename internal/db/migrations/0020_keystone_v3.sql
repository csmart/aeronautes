-- Keystone v3 bookmark auth (ADR 0100). A bookmark names its auth version
-- and, for v3, the password-method project-scope identity. Every existing
-- row is TempAuth v1, so auth_version defaults to 1; the identity columns
-- are NULL for v1 and read back as '' through COALESCE in selectColumns.
ALTER TABLE bookmarks ADD COLUMN auth_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE bookmarks ADD COLUMN user_domain TEXT;
ALTER TABLE bookmarks ADD COLUMN project_name TEXT;
ALTER TABLE bookmarks ADD COLUMN project_domain TEXT;
ALTER TABLE bookmarks ADD COLUMN region TEXT;
