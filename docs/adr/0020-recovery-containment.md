# ADR 0020 — retain uncertain recovery data

**Status:** Accepted after independent local design review under the authorised audit remediation plan.
**Scope:** The P01 ownership, concurrency and durability decisions needed by P04 recovery containment and P05 transfer refusal.
Implementation and release evidence remain separate gates.

## Problem and oracle

The [DS-01 reproduction](../audits/2026-09-05/data-safety.md#ds-01--critical-recovery-deletes-segments-referenced-by-an-unrelated-live-object) preserves an outside DLO manifest while recovery deletes its backing segments.
The current sweeper treats a generated prefix and the absence of selected recorded references as deletion authority.
Neither excludes an outside DLO or SLO, including a reference created after the last check.
Malformed provenance can fall through to deletion, and a zero-note sweep can erase the recovery records.
The same startup path obtains a session through the bookmark's current configuration even though the historical record carries no binding generation.

The behaviour oracle is K-XFER-002, K-SEG-001, K-SEG-004 and the accepted [P01/P04/P05 containment gates](../audits/2026-09-05/remediation-plan.md#phase-a--trustworthy-reproductions-and-containment): preserve other objects and retain uncertainty instead of declaring cleanup complete.
The [SEC-03 modelled history-loss case](../audits/2026-09-05/security-release.md#sec-03--the-configured-database-durability-contradicts-recoverys-assumptions) also shows why a missing local commit fact cannot prove that a remote mutation did not land.
These are executable mechanism reproductions, not physical power-cut evidence.

## Ownership and concurrency decision

A captured generated prefix proves creation provenance only.
`Confirmed` evidence proves a reference only.
Neither a unique random name, a local ownership row, a committed row, an absent derived name nor a complete point-in-time reference scan establishes exclusive deletion authority.
An ownership record does not reserve a Swift namespace or stop another client from referring to its objects.

`ExclusiveOwnershipProof` means authority over the exact objects to be removed, including exclusion of other references and relevant concurrent writers through the destructive effect.
The current Swift workflow establishes no such proof.
No automatic segment cleanup may be enabled by promoting a generated-prefix or reference record to this stronger meaning.
Do not add an account-wide scan as a substitute; absence at the end of a scan does not exclude a later reference.
Explicit force cleanup remains a separate destructive operation with sharing disclosed; it must not be labelled an exclusive-ownership proof or invoked automatically by recovery.
Its capability and exact-prefix gaps remain P11/P15 work.

A fresh HEAD establishes observed identity at that instant, not an atomic compare-and-mutate boundary.
Native manifest-only deletion avoids following segments, but can still remove a replacement at the same name or an object referenced by another manifest.
Native stage-and-commit prevents rclone from overwriting the live name; it does not protect an unconditional final PUT/COPY from an intervening writer.
No app-local lock, plan token, typed confirmation or versioning setting proves exclusion of external writers.
The existing repository evidence that Swift ignores `If-Match` on DELETE must not be worked around by adding that header and calling the race fixed.

| Operation | Allowed effect under containment | Forbidden inference or effect |
| --- | --- | --- |
| Read/list/HEAD | Observe current state without authorising a later destructive effect | Treat the observation as a namespace lock |
| Planned-new transfer commit | Use the established native conditional-create path; a concurrently created destination causes a conflict | Unconditional fallback or overwrite after a lost acknowledgement |
| Transfer overwrite of an existing destination | P05 must refuse before staging that object or writing its live name until an enforceable protocol is implemented | Overwrite based only on the last HEAD, operator confirmation or local job exclusion |
| Destructive Mirror | P05 must refuse the Mirror start before transfer or deletion while its external-writer/source-absence guarantee is unsupported | Run a partial copy and then silently omit deletion, or delete a later replacement |
| Automatic recovery or post-job reconciliation | Retain staging objects, captured segments and every unresolved recovery fact | Delete based on creation provenance, a missing record, an empty listing or a nonreferencing recorded manifest |
| Explicit force cleanup | Keep the existing separately confirmed action and its disclosed sharing risk, pending its own audit fixes | Call it safe automatic recovery or use its confirmation to grant exclusivity |

P05 will enforce refusal in the backend, including sibling local-upload and native-name paths, and explain it in the relevant preview/start UI.
Known-equal skips and conditional planned-new Copy remain available within their existing verification limits.
An operation admitted under an earlier implementation must still report any actual partial effects honestly.
This decision does not claim that ordinary manual deletion, metadata updates, local materialisation or version handling have completed their separate audit gates.

## P04 recovery decision

Suspend remote reclamation by both in-process reconciliation and startup recovery.
This includes explicit staged-residue names as well as prefix-based staging and segment sweeps.
Remove the final staging-prefix sweep in `local.Upload` as well as `Executor.Reconcile` reclamation and historical recovery sweeps.
A live alias may refer to either kind of object.
Recovery performs no remote listing, HEAD, DELETE or automatic authentication using a historical bookmark record.
It does not infer an original endpoint or a destination container from naming conventions.

Preserve all existing recovery rows, including malformed, legacy, captured, committing, committed and staging records.
Stop clearing the job's recovery rows merely because Copy, Mirror or local upload completed, verification passed, a response was acknowledged or reconciliation returned no notes.
Those facts do not prove that no uncertain residue remains and no accepted request can finish later.
There is no automatic or frontend-accessible clear/reclaim action in this containment slice.
Keep recovery-held jobs exempt from history pruning.

This conservative first implementation may retain records for successfully completed jobs even when their temporary objects were already removed during normal execution.
The UI must therefore say that recovery records are retained and remote residue is unverified; counts are records or jobs, not object counts, bytes, proven orphans or failed transfers.
Retained recovery work does not rewrite the transfer's existing terminal result.
Expose a bounded read-only summary with the number of terminal jobs holding recovery records and the number of those records, independent of the recent-job list limit.
Also expose a per-job retained-record count in the job view.
Do not contact Swift to calculate that status.
An unavailable database read is unknown, never a zero count or a successful cleanup claim.

Keep startup classification of interrupted jobs, but change its message to say that data and recovery records are retained.
The existing job-state/error update and diagnostic log record that classification; this slice adds no cleanup audit event.
An unavailable classification write is reported as a failure, not a successful recovery.
Remove the background deletion/retry loop so retained work cannot repeatedly authenticate or emit false applied-cleanup events.
The executor's `cleanStaged`, the per-object deletion in `local.commitStagedUpload`, and the child's own cancellation cleanup remain the exact transfer-time boundary for separate P12/P15/P21 work.
This slice makes no guarantee about those earlier effects; the final namespace-wide sweep in `local.Upload` is included in P04 above.
Retaining records also prevents local upload from discarding evidence of its existing best-effort cleanup failures, without claiming that upload verification and cleanup parity are complete.

## Durability and outcome boundary

Continue requiring effective WAL/FULL settings on each physical SQLite connection before use, as implemented under ADR 0015.
Persist required intent before the dependent remote effect and refuse that effect when persistence reports failure.
Successful synchronisation depends on the operating system, filesystem and device honouring it; it is not evidence of a tested power-loss outcome.
A response failure or cancellation can follow a landed mutation.
A durable record can be missing from an older or damaged database even though remote data survived.
Missing history therefore reduces authority; it never authorises deletion or a retry that could overwrite a later object.
Database unavailability retains uncertainty and must be surfaced.

Re-enabling automatic reclamation needs a separately reviewed P12 protocol with original endpoint/account/trust authority, exact durable intent and outcomes, completed or fenced native/child writers, reference/concurrency authority through deletion, and verified handling of ambiguous remote and database responses.
Do not invent a new workflow engine or claim that FULL, a process-local pin or the current HTTP barrier supplies those missing guarantees.

## Implementation sequence and verification

1. Reconcile the ownership and recovery paragraphs in DESIGN and REQUIREMENTS and review this decision before dependent code.
2. Make in-process and historical recovery retain data and records, including Copy, Mirror and local upload; remove background reclamation and add the read-only job status with accurate UI wording.
3. Prove the original outside-alias and modelled lost-history failures before the fix, then preserve independent content digests and exact recovery rows afterwards.
   Include DLO and SLO aliases to segments and staging objects, malformed/missing provenance, committed and committing facts, cancelled/failed/completed jobs, changed bookmark bindings, unavailable databases and repeated recovery.
4. Use an actual child test process reopening a task-owned state database to distinguish restart evidence from an in-process reopen.
   Keep mock, real Swift, HTTP response loss, process restart and power-loss evidence separately labelled.
5. Deliberately restore deletion or record clearing and require the corresponding system-level evidence to fail.
   Verify read-only status across recent-job limits, terminal states and stale/error frontend responses.
6. Run relevant local tests and independent review, then all five current-head/current-base PR jobs, protected merge and master verification.
7. Deliver P05 final-hop refusal as a separate reviewed implementation with competing-writer and zero-effect refusal regressions.

The broader P01 local-filesystem, capability and distribution decisions remain open for their dependent work packages.
P04 containment preserves evidence while reclamation is unavailable; it does not complete P12/P13 recovery/outcome handling, P10 historical authority or the wider release gates.
The valuable-data release hold remains.
