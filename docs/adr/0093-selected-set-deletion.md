# ADR 0093 — delete the selected objects as one job

**Status:** Accepted and delivered; proposed on 27 October 2026 as the record [ADR 0092](0092-exact-object-deletion.md) decision 9 named, for the operator's question of the same day recorded under decision 30 in the [Linux finish line](../audits/2026-09-05/linux-finish-line.md): "do we need like a selector or something to do some batch deletion/modification?".
Written for review before any implementation, and implemented on 27 October 2026 behind the `object-delete` token: the deletion-capable item ledger, the per-member protocol, the job, the multi-selection with its browser-owned dialog and the public proof on both fixtures are in the range that carries this sentence, with the capability deferred behind that token when this record was written.
That capability was returned to the release on 29 October 2026 by [ADR 0052](0052-v1-capability-gate.md) decision 11, the operator's word for one object and the selected set together.
It adds one bridge action behind the capability "Object deletion" that ADR 0092 created, lifts nothing from the [ADR 0052](0052-v1-capability-gate.md) gate, and changes no refusal of [ADR 0020](0020-recovery-containment.md).
Amended on 16 November 2026 by [ADR 0122](0122-the-stores-answer-is-the-result.md) decision 6: each member is deleted by one DELETE of its name and the store's answer is its outcome, so the comparison with the listing, the later-baseline read, the conflict and link refusals and the verifying read are no longer made; the selection, the typed count, the ceiling, the admission of the whole member set before any effect and cancellation between members stay.
Amended on 17 November 2026 by decisions 8 and 9, on the operator's decision of that day that a deletion's selection, like a copy's and a download's, takes a selected folder with everything in it ("we should also do the same for delete in 0.5.0"): a folder row in a selection is a member, decisions 1, 2 and 5 are amended below, and the dialog's line naming the prefix rows left out is withdrawn; large objects in a selection lose their segments as a folder deletion's do; and every deletion's typed words begin with "delete".
**Scope:** Selecting several loaded rows in the working list, and deleting the selected objects as one job whose members are each deleted exactly, by the protocol [ADR 0085](0085-reobserved-destructive-identity.md) decided for prefix deletion and ADR 0092 refined for one object.
Batch modification of a selection, such as a metadata change over many objects, is a later record and is named here only so it is not lost.

## The requirement, and what existed when this was written

When this record was written v1 selected one row at a time, and [ADR 0072](0072-large-namespace-handling.md) decision 3 recorded the rule for later work: select all means the loaded rows, as every surveyed client does, and whole-prefix intent stays the prefix scope.
ADR 0092 gives one selected plain object an exact deletion with an observed outcome, and says a set deletion composes that shape, one name with its own identity per member, deciding here whether a set of N is N synchronous calls or a job and what one confirmation names.

The deletion job family exists: `DeleteService.StartDelete` sweeps a prefix or a container through listing pages and bulk-delete requests, with job rows, counters, the issued index of [ADR 0039](0039-delete-request-authority.md) that is discarded with the job, and cancellation.
The durable item ledger exists for transfers only: a job created with item records admits its members once, records an execution and a verification observation for each, derives its terminal state from them, blocks new jobs for the session when an item record cannot be saved after a change may have occurred, and is failed on reopen when it was interrupted; the store admits that ledger for copy, upload and download, an item's identity is a source and a destination, and its result words are a transfer's.
ADR 0085 decided that a verified deletion gives up the bulk request and issues one DELETE per name immediately after that name's own HEAD, at a cost of 2N + Z object requests, with the listing row as the baseline for an ordinary row and a HEAD of its own as a later baseline for a row listed as zero bytes or as a static large object; it recorded that protocol as owed, and prefix deletion stays deferred until it is built.

## Why a job and not a sequence of calls

