# ADR 0093 — delete the selected objects as one job

**Status:** Proposed on 27 October 2026 as the record [ADR 0092](0092-exact-object-deletion.md) decision 9 named, for the operator's question of the same day recorded under decision 30 in the [Linux finish line](../audits/2026-09-05/linux-finish-line.md): "do we need like a selector or something to do some batch deletion/modification?".
Written for review before any implementation.
Nothing here is built.
It proposes one new bridge action behind the capability "Object deletion" that ADR 0092 created, lifts nothing from the [ADR 0052](0052-v1-capability-gate.md) gate, and changes no refusal of [ADR 0020](0020-recovery-containment.md).
**Scope:** Selecting several loaded rows in the working list, and deleting the selected objects as one job whose members are each deleted exactly, by the protocol [ADR 0085](0085-reobserved-destructive-identity.md) decided for prefix deletion and ADR 0092 refined for one object.
Batch modification of a selection, such as a metadata change over many objects, is a later record and is named here only so it is not lost.

## The requirement, and what exists today

v1 selects one row at a time, and [ADR 0072](0072-large-namespace-handling.md) decision 3 recorded the rule for later work: select all means the loaded rows, as every surveyed client does, and whole-prefix intent stays the prefix scope.
ADR 0092 gives one selected plain object an exact deletion with an observed outcome, and says a set deletion composes that shape, one name with its own identity per member, deciding here whether a set of N is N synchronous calls or a job and what one confirmation names.

The deletion job family exists: `DeleteService.StartDelete` sweeps a prefix or a container through listing pages and bulk-delete requests, with job rows, item accounting under [ADR 0036](0036-delete-identity-accounting.md), one destructive request per name under [ADR 0039](0039-delete-request-authority.md), and cancellation.
ADR 0085 decided that a verified deletion gives up the bulk request and issues one DELETE per name immediately after that name's own HEAD, at a cost of 2N + Z object requests, and recorded that protocol as owed; it has not been built, and prefix deletion stays deferred until it is.

## Why a job and not a sequence of calls

A sequence of `MutateService.DeleteObject` calls driven by the interface would reuse everything and add no bridge action, and it would be wrong for the sizes that make a selector worth having.
Each member costs four requests through a synchronous bridge call that holds the writer gate, a thousand members take minutes with no progress, no cancellation inside the bridge and no record of what happened if the window closes, and the loaded rows can number a hundred thousand under the retention ceiling.
Every other long operation in this product is a job with progress, cancellation, item records and a history, and the operator has that drawer already.
The job also builds, once, the per-name protocol that 0.2.x's prefix deletion then runs over a listing instead of over a selection, so the owed protocol is paid here rather than twice.

## The behaviour oracle

- ADR 0085: the baseline for a name a job read from a listing is that listing row; a zero-byte row, which may be a manifest, takes a HEAD of its own as a later baseline; one DELETE per name immediately after its own HEAD; no bulk request; the exposure after the final comparison is stated, never closed.
- ADR 0092 decisions 2 and 3: classification before comparison, a symlink refused in either representation, the literal verifying read with both expansions suppressed, and the outcome words.
- ADR 0092's session layer: one credentials snapshot binds a member's comparison HEAD, DELETE and verifying read, and a DELETE is never repeated.
- [ADR 0079](0079-caller-named-mutation-binding.md): a destructive request names the portrait its view was built under and is refused before anything when that portrait moved.
- ADR 0039: one destructive request per name, recorded as issued before it leaves.
- [ADR 0043](0043-cancellation-delta-authority.md): a cancellation produces exactly the accepted delta and a truthful retained outcome.
- ADR 0072 decisions 3 and 4: select all is the loaded rows; a selection has a ceiling the operator can change.
- DESIGN §4.4: the Delete key opens a safe delete flow, never an immediate deletion; §28.2: a destructive confirmation is typed.

## Decision

1. **Several loaded rows can be selected.**
   Ctrl or Cmd with a click toggles a row, Shift with a click extends from the anchor, Ctrl or Cmd with A selects every loaded row and nothing beyond them, and Escape clears the selection; a plain click keeps today's single selection.
   With several rows selected the inspector shows a summary, the number of objects and their listed bytes and the number of prefix rows, instead of one object's detail, and no HEAD is made for the summary.
   Selection is presentation; no identity decision reads it, and the rows a selection names are the loaded rows at the moment of the action, carried into the request by name.

2. **One job deletes the selected objects, and only objects.**
   `DeleteService.StartSelectedDelete(ctx, req SelectedDeleteRequestDTO) (string, error)` starts a job of a new type, `delete_selected`, from a request carrying the scope the view was built under, the container, the selected object rows as name, listed bytes and listed entity tag, and the typed confirmation.
   Prefix rows in a selection are not members: the dialog names them as not included and points at the bulk deletion that owns prefixes.
   The request is refused before any job row when the scope is empty or has moved, when it names no member, or when its members exceed the selection ceiling of ADR 0072, which this record reuses for selected sets; the dialog states the ceiling.

