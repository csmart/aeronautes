-- The last container settings seen per bookmark, storage URL and container,
-- shown muted on a later connection and used for nothing with authority
-- (ADR 0110 decision 3).
CREATE TABLE container_settings_seen (
    bookmark_id   TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
    storage_url   TEXT NOT NULL,
    container     TEXT NOT NULL,
    observed_at   TEXT NOT NULL,
    settings_json TEXT NOT NULL,
    PRIMARY KEY (bookmark_id, storage_url, container)
);
