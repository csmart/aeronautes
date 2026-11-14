# ADR 0120 — a large object's segments go with it by default, and stay wherever versions are kept

**Status:** Accepted on 15 November 2026 for 0.5.0, on the operator's decision of that day.
It answers the question [ADR 0119](0119-a-bulk-deletion-may-take-segments.md) left open: the option that deletes segments starts ticked.
The same message asked that deleting a large object in a container that keeps versions be made correct, and that browser journeys prove deletion with and without versioning.
Tier 1 under DESIGN §65.12.
**Scope:** the starting value of the segments option in the bulk deletion dialog (ADR 0119) and in the one-object manifest dialog ([ADR 0095](0095-prefix-deletion-by-the-per-name-protocol.md) decision 8); `DeleteService.DeleteManifestWithSegments`, which gains the refusal ADR 0119 decision 3 gave the bulk route; and, in a bulk deletion with the option, when a dynamic manifest is read (decision 6).
How segments are bounded, compared and recorded does not change.
The selected-set deletion, the object and marker dialogs, Copy, Mirror and the upload's reclamation ([ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 5) are not changed.

## What exists, and what was found

Two dialogs can delete a large object's segments with it.
The bulk dialog offers "Also delete the segments of large objects" for a prefix, a container's contents or a container; the manifest dialog offers "Also delete the segments" for one large object.
Both start unticked, and the operator types words that name the choice: the scope followed by ` and segments`, or the object's name.

ADR 0119 left one question for the operator, whether the bulk option should start ticked, because K-SEG-003 and K-SEG-004 allow segments to go only as an explicit force action and a ticked start is a choice only the operator can make.
The operator has answered: it should remove segments by default.

The operator also asked what happens to a versioned large object.
[A measurement on the owned Swift](../experiments/2026-11-15-large-objects-under-versioning.md) answers it:

- Under a history location the store keeps a deleted manifest in the archive as a manifest of the same kind, naming the same segments.
  That version can be read only while its segments exist.
- The bulk route refuses the segments option in a container that keeps versions (ADR 0119 decision 3).
  The one-object route does not look: under a history location it completed and deleted the segments the archived manifest still names.
  That is a defect, and it is older than ADR 0119.
- Deleting a container that has a history location moves what it held into the archive and deletes the container; the archive, which is another container, keeps everything.
- Deleting that archive with the segments option deletes each archived manifest with its segments.

### What proving this found

The browser journey this record asks for ran against a real Swift and ended partial at its last step, the deletion of the archive.
[Its records](../experiments/2026-11-15-large-objects-under-versioning.md) show two more things.

- **An upload into a container that keeps history leaves its staged objects in the archive.**
  This application stages an upload beside its destination and removes the stage once the object is published; for a file uploaded in segments the stage is a dynamic manifest ([ADR 0111](0111-segmented-uploads-as-static-large-objects.md) decision 4).
  The store archives whatever is deleted from such a container, so the archive receives a version of each staged object: a second copy of a plain object's bytes, and for a segmented file its staged dynamic manifest, which names the same segments as the published object.
  This is older than this record, and this record does not change it.
  It does make decision 2 a rule for the ordinary case: after any segmented upload by this application, the archive holds a manifest that names the live object's segments.
- **With the option, a dynamic manifest that shares its segments with another manifest of the same job was refused as changed.**
  The archive held the published static manifest and the staged dynamic manifest of one upload.
  The job deleted the static manifest and queued its segments, and while its workers deleted them it read and compared the dynamic manifest, whose length and entity tag are made from those segments.
  It refused that manifest as changed, by its own doing, left it, and ended partial with the archive standing.
  ADR 0119 decision 4 describes this for a namespace that is the scope or lies inside it, and lets the members in flight settle only there.

## The behaviour oracle

- The operator's decision of 15 November 2026: segments are removed by default.
- ADR 0119 decision 3: segments are never taken where the container keeps versions, or where that cannot be read.
- ADR 0119 decisions 2 and 4 to 8, and ADR 0095 decision 8: the typed words, the order, the comparisons and the records, all unchanged.
- [ADR 0102](0102-versioning-matrix-fixed-point-and-archives.md) decision 3: a bulk deletion's preview says whose archive the container is, or that this could not be established.
- The measurement above, for what the store does.

