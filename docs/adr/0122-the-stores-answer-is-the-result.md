# ADR 0122 — the store's answer is the result

**Status:** Accepted on 16 November 2026 for 0.5.0, on the operator's decisions of that day, made during the candidate's test run.
Tier 1 under DESIGN §65.12, because it changes what every upload, copy, download and deletion sends and what each reports.
Amended on 17 November 2026 by decision 13, which lists what the removal step removes and keeps, and by decision 12, on the operator's decision of that day that a transfer follows rclone's semantics: after one confirmation an upload or a download replaces what differs, as rclone copy does.
**Scope:** how a transfer or a deletion writes and deletes names, what it reads around its own requests, and what a job records.
It replaces the staging and commit steps, the conditions and re-reads around a write, and the comparison and verifying reads around a deletion.
It does not change the previews, the acknowledgements and typed words, what versioning keeps, or the rules for segments that large objects share.

## What exists

Aeronautes was built to a stricter rule than the store offers: never write over, and never delete, something the operator was not shown.
To hold that rule it surrounds each of its own requests with others.

- **Uploads and copies are staged.**
  The engine, rclone, is pointed at `.aeronautes/stage/<job>/…` inside the destination container, never at a live name.
  The application then publishes each name itself: it reads the staged object, sends it again to the live name with a create-only condition or after comparing the live name with what the operator acknowledged, checks the live name, and deletes the staged copy.
  A plain file crosses the network three times, and a file uploaded in segments is published by swapping a manifest.
- **Downloads are staged.**
  The engine writes into `.aeronautes-stage-<job>` inside the chosen folder, and the application then checks each file and moves it into place.
- **Deletions are compared and verified.**
  Each name is read and compared with its listing before its DELETE, and read again after it; a name that still answers is "unverified" and the job ends partial.
  A container deletion looks for the container up to five times after its DELETE.
- **Copies are stamped.**
  A copied object carries a fingerprint in its metadata, which a later copy reads to decide whether it may skip.

The costs were found in the 0.5.0 test run.

- The extra passes are slow, and an upload into a container that keeps history leaves a version of every staged copy in the archive (finding F65).
- On a store whose replicas lag, the reads after an accepted DELETE still find the object or the container, so deletions that worked are reported partial; three deletion journeys failed that way in two runs on one cloud (findings F66 and F68).
  When the name is a large object's manifest, its segments are then left behind.
- The rule cannot be kept in any case.
  The store offers no "replace only if unchanged", and the one condition it honours, create only if the name is empty, is decided before the body arrives: a name created during an upload was never refused, twelve times of twelve ([the measurement](../experiments/2026-10-10-conditional-evaluation-point.md)).

The operator ran rclone alone against the same store and saw what it does: each file goes to its final name with one PUT, and the store's answer is the result.

## The behaviour oracle

- The operator's decisions of 16 November 2026: "don't protect against concurrent writers, rely on the service backend, keep the process simple. we have to get rid of the .aeronautes writing and the intermediate stuff and the moving around etc and rely on the service"; "this is just how swift works ... whoever's is last wins"; and all of it in 0.5.0.
- The store's own contract: an object PUT answers once the object is written, and a failed PUT leaves the name as it was; a declared digest that does not match is refused; the last write to a name wins; a container that is not empty is not deleted; a DELETE that is accepted is a deletion.
- What rclone does when pointed at a live name, which is the engine's ordinary use.

## Decision

1. **The rule.**
   The store's answer to a request is the result of that request.
   The application decides what to send from what the operator was shown and confirmed, sends it once, and reports what the store answered.
   It does not read a name again, before or after its own request, to find out whether another writer changed it, and it does not send a condition for that purpose.
   Where two writers act on one name, the last one wins, as the store defines; that is expected and is not an error of the application.
2. **What stays, because it is not about another writer.**
   - The preview of every upload, copy, download and deletion, made from a fresh listing when the operator asks for it.
   - The acknowledgement before anything is replaced, and the typed words that arm a deletion.
   - The digest an upload declares, which the store or the engine checks against the bytes that arrived.
   - "Unknown" as an outcome, when a request got no answer or the store answered with a server error: the application then does not know, and says so.
   - What a container that keeps versions keeps, and the rules and disclosures for segments that large objects may share ([ADR 0102](0102-versioning-matrix-fixed-point-and-archives.md), [ADR 0119](0119-a-bulk-deletion-may-take-segments.md), [ADR 0120](0120-segments-go-by-default-and-versions-keep-them.md)).
   - The engine's containment: it is given the names to transfer and may write only under the destination the operator chose and that destination's segments container ([ADR 0014](0014-child-storage-gateway.md), [ADR 0022](0022-complete-rclone-selection.md)).
