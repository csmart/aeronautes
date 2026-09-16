-- Resolved native collection identity. Empty remains unverified.
ALTER TABLE bookmarks ADD COLUMN origin_persistent TEXT NOT NULL DEFAULT '';
ALTER TABLE credential_work ADD COLUMN origin_persistent TEXT NOT NULL DEFAULT '';
