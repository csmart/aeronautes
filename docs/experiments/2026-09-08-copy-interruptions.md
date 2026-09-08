# Copy interruption outcome reproductions — 8 September 2026

## Scope and source

This is the separate earlier-report/later-error work scheduled by [ADR 0026](../adr/0026-copy-job-completion.md), after its atomic Copy summary migration.
The baseline ran on independently approved source `c8edb9c0cf5a605efb247d3065d32a9fb2d067fc` while PR56 CI ran.
The protected PR56 merge later preserved that exact tree at `efbbc00cda33bbd1c79c8ae284c41bcc245c7af3`.
One external Go test overlay adds only a system-test file; it substitutes no application, store, executor, engine or shared fixture implementation.
The work does not lift the valuable-data hold.

## Fixture and observable boundary

Actual StartCopy runs against owned mock Swift and two-account SAIO fixtures with the official pinned rclone 1.74.3.
Go 1.27.1 runs the cases with race detection and required two-account configuration.
Source and destination belong to different accounts, and the destination bookmark is active in the browser when Copy captures the source authority.
Selected tiny objects include native newline names, a lookahead engine name and a later engine name.
The successful control exercises both the native path and real engine batch.
The destination also contains another job's staging sentinel and shared DLO/SLO aliases outside the selected destination container; both complete account snapshots cover their backing bytes.

A fixture HTTP proxy requests one-object source listing pages while preserving the real service, listing iterator and planner.
The planner advances its source before emitting a name, so the selected page after the lookahead engine name occurs after the earlier native copy, refused source GET or overwrite refusal has been reported, but before that engine name is emitted.
The existing strict HTTP response barrier holds that exact method, escaped container path, canonical query and first occurrence.
The test requires its acknowledged response and the running job before proceeding.
For cancellation, actual CancelJob must end the selected exchange and the barrier must record context cancellation.
For a later error, the selected response is a controlled HTTP 403 and is explicitly released.
No elapsed sleep is used to infer that execution reached the selected boundary.

After execution, successful publication of each owned credential acknowledges release of the job's captured bindings and deferred body cleanup.
The tests compare the public and durable job records, final counters, terminal audit operation/result/resource/source bookmark, retained staging recovery under the destination bookmark and cancellation deregistration.
They compare both complete namespaces, exact copied bytes and all unselected names and shared aliases, then close and reopen the database in a child process and compare the retained records with no remote traffic.
All 26 cases reach the saved observation and orderly child-reopen checks.
These are remote newline names, with no newline local-file dependency or explicit Windows skip.
The executed evidence is Linux evidence, not six-platform proof.

## Before-state results

The complete baseline has 26 leaf cases: **16 intended failures, 10 passing controls and zero skips**.
Its Go producer exits 1 because of those intended assertions.
The two parent test failures are not additional leaf cases.
There are no unexpected fixture, collateral, counter, recovery or child-reopen diagnostics.

The following results occur on both providers:

| Earlier report and later reason | Observed before-state | Required result |
| --- | --- | --- |
| One native copy, then cancellation | `cancelled`, copied 1, verified 0, empty error | `partial`, copied 1, with wrapped cancellation |
| One native copy, then listing refusal | `failed`, copied 1, verified 0 | `partial`, with the listing error |
| One native failure, then cancellation | `cancelled`, failed 1, empty error | `partial`, failed-count/first-failure detail and cancellation |
| One native failure, then listing refusal | `failed`, failed 1, only the later listing error | `partial`, earlier failure and later error |
| One native copy plus one failure, then cancellation or listing refusal | Plain `cancelled` or `failed`; counters survive but earlier detail does not | `partial`, exact copied/failed counts, earlier failure and later reason |
| One failure plus one overwrite refusal, then cancellation or listing refusal | `partial` with refusal and later reason, but no earlier failure detail | Preserve all three reasons and independent counts |
| Refusal only, then cancellation or listing refusal | Existing `partial`, one refusal, zero copied/failed and both refusal/later reasons | Unchanged control |
| Cancellation before the first source page is released to the planner | `cancelled`, zero counters and empty error | Unchanged control |
| Listing refusal before any reported work | `failed`, zero counters and listing error | Unchanged control |
| Successful native-plus-engine Copy | `complete`, three copied and verified objects, thirty bytes, one native route | Unchanged control |

The tests independently observe only the expected copied objects and no collateral changes.
The two names beyond the interruption remain absent at the destination and do not enter planned, copied, failed or verified counts.
The current terminal write is atomic, so its audit consistently repeats the wrong state; this is a caller outcome-selection defect, not another transaction failure.

Raw commands, exits, source and overlay hashes, Go event streams and 26 parsed job/audit/recovery observations are retained under `/home/csmart/code/.aeronautes-work/20260908/copy-interruption-*`.
The complete producer is `copy-interruption-before-v1`; `copy-interruption-smoke-v1` is the earlier four-case fixture check and is not added to the complete case count.

## Decision and limits

[ADR 0027](../adr/0027-copy-interruption-outcomes.md) proposes the narrow terminal-branch correction and maintained regressions.
No production implementation is included in this reproduction record.
The existing choice between simultaneous plan and Finish errors and logged close/reconcile failures remains separate follow-up work.
The later verification-stream error branch also needs separate reproduction and review; the current fixtures interrupt before that phase.
Conflict-only reports and complete conflict details beside a later error remain wider outcome work and are not covered by these fixtures.
Identity accounting beyond the 200-failure sample, unknown acceptance, stronger verification, durable per-effect records, concurrency and cleanup authority, native child lifetime and the valuable-data release hold remain open.
Orderly child-process reopening is not process-kill, fsync, lost-history or power-loss evidence.
The controlled HTTP failures are observations from owned fixtures and do not imply safe retry or zero effects for arbitrary failed jobs.
