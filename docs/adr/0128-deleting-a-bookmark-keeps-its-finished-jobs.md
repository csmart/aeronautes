# ADR 0128 — deleting a bookmark keeps its finished jobs

**Status:** Proposed on 19 November 2026, from a bug the browser journeys found.

## Context

A browser journey written for test plan step 4c made a second bookmark, uploaded through it, and deleted it.
The editor showed `constraint failed: FOREIGN KEY constraint failed (787)` and the bookmark stayed on the rail.
Any bookmark that has ever run a job cannot be deleted.

Migration 0002 gave `jobs` two columns, `source_bookmark_id` and `dest_bookmark_id`, that reference `bookmarks(id)` with no rule for a deletion, so SQLite refuses to delete a bookmark any job names; migration 0005 gave `benchmark_results.bookmark_id` the same reference.
[ADR 0015](0015-credential-bindings.md) already refuses a deletion while a running or queued job holds the bookmark, in words, before the store is asked; nothing decided what happens to the jobs that have finished.
The audit trail keeps a deleted bookmark's id as plain text and labels it unavailable ([ADR 0064](0064-audit-account-attribution.md) decision 5).

## Decision

1. **A bookmark with finished jobs can be deleted.**
   Its complete, partial, cancelled and failed jobs stay in the job history with everything they recorded: their route, counts, items and errors.
   Their link to the deleted bookmark is cleared, so they show no bookmark and the drawer's account filter lists them under all accounts only.
   Such a copy job is not re-run: re-running it is refused in words, since it has no bookmark to run against, and a new copy is started instead.
2. **A running, queued or paused job still refuses the deletion**, with ADR 0015's words naming how many jobs use it.
3. **The bookmark's benchmark result goes with it**: it describes the deleted bookmark's endpoint and nothing else reads it.
4. **The database does it, in the deletion's own transaction**: a trigger before a bookmark's deletion clears the finished jobs' links and removes its benchmark result.
   Rebuilding the jobs table to change the reference is not done: other tables reference `jobs`, and rebuilding it with foreign keys on would delete their rows.
   A job that is not finished keeps its link, so a deletion that slipped past the refusal still fails rather than orphan a job that is running.

## What does not change

The audit trail's records and how it labels a deleted bookmark.
Recovery records, which keep their bookmark id as text.
The credential cleanup after a deletion (ADR 0076).

## Required proof (tier 2)

Go: a bookmark whose complete, partial, failed and cancelled jobs name it as source and destination is deleted; the jobs remain with their records and no bookmark; a queued job still refuses the deletion with ADR 0015's words; a benchmark result goes with its bookmark; re-running a copy that lost its bookmark is refused in words.
Journey, on the mock: a second bookmark runs a job and is deleted from its editor; it leaves the rail and the job stays in the history.
