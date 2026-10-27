-- ADR 0093 decisions 3 and 4: a deletion member's record carries the facts
-- it was admitted with, the baseline its comparison was made against, and
-- what the comparison or the verifying read found at the name, so a member
-- changed since it was listed keeps both identities durably.
ALTER TABLE job_items ADD COLUMN listed_etag TEXT NOT NULL DEFAULT '';
ALTER TABLE job_items ADD COLUMN listed_slo INTEGER NOT NULL DEFAULT 0 CHECK (listed_slo IN (0, 1));
ALTER TABLE job_items ADD COLUMN baseline_class TEXT NOT NULL DEFAULT '';
ALTER TABLE job_items ADD COLUMN baseline_length INTEGER NOT NULL DEFAULT 0 CHECK (baseline_length >= 0);
ALTER TABLE job_items ADD COLUMN baseline_etag TEXT NOT NULL DEFAULT '';

-- The baseline is written once, with the member's outcome, and never moves:
-- it is the identity the DELETE was or was not issued against.
CREATE TRIGGER job_item_baseline_immutable BEFORE UPDATE OF baseline_class, baseline_length, baseline_etag ON job_items
WHEN OLD.baseline_class != '' AND (NEW.baseline_class != OLD.baseline_class
    OR NEW.baseline_length != OLD.baseline_length OR NEW.baseline_etag != OLD.baseline_etag)
BEGIN
    SELECT RAISE(ABORT, 'a member baseline is immutable');
END;

-- Which read observed the successor: the verifying read after the DELETE,
-- or the comparison HEAD that found the member changed before any DELETE.
ALTER TABLE job_item_successors ADD COLUMN role TEXT NOT NULL DEFAULT 'after' CHECK (role IN ('after', 'comparison'));
