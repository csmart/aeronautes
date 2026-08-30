-- The durable commit protocol (§65.9 item 5): a 'committing' record —
-- persisted BEFORE the live manifest PUT — carries the LIVE identity
-- {object} for a captured segment prefix, so the startup sweep decides
-- from the recorded live name's current manifest instead of deriving a
-- staged name that its own staging sweep may already have deleted.
-- SQLite cannot widen a CHECK, so the table is rebuilt.
CREATE TABLE job_recovery_new (
    id          INTEGER PRIMARY KEY,
    job_id      TEXT NOT NULL REFERENCES jobs(id),
    bookmark_id TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('staging','captured','committing','committed')),
    container   TEXT NOT NULL,
    prefix      TEXT NOT NULL,
    object      TEXT NOT NULL DEFAULT ''
);
INSERT INTO job_recovery_new (id, job_id, bookmark_id, kind, container, prefix)
    SELECT id, job_id, bookmark_id, kind, container, prefix FROM job_recovery;
DROP TABLE job_recovery;
ALTER TABLE job_recovery_new RENAME TO job_recovery;
CREATE INDEX idx_job_recovery_job ON job_recovery (job_id);
