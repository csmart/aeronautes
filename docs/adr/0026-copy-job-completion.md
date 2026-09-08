# ADR 0026 — save Copy job completion atomically

**Status:** Proposed under the authorised audit remediation plan; independent local design review is required before implementation.
**Scope:** The separate Copy admission and terminal-summary migration scheduled by ADR 0024.

## Problem and oracle

The [actual-service reproduction](../experiments/2026-09-08-copy-completion.md) confirms that Copy still ignores rejected planning, running and start-audit writes and can transfer both native and engine-routed objects afterwards.
Its separate final-counter, error, state and audit writes can leave a terminal row without the matching summary or audit, or a completion audit beside a non-terminal row.
Queued cancellation also uses a separate generic state/audit path.
ADR 0024's shared contract, DESIGN §§46/50/51, K-XFER-008 and the operator's valuable-data priority govern this migration.
The existing overwrite refusal, conditional-new-name and recovery-retention boundaries remain mandatory.

## Decision

Copy uses the existing jobs-store atomic completion and persistence-unavailability contract:

1. The start audit must succeed before launch.
   Refusal returns a start error, releases cancellation and both captured bindings, and attempts an atomic failed summary without transferring data.
   Planning and running transitions must succeed before their following work is admitted.
   A rejected planning transition creates no executor recovery record; a rejected running transition retains the staging intent already saved before it.
   An existing persistence latch stops admission, as it does for local transfers.
2. Final state, counters, error text and finish time share the existing SQLite write transaction with the terminal audit event and retention operation.
   Progress writes remain advisory; the final in-memory counter snapshot is copied under the counter mutex, then persisted without holding that mutex during database work.
   Remove separate forced final-counter writes, error writes and terminal audit appends from this caller.
   On a failed completion transaction, release cancellation, retain prior facts and report the shared non-secret unavailability error through job Get/List and subsequent job creation.
   Do not attempt a separate terminal audit or claim the result was saved.
3. The terminal audit retains the captured **source** bookmark, source-to-destination resource locator and `job <id>` summary.
   Recovery records retain the **destination** bookmark.
   Any reported overwrite refusal still forces `partial`, preserves the existing count and reason, and adds the separate refusal count to the audit summary even if no object was copied or a later error occurred.
   Preserve the existing coarse audit-result mapping; `partial` does not prove a successful write or an actual verification mismatch.
4. A rejected verification transition after execution produces `partial` with its accounting error and final execution counters if the terminal transaction can succeed.
   It does not run verification or acquire verified counts from work that never entered that phase.
   Preserve reported transfer failures and the existing overwrite-refusal summary alongside that new error.
5. Copy cancelled before acquiring a slot uses the same Copy completion operation with its initial counters, empty error and unchanged zero start time.
   Its audit is `transfer.copy.cancelled`, with the source bookmark, resource locator, job identity and existing terminal `failed` result vocabulary.
   This replaces only Copy's generic `transfer.cancelled-queued` event and separate state write.
   Keep captured bindings until the entire queued-cancellation path returns, release its cancellation registration and admit no executor or remote writes.

The existing store latch is process-local diagnostic state, not a durable ledger or a global mutation interlock.
Already running bodies may finish; no claim is made that every other worker or synchronous mutation stops.
Queued admission uses the existing shared latch check.
Restart preserves the existing conservative interrupted-job handling and retained recovery rows, without automatic retries or remote reclamation.

## Proof and implementation sequence

Review this contract and the reproductions before changing the caller.
Then implement the Copy start, phase-admission and completion paths, including queued cancellation, in one logical commit with maintained regressions.
Reuse `jobs.Store.Finish` and the existing queued-outcome callback rather than introducing a workflow engine or schema migration.

Maintain actual StartCopy cases on mock Swift and two-account SAIO with the pinned official engine and native fallback.
Exercise final-counter, error, terminal-state, audit and retention refusals; rejected start/planning/running/verifying records; the existing recovery-intent refusal; and new-name, mixed-refusal and all-refused controls.
Verify source audit authority while another bookmark is active, destination recovery identities, exact selected bytes, full owned namespaces, shared DLO/SLO aliases and other-job/unselected collateral.
A successful owned credential publication acknowledges release of the completed body's captured binding before inspecting its final database state.
For queued cancellation, hold an actual download GET response in the existing strict barrier, set the public active-job budget to one, assert the Copy is queued, cancel it and acknowledge its distinct destination binding release before releasing the download.
Do not use timing sleeps as evidence that the queue or selected HTTP boundary was reached.

Verify shared unavailability, refusal of new Copy jobs and queued Copy execution after a completion failure, cancellation deregistration, and unchanged execution/refusal counters.
Reopen the database in a separate process and compare prior facts plus conservative interrupted handling without remote requests.
An orderly close/reopen does not prove crash, fsync, lost-history or power-loss safety.
Restore the old caller and independently break transaction use, admission gates, refusal/counter reporting, authority attribution and queued finalisation; the relevant regression must fail for each broken rule.
Run the relevant full race, frontend, build and source checks, obtain independent local implementation review, and require all five latest PR and resulting master jobs before delivery.

## Limits and follow-up

This migration makes the existing job-level summary atomic; it cannot reconstruct every affected identity or prove unknown remote acceptance.
The 200-failure sampling limit, duplicate transfer/verification failure counts, planned DLO logical-byte estimate and complete failure access remain P13 work.
Copy's earlier outcomes hidden behind non-refusal plan/cancellation errors, selection of only one of simultaneous plan/finish errors, and logged executor-close or reconciliation errors need separate reviewed outcome work.
Do not silently fold those changes into this persistence migration or infer their correctness from local upload/download behaviour.
Per-identity durable intent/outcomes, persistence before individual effects, stronger integrity verification, native authority and child quiescence, concurrent writers, cleanup safety and other mutation families remain blocking.
Mirror starts, changed-destination overwrites and automatic recovery reclamation remain refused under ADR 0020, and the valuable-data release hold remains in force.
