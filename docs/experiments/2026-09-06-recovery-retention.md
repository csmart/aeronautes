# Recovery retention

Date: 2026-09-06.
Scope: P04 backend containment and its separate read-only status/UI slice under [ADR 0020](../adr/0020-recovery-containment.md), based on master `beaaf216ca9242f0c7289ebfda477c94f11c577b` after PR33.
The first backend slice is commit `e6e39954daa1ad7a`; its independently reproduced evidence is separated from the status follow-up below.

## Behaviour and boundary

Reconciliation no longer deletes explicit staged-residue names, staging prefixes or captured segments.
The first slice made historical recovery entry points perform only local database reads; they could no longer authenticate through a changed bookmark, contact Swift, add an applied-cleanup audit event or clear records.
The status follow-up removes those unused application hooks, and the background retry loop remains removed.
The domain reconciliation interfaces remain inert for their existing callers and unchanged archived probes.
Copy, Mirror and local upload retain recovery rows even on completion, and the unused internal record-clearing method is removed.
Local upload also stops its final namespace-wide staging sweep.
Interrupted jobs still become failed with a finish time; their error now says that data and recovery records are retained and remote residue is unverified.
Database failure remains an error.

Generated prefixes prove creation, and committed records prove an observed reference; neither excludes outside aliases or later writes.
No reference scan, unconditional retry or invented conditional-delete guarantee replaces the removed cleanup.
Completed jobs may retain records even when normal execution removed their temporary objects, so retained records do not count remote objects or prove a failed transfer.
The existing pruning exclusion for jobs holding recovery records stays in place.

## Reproductions and regression evidence

The unchanged archived `TestAuditSweepKeepsExternallySharedCapturedPrefix` and `TestAuditPowerLossMissingCommitFactPreservesLiveDLO` both failed against the merged production code before containment.
The former erased an outside alias's backing bytes; the latter changed the live DLO from eight bytes to zero after modelled loss of a commit fact.
Both pass with the containment implementation through the same temporary Go overlay, without editing the archived probes.
Despite its historical name, the second probe models missing history; it does not perform a power cut.

| Boundary exercised | Evidence |
| --- | --- |
| Domain reconciliation | `TestRecoveryRetainsOutsideAliases` covers captured, committed, committing, malformed-prefix, malformed-live and missing-fact inputs; `TestReconcileRetainsStagingAliases` covers explicit staged residue |
| Outside storage | The independent reader uses another listener over the mock, verifies DLO/SLO content digests and referenced leaves, and compares the selected containers before and after recovery; request counts also require zero recovery traffic |
| Real Swift | `TestRecoveryRetentionRealSwift` creates real DLO and SLO aliases to both staging and captured objects; all four assemble the expected bytes and remain unchanged through repeated recovery with missing or malformed recorded live names |
| Cancelled native child | `TestCopyExecutorRealSwift/cancel-reconcile` snapshots the destination and segment container after the child and Finish return, then requires no reconciliation delta |
| Local upload service | `TestUploadRetainsFinalSweepResidue` plants an outside staging alias at the acknowledged engine-entry barrier, completes the upload, and proves the only storage delta is the intended new file while exact recovery rows remain |
| Completed Copy and Mirror | Existing service tests now require retained recovery rows after successful completion |
| Changed bookmark | `TestRecoveryDoesNotUseChangedBookmark` provides a usable replacement binding and data at both endpoints, then requires no recovery requests, no audit append, unchanged exact records and no storage delta |
| Reopened process | `TestRecoveryRecordsAfterProcessReopen` launches an actual child test executable against the task-owned SQLite file; it classifies interruption without connection authority, preserves exact rows and leaves outside data unchanged; the status follow-up reads the new counts instead of invoking the removed hook |
| Unavailable database | `TestRecoveryReadFailureIsNotEmpty` rejects cancelled reads and a closed database instead of reporting empty or successful recovery |

The mock does not assemble SLO bodies itself; its SLO fixture carries the body while the independent reader checks the separately referenced leaf.
The real-Swift test supplies the stronger assembly evidence using actual multipart-manifest PUTs.
The child-process check opens the database while the parent fixture still owns a connection.
It proves a separate process can read retained facts and classify the interrupted job; it is not a full desktop restart, parent kill, child-quiescence or physical power-loss test.
Normal `cleanStaged`, `local.commitStagedUpload` per-object cleanup and child cancellation cleanup occur before the tested reconciliation boundary and remain P12/P15/P21 work.

## Verification and catching controls

