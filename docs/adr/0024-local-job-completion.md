# ADR 0024 — save local job completion atomically

**Status:** Accepted after independent local design review under the authorised audit remediation plan.
**Scope:** A bounded P12/P13 foundation for upload and download admission and terminal summaries.
Copy and other job families migrate in separate reviewed slices.

## Problem and oracle

The [current-master experiment](../experiments/2026-09-08-terminal-persistence.md) reproduced terminal jobs with stale counters, missing error text or missing audit events, and completion audit events whose jobs remained non-terminal.
Rejected planning, running and start-audit writes did not stop actual uploads on mock Swift or SAIO.
The operator's valuable-data priority, DESIGN §§46/50/51 and ADRs 0015/0020 require explicit accounting and conservative uncertainty.
This decision does not establish a complete identity ledger or lift the valuable-data hold.

## Shared durability contract

Required intent must be durable before the effect it admits.
An intent names the original authority and exact affected identity; a display locator, sampled failure list or generated staging prefix cannot replace those facts.
An attempted effect with no durable, authoritative observation remains unknown, including after response loss or loss of a later local write.
Unknown does not mean absent, safe to retry, or safe to reclaim.
Observed acceptance, verified content and permitted cleanup are separate facts.
The eventual per-identity ledger must retain every identity beyond display limits and preserve these distinctions across restart; its schema and caller migrations need their own reviewed implementation slices.

The first bounded implementation strengthens the existing job-level record without pretending it is that ledger:

1. Upload and download require their planning and running transitions to succeed before entering the execution body.
   Their start audit must succeed before launch.
   Refusal stops the operation and attempts a failed terminal record; no storage or filesystem transfer effect is admitted.
2. A shared jobs-store operation saves final state, counters, error text and finish time together with the matching terminal audit event in one existing SQLite write transaction.
   It validates the existing state transition and uses the existing audit validation and retention policy.
   A terminal row cannot become visible independently of that transaction's audit event.
   Progress updates remain advisory and cannot substitute for this final snapshot.
3. If the transaction fails, do not append a separate success or failure audit event and do not claim that the result was saved.
   Release the completed body's cancellation registration and retain all existing database and recovery facts.
   Latch a non-secret process-local persistence error in the shared jobs store: job Get/List return it and new job creation refuses.
   Queued bodies check that admission latch before entering execution.
   The drawer must say job status is unavailable, including while collapsed, rather than displaying an active/empty count as current truth.
4. The latch is bounded diagnostic state, not a durable outcome ledger or proof that other workers stopped.
   Already running jobs and synchronous mutations need their own remaining persistence/effect gates.
   Restart uses the existing conservative interrupted-job handling, retains recovery records, performs no automatic retry or remote cleanup, and cannot reconstruct missing final observations.
   Do not promise exact outcomes after a rejected write or imply that restarting makes a retry safe.
5. A failed transition into verification after execution produces a partial result with its accounting error if the atomic terminal transaction can succeed.
   It cannot become a clean completion merely because the transfer body returned successfully.
   The existing post-commit issue semantics of ADR 0023 remain intact.

## Implementation and proof sequence

Review this decision and the reproductions before changing storage or caller behaviour.
Implement the transaction primitive with rollback tests for each write component, audit validation/retention, illegal transitions and a successful control.
Migrate the local runner and upload/download start audit together, with real service regressions on owned mock and two-account SAIO fixtures.
Check independent complete account and local-file snapshots, retained recovery facts, visible unavailability, rejected subsequent starts, cancellation deregistration and queued admission.
Reopen the database in a separate process and compare the persisted state and conservative interrupted result without remote traffic.
An orderly close/reopen is not a kill, lost-history, failed-fsync or power-loss test.
Use deliberately broken transaction/admission/error-surface controls and rerun the relevant race, frontend, system and build checks before local review and protected PR/master delivery.

Then preserve earlier transfer failures alongside later errors or cancellation in a separate reviewed change, with an actual service reproduction.
Migrate sibling Copy finalisation separately, preserving overwrite refusals and their separate counts.
Per-identity intents/outcomes, unknown remote acceptance, complete failure access, spill failures, audit export/retention, native authority and writer quiescence remain blocking work.

## Tradeoffs

An atomic terminal summary prevents contradictory committed job/audit records but cannot undo an already completed remote or filesystem effect.
A failed final transaction may leave advisory counters in a non-terminal database row; the process reports unavailability instead of presenting that row as current truth.
The coarse process latch hides job-list results until restart and refuses new job records rather than attempting an unreviewed automatic repair.
It does not disable every mutating service or certify other job families.
No schema migration, new lifecycle state, automatic recovery action, stronger verification claim or re-enabled overwrite/Mirror operation is part of this slice.
