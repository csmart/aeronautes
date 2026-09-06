# Recovery retention

Date: 2026-09-06.
Scope: the first P04 backend slice under [ADR 0020](../adr/0020-recovery-containment.md), based on master `beaaf216ca9242f0c7289ebfda477c94f11c577b` after PR33.
Global and per-job status with UI wording remains a separate implementation slice before P04 delivery.

## Behaviour and boundary

Reconciliation no longer deletes explicit staged-residue names, staging prefixes or captured segments.
The historical recovery entry points perform only local database reads; they cannot authenticate through a changed bookmark, contact Swift, add an applied-cleanup audit event or clear records.
Their existing interfaces remain conservative entry points so existing callers and the unchanged archived probes cannot regain cleanup through an alternate path.
The background retry loop is removed.
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
| Reopened process | `TestRecoveryRecordsAfterProcessReopen` launches an actual child test executable against the task-owned SQLite file; it classifies interruption without connection authority, preserves exact rows and leaves outside data unchanged |
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

P04 still needs local-only summary and per-job counts with unavailable reads displayed as unknown, plus frontend stale-response and recent-job-limit checks.
P05 will separately refuse unsupported overwrite and Mirror work before irreversible effects.
Original cleanup authority, exact remote outcomes, native writer quiescence, physical durability and the wider audit/release gates remain open.
The valuable-data release hold remains.