3. **An upload writes each file at its name.**
   The engine is pointed at the destination container and folder and is given the files the preview admitted.
   There is no stage name, no second pass and no publishing step.
   A file larger than the chunk size is uploaded in segments by the engine, which writes a dynamic manifest at the file's name; where the job is to publish static large objects ([ADR 0111](0111-segmented-uploads-as-static-large-objects.md)), the application then writes the static manifest over it, one request naming the same segments, and the object stays dynamic if the store refuses that.
   A name the engine cannot carry is sent by the application as one PUT that declares the file's digest.
   Neither route sends a create-only condition, and neither reads the destination before or after its write to learn whether it changed.
   **What a file's outcome is** (added with the upload step, 16 November 2026).
   The engine says which files it wrote, one line for each, and that line is the file's outcome: measured on the pinned engine, a file it was given and did not write gets no such line, even when the engine ends without an error.
   A file the engine reports an error for failed with that error; a file it says nothing about is unknown when the engine itself failed, and was not written when it did not.
   The application asks the store nothing about a name around its write.
   The engine looks each name up once before it writes it, as it does when used alone, to know whether it is replacing an object and which segments that object has; that is the engine's own request, and it decides nothing about whether the file is written.
   **Whether the store returns static manifests** is still learned once for a connection ([ADR 0111](0111-segmented-uploads-as-static-large-objects.md) decision 9), now by reading the first static manifest back at its own name, with no check manifest at a name of the job's own.
   A store that does not return it gets the dynamic manifest put back at that name, and dynamic large objects from then on.
   The store checks a static manifest against the segments it names, by the size and digest of each of the file's chunks: a manifest it refuses fails that file, and the object stays the dynamic one the engine wrote.
   **A replaced large object's own segments.**
   When the engine writes over a large object it removes that object's old segments itself, as it does when used alone: it reads the old manifest before its write and deletes the segments it named after it.
   Those requests are the engine's, they are for the predecessor's own segments, and they are not a check for another writer; the engine can delete only where it can write, under the destination folder in the container and in its segments container, so segments kept anywhere else stay.
   The application's own reclamation after a verified replacement ([ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 5) goes with the verifying read it waited for.
   A name the application sends itself replaces the manifest and leaves the old segments where they are, and the acknowledgement says which of the two will happen.
   **Where the engine may not remove them** (added with the upload step, 16 November 2026).
   Measured on the pinned engine: writing over a large object in a container with a history location keeps the old segments, and in a container with a versions location removes them, although the store's archived version still names them.
   So an upload decides once, at its start, from one read of the container: where it keeps versions of either kind, where the operator's setting keeps replaced segments, and where that setting cannot be read, the engine's deletes are refused and the old segments stay.
   The acknowledgement states which of these holds, and an upload that replaces nothing by its preview is started on the same decision.
   The cost is that the engine's own removal of the segments of an upload that failed is refused with the rest, so those stay as well, as decision 8 already allows.
   **Where versions are kept, a large upload stays dynamic** (added with the upload step, 16 November 2026).
   The static manifest is written over the dynamic one the engine wrote, and a container that keeps versions keeps what is written over.
   Measured on the Swift cloud with a history location: the engine's dynamic manifest stayed in the archive as one more version of each large file.
   With a versions location the store puts the newest kept version back at a name when its object is deleted, so a deleted large object would come back as that manifest.
   So where the container keeps versions of any kind, a static choice uploads as dynamic, the preview says why, and the job records the origin `versioning`.
   The cost is the one [ADR 0111](0111-segmented-uploads-as-static-large-objects.md) chose static to avoid: a dynamic object's reads depend on the segments container's listing.
   **The file's modification time.**
   The engine records a file's modification time on every object it writes, in the metadata key `Mtime`, as it does when used alone; the staged route's publishing request did not carry it.
   The application's own writes of an uploaded file set the same key from the file: the one PUT of a name the engine cannot carry, the static manifest, and a dynamic manifest put back.
   An uploaded object therefore carries it whichever route wrote it.
   **What a running upload holds.**
   An upload claims the folder it writes under, in the container and in its segments container, in place of a stage root ([ADR 0105](0105-concurrent-jobs.md) decision 3): a deletion of ours that would sweep there, or that names an object there, is refused while the upload is alive, and an upload into a folder a sweep of ours holds is refused.
