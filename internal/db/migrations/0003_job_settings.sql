-- Per-job effective settings (§45): the exact typed overrides a job ran
-- with, recorded at start — what the operator can hold the job to.
ALTER TABLE jobs ADD COLUMN settings_json TEXT;
