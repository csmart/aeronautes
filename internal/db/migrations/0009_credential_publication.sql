-- Existing references do not prove whether their key was persistent.
ALTER TABLE bookmarks ADD COLUMN origin_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE bookmarks ADD COLUMN origin_instance TEXT NOT NULL DEFAULT '';
ALTER TABLE bookmarks ADD COLUMN generation INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0);
ALTER TABLE bookmarks ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0);

-- A removed bookmark must not erase the only evidence of an external key.
-- Instance provenance does not claim a verified persistent keychain realm.
CREATE TABLE credential_work (
    secret_ref       TEXT PRIMARY KEY,
    bookmark_id      TEXT NOT NULL,
    origin_kind      TEXT NOT NULL CHECK (origin_kind IN ('', 'session', 'keychain')),
    origin_instance  TEXT NOT NULL,
    state            TEXT NOT NULL CHECK (state IN ('prepared', 'retired')),
    created_at       TEXT NOT NULL,
    CHECK ((origin_kind = '' AND origin_instance = '') OR
           (origin_kind <> '' AND origin_instance <> ''))
);
CREATE INDEX idx_credential_work_bookmark ON credential_work (bookmark_id);