4. **A copy writes each object at its name**, on the same terms.
   The engine reads the source and writes the destination; a name it cannot carry is read and written by the application in one pass.
   No object is stamped, and no stamp is read: the preview compares the two listings.
   **What is copied, skipped and replaced** (added with the copy step, 16 November 2026).
   A name the two listings show with the same size and tag is skipped, and nothing is sent for it.
   A name only the source has is copied.
   A name the listings show as different is replaced when the operator confirmed replacing, and refused when they did not.
   The listings cannot compare an empty row, which may be an empty object, a dynamic manifest or a link, nor a large object, whose row describes its manifest: for such a pair the copy reads each side once, to learn what the names hold and not to watch for another writer.
   The store's description of the two then decides: the same length and the same entity tag are the same content, a name that is no longer there is a first copy, and anything else is a replacement.
   A large object's tag is made from its segments' tags, so two large objects compare as the same only when they are cut at the same places (measured on the owned Swift: a copy made at the source's chunk size has the source's tag).
   A pair cut differently is treated as different, which costs a copy, or a refusal the operator can answer, and never skips a copy that was needed.
   The confirmation asks about the names found this way, and a copy that replaces nothing asks nothing.
   A difference in metadata alone is not a difference: an object the listings show as the same is not written again to refresh its headers.
   **What a copied object carries.**
   Measured on the pinned engine, copying store to store: it copies the content type and the source's modification time, and no other header; and it copies a link as its target's bytes.
   So each name the engine is to copy is read once at the source first.
   A link found by that read is carried as a link, by the application, in one request.
   Where the source has user metadata, a content encoding or disposition, or an expiry, the application gives the destination the source's set in one POST after the engine has written the object, and sends none where the engine delivered all there was.
   A POST replaces the whole set, so for a source that states no modification time it repeats the one the engine wrote, the time the source was last changed: a copied object carries that key whichever of the two wrote its headers.
   That request is sent for every object the engine reported writing, also when the job was cancelled meanwhile, so that a cancel leaves no copied object without its headers; when it fails, the object is reported failed for that reason, and it is at its name without them.
   A POST to a dynamic manifest that does not repeat the manifest header turns it into an empty object (measured on the owned Swift).
   So the request for an object the engine wrote in segments names the segment path the engine reported, and an object too large to be unsegmented, whose path the engine did not report, is sent none and is reported failed for its metadata.
   **The application's own copy** of a name the engine cannot carry is one GET and one PUT that carries the source's headers.
   A plain source's entity tag is declared, and the store refuses bytes that do not hash to it; a large source has no such tag for its assembled bytes, declares none, and is written as one object of at most 5 GiB.
   **What an object's outcome is** is decided as for an upload: the engine's line for it, its error line, or its silence; measured on the pinned engine, a listed name that is no longer at the source gets no line and no error, and is reported as not copied.
   **A replaced large object's old segments** follow the upload's rule, from one read of the destination container at the start, and the confirmation says which applies.
   **The engine's size check is off for a copy.**
   After it writes an object in segments the engine reads the dynamic manifest's size through a listing that can lag the last segment (finding F35), and a copy learns which objects are that large only as it runs.
   Each object and each segment is still checked against the store's entity tag as it is written.
   **What a running copy holds** is what an upload holds: the folder it writes under, in the destination container and in its segments container.
   A `.aeronautes/stage/` name is an ordinary name from this step on: it can be a copy's member, an upload's destination and a copy's destination.
