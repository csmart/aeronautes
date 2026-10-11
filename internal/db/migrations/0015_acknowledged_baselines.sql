-- ADR 0085's upload-overwrite amendment: what the operator was shown at a name
-- when they acknowledged replacing it, so the publication can be compared
-- against it at the fixed point and refused when the name no longer holds it.
--
-- This does NOT displace job_item_predecessors. That record is written by the
-- route immediately before it publishes, and the comparison is between THIS
-- baseline and a fresh observation taken at the fixed point — never between the
-- two stored records, which both describe moments before the preparation the
-- decision refuses to leave inside the window and could therefore agree while
-- the replacement lands between them. Both records exist because a reader of
-- the trail needs the object the operator acknowledged as well as the one the
-- route found.
--
-- The three observed facts are stored rather than a digest of them, so the
-- record stays readable and the comparison derives its own identity. They are
-- HEAD-grade: a listing row cannot establish class, and a dynamic manifest's
-- row is zero-byte carrying the manifest object's hash rather than a digest of
-- what it assembles.
CREATE TABLE job_item_acknowledged_baselines (
    item_id       INTEGER PRIMARY KEY REFERENCES job_items(id) ON DELETE RESTRICT,
    -- class is the large-object classification from an authoritative HEAD.
    class         TEXT NOT NULL CHECK (class <> ''),
    -- length is the logical size: for a manifest the assembled size, not the
    -- stored manifest bytes.
    length        INTEGER NOT NULL CHECK (length >= 0),
    -- etag is the entity tag as the store reported it, unquoted.
    etag          TEXT NOT NULL CHECK (etag <> ''),
    created_at    TEXT NOT NULL
);

-- What the operator was shown cannot be revised. The API refuses a differing
-- re-record, but that guard is only reached through the API: without this the
-- row is editable underneath the store, and a comparison at the fixed point
-- would then match against something the operator may never have seen. The
-- predecessor record carries the same trigger for the same reason.
CREATE TRIGGER job_item_acknowledged_baseline_immutable BEFORE UPDATE ON job_item_acknowledged_baselines
BEGIN
    SELECT RAISE(ABORT, 'an acknowledged baseline cannot change');
END;
