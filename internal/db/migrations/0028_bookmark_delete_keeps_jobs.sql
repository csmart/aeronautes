-- Deleting a bookmark keeps its finished jobs (ADR 0128): their links to
-- it are cleared and its benchmark result goes, in the deletion's own
-- transaction. The jobs table is not rebuilt to change its references,
-- because other tables reference jobs. A job that is not finished keeps
-- its link, so a deletion that slipped past ADR 0015's refusal still fails.
-- The finished states are the same four as jobs.Terminal and the refusal
-- in jobs.Store.ActiveForBookmark; a new state changes all three.
CREATE TRIGGER bookmark_delete_keeps_finished_jobs
BEFORE DELETE ON bookmarks
BEGIN
    UPDATE jobs SET source_bookmark_id = NULL
        WHERE source_bookmark_id = OLD.id AND state IN ('complete', 'partial', 'cancelled', 'failed');
    UPDATE jobs SET dest_bookmark_id = NULL
        WHERE dest_bookmark_id = OLD.id AND state IN ('complete', 'partial', 'cancelled', 'failed');
    DELETE FROM benchmark_results WHERE bookmark_id = OLD.id;
END;