5. **A download writes each file in the folder the operator chose.**
   The engine writes there directly, as it does when used alone; there is no stage folder and no placing step.
   A download still does not replace a local file, because that file is on the operator's own disk and is not another writer on the store: the preview refuses one that exists, and the engine is told to skip a file that has appeared since, which the job reports as not downloaded because it exists.
   **What the engine is given** (added with the download step, 16 November 2026).
   The engine is anchored to the chosen folder, which has been held open since the pick, and is passed `--ignore-existing`.
   Measured on the pinned engine: with that flag a file that exists is left untouched and the engine says so in a line that names it, and without it the file is replaced.
   The engine looks at a name once, just before it writes the file: a file the operator creates in the moment between is replaced, and nothing here can know of it.
   The flag stays forbidden for a copy to a store, where the plan decides what is replaced and the engine must not skip; the runner refuses it there.
   The application makes the folders a file needs before the engine runs, one component at a time through the held folder, refusing a link or a mount point on the way and reporting the folders it made, as before ([ADR 0043](0043-download-created-directory-accounting.md)); the engine then writes files only.
   The preview's refusals stay: a file that exists, a name the filesystem cannot hold, and two names the filesystem would merge are about the operator's disk and not about another writer.
   **What a file's outcome is.**
   The engine's line for a file means it was downloaded.
   Its line that the destination exists means the file was left alone, which is recorded as a conflict.
   Its error line means the file failed with that error.
   A file it said nothing about is unknown when the engine itself did not end normally, and was not downloaded when it did: measured, a listed name that is no longer at the source gets no line and no error.
   Nothing is read back, compared against the source's listing or segments, or pinned, and the separate record of a final publication is no longer made.
   **The folders below the chosen folder.**
   The engine is held to the chosen folder, not to the folders below it: it resolves each file's path itself, as rclone does when used alone, so a folder that another program replaces with a link while the engine writes is followed, and the file lands wherever the link points (found by the reworked system tests; the earlier route had the same gap inside its stage and refused to place such a file).
   By the operator's decision of 17 November ("this uses rclone so it should function like rclone") a download works as rclone does here: nothing is checked after the engine has written, and keeping the engine inside each folder, which would take one engine child for every folder, is not done.
   **A name the engine cannot carry** is read by the application in one GET into a temporary file beside its final name, and is given that name by a link, which does not replace: a file that appeared since the preview makes that a conflict and is left as it is.
   For a plain object the store's own tag is checked against the bytes as they arrive, at no extra request, as the engine checks it on its route; bytes that do not match fail the file and leave nothing at its name.
   **A file or folder that has changed since the preview** is met by the file it concerns: a file that has appeared is a conflict, and a folder that is no longer a folder fails the files under it; the run is not refused as a whole.
   Only a chosen folder that is gone refuses the whole run.
   **What a stopped download leaves.**
   Measured on the pinned engine: interrupted, it removes the file it was writing; killed, it leaves that file in the folder under its own temporary name, which ends `.partial`.
   The application removes nothing, and a job whose engine did not end normally says that such a file may be there.
   **Where the engine route runs** is unchanged: only where the engine can be held to the chosen folder, which is Linux, and every engine file is refused elsewhere as before.
6. **A deletion sends each name's DELETE and takes the answer.**
   Accepted is deleted.
   "Not found" is already gone: it is counted with the deleted, because the outcome asked for is that the name is gone, and it is not a failure.
   Any other refusal is a failure with the store's status, and no answer or a server error is unknown.
   No name is read and compared before its DELETE, and none is read after it; a container is not looked for after its DELETE, and a container the store refuses because it is not empty is reported as that.
   A large object deleted with its segments is still read once, for the segments its manifest names, because nothing else names them; and where a deletion takes segments, a row the listing shows as empty is read once to learn whether it is a dynamic manifest, because the listing cannot say.
   A deletion deletes the name it is given, whatever now holds it: a link is deleted as the link it is, and a manifest deleted without its segments leaves them, as the store does.
   This holds for one object, a marker, a manifest, a selection, a prefix and a container alike.
7. **What a job records for each name** is what was sent and what the store or the engine answered: done, failed with the reason, unknown, or not attempted.
   The records of what a name held before and after, and of what was staged and published, are no longer made.
   Until the removal step a transfer's item keeps its two records, and for an upload the second states the answer that was taken, not a read of the name.
8. **An interrupted job leaves what it had written.**
   Each object the store accepted is whole and at its name.
   A file that was being uploaded in segments can leave segments with no manifest, as the engine leaves them when it is stopped; the job's record names the segment paths the engine reported, and nothing is removed automatically.
9. **No compatibility with earlier builds.**
   By the operator's decision nothing before 1.0.0 is migrated.
   Job records of earlier builds stay readable as far as they are, and a `.aeronautes/stage/` name an earlier build left is an ordinary object: it is listed, and deleted, like any other.
