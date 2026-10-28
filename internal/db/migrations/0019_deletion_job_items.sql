-- ADR 0095 decision 2 and decision 8: every deletion job keeps the durable
-- item ledger, a prefix, a container's contents, a container and a manifest
-- with its segments as the selected set already does, so their members
-- are admitted a listing page at a time, issued before their final HEAD
-- and settled one outcome each.
DROP TRIGGER job_item_version_insert;
CREATE TRIGGER job_item_version_insert BEFORE INSERT ON jobs
WHEN NEW.item_records_version = 1 AND NEW.type NOT IN ('copy', 'upload', 'download', 'delete_selected',
    'delete_prefix', 'empty_container', 'delete_container', 'delete_segments')
BEGIN
    SELECT RAISE(ABORT, 'item records require a supported transfer or deletion job');
END;