## Decision

1. **The bulk option starts ticked.**
   When a preview counted a row that may be a manifest and the container keeps no versions, the option is offered as before and is now ticked when the preview arrives.
   The text to type is then the scope followed by ` and segments` from the start, and the dialog says what goes and what is not known, as ADR 0119 decision 8 words it.
   Unticking it clears what was typed and returns the text to the plain scope, and the job is then manifest-only.
   A new preview returns the option to its starting value.
   The request, the comparison of the typed words with the request, and every rule of the job are ADR 0119's, unchanged: nothing is deleted that the operator did not type words for.
2. **It starts unticked in a container that is, or may be, another container's archive.**
   An archived version and the object it was a version of can name the same segments: a static manifest written again with the same entries, or a dynamic manifest whose prefix did not change.
   Deleting the archived version's segments then breaks the current object, and an archive is the one place where that sharing is likely and not just possible.
   So when the preview names a container that keeps its versions here, or says that this could not be established, the option is offered unticked and the dialog says why; the operator may tick it.
   Once the owning container is gone the preview names no owner and the option starts ticked.
3. **The one-object option starts ticked too.**
   The manifest dialog's "Also delete the segments" is ticked when the dialog has read that the container keeps no versions.
   The dialog asks for the ownership check then, as it does today when the option is ticked by hand, and the object's name is typed before the deletion can start, as today.
   Unticking it returns the dialog to the manifest-only deletion and its button.
   One starting value for both dialogs is the rule an operator can remember: a large object deleted from either loses its segments unless they say otherwise.
4. **Segments are never taken where the container keeps versions, by either route.**
   `DeleteManifestWithSegments` reads the container before any job row exists and refuses when the versioning mode it observes is not off, or cannot be read, with the words the bulk route uses.
   The manifest dialog does not offer the option there and says that segments are kept because a kept version still uses them; where the container's versioning could not be read it says that instead.
   The manifest-only deletion is unchanged: under a history location the manifest moves to the archive, and under a versions location the name reverts to an archived predecessor if there is one ([ADR 0094](0094-marker-and-manifest-deletion-return-ready.md)).
   The mode is read once, at the start, as ADR 0119 decision 3 reads it.
5. **A container that keeps versions is deleted as before, and its archive is deleted as a container of its own.**
   Nothing in this record deletes an archive because the container that used it was deleted.
   Deleting a container that keeps history moves its objects into the archive, manifests as manifests, keeps every segment, and deletes the container.
   Deleting the archive, a second deletion the operator starts, deletes the versions, and with the option their segments.
   So the operator can delete everything, in two deletions, and each one says what it takes.
   A single action that deletes a container together with what it archived is not built, for the reason under "What this does not do".
6. **With the option, every dynamic manifest waits for the members in flight.**
   ADR 0119 decision 4 lets the members a sweep has in flight settle before a dynamic manifest whose namespace is the scope or lies inside it.
   The same now holds for every dynamic manifest the option meets, wherever its segments are: when the read at a row's turn shows a dynamic manifest, the sweep lets the members it has in flight settle and reads the manifest again as it then stands, and decides and compares from that second read.
   A row that is no longer a dynamic manifest at that read is left to the ordinary member protocol, as a row that cannot be read is.
   The cost is one more read for each dynamic large object, and no deletion of the sweep runs beside one.
   Without the option nothing changes, as ADR 0119 promised.
7. **What follows in the contract.**
   K-SEG-003 and the `AGENTS.md` invariant say that the force action starts ticked by the operator's decision, except in an archive, is typed by name and is refused where the container keeps versions, for one object and for a scope.
   DESIGN §33 to §35, the release notes, the test plan and the journeys table follow.
   ADR 0119's closing question is answered by this record.

## Why these shapes

**Refusing the option in an archive** was considered for decision 2 and not taken.
Pruning an archive is ordinary maintenance (ADR 0102), and an archive whose owner is gone is an ordinary container of large objects, so the option stays available there.
Starting unticked keeps the operator's default from deleting a live object's data by sharing, and leaves the choice with them.
Whether one deletion should take a container's archive as well is still the operator's question, and nothing here answers it.