The full internal race suite passed **472 top-level tests across 20 packages, with zero failures or skips**.
The run used Go 1.27.1, both owned SAIO accounts, official rclone 1.74.3 with its matrix enabled and the mandatory private keychain protocol fixture.
Statement coverage is **83.7%**.
The scenario report is valid at **10/104**, with **47/48 actions still carrying gaps**; these new domain and service regressions do not claim additional scenario cells.
Application build, vet, the internal build without CGO, formatting and diff checks pass.
The frontend application source is unchanged; the build uses the already built assets from the identical application source after PR33.
Independent local review caught generated JSDoc drift from the changed `CancelJob` comment, so the bindings are regenerated as part of this slice and the required generation gate is checked.
An initial focused invocation could not bind its mock listeners inside the command sandbox; the permitted loopback rerun passed, and the sandbox failure is not counted as a behavioural reproduction.

Seven separately compiled temporary variants restored unsafe behaviour and failed the corresponding regressions:

| Restored behaviour | Failing leaf tests |
| --- | ---: |
| Previous reconciliation, including real Swift | 7 |
| Local upload's final staging-prefix sweep | 1 |
| Clear Copy recovery rows | 1 |
| Clear Mirror recovery rows | 1 |
| Clear upload recovery rows | 1 |
| Previous startup recovery through current bookmarks | 1 |
| Hide a recovery read error | 1 |

The record-clearing variants restore the removed store method as well as the caller, so compilation failure cannot substitute for detecting missing rows.
The startup variant contacts the replacement endpoint and appends false applied-cleanup events even while the domain recovery function remains disabled.
The local-upload variant changes the outside DLO's content, size and segment list and removes its backing object.
No variant is credited for a build failure, sandbox failure or test-process timeout.

## Remaining work

P04's local-only summary and per-job counts are implemented in the status follow-up below; independent approval and protected delivery remain the completion gate.
P05 will separately refuse unsupported overwrite and Mirror work before irreversible effects.
Original cleanup authority, exact remote outcomes, native writer quiescence, physical durability and the wider audit/release gates remain open.
The valuable-data release hold remains.

## Read-only status follow-up

`JobService.RecoveryStatus` returns only the number of terminal jobs holding recovery records and the number of those records.
One SQLite aggregate covers complete, partial, cancelled and failed jobs without fetching all rows or using the recent-job limit.
Get/List include each job's record count in the same query that reads the job.
The service needs no connection or credential service and returns a generic unavailable error when the database read fails.
The former startup sweep hooks are removed instead of leaving an unused background-maintenance API beside the new status.
The domain report's zero deletion counts remain for its callers and the original audit probes; they never claim that remote residue is absent.

The drawer displays counts for all accounts above its filtered recent-job list and shows each terminal job's retained count without changing its existing result.
An empty filtered list says there are no jobs in that view, avoiding a claim that no jobs exist elsewhere.
The global notice explains that counts describe records, not objects or failed transfers, and that remote residue is unverified and automatic recovery cleanup is unavailable.
It exposes no clear or reclaim action.
Polling keeps the last successful counts while a refresh is pending, drops them on a failed read, and rejects older successes and failures using a request sequence.
Unknown status persists until a newer successful read; the next drawer poll can recover without a new connection.

`TestRecoveryStatusBeyondRecentJobs` covers every job state, 65 newer jobs that hide the retained jobs from the 50-entry window, per-job Get/List counts and the pruning exemption.
Four terminal jobs hold 26 records, while all eight jobs together retain 36 records; active jobs are excluded from the global terminal summary.
Pruning removes only the 65 newer jobs without recovery facts, preserving every live and terminal fact.
`TestRecoveryStatusRefusesUnavailableDatabase` verifies a failed bridge read, with Get/List also refusing unavailable per-job counts.
Both service tests supply real SQLite and no Swift connection, and credit the new action's happy/refusal scenarios only after assertions pass.
The existing changed-bookmark, separate-process reopen and read-failure tests now exercise the new service, retaining their exact-row and independent-storage checks.

The complete status tree passed **474 top-level internal race tests across 20 packages, zero failures or skips**, with the same required two-account SAIO, pinned rclone/matrix and private keychain fixtures.
Go statement coverage remains **83.7%**; the valid scenario report is **12/106**, with **47/49 actions still carrying gaps**.
The frontend passed **68 tests across 10 files**, including ten new state/drawer cases, and reports **24.6% statement coverage**; the broader frontend coverage gap remains open.
Svelte checking reports zero errors and warnings, and the production frontend and application builds, vet and internal build without CGO pass.
Generated bindings include the new status method, its count model and the per-job count.

Four separately compiled Go variants each fail one service regression when they restrict totals to 50 recent jobs, include active jobs, hide the database error or return zero per-job counts.
Two isolated frontend copies fail four tests when accepting stale responses and one test when retaining counts after a failed read.
The final tree also rechecks the unchanged archived probes and the five still-applicable deletion/record-clearing controls from the first slice.
The historical startup and read-error controls above apply to `e6e39954`; their hooks no longer exist in the final tree, and current read-error refusal is covered by the new status control.
These results establish retention and truthful local status, not original cleanup authority, exact remote residue or broader audit closure.
