# Copy completion and admission reproduction

## Scope

The reproduction ran against master `b5ca0f3ac4663e5b496ab210be6b3bdd60b3a694`, whose production tree is identical to the independently reviewed PR55 source.
That PR and its resulting master have passed all five required jobs.
The isolated `copy_completion` worktree contained no changes when the probe ran.
One Go overlay added only an external system-test file; no application, executor, store or fixture source was replaced.

Both mock Swift and two-account SAIO used the actual StartCopy service, the pinned official rclone 1.74.3 and the native fallback for a newline-containing remote name.
The ordinary selected set contains two plain ten-byte objects, one native and one engine-routed.
Mixed and all-refused controls add a changed existing destination whose bytes must remain intact and whose refusal must not count as a failed or copied object.
Each fixture snapshots both complete owned account namespaces, including an unselected source, another job's staging sentinel and shared DLO/SLO aliases and backing bytes.
The independent witness uses its own direct account connections rather than the service proxies.

Exact SQLite triggers refuse one named accounting boundary.
The retention case fills the existing 10,000-event cap and rejects its required deletion only after the attempted Copy completion audit insert.
No trigger is treated as a failed physical sync or lost durable history.
Before observing the result, the probe republishes the same owned credential: publication refuses while the job retains its binding, so success acknowledges that all deferred body cleanup has returned.
The source and destination bindings are checked independently.

Queued cases set the existing public active-job limit to one and hold an actual download GET response with the common strict HTTP barrier.
After asserting the Copy is queued with no start time, they call CancelJob and wait for release of its distinct destination binding while the download still occupies the slot.
They then release the selected response and verify the completed download's exact local-file delta and unchanged remote accounts.

## Results before the Copy migration

All 32 leaf cases completed without skips: 24 targeted cases failed and eight unchanged controls passed.
Each provider produced the same sixteen case dispositions.
The two parent tests also fail because their children fail; they are not additional reproduced defects.

| Boundary or control | Observed old behaviour on both providers |
| --- | --- |
| New-name success | Complete, two copied and verified objects, twenty copied bytes and one native route; matching Copy audit |
| Mixed refusal | Partial, two copied/verified objects and one separate refusal; existing destination and aliases retained |
| All refused | Partial with one refusal, zero copied/verified/failed objects and no remote writes |
| Rejected final verified counters | Complete and applied audit, but zero verified objects persisted |
| Rejected error text | Partial refusal row and audit, but the job's error text is empty |
| Rejected terminal state | Verifying row beside an applied completion audit |
| Rejected terminal audit or retention | Complete row without a terminal Copy audit |
| Rejected start audit | Start succeeds and both selected objects are copied |
| Rejected planning state | Row remains queued, both selected objects are copied and an applied completion audit is appended |
| Rejected running state | Both selected objects are copied and the job ends complete |
| Rejected verifying state | Row remains running with two verified objects recorded and an applied completion audit |
| Rejected recovery intent | Existing refusal works: failed job, zero transfer writes and no recovery rows |
| Queued cancellation | Cancelled row uses the separate generic queued-cancellation audit rather than a matching atomic Copy summary |
| Rejected queued cancellation state | Row remains queued; the generic cancellation audit is still appended |
| Queued Copy-audit refusal | The old generic path never attempts a Copy terminal audit and does not expose persistence unavailability |

The five terminal-persistence refusals also leave job Get/List presenting rows as current instead of reporting the required unavailability.
Start, planning and running refusals are witnessed as real writes through both engines, with exact selected bytes present afterwards.
The probes still check collateral and retained recovery identities when an expected refusal fails; they do not stop at the first state mismatch.
Recovery records remain attached to the destination bookmark; terminal Copy audits use the source bookmark even while the destination is active in the browser.

Every case records its job, counters, terminal Copy audit, recovery rows and visible recovery status.
The probe removes only its own SQL refusal, checks the database close, starts a separate test process and compares the persisted facts before and after the existing interrupted-job handling.
All 32 orderly child-process comparisons completed without errors or remote requests.
These are not process-kill, child-lifetime, fsync, power-loss or missing-history tests.
The queued table describes the existing generic event from the caller and runner code; the captured per-job audit list deliberately includes terminal Copy events only.

