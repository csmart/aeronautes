# ADR 0043 — permitted effects and observed creation for a cancelled download

**Status:** Accepted for the native staging boundary of a cancelled download, which is delivered with this decision.
The open product choice in decision 7 was answered by the operator on 14 September 2026 and is recorded in [ADR 0048](0048-cancelled-stage-removal.md); engine-route cancellation and the remainder of P20 stay open.
**Scope:** The native staging boundary of a cancelled download: what it may leave in the directory the operator picked, and what the durable records say about it.
The engine route keeps its existing cancellation coverage. The open product choice in decision 7 is the operator's and is not decided here.

## Problem and oracle

[ADR 0035](0035-download-root-authority.md) bound every download effect to the held directory handle.
It did not say which effects are permitted when the operator cancels part way, and the records do not describe the ones that occur.

The [experiment](../experiments/2026-09-13-download-cancellation-delta.md) reproduces this through the bridge-facing start and cancel.
A directory marker materialises, then the cancel lands inside the following native item, whose ancestors do not yet exist.
Twelve runs of one unchanged fixture produced three outcomes: seven created the item's ancestors and recorded the item `failed` with effect `unresolved` at stage `staging`; four created nothing and recorded `not_attempted` at `source_read`; one failed the source read itself and recorded `failed` with effect `not_attempted` at `source_read`.

A cancel is asynchronous, so which of the executor's checks observes it is not fixed and cannot be made fixed.
An earlier version of this record proposed removing the check between the ancestor walk and the temporary so that one boundary decided.
Review showed that this is not achievable and not safe: cancellation still arrives before or after the remaining check, and removing the later one permits a temporary to be created after cancellation where today none is.
The problem is therefore not that the orderings differ. It is that the permitted effects for each ordering are unstated, and that the records describe the wrong thing.

The oracle is the L07 acceptance in [the finish line](../audits/2026-09-05/linux-finish-line.md): cancellation "produces exactly the accepted directory/file delta and truthful retained outcomes, preserving selected-root authority".
Read as an accepted *set* of effects with records that say which occurred, that is achievable; read as a single delta regardless of when the cancel lands, it is not.

## Decision

1. State the permitted effects, and keep every existing check.
   For a cancel observed at the native staging boundary the permitted effects are: no effect; or ancestor directories of the item created by the walk; and in no ordering a final file at the item's name.
   A temporary may exist only transiently and is removed by the existing cleanup before the item's record is written; a temporary that survives is residue and keeps its `unresolved` record today.
   Both cancellation checks either side of the ancestor walk stay. Neither is removed, because each prevents work the other cannot.
2. Do not report an effect that certainly did not happen as uncertain.
   The check between the ancestor walk and the temporary fires before the temporary's exclusive creation, so at that point no file and no temporary exists for the item.
   Its record becomes `not_attempted` at `source_read`, which is what both neighbouring checks and the upload twin already record, rather than `failed` and `unresolved` at `staging`.
   The diagnostic code was already `cancelled` in both, because classification maps a cancelled error to it whatever code the call site passes; what changes is the result, the effect and the stage.
   `unresolved` keeps its meaning of residue the operator must go and look for, which is what makes it worth acting on.
3. Report residue, not intended placement.
   Ancestors are reported for an item that did not copy.
   An item that copied has the directories it needed as its intended outcome and its own record already carries the placed file, so counting them again would describe the result rather than what a stopped job left behind.
   The distinction is intended placement against residue; it is a per-item scope, and it sits beside the job-level condition in decision 5 rather than replacing it.
4. Record observed creation, never inferred creation.
   The component walk already distinguishes the two: it creates a component only when the entry is absent and its `Mkdir` succeeds, and an entry that already exists or is won by another writer is not a creation by this executor.
   The walk reports the components it created; a partial walk reports the prefix it got through; a pre-existing or raced directory is never counted.
   Nothing infers creation from a directory merely existing afterwards.
5. Carry it on the existing job-level channel, not a new structure.
   Created ancestors are reported the way root displacement already is: a job-level fact on the report that the service appends to the terminal message and the audit summary, so a cancelled job says what now exists inside the picked directory that did not before.
   This is reporting only and grants no removal authority.
6. Directories are never removed to tidy a cancellation.
   A destination ancestor may already have held the operator's own data, and once it exists this executor cannot tell one it created from one it merely opened.
   This concerns destination ancestors only. The owned stage directory and the planned directories beneath it keep the cleanup ADR 0035 decision 7 already gives them, unchanged by this record.
7. Open product choice, for the operator and not for this contract.
   Whether a graceful cancel removes the retained stage is unresolved and is recorded here so it can be answered without redoing this work.
   Current behaviour: cleanup runs, removes only empty planned directories, and reports an issue per directory it cannot remove, so a stage holding staged bytes is retained by failure rather than by decision, and `StageCleanupResult.Retained` stays false while the stage is in fact retained.
   The options are to keep that, to retain deliberately and say so, or to remove the stage and its staged bytes on a graceful cancel.
   The recommendation, offered as a recommendation and not as a decision, is to retain deliberately and say so: the staged bytes are the operator's data, a cancel is not a request to discard them, and the flag should describe the disk rather than the code path that reached it.
   Decisions 1 to 6 do not depend on the answer.
   The operator answered this on 14 September 2026, against the recommendation above, and [ADR 0048](0048-cancelled-stage-removal.md) records the decision and its limits.

## Consequences

A cancelled download still varies in when it stops, and now states which of the permitted effects occurred.
No check is removed, so no ordering gains an attempt it does not have today.
The behaviour change is confined to two records: an item cancelled before its temporary exists is `not_attempted` at `source_read` rather than `failed` and `unresolved` at `staging`, and ancestors created for items that did not copy appear in the terminal message and audit summary.
Items already attempted keep every record they have, and no existing refusal, verification, placement or stage-cleanup assertion is relaxed.
Whether the stage survives a graceful cancel is unchanged by this record and remains the operator's to settle.
