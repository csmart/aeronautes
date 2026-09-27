-- ADR 0081 decisions 5 and 7: what was live at a name immediately before an
-- acknowledged overwrite published over it, and the segment objects a
-- replaced manifest named.
--
-- The names are durable because the manifest that held them is gone
-- afterwards and the set cannot be recovered, and because a count cannot
-- tell the explicit, force-gated cleanup which objects it would be
-- clearing. Nothing here authorises removing any of them: creation
-- provenance is not authority over other references.
CREATE TABLE job_item_predecessors (
    item_id        INTEGER PRIMARY KEY REFERENCES job_items(id) ON DELETE RESTRICT,
    -- present is 0 when the name held nothing at the moment of publishing
    -- although the preflight found it live, which is what the race this
    -- contract admits looks like from here.
    present        INTEGER NOT NULL CHECK (present IN (0, 1)),
    etag           TEXT NOT NULL,
    length         INTEGER NOT NULL CHECK (length >= 0),
    last_modified  TEXT NOT NULL,
    class          TEXT NOT NULL,
    -- prefix is the dynamic manifest's container and prefix, which stays
    -- listable after the replacement and is empty for anything else.
    prefix         TEXT NOT NULL,
    -- counted is how many segment entries were read; it exceeds the number
    -- of fragment rows when the per-item cap stopped naming them.
    counted        INTEGER NOT NULL CHECK (counted >= 0),
    inline_entries INTEGER NOT NULL CHECK (inline_entries >= 0),
    ranged_entries INTEGER NOT NULL CHECK (ranged_entries >= 0),
    nested_entries INTEGER NOT NULL CHECK (nested_entries >= 0),
    -- exact is 1 only when the entries read are the whole set the manifest
    -- named. A listed prefix is a lower bound and an unrecognised entry
    -- shape makes the rest of the list not the whole of it.
    exact          INTEGER NOT NULL CHECK (exact IN (0, 1)),
    -- unread is why no inventory could be taken, empty when one was.
    unread         TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    CHECK ((present = 1) OR (etag = '' AND length = 0 AND class = '' AND counted = 0))
);

CREATE TABLE job_item_predecessor_fragments (
    item_id   INTEGER NOT NULL REFERENCES job_item_predecessors(item_id) ON DELETE RESTRICT,
    -- position is the order the inventory read them in, so the record is
    -- the manifest's own order and not a set.
    position  INTEGER NOT NULL CHECK (position >= 0),
    -- shape is what the entry referenced. It is per entry and not only a
    -- total, because after the replacement this record is the only thing
    -- that can say which name was a containing object, which was a
    -- manifest of its own, and where data carried inside the manifest sat.
    shape     TEXT NOT NULL CHECK (shape IN ('whole', 'ranged', 'nested', 'ranged_nested', 'inline', 'unknown')),
    -- An inline or unrecognised entry names no remote object, so it holds
    -- its position with no name; every other shape names one.
    container TEXT NOT NULL,
    object    TEXT NOT NULL,
    PRIMARY KEY (item_id, position),
    CHECK ((shape IN ('inline', 'unknown') AND container = '' AND object = '') OR
           (shape NOT IN ('inline', 'unknown') AND container != '' AND object != ''))
);

-- An observation of what was there cannot be revised: a second reading
-- would be of something else.
CREATE TRIGGER job_item_predecessor_immutable BEFORE UPDATE ON job_item_predecessors
BEGIN
    SELECT RAISE(ABORT, 'an observed predecessor cannot change');
END;

CREATE TRIGGER job_item_predecessor_fragment_immutable BEFORE UPDATE ON job_item_predecessor_fragments
BEGIN
    SELECT RAISE(ABORT, 'a recorded fragment name cannot change');
END;
