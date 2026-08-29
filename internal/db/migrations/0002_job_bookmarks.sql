-- Jobs are bookmark-scoped (§47, owner input 2026-08-29): the drawer
-- filters in-flight work by account, and cross-account jobs record
-- source and destination endpoints separately. Nullable: not every job
-- type has both ends.
ALTER TABLE jobs ADD COLUMN source_bookmark_id TEXT REFERENCES bookmarks(id);
ALTER TABLE jobs ADD COLUMN dest_bookmark_id TEXT REFERENCES bookmarks(id);
-- idx_jobs_state already exists from 0001.
CREATE INDEX idx_jobs_bookmarks ON jobs (source_bookmark_id, dest_bookmark_id);