10. **What this supersedes.**
    The staging, commit and fixed-point rules of [ADR 0008](0008-copy-data-path.md), [0037](0037-staged-content-read.md), [0045](0045-route-content-binding.md), [0048](0048-cancelled-stage-removal.md), [0080](0080-upload-content-binding.md), [0081](0081-upload-overwrite-acknowledgement.md) decisions 5 and 6, [0083](0083-native-copy-content-binding.md), [0084](0084-held-manifest-copy.md), [0085](0085-reobserved-destructive-identity.md) and [0087](0087-copy-overwrite-on-upload-terms.md); the stage and placement rules of [0032](0032-download-placement-prerequisites.md), [0060](0060-native-download-content-binding.md), [0061](0061-download-publication-facts.md) and [0070](0070-engine-download-content-binding.md); the stamp of [0096](0096-copy-skip-requires-full-fidelity.md); and the comparison and verifying reads of [0036](0036-delete-identity-accounting.md), [0092](0092-exact-object-deletion.md), [0093](0093-selected-set-deletion.md), [0094](0094-marker-and-manifest-deletion-return-ready.md) and [0095](0095-prefix-deletion-by-the-per-name-protocol.md).
    These are the main ones and not a complete list: each change that carries this out marks the records it replaces, in the change itself.
11. **The scenario contract loses the cells no deletion can reach** (added with the deletion step, 16 November 2026).
    A deletion compares nothing, so it cannot end in a conflict, and the `conflict` scenario leaves the six deletion actions of `internal/scenariocontract`: `MutateService.DeleteObject`, `MutateService.DeleteMarker`, `MutateService.DeleteManifest`, `DeleteService.StartDelete`, `DeleteService.StartSelectedDelete` and `DeleteService.DeleteManifestWithSegments`.
    The behaviour oracle for each is decision 6.
    All six pairs were protected by the preservation baseline of [ADR 0069](0069-preserve-covered-scenario-cells.md), and all six are given up on purpose: the tests that credited them drove a refusal this record removes, so there is no such outcome left to produce.
    The required coverage after the change is `happy` and `refusal` for the three single deletions, and `happy`, `refusal`, `cancel` and `partial` for the three jobs.
    Every other pair the baseline protected stays credited by the system tests on a real Swift: a refusal a caller can cause, a partial job whose member the store refused, and a cancellation that lands between members.
    No other action's row changes in this step, and the replacement baseline is built by ADR 0069's drift route in the change that carries the step out.
    A later step that changes a row names its identities here in the same way.