A sequence of `MutateService.DeleteObject` calls driven by the interface would reuse everything and add no bridge action, and each call already records its intent, outcome and audit event durably and the interface could report progress and stop between calls.
What the sequence cannot give is ownership of the set apart from the window that started it: which members were asked for, which are still pending and what each one became is state the interface would hold and lose on a close, a navigation or a crash, with nothing in the drawer to show it, and every call holds the writer gate for its own four requests with the rest of the set waiting on a view.
A job owns the set independently of the interface: one durable record of the membership, per-member progress and outcomes in the drawer the operator already has, cancellation between members, and a history that survives the window, which is what every other long operation in this product gives.
The boundary that a single member cannot be cancelled inside its own comparison, DELETE and verifying read is kept deliberately, for one member at a time, rather than removed.
The job also builds, once, the per-name protocol that 0.2.x's prefix deletion then runs over a listing instead of over a selection, so the owed protocol is paid here rather than twice.

## The behaviour oracle

- ADR 0085: the baseline for an ordinary row a job read from a listing is that row; a row listed as zero bytes or as a static large object takes a HEAD of its own as a later baseline; one DELETE per name immediately after its own HEAD; no bulk request; the exposure after the final comparison is stated, never closed.
- ADR 0092 decisions 2 and 3: classification before comparison, a symlink refused in either representation, the literal verifying read with both expansions suppressed, and the outcome words for an originally plain object.
- ADR 0092's session layer: one credentials snapshot binds a member's comparison HEAD, DELETE and verifying read, and a DELETE is never repeated.
- [ADR 0079](0079-caller-named-mutation-binding.md): a destructive request names the portrait its view was built under and is refused before anything when that portrait moved.
- ADR 0039: one destructive request per name, recorded as issued before it leaves, and a name whose issued record cannot be written is not requested.
- [ADR 0043](0043-cancellation-delta-authority.md): a cancellation produces exactly the accepted delta and a truthful retained outcome.
- The item ledger as the transfer families keep it: admission once, fail-closed recording, status unavailable and new jobs blocked when a record cannot be saved after a possible change, item-derived completion, and interrupted jobs failed on reopen with their retained facts.
- ADR 0072 decisions 3 and 4: select all is the loaded rows; a selection has a ceiling the operator can change.
- DESIGN §4.4: the Delete key opens a safe delete flow, never an immediate deletion; §28.2: a destructive confirmation is typed.

## Decision

1. **Several loaded rows can be selected.**
   Ctrl or Cmd with a click toggles a row, Shift with a click extends from the anchor, Ctrl or Cmd with A selects every loaded row and nothing beyond them, and Escape clears the selection; a plain click keeps today's single selection.
   With several rows selected the inspector shows a summary, the number of objects and their listed bytes and the number of prefix rows, instead of one object's detail, and no HEAD is made for the summary.
   Selection is presentation; no identity decision reads it, and the rows a selection names enter the request only through the captured snapshot of decision 5.

2. **One job deletes the selected objects, and only objects.**
   `DeleteService.StartSelectedDelete(ctx, req SelectedDeleteRequestDTO) (string, error)` starts a job of a new type, `delete_selected`, from a request carrying the scope the view was built under, the container, the member rows and the typed confirmation.
   A member row is the listing row as the working list carries it, with its kind, name, listed bytes, listed entity tag and whether the listing marked it as a static large object, so the request preserves the facts the baseline rule and the prefix exclusion read; a row whose kind is prefix is not a member, and the dialog names such rows as not included and points at the bulk deletion that owns prefixes, while an object row whose name ends in a slash, a literal marker, is a member like any other object.
   The member set is unique by name; a request naming one object twice is refused.
   The request is refused before any job row when the scope is empty or has moved, when it names no member, when a member's kind is not object, or when its members exceed the selection ceiling of ADR 0072, which this record reuses for selected sets; the dialog states the ceiling.