**Leaving the one-object dialog unticked** was considered for decision 3 and not taken.
It would make the same object lose its segments when its folder is deleted and keep them when it is deleted alone, which nobody could predict from the dialogs.

**Deleting the archive's entries when the owning container is deleted** was considered for decision 5 and not taken.
An archive is named by a header and several containers can name the same one.
The store files each version under the length and the name of the object it came from, and nothing in that name says which container it came from, so in a shared archive the versions of one container cannot be told from another's.
Deleting by that name could destroy another container's history, and no listing can rule it out.

## Required proof before implementation is accepted

Tier 1.

- Go, service, on the mock: `DeleteManifestWithSegments` is refused in each of the three versioning modes and where the container cannot be read, with no job row and no DELETE sent; with versioning off it behaves as before.
- System, on the owned Swift, in a container with a history location holding a plain object, a static and a dynamic large object: the one-object deletion with segments is refused and nothing changes; the manifest-only deletion moves the manifest into the archive as a manifest of the same kind, with every segment unchanged; deleting the container is refused the option, and without it completes, deletes the container and leaves every segment; deleting the archive with the option completes, deletes the archive and every segment those manifests named, and leaves a neighbour's segment unchanged.
- Go, service, on the mock: a static and a dynamic manifest in scope name the same segments, held in a container outside the scope. On a store that holds each segment's DELETE until the dynamic manifest has first been read, and the dynamic manifest's comparison until those segments are gone, each for a bounded time, the job is complete, both manifests are deleted, each segment is requested once and nothing is recorded as left.
- Frontend, bulk dialog: the option is ticked when a preview with a possible manifest arrives in a container that keeps no versions, and the text to type names the segments; unticking clears what was typed and returns the plain text; a new preview ticks it again; it is offered unticked, with the reason, when the preview names an owner or says the answer is not established; it is not offered where versions are kept; `segments` is sent only when ticked.
- Frontend, manifest dialog: the option is ticked once the container's facts say no versions, the ownership check is asked, and the deletion waits for the typed name; unticked, the manifest-only deletion runs as before; it is not offered, with the reason, where the container keeps versions or its versioning could not be read.
- Browser journeys, on the mock and both real clouds: a file uploaded with the static choice and one with the dynamic choice (ADR 0111), each as the store publishes it, are each deleted from the manifest dialog without touching the option, and the run's own session lists none of their segments afterwards; a container holding a large object is deleted without touching the option, and the same listing is empty.
- A browser journey on each store that keeps versions, which of the three is the Swift cloud: in a container given a history location through the container editor, deleting a plain object and a large object offers no segments option, the run's own session finds each in the archive and every segment still listed; deleting that container leaves the archive and the segments; deleting the archive without touching the option leaves neither a version nor a segment.
- Retained mutants, each failing a case above: a dynamic manifest outside the scope handled without waiting for the members in flight; the one-object route admitting a container that keeps versions; either dialog starting unticked; the bulk dialog starting ticked in an archive; either dialog offering the option where versions are kept; the unticked bulk dialog still sending `segments`.

## What this does not do

It does not prove that no other object uses a deleted segment; the dialogs say so, as before.
It does not delete an archive's contents when the container that used it is deleted: the operator deletes the archive, as a container.
Whether one action should do both is the operator's to decide, and the reason it is not built is given above.
A selection of objects still has no segments option, so a selected large object keeps its segments.
The manifest dialog does not scan for owners, so in an archive its option starts ticked; the object's name is still typed.
That matters more than it reads: an archive holds, for every file this application uploaded in segments into the container it serves, a staged manifest that names the live object's segments.
An upload into a container that keeps history still leaves its staged objects in the archive; whether uploads should stage where no versions are kept is a later decision.
The versioning mode is read at the start of a job; a container that begins to keep versions after that is not seen.
Modern object versioning is refused the option like the legacy modes and is otherwise not managed (ADR 0102 decision 4).
The mock shows a versioning mode and archives nothing, and the Ceph cloud offers no container versioning, so the versioned browser journey runs on the Swift cloud alone; the owned Swift proves the same behaviour through the services on every CI run.
