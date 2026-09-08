ALTER TABLE jobs ADD COLUMN item_records_version INTEGER NOT NULL DEFAULT 0
    CHECK (item_records_version IN (0, 1));

CREATE TRIGGER job_item_version_insert BEFORE INSERT ON jobs
WHEN NEW.item_records_version = 1 AND NEW.type NOT IN ('copy', 'upload', 'download')
BEGIN
    SELECT RAISE(ABORT, 'item records require a supported transfer job');
END;

-- Enabling recording after dispatch would disguise missing admission history.
CREATE TRIGGER job_item_version_immutable BEFORE UPDATE OF item_records_version ON jobs
WHEN NEW.item_records_version != OLD.item_records_version
BEGIN
    SELECT RAISE(ABORT, 'item record version is immutable');
END;

-- Late progress callbacks must not replace the item-derived terminal snapshot.
CREATE TRIGGER job_item_terminal_summary BEFORE UPDATE OF state, counters_json, error, finished_at ON jobs
WHEN OLD.item_records_version = 1 AND OLD.state IN ('complete', 'partial', 'cancelled', 'failed')
BEGIN
    SELECT RAISE(ABORT, 'recorded transfer summary is immutable');
END;

CREATE TABLE job_items (
    id               INTEGER PRIMARY KEY,
    job_id           TEXT NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT,
    item_key         TEXT NOT NULL COLLATE BINARY,
    source_json      TEXT NOT NULL,
    destination_json TEXT NOT NULL,
    staging_json     TEXT,
    planned          INTEGER NOT NULL CHECK (planned IN (0, 1)),
    admitted         INTEGER NOT NULL CHECK (admitted IN (0, 1)),
    listed_bytes     INTEGER NOT NULL CHECK (listed_bytes >= 0),
    created_at       TEXT NOT NULL,
    UNIQUE (job_id, item_key)
);
CREATE INDEX idx_job_items_page ON job_items (job_id, id);

CREATE TABLE job_item_observations (
    item_id          INTEGER NOT NULL REFERENCES job_items(id) ON DELETE RESTRICT,
    phase            TEXT NOT NULL CHECK (phase IN ('execution', 'verification')),
    result           TEXT NOT NULL,
    effect           TEXT NOT NULL,
    diagnostic_stage TEXT NOT NULL,
    diagnostic_code  TEXT NOT NULL,
    http_status      INTEGER NOT NULL,
    copied_bytes     INTEGER NOT NULL CHECK (copied_bytes >= 0),
    created_at       TEXT NOT NULL,
    PRIMARY KEY (item_id, phase)
);