3. **Each member is deleted by the verified per-name protocol, built here.**
   For a member listed with a non-zero size and not marked as a static large object, the baseline is its listed row: the bytes and entity tag the operator saw, as a plain object.
   For a member listed as zero bytes or as a static large object, the baseline is a HEAD the job takes before the member's comparison, as ADR 0085 decided, because such a row may be a plain object, a marker, a dynamic or static large object or a link and the row cannot say which; that later baseline fixes the member's class and identity, and a link found there is refused at once.
   Then, under one credentials snapshot, the job reads the name again immediately before acting, classifies what it finds and compares it with the member's baseline: a symlink in either representation is refused; a plain object whose class, length and entity tag match a plain baseline is deleted by one DELETE issued immediately after that HEAD; a dynamic or static large object matching a later baseline of that class is deleted manifest-only, by a plain DELETE of the manifest with its segments untouched; a directory marker reached as a row is a literal object and is deleted as one, its children untouched.
   Any difference between the baseline and the comparison HEAD refuses that member as changed since listed, with both identities recorded, and deletes nothing for it; an ordinary row keeps its listing baseline whatever the comparison finds, so a manifest or a link now at the name is a difference and never a replacement baseline.
   Each DELETE is followed by the literal verifying read of ADR 0092 decision 3, and for a member whose baseline was plain its outcome words apply unchanged: absence or a successor of another kind or another plain identity is deleted with the successor recorded; the same identity, a lost reply, a server error, an incomparable or failed read is unverified.
   For a member whose baseline was a manifest the two reads describe unlike things, the assembled content before and the stored manifest after, so no identity comparison is made: absence, or a successor that is not a manifest of the same kind, is deleted with the successor recorded, and a manifest of the same kind still observed at the name is unverified, as are a lost reply, a server error and a failed read; a comparable manifest baseline is not taken in this record.
   No bulk request is ever sent, and no DELETE is repeated for any reason (ADR 0039).

4. **Members are durable items, and the job's state is the sum of theirs.**
   The item ledger is extended, in the smallest way that makes it carry a deletion, and the `delete_selected` type is admitted to it: an item's identity is its source alone, the Swift object, with no destination; `deleted` and `unverified` join the result words; and an item may record the successor the verifying read observed, its class, bytes and entity tag, as an uploaded item records its predecessor.
   At the start the whole member set is admitted once, with each member's baseline facts; if that admission fails the job fails before any request and nothing is sent.
   Before a member's comparison HEAD its issued mark is recorded, as ADR 0039 requires, so nothing of ours, local or remote, runs between the final HEAD and the DELETE; that mark proves only that the member was about to be requested, and whether a DELETE was actually sent is what the member's outcome record says.
   If the issued mark cannot be written the member is not attempted and no later member is touched.
   Each member's outcome is recorded when its verifying read settles: deleted, deleted with its successor, refused with its kind, changed since listed with both identities, unverified, or not attempted; if that record cannot be written after a DELETE may have been sent, the member is left unresolved, no later member is touched, the job's status becomes unavailable with the retained reason, and new jobs are blocked for the session, as the ledger already does for a transfer; nothing is retried.
   The job's terminal state is derived from its items by whether any DELETE was issued: complete when every member was deleted; partial whenever a DELETE was issued and the job is not complete, which covers a member deleted beside one that was not, every reply lost, a cancellation after a DELETE whether or not it was confirmed, and recording that became unavailable after a DELETE, because an uncertain effect is never presented as no effect, as the existing deletion job already holds; cancelled when cancellation landed before any DELETE was issued, whatever refusals were recorded before it; failed when the job could not start, when recording became unavailable before any DELETE was issued, when it was interrupted, or when it finished with no DELETE issued because every member was refused or changed since listed, with a reason that says nothing was deleted and the items saying why.
   When recording failed, the status unavailable stays authoritative over any terminal snapshot that could still be saved honestly afterwards.
   Cancellation lands between members: the member in flight finishes its own HEAD, DELETE and verifying read, no later member is touched, every later member is recorded as not attempted, and the counts are exact per item, never inferred from aggregates.
   A job interrupted by a shutdown is failed on reopen with its retained facts, as every interrupted job is: a member with an outcome keeps it, a member with an issued mark and no outcome is shown as unresolved because its DELETE may have been sent, a member with neither is not attempted, and nothing resumes; the operator selects and starts again.
   The job row and its items are the durable record, with the start and end audit events the deletion family already writes; the synchronous ledger of [ADR 0058](0058-object-metadata-durable-facts.md) is for one-shot mutations and is not used here.