12. **After one confirmation, an upload or a download replaces what differs, as rclone copy does** (17 November 2026).
    The operator decided on 17 November that a transfer follows the engine's own semantics ("we are using rclone, we should follow the same semantics"), and chose that copies, uploads and downloads replace what differs after one native confirmation, the preview naming what may be replaced.
    A copy already does so by decision 4 and is not changed here.
    **Without the confirmation nothing changes.**
    An upload's preview refuses a name that is live, a download's preview refuses a file that exists, and a download's engine leaves a file that exists alone (decision 5).
    **The confirmation.**
    A download gains the acknowledgement an upload has ([ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 1): with it, the preview names each planned file whose name holds a regular file as one that may be replaced, and refuses outright, as before, a directory, a link or anything else at a file's name or on the way to it, because replacing that is not replacing a file.
    The start then asks the one native confirmation, raised by the backend as for an upload: for a download it names the chosen folder, the bookmark, account and container the files come from, the number of files that may be replaced and the first names; declined, nothing is written and no job starts.
    For both, the question says that each named file or object is replaced when it differs from the source, as rclone copy decides: one of another size is replaced; one of the same size and time is left as it is; and one of the same size whose time alone differs keeps its bytes and has its time set to the source's when its MD5 matches, and is replaced when it does not or when there is no MD5 to compare, as for a large object. For a download it also says that a replaced local file is gone, and an upload's question keeps what it says about versions and segments.
    **What the engine is given.**
    A confirmed upload or download is run without `--ignore-times`, and a confirmed download without `--ignore-existing`, so the engine compares each name as rclone copy does by default.
    Measured on the pinned engine, in both directions on the owned Swift: a name whose size differs is replaced, "Copied (replaced existing)", a local file by renaming the engine's partial file over it; a name whose size and modification time are the same is left alone, "Unchanged skipping"; a name of the same size whose time differs and whose MD5 is the same has only its time set to the source's, "Updated modification time in destination", and is left otherwise alone; one whose MD5 differs is replaced ("md5 differ"); and a large object, for which the store gives no MD5, is replaced whenever its time differs ("Src hash empty" or "Dst hash empty"), measured for a dynamic and a static large object downloaded and a segmented upload.
    Every other run keeps the flags it has.
    The engine sets an object's time by a POST to it, which the application's gateway in front of the engine refused to every child; it lets that request through for a confirmed upload, and only to an object under the destination folder (found by the system test of this decision, where the object kept its old time while the job reported it the same).
    A large file the engine leaves alone is not published again: the static manifest of [ADR 0111](0111-segmented-uploads-as-static-large-objects.md) is written only for a file the engine reports written. A large object never has its time alone set, because it has no MD5 to compare, so no request that sets a time is sent to a manifest.
    **What a name's outcome is**, from the engine's lines as in decisions 3 and 5: replaced is done and counted as a replacement; left alone because it is the same, with or without its time set, is done and counted as already the same, not as copied and not as failed; a new name is done as before.
    **The application's own route** for a name the engine cannot carry does not compare: after the confirmation it replaces what is there, an upload by its PUT and a download by renaming its temporary file over the name, and counts it as a replacement.
    **What this gives up.**
    A file or object whose bytes changed but whose size and modification time did not is not replaced, as with rclone copy used alone; `--checksum` is not used, because a large object has no whole-content MD5 at the store and rclone would then compare it by size alone.
    A large object whose bytes are the same and whose time alone differs is sent again in full.
    A name that appears after the preview is compared and replaced on the same terms without being named in the question.

13. **What the removal step removes, and what stays** (added 17 November 2026, before step 7 of the order of the work).
    Steps 1 to 6 left code and records that served only the staged model, with no live caller; this decision lists them so the removal is one reviewed change with a known edge.
    **Removed, with their tests:**
    - The engine's one-object copy (`Runner.CopyTo`, the copy package's `Engine.CopyTo`) and the gateway's exact-object scope that only it used; the runner's rule that a remote destination which is not live must lie under `.aeronautes/stage/`, with the option that says a destination is live, since every remote destination is now the names themselves; and the stage-namespace helpers. The refusal of a `..` segment stays.
    - The recovery kinds `staging`, `committing` and `committed`, which only the staged model wrote, and the interrupted-job count of inconclusive commits that read them; only `captured`, the segment paths an engine reported, is still written. Applied migrations are history and are not edited.
    - The item records of the staged model and everything that writes or reads them: the final-publication record, the predecessor, successor and acknowledged-baseline records of a deletion and an overwrite, an item's staging fields, the copy's content ladder and the store helpers only it and the removed verifying reads used; the small tag parser the copy still uses stays. The jobs drawer's lines for them go with them. A job an earlier build recorded stays readable as far as it is (decision 9): its rows still read, and the parts this build no longer reads are not shown.
    - The `conflict` scenario from the rows of `TransferService.StartUpload`, `StartCopy`, `StartMirror` and `RerunJob` in `internal/scenariocontract`: an upload never records a conflict, since each name is replaced, left as the same after a confirmed comparison (decision 12), failed, unknown or not attempted; a copy records an unconfirmed difference as refused; a re-run only starts a copy; and Mirror refuses every start; so nothing can end in a conflict there. No test credited any of these cells and the preservation baseline of ADR 0069 protects none, so the drift route records the change and gives nothing up. `StartDownload` keeps `conflict`, which a file that appeared since the preview produces (decision 5): the queued-download system test shows that outcome today but emits no credit for it, and the removal change makes it emit the credit, so the cell is proved by a claim and not only by the outcome.
    **Renamed:** the claim refusals that say a deletion may not take a name a running upload or copy writes under are named for the claim they check, not for a stage, and their messages say so.
    **Kept, on purpose:**
    - Each transfer item's second record, against the expectation in decision 7. It now states the answer taken, at no request to the store; the job's item totals, a re-run's refusal of a job with unverified items and the jobs drawer read it; and a download's check of a folder it made is a real second answer. Removing it would change what those readers say about every job for no operator-visible gain.
    - The stage names `staging` and `placement` in the allowlist of an item's stage, so a row an earlier build wrote still reads.
    - The application's own read of an object's whole content, which the service journeys use as an independent read, and its literal read of an object's headers, which a deletion test uses the same way; their comments are corrected.
    **The design document** (`docs/DESIGN.md`) is rewritten where it describes staging, commit, the fixed point, stamps or verifying reads as current behaviour, to describe what ADR 0122 does instead, and the engine document's staged rows and `copyto` section go. Dated audit and experiment records are history and are not edited.

