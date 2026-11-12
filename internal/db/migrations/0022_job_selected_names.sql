-- A copy or a download of selected objects records how many were selected,
-- and a copy also records their names, so Re-run plans the same members
-- (ADR 0108 decision 5). Zero means the job acted on its whole scope.
ALTER TABLE jobs ADD COLUMN selected_members INTEGER NOT NULL DEFAULT 0;

CREATE TABLE job_selected_names (
    job_id  TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    name    TEXT NOT NULL,
    PRIMARY KEY (job_id, ordinal)
);
