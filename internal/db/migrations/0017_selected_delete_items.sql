-- ADR 0093 decision 4: the item ledger carries a deletion. A selected-set
-- deletion admits its members once, marks each as issued before its final
-- HEAD, and records one outcome per member together with what the
-- verifying read observed at the name afterwards.
DROP TRIGGER job_item_version_insert;
CREATE TRIGGER job_item_version_insert BEFORE INSERT ON jobs
WHEN NEW.item_records_version = 1 AND NEW.type NOT IN ('copy', 'upload', 'download', 'delete_selected')
BEGIN
    SELECT RAISE(ABORT, 'item records require a supported transfer or deletion job');
END;

-- The issued mark is written before the member's final HEAD and is never
-- cleared: a member that was issued and has no outcome may have been deleted,
-- and that uncertainty must survive whatever happens to the process.
ALTER TABLE job_items ADD COLUMN issued_at TEXT;
CREATE TRIGGER job_item_issued_immutable BEFORE UPDATE OF issued_at ON job_items
WHEN OLD.issued_at IS NOT NULL AND NEW.issued_at IS NOT OLD.issued_at
BEGIN
    SELECT RAISE(ABORT, 'an issued mark is immutable');
END;

-- What the literal verifying read found at a deleted member's name: nothing,
-- or the object now there. An observation, never an identification of where
-- it came from, and nothing here authorises touching it.
CREATE TABLE job_item_successors (
    item_id    INTEGER PRIMARY KEY REFERENCES job_items(id) ON DELETE RESTRICT,
    present    INTEGER NOT NULL CHECK (present IN (0, 1)),
    class      TEXT NOT NULL,
    length     INTEGER NOT NULL CHECK (length >= 0),
    etag       TEXT NOT NULL,
    created_at TEXT NOT NULL
);
