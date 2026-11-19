# ADR 0129 — a job you stopped is incomplete, not failed

**Status:** Proposed on 19 November 2026, from the operator's decision of the same day.

## Context

A browser journey quit the application while an upload was running and part of its file had landed.
The job ended partial, as [ADR 0122](0122-the-stores-answer-is-the-result.md) decision 7 requires once something was written, and the drawer counted the file the engine was writing as "1 failed".
The file's item record says why: its execution failed with the code `cancelled`, because the engine was stopped, not because the store or the file was at fault.
The operator asked for a word that is neither a failure nor a promise that the job can be started again, since a cancelled upload or download cannot be (only a paused one resumes, and a copy is re-run as a new job), and chose "incomplete".

## Decision

1. **A file the engine was stopped on is incomplete.**
   An item whose execution failed with the code `cancelled` is counted as incomplete, not failed, in the job's counts and in its item totals, and its record in the item details reads "incomplete".
   It is still not complete, and a job holding one is never complete.
2. **A job you stopped after something landed reads "cancelled, incomplete".**
   Where a job ended partial and holds incomplete items, its badge reads "cancelled, incomplete" and its counts read, for example, "2 copied · 1 incomplete".
   A job stopped before anything landed reads "cancelled", as now.
   The state stored is unchanged (partial), so a job that wrote something is never recorded as if nothing happened.
3. **"Failed" is for real errors.**
   A store's refusal, a file that could not be read, or an engine error that was not a stop still count and read as failed, beside any incomplete ones.

## What does not change

The states a job can reach and when it reaches them; what the item ledger records; and the audit trail's summary, whose "failed names" still counts every failed execution, incomplete ones included.

## Required proof (tier 2)

Go: a job finished from items holding one item failed with code `cancelled` and one failed with another code reports one failed and one incomplete in its counts and item totals.
Frontend: a partial job with incomplete items shows "cancelled, incomplete" and "1 incomplete"; an item failed with code `cancelled` reads "incomplete".
Journey, on the mock: an upload slowed by a bandwidth limit and cancelled from the drawer after part of it landed reads "cancelled, incomplete" with no failed count.