## Evidence and next step

The actual producer exit is one because of the 24 expected failing leaf assertions.
Raw events, stderr, input and overlay hashes, the actual exit, per-case dispositions and all 32 observed records are retained under `/home/csmart/code/.aeronautes-work/20260908/copy-completion-before-v1*` and `copy-completion-probe-observations.json`.
The external source is `copy_completion_probe_test.go`; `run_copy_completion_go.py` is pinned to the new worktree and owned fixtures.
No system-matrix credit is claimed by these probes.

[ADR 0026](../adr/0026-copy-job-completion.md) defines the separate reviewed Copy admission and terminal-summary migration.
The identity ledger, incomplete/duplicate accounting, earlier Copy outcomes hidden by later errors, individual effect boundaries, native evidence and valuable-data hold remain open.

## Maintained implementation and evidence

The independently approved ADR 0026 contract is implemented in the Copy caller.
Start audit, planning and running records now gate admission, and final state, counter snapshot, error, finish time and terminal audit use the existing atomic completion operation.
The counter snapshot is copied under its mutex and persisted after releasing that mutex.
Separate forced final-counter, error and terminal-audit writes are removed from this caller.
Queued Copy cancellation uses the same operation, with source audit authority, the original resource and job identity, and zero execution counters.
The existing source/destination bindings, overwrite-refusal summary, destination recovery records and cleanup refusals remain intact.

`internal/systemtest/copy_completion_test.go` maintains the original 32 cases and adds four cases across the two providers.
Two combine a failed native source read, a refused overwrite, a successful engine copy and rejected verification admission; they require one copied object, one failure, one separate refusal, zero verified objects and both the earlier failure and later accounting reason.
Two let a running download's final audit fail while a Copy is queued; they require shared unavailability, no Copy execution or recovery intent, released bindings and rejection of a subsequent actual StartCopy.
New Copy starts are also refused after the other terminal and queued persistence failures, with no additional job row or destination writes.
The 210-refusal regression now requires exactly one matching audit immediately after observing the terminal Copy row, without the old separate-append wait.

All 36 actual-service cases pass on mock Swift and two-account SAIO with the pinned official engine and native fallback.
They preserve complete account and local-file checks, exact selected bytes, shared aliases, other-job collateral, recovery identities and source audit authority while a different bookmark is active.
Thirty-four cases perform the checked orderly child-process reopen and conservative interrupted comparison without remote requests.
The two shared-latch queue cases instead inspect both uncertain rows and live admission behaviour; they do not claim a process-reopen check.
None of these tests proves physical sync, crash/lost-history recovery, native child lifetime or power-loss safety.

Restoring the exact old Copy caller fails 26 leaf cases while ten controls pass.
Twelve additional broken variants are detected: separate terminal transactions; ignored start, planning, running or verifying gates; omitted earlier verification failure; corrupted final copied count; destination-attributed terminal audit; erased overwrite refusal; legacy queued finalisation; ignored shared queue/Copy admission availability; and cancellation registration retained after failed persistence.
The availability control deliberately defeats both existing queue checks and the new Copy admission check to test the shared refusal rule.
All controls use external Go overlays, have actual failing test assertions and zero skips, and leave application sources unchanged.

The full internal run passes 605 top-level tests and 1,566 leaf cases in twenty packages, with zero failures or skips and 85.3% statement coverage.
It uses Go 1.27.1, `-race -count=1`, the required keychain fixture, official rclone and two-account SAIO.
All tested Go source hashes match the implementation.
Schema-3 scenario reconciliation is valid and remains 32/110 cells with 38/49 actions gapped; these regressions invent no additional action credit.
All 852 frontend tests pass in 22 files at 79.33% statement coverage, with no Svelte errors or warnings.
Frontend build, Go vet, the native Linux build, the CGO-disabled internal build and patched Swift source verification pass.
Raw events, exits, source/overlay hashes, controls, checks and the reconciled report are retained under `/home/csmart/code/.aeronautes-work/20260908/copy-completion-*`.

Independent implementation review and protected PR/master checks remain required before delivery.
Only the existing Copy job summary and admission contract are implemented here; the identity, outcome, durability, verification, authority, native and valuable-data gates above remain open.
