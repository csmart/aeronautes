# ADR 0031 — checked upload item records

**Status:** Proposed for independent local review under the authorised audit remediation plan.
**Scope:** The local-upload caller migration in [ADR 0030](0030-copy-item-recording.md) step five, including its bounded source-path prerequisite.
Download remains a separate migration.

## Problem and oracle

The [reviewed upload reproductions](../experiments/2026-09-08-upload-item-boundaries.md) show 201 and 401 actual failed uploads reported as 200, without retained item identities.
Smaller local/mock probes show later engine or commit work after required recovery-record refusal, and an engine upload of `bravo` when the captured pathname reads `alpha`.
The independent review reproduced all fifteen leaves, with eight expected failures, seven passing controls and no skipped or unrelated failures.
The concrete oracle is that exact failed-name set and captured-path content, the checked recording rules in [ADR 0028](0028-transfer-item-records.md), and the established upload completion/issue semantics in [ADRs 0023](0023-upload-post-commit-errors.md), [0024](0024-local-job-completion.md) and [0025](0025-local-interruption-outcomes.md).
Copy's checked admission, typed observations, shared-store latch and terminal transaction provide the existing implementation pattern.

This migration must retain every admitted upload identity and its witnessed logical outcome, stop this upload's further dependent work after required recording fails, and expose exact failed-name totals without changing successful-copy accounting.
It does not establish complete per-request history, stable filesystem object identity, stronger content verification or native approval.

## Source-path prerequisite

Before any source stat/open, grouping or upload admission, require a valid UTF-8 absolute backend-captured path whose spelling is already unchanged by the host's `filepath.Clean`.
Reject the whole selection during the existing complete preflight when any path fails that check.
Use a constant refusal reason rather than a lossy rendering of an invalid pathname.
Do not clean, resolve symlinks, normalise Unicode or substitute a new spelling in the retained selection.
Preserve valid replacement characters, case and supported control characters exactly.
Apply the same guard at execution entry so an internal preflight value cannot bypass it.
No invalid selection may open a source for transfer, enter an engine batch or cause a remote write.

This intentionally refuses `root/link/../item`, relative paths and other spellings whose lexical cleaning could change the grouped source.
It is a conservative compatibility restriction, not a claim that every refused spelling denotes a different file.
An operator can select the file through a supported canonical spelling in a fresh picker action.
Do not silently route a rejected representation through native upload merely to make it succeed.
For accepted paths, the engine's parent and basename must refer to the unchanged captured spelling; retain the exact path and prove the selected bytes in an actual-service fixture.
Existing duplicate-name, regular-file, destination-absence and conditional-create rules remain required.

This is a bounded correction to pathname grouping.
It does not stop symlink targets, parents or files being replaced after selection/preflight, establish an approved root capability, or prove native-dialog behaviour on all six platforms.
P18/P19/P11 and the valuable-data hold remain open.
Windows path syntax follows its host `filepath` rules; accepting or refusing a spelling is not cross-platform execution evidence.

## Checked upload execution

Add an upload-specific optional recorder with checked admission, execution and verification callbacks, separate from advisory progress.
All three callbacks are required when configured; reject partial configuration before work.
Unrecorded direct executor callers keep their existing mode until explicitly migrated.
The actual new upload service supplies the complete recorder and creates record-version-one jobs only after this contract's prerequisites are implemented and reviewed.

Reuse the existing Copy observation/diagnostic value types and expose their pure diagnostic classifier for the two callers, preserving its existing behaviour.
Keep upload admission separate because it carries an `UploadItem` and its actual optional staging object, rather than a Copy planner item.
The transfer layer must not depend on the jobs store or bridge DTOs.
The application adapter validates immutable identity and intent through the existing pure jobs validation before attempting a store write.
Invalid caller representations refuse effects without deliberately tripping the persistence latch.