## What this gives up

It is written down so that nobody reads the old promise into the new behaviour.

- A name that another writer creates or changes between the preview and the request is written over, or deleted, without notice; after decision 12's confirmation an upload or a download compares it as rclone copy does instead, and leaves it when it is the same.
- An object the store still serves for a moment after it accepted the DELETE is reported deleted, which it is.
- A transfer that fails part-way has written some names and not others, and says which; nothing is rolled back.
- A large upload that is stopped can leave segments behind.
- A large object replaced through the application's own PUT, or one whose segments are kept outside the destination's segments container, leaves its old segments behind.

## Why these shapes

**Keeping the stricter rule where it is cheap**, for instance the create-only condition on the application's own PUT, was considered and not taken.
It would make the two routes behave differently for the same job, it adds an outcome the operator has to understand, and the measurement above shows it does not hold during the upload itself.

**Writing static manifests through the engine** is not possible: rclone writes dynamic manifests only.
Dropping static large objects would be simpler still, but the store checks a static manifest against its segments and a dynamic one names a prefix that anything can be written into, so the one extra request is kept.

**Keeping the reads after a DELETE as information only** was considered and not taken: a read that changes nothing is a request the operator pays for, and on a lagging store it is wrong.

## Order of the work

Each step leaves the application whole, and is its own reviewed change.

1. Deletions take the store's answer: no comparison before a DELETE, no read after it, no container check (findings F66 and F68 end here).
2. Uploads in place.
3. Copies in place, with the stamp gone.
4. Downloads in place.
5. After one confirmation, uploads and downloads replace what differs (decision 12, added 17 November 2026).
6. A selected folder is copied or downloaded with everything in it ([ADR 0108](0108-copy-and-download-what-is-selected.md) decision 7, added 17 November 2026).
7. Removal of what no longer has a caller, and the design document (decision 13).

## Required proof before implementation is accepted

Tier 1, stated here for the whole and repeated in each step for its part.

- For each kind of job, on the mock, the requests the store received are exactly the ones this record allows: an upload of plain files is one PUT for each to its own name; no job writes or reads a name of its own invention, a stage name or any other, so every object a transfer names is one of the destination's own names or a segment of one; a deletion of N plain objects is N DELETEs and the listing that found them, with no HEAD of a member.
- A name written by another writer between the preview and the request is written over or deleted, and the job reports success: the old refusals are shown to be gone, not merely untested.
- A store that answers a HEAD after an accepted DELETE as if the object were still there does not change a deletion's outcome.
- Replacement still needs the acknowledgement, and a deletion its typed words.
- An upload's bytes that do not match the declared digest are refused by the store and reported failed, with the name left as it was.
- The browser journeys and the service journeys pass on the mock and on both real stores, and every upload, copy and download journey compares digests with the source.
- The system tests against a real Swift pass for each step.
- For decision 12 (17 November 2026): Go, upload and download, against the fake engine that prints the real engine's lines: confirmed, each of replaced, unchanged and time-set gives its outcome and counter; declined, nothing is written and no job exists; unconfirmed, the preview refuses and names the name, and the flags are unchanged; a directory or a link at a download's file name is refused even when acknowledged; the application's own route replaces after the confirmation and does not without it; a large file left alone is not published again. Runner: confirmed runs have neither flag, every other run keeps its flags. System, on the owned Swift with the real engine: a confirmed download and a confirmed upload over a folder holding one same, one resized and one touched file replace only the resized one, set the touched one's time, and leave the same one, and a touched dynamic and a touched static large object are replaced whole, by a download and by an upload; an unconfirmed download leaves a file that appeared after the preview alone. Frontend: the download dialog offers "Replace existing files…" when its preview has collisions and nothing else, the request carries the acknowledgement, and the summary counts already-the-same files. Journeys: a confirmed download over an earlier download of the same folder replaces nothing and reports every file already the same, on the mock and both clouds.
- For decision 13 (17 November 2026): the whole-tree gate, the browser and service journeys on the mock and both real stores, and the system tests on a real Swift pass as before; a search shows no non-test reference to a removed symbol; the scenario contract's validation passes with the changed rows and its drift is recorded by ADR 0069's route; an item list of a job written before this change (a fixture holding publication and predecessor rows) still reads.