5. **What the operator is told, and what the confirmation binds.**
   Delete selected in the working list, and the Delete key with several rows selected, open one dialog owned by the browser state, as the single object's is, around one snapshot captured when it opens: the scope, the container, the unique member set with each member's listed facts, and the excluded prefix rows.
   The dialog lists the members, the first fifty by name with the full count, the excluded prefix rows if any, the ceiling, and the container's versioning consequence in the sentence ADR 0092 settled from the [measurement of 27 October 2026](../experiments/2026-10-27-delete-under-versioning.md).
   The confirmation is the number of members in that snapshot, typed (§28.2), because a count the operator has to type is a count they have read, and the request is built from the snapshot alone, never from the live rows.
   While the dialog is open the snapshot is checked against the view: if the scope moves, the selection changes, a member leaves the listing, or any member's listed kind, bytes, entity tag or static-large-object mark differs from the snapshot after a refresh, the dialog closes without deleting and says why, and the operator selects and opens it again; a count that still matches is never enough.
   Progress and per-member outcomes show in the jobs drawer as for every job, the listing refreshes when the job ends, and a member whose verifying read observed a successor says so in its item, never as a plain deletion.

6. **The action is born deferred under the capability ADR 0092 created.**
   `DeleteService.StartSelectedDelete` joins the deferred table under "Object deletion", so the one token `object-delete` offers one object and a selected set together or neither, and returning the capability is one word of the operator's for both.
   Its contract row is a job with the scenarios happy, refusal, conflict, cancel and partial, which is the real contract of a job that never retries a destructive request, and the preservation baseline is admitted through the [ADR 0069](0069-preserve-covered-scenario-cells.md) drift route from the candidate run.

7. **What follows from it.**
   0.2.x's prefix and container deletion becomes the same per-name protocol run over a listing pass instead of a selection, with the bulk request retired as ADR 0085 decided, and its own record decides the listing-to-request race and names first listed in a later pass.
   Batch modification of a selection, the operator's other half, gets its own record once the selection model is in place.

