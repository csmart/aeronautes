ALTER TABLE job_items ADD COLUMN publication_required INTEGER NOT NULL DEFAULT 0
    CHECK (publication_required IN (0, 1));

-- The early verification remains immutable when a later pathname changes.
CREATE TABLE job_item_publications (
    item_id          INTEGER PRIMARY KEY REFERENCES job_items(id) ON DELETE RESTRICT,
    result           TEXT NOT NULL CHECK (result IN ('verified', 'failed')),
    effect           TEXT NOT NULL,
    diagnostic_stage TEXT NOT NULL,
    diagnostic_code  TEXT NOT NULL,
    http_status      INTEGER NOT NULL,
    copied_bytes     INTEGER NOT NULL CHECK (copied_bytes = 0),
    created_at       TEXT NOT NULL,
    CHECK ((result = 'verified' AND effect = 'not_attempted') OR
           (result = 'failed' AND effect = 'unresolved'))
);