| Recorded field | Upload value |
| --- | --- |
| Logical key | Exact destination object name from the preflight item |
| Source | Local location with the exact backend-captured absolute path |
| Destination | Captured bookmark, container and exact destination object name |
| Staging | Engine item's actual `.aeronautes/stage/<job>/batch/<basename>` object; absent for a direct native item |
| Listed bytes | Nonnegative preflight stat size; advisory rather than proof of stable source identity |
| Planned/admitted | Both true for a preflight-approved item admitted to this execution |

The executor supplies the original item to the observation callbacks so the adapter need not reconstruct a local path from a display name or retain another whole-selection map.
Check each admission before opening a native source or buffering an engine item.
Every name in a dispatched directory group must already have an acknowledged admission.
Use bounded one-item admission transactions initially, and measure their cost; do not weaken acknowledgements to optimise throughput.
The existing complete local preflight is retained, but this change must not collect an additional full namespace merely for recording.

A required admission or observation failure becomes a sticky error for this upload.
No subsequent native source open, engine invocation, authentication retry, live commit or staging cleanup may start under that failed acknowledgement.
Required captured/committing/committed recovery-write failures also stop subsequent upload work; a committed-record refusal retains the affected stage and permits the existing read-only live HEAD check to report witnessed copied bytes.
This deliberately strengthens ADR 0023's earlier continuation boundary while preserving its affected-stage retention and issue accounting.
After a captured-record refusal during an engine call, wait for that synchronous call to return and refuse its retry and all later commit/group work.
An already started child may have written any admitted batch member; do not claim immediate child termination or global writer quiescence.

An admitted engine item whose invocation never started may record `not_attempted` while storage remains writable.
An item whose engine invocation started retains unresolved staging effects if its final observation is missing.
Do not invent observations for later items that were never admitted or claim they were attempted.
The shared store's existing item-write latch makes public reads unavailable and refuses all new job families after required item persistence fails; already admitted work may still save remaining facts when storage permits.
Recovery-write errors must remain explicit in the upload outcome even if item persistence still succeeds; this decision does not extend the process-wide item-store latch to every recovery callback error.
Neither latch nor callback refusal is a new retry or cleanup capability.

## Final observations and verification

Upload verifies each live object with its existing HEAD length check; it has no separate fresh namespace-comparison pass.
Do not invent a verification observation when that live check was never attempted.
Keep execution and an actually attempted verification as separate observations under the same admitted identity, without counting that name twice.
Return typed results from native and staged helpers and append the final execution observation only after required stream closure and the relevant staging cleanup decision.
Check native file-close and staged response-body-close errors explicitly.
If an existing live HEAD has already succeeded, later closure, cleanup or bookkeeping problems must not erase its copied/verified object and byte contribution.

| Witnessed upload boundary | Final execution / optional verification |
| --- | --- |
| Source open failure before a destination mutation | `failed`, `not_attempted`; no verification observation |
| Admitted item stopped before its engine invocation or native mutation | `not_attempted`, `not_attempted`, with typed interruption/incomplete reason; no invented verification |
| Engine invocation started but failed or left an item unresolved | `failed` or missing execution, unresolved staging effects; no invented live verification |
| Staged HEAD/read/size check fails or a live destination appears before commit | Failed or conflict execution with unresolved staging effects; no live verification observation |
| Any live PUT is invoked and fails, including conditional 412 | Failed or conflict execution, `unresolved`; no verification observation unless an actual live check occurred |
| PUT succeeds but the existing live HEAD errors or mismatches length | Failed execution with unresolved effects, plus failed verification; one distinct failed name |
| Existing live HEAD succeeds and no later issue is known | `copied`, `reported_applied`, with reported bytes; verified observation |
| Existing live HEAD succeeds but closure, required bookkeeping or cleanup has a problem | Keep `copied` and reported bytes with a typed problem and unresolved effects; retain the actual verified observation when its acknowledgement succeeds |

