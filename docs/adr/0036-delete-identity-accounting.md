# ADR 0036: bounded deletion identity accounting

Status: Proposed
Date: 2026-09-11

## Context

K-DEL-002 requires streaming deletion with exact failed-name retention, and K-PERF-001 forbids collecting the namespace in memory.
The [bulk outcome contract](../experiments/2026-09-11-bulk-delete-identities.md#the-outcome-contract) counts each reported-failure and unresolved identity once across the entire run.
The initial implementation satisfies that distinctness using two run-wide maps, which can retain every listed name even though the requests and displayed samples are bounded.
A valid all-failure reply for each page exercises that growth without triggering the early stop used for an untrusted reply.

Exact counts and bounded memory must hold together.
Resetting the maps per request or pass, capping them at the display limit, or estimating membership would falsify the accepted counts.
The correction therefore needs an exact disk index and an explicit outcome when that index cannot establish the accounting.
This index is private working state, not a durable item ledger, a restart protocol or deletion authority.

## Decision

### Exact identity storage

Each `DeletePrefix` or `DeleteNames` invocation owns a fresh private directory under the application's cache location and a SQLite file using the existing pure-Go dependency.
Do not silently fall back to the general temporary directory when that location is unavailable.
One pinned connection owns setup, prepared statements, transactions and close; parallel workers serialise accounting through the existing result guard.
The key is the exact name bytes plus the reported-failure or unresolved facet, without path normalisation, case folding or collation-based equality.
Use parameter binding and an exact binary unique key.
Only a checked newly inserted facet increments its counter, enters the bounded display sample and reaches the existing once-per-identity callback.
Publish those changes only after the bounded transaction commits successfully.

Set and read back the private connection's settings before dispatch: 4,096-byte pages, a 1,024 KiB page-cache target, memory mapping disabled, file-backed temporary storage, DELETE rollback journalling, FULL synchronous writes and no busy wait.
Keep cache spilling enabled so a bounded transaction need not keep all its dirty pages in memory.
Do not reuse or change the application's durable WAL database configuration.
A page-cache target is not a hard whole-process memory limit; the implementation must also bound transaction state and every retained Go collection.
Each transaction covers at most one bulk request of 1,000 names and its two facets, or one parallel request's outcome.
The only retained name collections are the current listing page, bounded request/worker queues, at most two 200-name display samples, bounded reply diagnostics and the bounded exceptional attempt record below.
No run-wide in-memory membership set or fallback is allowed.
`DeleteNames` must not copy its caller-owned selection into another run-sized structure.

Check setup, statement execution, affected-row results, commit, rollback, statement/connection close and directory removal.
Use context-aware database calls with a ten-second cooperative accounting deadline, detached from caller cancellation while recording already-issued outcomes.
Cancellation must not discard those outcomes solely because its context is already cancelled.
An execution or commit error may leave the write outcome uncertain; latch failure rather than treating it as a duplicate, assuming it did not commit or retrying it to reconstruct counts.
SQLite cancellation remains cooperative, and the pinned driver's open, commit and rollback paths cannot promise an exact interruption deadline for a blocked filesystem call.
Remove only this invocation's private files after accounting and checked closure.
A cleanup failure is reported; a retained private index is not advertised as a complete recovery ledger.

### Storage failure and dispatch

If index setup fails before any destructive request, refuse dispatch and return a failed job with zero attempted outcomes.
Once an outcome cannot be indexed reliably after dispatch, latch incomplete accounting, stop admitting further requests and join all admitted workers before returning.
Each worker must pass the same guarded dispatch admission that checks the latch; a name merely buffered in the queue has not been issued.
At most the current bulk request, or the bounded worker requests admitted before the latch, may still take effect.
Do not retry them, start a new sweep, verify the prefix as complete or delete the container after an accounting failure.

Keep the exact names and available reported-error details of the unindexed admitted attempts in a separate bounded result record.
For bulk, retain the affected request; for parallel deletion, retain the failing attempt and drain the other admitted attempts.
This record describes attempts and may repeat an identity; it is not a deduplicated failure set and its length cannot supply either distinct counter.
Attempt to retain it through a checked, separately labelled evidence writer before publishing the terminal outcome.
If evidence open, write, flush or close fails too, explicitly report that the issued identities could not all be retained and offer no complete-list claim.
A bounded in-memory record alone is not durable retention.

An outcome-indexing failure after dispatch makes final identity totals unavailable.
Keep the last committed counter snapshot, never substitute zero, infer a total from planned minus deleted, or count storage errors as failed objects.
The job is partial even when no deletion was credited: a missing accounting record does not establish that no effect occurred.
A close or removal failure after all outcome accounting was checked does not itself invalidate known totals; report that separate problem and refuse silent completion.

Keep the existing, different retention-failure contract: when indexing succeeds but the normal JSONL retained set fails, the identity counts remain exact while the list is unavailable or incomplete.
Index failure must not silently replace that promise with approximate counts for ordinary spill failures.

### Public and persisted outcome

Add an optional deletion-specific `deletionAccountingIncomplete` boolean to the existing counters JSON and public bindings.
When true, the numerical values are last-confirmed progress, not exact final failure or unresolved totals.
The drawer must label that condition explicitly and must not show an unqualified failed-name total or a completion percentage as final truth.
The terminal reason states that some requests may have taken effect, distinguishes partial distinct-set evidence from admitted-attempt evidence, and reports evidence availability.
Show that reason in both the recent drawer and older history.
An absent flag in a historical job is not retrospective proof of complete accounting.

Reuse `jobs.Finish` for deletion's version-zero job completion so the counters, flag, reason, state and terminal audit event commit together.
Use a controlled audit summary that identifies incomplete deletion accounting without copying arbitrary storage/provider errors into the summary.
A failed completion write must leave public status unavailable through the existing store latch, rather than publish a terminal state without its qualifier or audit.
Do not add a table, item-record version, job family, generic outcome framework or alternate terminal state for this index.
This migration does not claim to repair every earlier progress, start-event or broader P12/P13 persistence failure.

## Implementation sequence and proof

1. Review this contract and the DESIGN sequence before production changes.
   Preserve the initial range and its passing checks as historical evidence, while retaining the unbounded-memory defect as a delivery blocker.
2. Record an increasing, generated paginated all-failure reproduction and a deliberately broken bound control without letting the producer or assertion itself accumulate the namespace.
   Cover distinct names beyond a page and sample limit, repeated names across passes, both executors, exact callback sets, cancellation and release of each invocation's resources.
3. Implement and review the deletion-private index with focused tests of exact membership, bounded transactions and checked setup, execution, commit, rollback and cleanup failures.
   This private storage prerequisite alone does not correct the existing executors or permit delivery.
4. Replace the maps and connect checked dispatch/attempt accounting with the app, bindings, drawer, history and atomic completion as one coherent failure-path correction.
   Inject failures at explicit boundaries; prove no post-latch admission, bounded draining and correct attempted-versus-queued identities.
   Measure retained consumer state as N increases and inspect the database/cache/transaction bounds; a small passing fixture or cache pragma alone is insufficient.
   Exercise healthy-index retention failure, index failure with successful attempt retention, simultaneous index/retention failure and terminal-store/audit failure.
   Check public `Get`/`List`/`History`, reopened durable job state, the exact audit event and rendered labels, including old jobs without the new field.
5. Preserve the reply-validation, encoding, effect-substitution and repeated-identity regressions and their intended negative controls.
   Run relevant race and frontend tests, owned real-Swift service evidence, the full suite and renewed independent review on the final range before the five-job PR and resulting-master gates.

Do not enable an engine failure result before its callers can refuse further destruction and qualify its incomplete totals.
The index prerequisite and integrated failure path are separate review units; an intermediate branch is not a deliverable correction.

## Limits

The existing sweep's re-attempt of unresolved names remains a separately recorded destructive-authority hazard.
This correction introduces no retry or permission to delete a replacement object.
Disk consumption grows with distinct identity facets; exhaustion stops the run under this contract rather than moving that growth into memory.
The selected cache filesystem may itself use memory; resource evidence must name its backing and cannot treat a temporary-file path as proof of physical disk storage or a global memory ceiling.
Global job/namespace ceilings, universal retained-file durability, native platform acceptance, other DS-08 work and the valuable-data hold remain open.
