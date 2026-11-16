# ADR 0122 — the store's answer is the result

**Status:** Accepted on 16 November 2026 for 0.5.0, on the operator's decisions of that day, made during the candidate's test run.
Tier 1 under DESIGN §65.12, because it changes what every upload, copy, download and deletion sends and what each reports.
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
   **A replaced large object's own segments.**
   When the engine writes over a large object it removes that object's old segments itself, as it does when used alone: it reads the old manifest before its write and deletes the segments it named after it.
   Those requests are the engine's, they are for the predecessor's own segments, and they are not a check for another writer; the engine can delete only inside the destination's own segments container, so segments kept elsewhere stay.
   The application's own reclamation after a verified replacement ([ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 5) goes with the verifying read it waited for.
   A name the application sends itself replaces the manifest and leaves the old segments where they are, and the acknowledgement says which of the two will happen.
4. **A copy writes each object at its name**, on the same terms.
   The engine reads the source and writes the destination; a name it cannot carry is read and written by the application in one pass.
   No object is stamped, and no stamp is read: the preview compares the two listings.
5. **A download writes each file in the folder the operator chose.**
   The engine writes there directly, as it does when used alone; there is no stage folder and no placing step.
   A download still does not replace a local file, because that file is on the operator's own disk and is not another writer on the store: the preview refuses one that exists, and the engine is told to skip a file that has appeared since, which the job reports as not downloaded because it exists.
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

## What this gives up

It is written down so that nobody reads the old promise into the new behaviour.

- A name that another writer creates or changes between the preview and the request is written over, or deleted, without notice.
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
5. Removal of what no longer has a caller, and the design document.

## Required proof before implementation is accepted

Tier 1, stated here for the whole and repeated in each step for its part.

- For each kind of job, on the mock, the requests the store received are exactly the ones this record allows: an upload of plain files is one PUT for each to its own name; no job writes or reads a name of its own invention, a stage name or any other, so every object a transfer names is one of the destination's own names or a segment of one; a deletion of N plain objects is N DELETEs and the listing that found them, with no HEAD of a member.
- A name written by another writer between the preview and the request is written over or deleted, and the job reports success: the old refusals are shown to be gone, not merely untested.
- A store that answers a HEAD after an accepted DELETE as if the object were still there does not change a deletion's outcome.
- Replacement still needs the acknowledgement, and a deletion its typed words.
- An upload's bytes that do not match the declared digest are refused by the store and reported failed, with the name left as it was.
- The browser journeys and the service journeys pass on the mock and on both real stores, and every upload, copy and download journey compares digests with the source.
- The system tests against a real Swift pass for each step.