The execution acknowledgement precedes the verification acknowledgement and any later dependent item/group work.
If execution recording fails after a successful HEAD, retain the earlier admission and report unavailability; do not append a verification fact to conceal the missing execution acknowledgement.
If verification recording fails, retain the acknowledged execution fact and expose its unverified state.
Recording uses a non-cancelled completion context so a cancelled transfer can still preserve its witnessed facts; a cancelled context must not accidentally authorise a new transfer effect.

Preserve the exact `PostCommitIssues` count independently of its 200 samples and the logical per-name problem facet.
Two issues on one copied name remain two reported issues and one name with problems.
A simultaneous verification failure and bookkeeping issue retains both, without adding the bookkeeping issue as another failed name.
Ordinary cleanup failures retain the established continuation behaviour needed to report more than 200 issues; they do not become required-record acknowledgement failures.
No recovery record is cleared and no uncertain failed deletion is retried.
The first typed item diagnostic cannot represent every simultaneous issue; retain the complete observed issue count and explicit job problem alongside it without claiming complete per-effect history.

New item diagnostics contain only typed stages, codes and bounded HTTP status values.
Classification uses known branches, typed context cancellation/deadlines, typed Swift errors and generic network errors; arbitrary provider text cannot determine a new item diagnostic.
Raw bodies, URLs, credentials, headers and engine output remain excluded from the stored item and public DTO.
The legacy job-error/audit paths still require their wider SEC-08 work.

## Application completion and access

Create only new uploads with `ItemRecordsVersion=1`; leave historical jobs and new downloads at version zero.
Carry the immutable record version through the shared local runner and every terminal path, including failed recovery/start-audit admission, planning/running refusal and queued cancellation.
Version-one upload completion must use `FinishFromItems`; an unmigrated download must keep ordinary `Finish`.
Release the completed body's cancellation registration even when final persistence fails.

Use retained facets to prevent `complete` when any failed, conflicting, refused, unresolved, unverified or problematic name remains.
Keep ADR 0025's partial/cancelled/failed distinction and earlier outcomes alongside a later error or cancellation.
The final transaction derives distinct failed names and appends all six item facets to the matching terminal audit from those same transaction-local totals.
Display failure samples must not supply either exact terminal failed counts or an apparently exact failure count in the job error text.
Preserve the upload's captured destination bookmark authority, `transfer.local.<state>` operation, job resource and existing coarse audit result vocabulary.
Copied/verified/byte counters still come from the upload's actual successful live checks; the preflight stat size remains advisory source information.
Post-commit issue totals remain independent of per-name facets and sampled text.

Use the already delivered bounded item pages and quoted local-location DTO/view to expose every retained upload identity, both phases, missing facts and overlapping facets.
Verify actual uploaded paths through the service and UI, including page boundaries beyond 200 and exact control/Unicode display.
Do not infer safety from a displayed path or accept it back as a new selection.
Keep backend and UI rerun refusal for upload/download: a fresh local dialog and current preflight remain required.
The existing version-one interrupted projection must preserve upload records and distinct failed totals on orderly reopening; no observation or successful terminal audit is fabricated for a missing suffix.

## Implementation and proof sequence

1. Review this contract and its existing before-fix evidence before modifying production code.
   First extend the source-path reproduction through the actual upload services, injected backend picker, pinned rclone and both owned providers, preserving the original archive.
   Keep the equal-length `alpha`/`bravo` content witness and a valid canonical-path control; disclose that an injected picker is not a native dialog.
2. Implement the bounded pathname refusal as its own logical change, with maintained preflight/executor and actual-service regressions before upload opt-in.
   Assert no job/engine dispatch or remote writes for the refused selection, unchanged selected/unselected local files and complete owned remote collateral.
   Cover relative, dot-segment, invalid UTF-8 and accepted exact Unicode/native/engine paths, without normalising identity or claiming that clean paths cannot race.