3. **Each member is deleted by the verified per-name protocol, built here.**
   For a member whose listed size is not zero, the baseline is its listed row: the bytes and entity tag the operator saw.
   For a zero-byte member the baseline is a HEAD the job takes, as ADR 0085 decided, because the row may be a manifest, a marker or an empty object and the row cannot say which.
   Then, under one credentials snapshot, the job reads the name again, classifies what it finds, and acts: a symlink in either representation is refused and recorded; a plain object whose class, length and entity tag match the baseline is deleted by one DELETE issued immediately after that HEAD; a dynamic or static large object is deleted manifest-only, by a plain DELETE of the manifest, its segments untouched, after the same comparison against its HEAD baseline; a directory marker reached as a row is a literal object and is deleted as one, its children untouched.
   A difference between the baseline and the comparison HEAD refuses that member as changed since listed, with both identities recorded, and deletes nothing for it.
   Each DELETE is followed by the literal verifying read of ADR 0092 decision 3, with its outcome words: absence or a successor of another kind or another plain identity is deleted with the successor recorded; the same identity, a lost reply, a server error, an incomparable or failed read is unverified.
   No bulk request is ever sent, and no DELETE is repeated for any reason (ADR 0039).

4. **Members are job items, and the job's state is the sum of theirs.**
   Each member becomes an item record in the shape the transfer and deletion families use, carrying its baseline, its outcome word and what the verifying read observed: deleted, deleted with a successor, refused with its kind, changed since listed, or unverified.
   The job completes when every member was deleted, is partial when any member was not, and fails when it could not start at all; counts in the drawer are exact per item, never inferred from aggregates.
   Cancellation lands between members: the member in flight finishes its own HEAD, DELETE and verifying read, no later member is touched, and the job records exactly which members were done, which were not, and the one it was on.
   The job row and the item rows are the durable record, with the start and end audit events the deletion family already writes; the synchronous ledger of [ADR 0058](0058-object-metadata-durable-facts.md) is for one-shot mutations and is not used here.

5. **What the operator is told.**
   Delete selected in the working list, and the Delete key with several rows selected, open one dialog that lists the members, the first fifty by name with the full count, the excluded prefix rows if any, the ceiling, and the container's versioning consequence in the sentence ADR 0092 settled from the [measurement of 27 October 2026](../experiments/2026-10-27-delete-under-versioning.md).
   The confirmation is the number of members, typed (§28.2), because a count the operator has to type is a count they have read.
   Progress and per-member outcomes show in the jobs drawer as for every job, the listing refreshes when the job ends, and a member whose verifying read observed a successor says so in its item, never as a plain deletion.

6. **The action is born deferred under the capability ADR 0092 created.**
   `DeleteService.StartSelectedDelete` joins the deferred table under "Object deletion", so the one token `object-delete` offers one object and a selected set together or neither, and returning the capability is one word of the operator's for both.
   Its contract row is a job with the scenarios happy, refusal, conflict, cancel and partial, which is the real contract of a job that never retries a destructive request, and the preservation baseline is admitted through the [ADR 0069](0069-preserve-covered-scenario-cells.md) drift route from the candidate run.

7. **What follows from it.**
   0.2.x's prefix and container deletion becomes the same per-name protocol run over a listing pass instead of a selection, with the bulk request retired as ADR 0085 decided, and its own record decides the listing-to-request race and names first listed in a later pass.
   Batch modification of a selection, the operator's other half, gets its own record once the selection model is in place.

## What this does not touch

Prefix and container deletion stay with `DeleteService.StartDelete` and the bulk request until 0.2.x; segment deletion stays force-only with bulk delete; manifests are deleted manifest-only here and nothing reclaims a segment; symlinks are refused.
Nothing here is a lock, a reservation or exclusive ownership, and a member deleted after a match may still have been replaced by a writer landing after its final HEAD, as ADR 0085 states for every path.

## Required proof before implementation is accepted

Tier 1 under DESIGN §65.12.
Backend cases run through the public service on the mock and the owned Swift, with namespace snapshots before and after.

- Happy: a selection of plain objects, one zero-byte object and one manifest is deleted; every member's item says deleted; the manifest's segments and every unselected neighbour, in both accounts, are unchanged; the job is complete; the request log shows one HEAD-then-DELETE pair per member, a second HEAD only for the zero-byte member, no bulk request, and no DELETE repeated.
- Refusals before any job row: an empty or moved scope; no members; members over the ceiling; each with no job row, no item and no request.
- Member refusals that do not stop the job: a symlink member refused with the rest deleted and the job partial.
- Conflict: a member replaced after it was listed is recorded as changed since listed with both identities, nothing is deleted for it, the others are deleted, and the job is partial.
- Partial through verification: a member whose DELETE reply is lost is unverified, the job is partial, and its item names the uncertainty.
- Cancel: a cancellation during a long selection ends the job with the members before it deleted, the one in flight settled, the ones after it untouched, and the counts exact.
- Adjacency: no authentication and no other request of ours runs between any member's comparison HEAD and its DELETE, under a gate that places a manifest at the name during any such request.
- Frontend, around the real browser state: toggle, range and select-all over loaded rows only; the summary; the dialog's member list, excluded prefixes, ceiling and typed count; one request carrying the rows and the scope; the Delete key opening the dialog and deleting nothing; outcomes shown per item.
- Retained mutants, each failing a case above: a bulk request used for the members; the listed row ignored in favour of a HEAD baseline for a non-empty member; a DELETE repeated after a lost reply; cancellation honoured inside a member between its HEAD and DELETE; the ceiling removed; prefix rows accepted as members; the successor dropped from the item.
- The candidate run's drift diagnostic retained and the replacement baseline built from its reports.

## What this does not establish

It does not bound the interval after a member's final HEAD, prove that bytes are gone, or identify an observed successor's origin.
It does not lift the gate, decide prefix or container deletion, batch modification, or the return of any deferred capability, and it leaves the valuable-data release hold in force.
