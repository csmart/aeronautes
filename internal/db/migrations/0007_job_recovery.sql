-- Crash-safe staging records (§65.8 item 2): jobs persist their staging
-- prefix and captured/committed segment prefixes AS THEY RUN, so a dead
-- process's litter is sweepable at the next startup. Rows are cleared
-- when a job's own reconciliation completes; surviving rows mean a
-- crash and drive the startup sweep.
CREATE TABLE job_recovery (
    id          INTEGER PRIMARY KEY,
    job_id      TEXT NOT NULL REFERENCES jobs(id),
    bookmark_id TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('staging','captured','committed')),
    container   TEXT NOT NULL,
    prefix      TEXT NOT NULL
);
CREATE INDEX idx_job_recovery_job ON job_recovery (job_id);