8. **A selected folder is deleted with everything in it** (17 November 2026).
   The operator decided that a transfer follows rclone's semantics, where deleting a folder deletes everything in it, and that a deletion's selection takes a selected folder whole, as a copy's and a download's do ([ADR 0108](0108-copy-and-download-what-is-selected.md) decision 7).
   **A folder row is a member.**
   "Delete selected…" is offered whenever two or more rows are chosen, folders alone included, and the request's members are the chosen rows: each object row as before, and each folder row, by its name ending in `/`, meaning that folder and everything under it, its own marker object included where there is one.
   A member that lies under a selected folder is refused as named twice, as for a copy.
   **What the job deletes.**
   Before any DELETE, the job lists everything under each folder member and admits each listed object as an item beside the object members, with its listed facts, once, as decision 4 admits a selection; the selection ceiling counts that whole set, and a set beyond it fails the job before any DELETE, naming the folder where the count crossed it.
   Every item is then deleted as decision 3 and [ADR 0122](0122-the-stores-answer-is-the-result.md) decision 6 say: one DELETE of the name, and the store's answer is its outcome.
   An object written under a selected folder after that listing is not deleted, because it was not listed; one deleted by something else in the meantime is already gone, as for any member.
   A folder member with nothing under it deletes nothing and is not an error.
   **Large objects**, whether selected or under a selected folder, are treated as a folder deletion treats them ([ADR 0119](0119-a-bulk-deletion-may-take-segments.md) as amended by [ADR 0120](0120-segments-go-by-default-and-versions-keep-them.md)), on the operator's question of 17 November, "why would we treat them any differently?": the dialog offers the same option to delete each large object's segments after its manifest, ticked when it opens except in a container that is or may be another container's archive; with it ticked each large object loses its segments by the route the folder deletion uses, and a container that keeps versions refuses it as it does there; unticked, a large object loses only its manifest.
   This withdraws, for the selection, decision 3's manifest-only rule and ADR 0119's sentence that the selected-set deletion is not changed.
   **What a running transfer holds.**
   The job claims each folder member as a prefix deletion claims its prefix ([ADR 0105](0105-concurrent-jobs.md) decision 3), so it is refused while an upload or a copy writes inside a selected folder, and no transfer starts inside one while it runs; an object member keeps the refusal it has.
   **What the operator is told, and what the confirmation binds.**
   The dialog lists the selected objects and the selected folders, and for each folder what a listing of it holds now, using the same counting rules as the prefix deletion preview: its objects and bytes, and its possible manifests, the listed large objects and zero-byte rows, in the words the prefix dialog uses for them, saying that their segments go when the segments option is ticked and stay when it is not.
   The selected-set preview shares one archive-owner observation and counts at most eight folders concurrently.
   Closing or retiring the confirmation cancels its preview; failed folder counts remain individually unknown, and object-only selections keep the unread archive default.
   `DeleteService.PreviewSelectedDelete` is a read action requiring the `happy` scenario in the scenario contract.
   Its oracle is the captured selection's folder counts and archive-owner disclosure, returned without a job, audit entry or destructive request; the existing mock and real-Swift archive-preview cases witness that read.
   This adds one required identity and removes or reclassifies none: every previously protected pair remains required and protected, including the existing single-prefix preview.
   Admit its required snapshot and every credited pair through the [ADR 0069 drift route](0069-preserve-covered-scenario-cells.md#review-every-baseline-change), retaining the candidate's expected drift refusal and producer evidence before an independently reviewed baseline update and a fresh full CI run.
   It says that everything under each folder goes, including anything written there before the job lists it.
   The confirmation is decision 9's words; the request is built from the snapshot, and the snapshot check of decision 5 applies to the folder rows as to the objects.
   The line naming the prefix rows left out is withdrawn, with its tests.

9. **Every deletion is typed with the word delete** (17 November 2026).
   The operator asked that a deletion, "whether a file, container or virtual folder", keep the verb "delete" in what is typed to confirm it.
   One object, and one directory marker from the same dialog, is confirmed by typing `delete ` and its name, where until now it took only the button: `MutateService.DeleteObject` and `DeleteMarker` gain the typed words as an argument, a check those two did not have, and refuse any other words before any request; a selection by `delete ` and the number of selected rows; a folder by `delete ` and the container and prefix, where until now the container and prefix alone; a container by `delete ` and its name, as before.
   Emptying a container keeps `empty ` and its name, because it deletes the contents and keeps the container, and a deletion that takes segments still ends ` and segments`.
   Every other deletion's service checks the new words where it checks its words now, so a request with the old words is refused; the Backstage manifest deletion, which types the object's name under its own force, is not changed.

## What this does not touch

Prefix and container deletion stay with `DeleteService.StartDelete` and the bulk request until 0.2.x; segment deletion stays force-only with bulk delete; manifests were deleted manifest-only here and nothing reclaimed a segment until decision 8 gave a selection the folder deletion's segments option; symlinks are refused.
Nothing here is a lock, a reservation or exclusive ownership, and a member deleted after a match may still have been replaced by a writer landing after its final HEAD, as ADR 0085 states for every path.

## Required proof before implementation is accepted

Tier 1 under DESIGN §65.12.
Backend cases run through the public service on the mock and the owned Swift, with namespace snapshots before and after.

- Happy: a selection of plain objects, a zero-byte object, a non-zero object the listing marks as a static large object and a dynamic large object is deleted; every member's item says deleted; the manifests' segments and every unselected neighbour, in both accounts, are unchanged; the job is complete; the request log shows one HEAD-then-DELETE pair and one literal read per member, a baseline HEAD only for the zero-byte and static-large-object members, no bulk request, and no DELETE repeated.
- Refusals before any job row: an empty or moved scope; no members; a prefix row offered as a member; a name offered twice; members over the ceiling; each with no job row, no item and no request.
- Member refusals that do not stop the job: a symlink member refused with the rest deleted and the job partial.
- Conflict: a member replaced after it was listed, including an ordinary row that now holds a manifest, is recorded as changed since listed with both identities, nothing is deleted for it, the others are deleted, and the job is partial.
- Surviving manifest: a manifest member whose name holds a manifest of the same kind after the DELETE is unverified, not deleted, with the job partial; a manifest member whose name holds a plain object afterwards is deleted with that successor recorded.
- Partial through verification: a plain member whose DELETE reply is lost is unverified, the job is partial, and its item names the uncertainty.
- Zero confirmed deletions: a selection whose every DELETE reply is lost ends partial with every item unverified; a selection whose every member is refused or changed since listed ends failed with nothing deleted, no DELETE sent and every item saying why; a cancellation landing after a first DELETE whose reply was lost ends partial, not cancelled.
- Cancel: a cancellation during a long selection ends the job with the members before it deleted, the one in flight settled, the ones after it recorded as not attempted, and the counts exact; a cancellation before any DELETE was issued ends it cancelled with nothing deleted.
- Recording failures: an issued mark that cannot be written leaves its member not attempted and no later DELETE is sent; an outcome that cannot be recorded after a DELETE leaves its member unresolved, sends no later DELETE, ends the job with its status unavailable and blocks new jobs for the session; a job interrupted after some members is failed on reopen with those members' outcomes kept, the one in flight unresolved and the rest not attempted, and no DELETE follows the reopen.
- Adjacency: no authentication and no other request of ours runs between any member's comparison HEAD and its DELETE, under a gate that places a manifest at the name during any such request.
- Frontend, around the real browser state: toggle, range and select-all over loaded rows only; the summary; the dialog's member list, excluded prefixes, ceiling and typed count; one request built from the snapshot; the dialog closing without deleting when the scope moves, when the selection changes to other names with the same count, and when a refresh changes one member's listed entity tag with the same count; the Delete key opening the dialog and deleting nothing; outcomes shown per item.
- Retained mutants, each failing a case above: a bulk request used for the members; the listed row ignored in favour of a HEAD baseline for an ordinary member; the replacement adopted as the baseline when an ordinary row's comparison finds a manifest; a DELETE repeated after a lost reply; cancellation honoured inside a member between its HEAD and DELETE; the ceiling removed; prefix rows accepted as members; the successor dropped from the item; the request rebuilt from live rows instead of the snapshot; a surviving same-kind manifest recorded as deleted; a later DELETE sent after an outcome record failed.
- The candidate run's drift diagnostic retained and the replacement baseline built from its reports.

Decision 8 withdraws four items above: the happy case's requirement that every manifest's segments stay, which now holds only with the segments option unticked; the refusal of a prefix row offered as a member, the retained case that fails when prefix rows are accepted as members, and the dialog's excluded prefixes; a folder row is now a member, and a member under a selected folder is refused instead.
For decision 8 (17 November 2026): on the mock and a real Swift, a deletion of two objects and a folder holding three, one of them in a subfolder, deletes those five and nothing beside the folder (a sibling whose name starts the same stays), and the folder's marker goes with it; a member under a selected folder is refused before any job; a folder whose contents cross the ceiling fails the job before any DELETE; a selected folder another transfer is writing into is refused, and a transfer into a folder a running deletion holds is refused; a folder with nothing under it deletes nothing; the frontend offers "Delete selected…" for folders alone and for a mix, lists the folders with their previewed counts, sends the folder rows and types the number of rows; a selected large object and one under a selected folder lose their segments with the option ticked and keep them unticked; in a container that keeps versions the option is refused before any job; in a container that is or may be another's archive the option starts unticked; the browser journey deletes a file and a folder selected together, on the mock and both real stores.
For decision 9: each deletion refuses its old words and accepts the new ones, in the service where it checks them and in the dialog, and the journeys type the new words.

## What this does not establish

It does not bound the interval after a member's final HEAD, prove that bytes are gone, or identify an observed successor's origin.
It does not lift the gate, decide prefix or container deletion, batch modification, or the return of any deferred capability, and it leaves the valuable-data release hold in force.
