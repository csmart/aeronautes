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
The destination also contains another job's staging sentinel and shared DLO/SLO aliases outside the selected destination container; both complete fixture namespace snapshots cover their backing bytes.
Snapshots include every container under that harness's unique prefix, including derived sidecars, and do not observe containers outside that prefix.

A fixture HTTP proxy requests one-object source listing pages while preserving the real service, listing iterator and planner.
The planner advances its source before emitting a name, so the selected page after the lookahead engine name occurs after the earlier native copy, refused source GET or overwrite refusal has been reported, but before that engine name is emitted.
The existing strict HTTP response barrier holds that exact method, escaped container path, canonical query and first occurrence.
The test requires its acknowledged response and the running job before proceeding.
For cancellation, actual CancelJob must end the selected exchange and the barrier must record context cancellation.
For a later error, the selected response is a controlled HTTP 403 and is explicitly released.
No elapsed sleep is used to infer that execution reached the selected boundary.

After execution, successful publication of each owned credential acknowledges release of the job's captured bindings and deferred body cleanup.
The tests compare the public and durable job records, final counters, terminal audit operation/result/resource/source bookmark, retained staging recovery under the destination bookmark and cancellation deregistration.
They compare both complete fixture namespaces, exact copied bytes and all unselected names and shared aliases within those namespaces, then close and reopen the database in a child process and compare the retained records with no remote traffic.
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
The before-state record above precedes the implementation below, which requires its own independent review.
The existing choice between simultaneous plan and Finish errors and logged close/reconcile failures remains separate follow-up work.
The later verification-stream error branch also needs separate reproduction and review; the current fixtures interrupt before that phase.
Conflict-only reports and complete conflict details beside a later error remain wider outcome work and are not covered by these fixtures.
Identity accounting beyond the 200-failure sample, unknown acceptance, stronger verification, durable per-effect records, concurrency and cleanup authority, native child lifetime and the valuable-data release hold remain open.
Orderly child-process reopening is not process-kill, fsync, lost-history or power-loss evidence.
The controlled HTTP failures are observations from owned fixtures and do not imply safe retry or zero effects for arbitrary failed jobs.

## Maintained implementation and evidence

After independent design approval, the Copy caller now examines its final execution report before choosing the terminal result for the existing selected run error.
Reported failures produce partial state with the failed-count/first-failure summary and complete later reason; a copied subset without failures produces partial state with the later reason.
The existing zero-outcome, verification, atomic-completion and overwrite-refusal paths remain intact.
The new rule runs before the refusal finaliser, so a refusal, earlier failure and later reason all survive together.
No executor, admission, schema, DTO, UI, cleanup or cancellation boundary changes.

`internal/systemtest/copy_interruption_test.go` maintains all 26 actual-service cases from the baseline, including the before-work and refusal-only controls and successful native-plus-engine case.
Partial cancellation assertions retain the source-listing wrapper, HTTP request context, selected marker and cancellation reason.
The existing `TestCancelCopyJob` keeps its original engine barrier, selected name and body; only the expectation changes, explicitly requiring one reported failure, zero copied/verified items and partial state with both the engine failure and cancellation.
The focused run passes all 27 leaf cases and both system-test parents across the app and system-test packages.
The snapshots cover each complete owned fixture namespace in both accounts, including its derived containers, aliases and other-job staging; names outside those namespace prefixes remain outside this witness.

Restoring the exact old caller fails seventeen leaf cases: the sixteen service defects and the original attempted engine-cancellation input with its corrected assertion.
All ten original passing service controls still pass with that caller.
Ten further broken variants are detected independently: omitted earlier failure, omitted later reason, ignored copied subset, ignored failed outcomes, completion taking precedence over failure details, corrupted copied count, invented failures for the two unattempted names, dropped wrapped cancellation, erased overwrite refusal and incorrectly partial zero-outcome interruptions.
Every variant has actual failing test assertions and zero skips, and the external Go overlays leave application sources unchanged.

All 852 frontend tests pass in 22 files at 79.33% statement coverage, and Svelte reports zero errors and warnings.
Frontend build, Go vet, native Linux build, the CGO-disabled internal build and patched Swift source verification pass.
The first full race run passes 607 top-level tests and 1,592 leaf cases in twenty packages with zero failures or skips.
That producer omitted CI's `-coverpkg=./internal/...` setting; its 79.9% coverage figure measures a different instrumentation scope and is not compared with the earlier cross-package figure.
The run and its coverage profile remain preserved as `copy-interruption-full-v1` and `copy-interruption-cover.out`.
The final `copy-interruption-full-v2` run includes `-coverpkg=./internal/...` and again passes all 607 top-level tests, 1,592 leaf cases and twenty packages without failures or skips, at 85.3% statement coverage.
It uses Go 1.27.1, `-race -count=1`, the required isolated keychain fixture, pinned official rclone and two-account SAIO.
All tested Go source hashes match the final implementation.
Schema-3 scenario reconciliation is valid and remains 32/110 cells with 38/49 actions gapped; these regressions invent no additional action credit.
Raw commands, exits, source and overlay hashes, controls, checks and final coverage evidence are retained under `/home/csmart/code/.aeronautes-work/20260908/copy-interruption-*`, including `copy-interruption-full-evidence-v2.json` and `copy-interruption-controls-final.json`.
Independent local implementation review and all five PR/master checks remain delivery gates.
The complete ledger and wider Copy error, authority, durability, native and valuable-data gates remain open.

## Delivery

Independent local review approved the design and final implementation, including the unchanged original engine-cancellation input, complete wrapped interruption reasons and separate overwrite refusals.
The implementation reviewer also passed the full Go race and frontend suites and detected nine independent broken variants, including a cancellation-first precedence error.
All five [PR57 checks](https://github.com/csmart/aeronautes/actions/runs/34199257000) succeeded on reviewed source `852a3564f23a3ec1254baed415f8a71adffb0a22` with current master `efbbc00cda33bbd1c79c8ae284c41bcc245c7af3` incorporated.
[PR57](https://github.com/csmart/aeronautes/pull/57) merged through the protected rebase operation at 07:39:49 UTC on 8 September 2026.
All five [resulting master checks](https://github.com/csmart/aeronautes/actions/runs/34200460589) succeeded on `87df5495aaa1711ad2bcc506104495087580dca8`.
The merged tree `2b0c7d12fa91b3c18af2aa8dba4a8de741b0d40e` exactly matches the reviewed source, and all three logical commits remain equal patches after the protected merge.
This delivers the bounded Copy interruption-reporting correction; the complete identity ledger, other Copy error paths, authority, crash and native evidence and valuable-data hold remain open.