3. Add checked upload execution and the application adapter/completion migration together, after the source prerequisite passes independent review.
   Keep the first actual caller, its fault seams and regressions in the same logical change; do not ship a version-one caller with unchecked callbacks or terminal paths.
   Preserve existing local interruption and more-than-200 post-commit issue regressions, and add an explicit version-zero download control for the shared runner.
4. Maintain actual mock and two-account SAIO 0/1/201/401 distinct-failure cases with complete public pages and matching terminal/audit counts.
   Keep the mixed native/engine case and add a non-newline local-filename case whose selected live conditional PUTs fail after engine staging, so the identity contract does not disappear behind a Windows newline skip.
   Prove exact selected live/staging effects and full body digests independently, and check that no selected failure or retained page is lost or duplicated.
5. Inject real SQL refusals at native and grouped admission, execution observation after an independently witnessed live write, and verification observation after its preceding execution acknowledgement.
   Assert ordering through acknowledged barriers, no later dependent effect, retained earlier facts, unavailable public reads and refusal of all eight new-job families sharing the store.
   Keep a writable-storage control and inspect the actual database/DTO, not only callback counts.
6. Maintain required captured-record refusal before authentication retry and committing/committed refusal before later commits or directory groups.
   Use actual service/SQL and real-child evidence where claimed, keeping the smaller original test-engine probes as bounded before controls.
   For DLO faults, inspect full live/staged manifest bodies, logical bytes, segment sets and shared aliases; HEAD length or a generated prefix cannot replace that witness.
   A committed-record failure must preserve a verified copied name, its issue and affected stage while preventing the next live commit.
7. Exercise queued cancellation, admitted-but-undispatched work, a returned failed engine batch, cancellation between staged commits, and simultaneous verification/closure/cleanup/bookkeeping problems.
   Keep verified bytes, exact issue counts and earlier failed names beside later reasons, without treating cancellation as proof of no remote acceptance.
   Check native file-close and staged body-close refusals at their actual helper boundary; maintain both close and cleanup observations when they happen together.
8. Extend orderly child reopening to compare complete item identities, observations and facets as well as job/audit/recovery state, with no remote traffic or automatic cleanup.
   Every actual fault fixture defines its exact permitted delta and checks both complete owned namespaces, derived sidecars, unselected sentinels, another job's staging data, shared DLO/SLO aliases and local selected/unselected files.
   Missing final observations must stay missing and unresolved across that reopen.
9. Deliberately remove pathname refusal, checked admission, sticky retry/commit stopping, observation ordering, close/error handling, distinct counting and complete access; matching maintained behavioural tests must fail.
   Include hostile diagnostic payloads, invalid representations without a manufactured store latch, missing/partial recorder configuration, exact issue totals and upload rerun refusal.
   An equivalent removed second guard is not a detected mutation unless it changes observable behaviour.
10. Run the relevant full race suite, frontend/type/build checks, native build, binding drift, source provenance and required owned real-Swift fixtures.
    Measure record cost and actual upload runtime without a production-throughput claim.
    Require independent local review of each logical implementation change, all five actual latest PR checks on current master, protected merge and all five resulting master checks.
    Preserve atomic commits and record the reviewed/merged tree and hosted integration duration; investigate measured suite growth instead of silently increasing CI budgets.

Each implementation step has its own local review gate before the next begins.
No initial caller or schema change is authorised by an unreviewed draft of this decision.
Do not credit new scenario cells unless their maintained evidence and reconciliation satisfy the existing coverage contract.

## Limits

This is logical item history and a bounded pathname correction, not complete per-effect or lost-history recovery.
Ordinary transfer-time staging deletion still needs its separate P15 ownership/concurrent-writer boundary; this migration grants no additional cleanup authority.
Source replacement and root confinement, remote identity and concurrent writers, stronger content/metadata verification, native approvals and child lifetime, resource limits, retention/export, full-app restart, fsync/power loss and all six platform targets remain open.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused.
The [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.
