-- ADR 0088: what a restart interrupted, recorded as a fact so the operator can
-- be told and can dismiss it.
--
-- Three nullable columns rather than two. `FailInterrupted` overwrites the
-- state of every non-terminal job with 'failed', and `Transition` stamps
-- started_at only on entering 'running' while a download reaches 'running'
-- through two separate transactions — so a job interrupted during 'planning'
-- carries a NULL started_at and, after the sweep, is indistinguishable from one
-- that was still 'queued'. The disclosure's most important sentence is whether
-- any request was made at all, so the state the sweep overwrote is retained
-- rather than inferred.
--
-- Keying the notice on a column rather than on the reason sentence is
-- deliberate: ADR 0020 owns that sentence and has already changed it once, and
-- a disclosure that breaks when the wording improves is not worth having.
ALTER TABLE jobs ADD COLUMN interrupted_at TEXT;

-- The state the sweep overwrote. Only the non-terminal states can be
-- overwritten, because those are exactly the ones the sweep selects; a terminal
-- value here would mean the sweep classified a job it should not have seen.
ALTER TABLE jobs ADD COLUMN interrupted_state TEXT
    CHECK (interrupted_state IS NULL
           OR interrupted_state IN ('queued', 'planning', 'running', 'verifying'));

-- When the operator was TOLD. This records a disclosure and never a cleanup:
-- the recovery records stay, the retained-record summary keeps counting them,
-- and nothing about the job's outcome changes. The notice is derived on read as
-- interrupted_at IS NOT NULL AND interrupt_disclosed_at IS NULL, so a crash
-- during the disclosure loses nothing and the notice returns until answered.
ALTER TABLE jobs ADD COLUMN interrupt_disclosed_at TEXT;

-- The two interruption facts are written together, in the sweep's own
-- transaction, and a disclosure cannot precede the interruption it discloses.
CREATE TRIGGER job_interruption_facts_consistent BEFORE UPDATE ON jobs
WHEN (NEW.interrupted_at IS NULL) <> (NEW.interrupted_state IS NULL)
  OR (NEW.interrupt_disclosed_at IS NOT NULL AND NEW.interrupted_at IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'an interruption is recorded as a time AND the state it overwrote, and only an interrupted job can be disclosed');
END;

-- ADR 0088 decision 3: dismissal is a disclosure fact and never a cleanup
-- claim. Enforced here rather than trusted to the caller, because the
-- difference between "you were told" and "it was dealt with" is the whole point
-- of the decision: a write that records the telling must not also revise the
-- outcome being told about. Any write that assigns the disclosure column is
-- held to that, not only the first disclosure: a repeat write on an already
-- disclosed job that holds no item records is otherwise unguarded (migration
-- 0010 protects terminal rows at item_records_version 1 only), and review
-- showed such a write replacing the state, counters and error.
CREATE TRIGGER job_disclosure_revises_nothing BEFORE UPDATE OF interrupt_disclosed_at ON jobs
WHEN NEW.state <> OLD.state
  OR COALESCE(NEW.counters_json, '') <> COALESCE(OLD.counters_json, '')
  OR COALESCE(NEW.error, '') <> COALESCE(OLD.error, '')
BEGIN
    SELECT RAISE(ABORT, 'disclosing an interruption cannot change the job state, counters or error');
END;
