# ADR 0119 — a bulk deletion deletes each large object's segments when the operator asks

**Status:** Accepted on 14 November 2026 for 0.5.0 and delivered with this record's series, from finding F56 of the final candidate's test run and the operator's decisions of the same day: first that an explicit option to delete the segments of the large objects in scope be built, then that it be built before the tag.
Amended on 15 November 2026 by [ADR 0120](0120-segments-go-by-default-and-versions-keep-them.md), which answers this record's closing question: the option starts ticked, except in a container that is or may be another container's archive, so decision 2's "unticked whenever the dialog opens" no longer holds.
It also widens decision 4's wait: with the option, the members in flight settle before every dynamic manifest, wherever its segments are, and the manifest is read again after they have.
Every other rule here stands.
Amended on 17 November 2026 by [ADR 0093](0093-selected-set-deletion.md) decisions 8 and 9: a selected-set deletion offers the same option, and a prefix deletion is typed with the word delete before the container and prefix.
Tier 1 under DESIGN §65.12.
**Scope:** `DeleteService.StartDelete` in its three modes, a prefix, a container's contents and a container, and the dialog that starts them.
The single-object dialogs, Copy, Mirror and the upload's reclamation ([ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 5) are not changed.

## The requirement, and what exists

A bulk deletion deletes manifest-only ([ADR 0095](0095-prefix-deletion-by-the-per-name-protocol.md) decision 1): a large object's manifest goes and its segments stay.
The operator deleted a container of large objects and found its segments still there and still counted against quota.
Since then the dialog names the container those segments are usually in and offers to open it, and adds no way to delete anything.

One route already deletes a large object's segments: `DeleteManifestWithSegments`, for one object, from Backstage, with the force and the object's name typed (ADR 0095 decision 8, [ADR 0044](0044-segment-deletion-namespace.md)).
Its rules are the ones a bulk deletion needs: the manifest first, by the reviewed-manifest protocol, bound to the graph that was read; the segments only after the manifest's deletion is recorded; a static large object's whole-object entries compared against the size and entity tag the manifest records; a dynamic large object's segments bounded by its namespace, with any name observed outside it stopping the segment deletion; and every member a durable item of the job.

What is missing is a way to ask for that for every large object in a scope.

## Why an option the operator ticks, and not the default

K-SEG-003 and K-SEG-004 allow a large object's segments to be deleted only as an explicit force action with the sharing risk disclosed, because nothing can prove that no other object uses a segment.
K-SEG-005 is the one exception, for an acknowledged upload, and it rests on the operator's own decision, made twice in words.
An option that starts ticked would delete segments without a separate act by the operator, which is a second exception of that kind, and only the operator can make it.
The operator was asked whether the option should start ticked and has not answered.
So this record builds the explicit force action for a bulk deletion and nothing more: the option starts unticked every time the dialog opens, and whether it should start ticked stays the operator's question (see the end of this record).

## The behaviour oracle

- ADR 0095 decisions 1 to 6: the per-name protocol, members as durable items, one request per name per job, the lost-record rules, the terminal state from the members and the postcondition, and the container deleted only after a complete sweep.
- ADR 0095 decision 8: the manifest by the reviewed-manifest protocol bound to the graph read for it, the segment phase only after the manifest's deletion is recorded, a static segment compared against what the manifest records, and a manifest whose deletion is not confirmed leaving every segment untouched.
- ADR 0044: a dynamic large object's segment deletion acts on its namespace and on nothing else, a complete listing of the raw prefix comes first, and an empty prefix has no bounded scope.
- ADR 0081 decision 5: segments are not removed where the container keeps versions, only whole remote parts are removed, never a ranged entry's containing object or a nested manifest's descendants, and the answer bounds the kind of effect, not its size.
- [ADR 0105](0105-concurrent-jobs.md) decision 3: a sweep never deletes inside another live job's stage.
- K-SEG-001: a name never authorises a segment deletion.

## Decision

1. **The request says whether segments go, and without it nothing changes.**
   `DeleteRequestDTO` gains `segments`.
   When it is false the job is exactly today's: the same requests, the same records and the same words.
   The preview does not change and takes no account of the option.
2. **The operator ticks the option and types it.**
   The dialog offers "Also delete the segments of large objects" under the preview when the preview counted at least one row that may be a manifest and the container does not keep versions.
   It is unticked whenever the dialog opens and whenever a new preview is made.
   Beside it the dialog says what goes and what is not known (decision 8).
   With the option ticked, the text to type is the scope followed by ` and segments`: `media and segments` for the container `media`, `media/scope/ and segments` for its prefix `scope/`.
   `StartDelete` compares the confirmation with the text for the request it was given, option included, before it admits the job, so a confirmation typed for a manifest-only deletion never starts one that takes segments, for any caller.
3. **Segments are never taken where the container keeps versions.**
   A deleted object's earlier versions, or the copy the store keeps of what was deleted, still name those segments.
   `StartDelete` reads the container before any job row exists and refuses the option when the versioning mode it observes is not off, or cannot be read; the dialog does not offer the option there and says why.
   The mode is read once, at the start, as the job's other startup facts are.
4. **Which segments are a large object's is read from its manifest when its turn comes.**
   A row listed as zero bytes or as a static large object already takes a HEAD of its own before it is deleted (ADR 0095 decision 1).
   With the option, when that read shows a manifest, the job reads its graph next:
   - for a static large object, the entries its manifest lists. Its segments are those entries when every one is a whole object with a recorded entity tag that is not empty.
   - for a dynamic large object, its namespace as ADR 0044 defines it, after a complete listing of the raw prefix its manifest names.

   The segments of a large object are **left**, and its manifest is deleted manifest-only exactly as without the option, when any of these holds:
   - a static manifest has a nested, ranged, inline or unrecognised entry, or cannot be read;
   - a static manifest has an entry that records no entity tag, or an empty one. Every segment of that large object stays, the ones beside that entry included, and the reason names the missing tag, not a nested or ranged entry.
     ADR 0095 decision 8 lets the single-object job read such an entry again and delete what it then finds, for a graph the operator reviewed; this option does not, because a segment here is deleted only when it can be compared with what its manifest recorded;
   - a dynamic manifest names no prefix, or the listing of its raw prefix fails, or that listing shows a name outside the namespace;
   - a dynamic manifest's namespace is strictly wider than the scope being deleted, in the same container: sweeping it would delete names outside the scope the operator named, whether or not they are this manifest's segments;
   - a static entry lies in the stage of another live job, or another job is staging into the container that holds a dynamic large object's segments (ADR 0105 decision 3).
     The job claims that container when it first sweeps a namespace there and holds the claim until it ends, as a sweep holds its scope, so no transfer starts staging there meanwhile; it is one claim for the container and not one for each namespace, so what a job holds does not grow with the number of large objects in its scope.

   Each large object left this way is counted and recorded in the audit trail with its name and the reason.
   A dynamic large object whose namespace is the scope being deleted, or lies inside it, needs no second sweep: its segments are rows of that scope and are deleted as such, with or without the option.
   Its manifest's length and entity tag are made from those rows, so a manifest compared while the job's own workers delete them would be refused as changed by the job's own doing.
   With the option, the sweep therefore lets the members it has in flight settle and then deletes that manifest by the ordinary member protocol, before it hands the workers anything more.
   Without the option a sweep does not wait, as before this record: such a manifest can be recorded as changed since listed and left, and a second run deletes it.
   The scope and the namespace are compared as whole path parts: each ends in a slash, or the scope is the whole container.
   A row that the read shows to be a plain object, a marker or a link is handled exactly as without the option.
5. **The manifest goes first and its segments only after that is recorded.**
   The manifest member is deleted by the reviewed-manifest protocol of ADR 0095 decision 8, bound to the graph just read, so a graph that changed between that read and the manifest's comparison HEAD refuses the manifest as changed, with nothing deleted.
   A manifest refused, changed, failed at its DELETE or unverified leaves every one of its segments untouched: the static entries already admitted are settled as not attempted, and a dynamic namespace is not listed again.
   Once the manifest's deletion is recorded, a static large object's entries are deleted by the per-name protocol, each compared against the size and entity tag the manifest recorded for it.
   A recorded size of zero is compared as zero with that tag: the HEAD of its own that a zero-byte listing row takes first (ADR 0095 decision 1) does not apply to a segment, because what the manifest recorded is its baseline, and a name that holds anything else is refused as changed.
   A dynamic large object's namespace is swept by the same sweep the single-object job runs, without this option, so a manifest found among segments is deleted manifest-only and never followed.
6. **Segments are members of the same job, each requested once.**
   A static entry is admitted to the job's ledger before its manifest is requested, keyed by its container and name, and only when the job does not already hold that key.
   So a segment two manifests in scope name, or one that is also a row of the scope, is requested once by this job, whichever reached it first, and is never admitted twice.
   Admission and the manifest's deletion run on the goroutine that reads the listing, one large object at a time, so no two admissions run together; the segments themselves are deleted on the sweep's workers beside ordinary members.
   Decisions 2 to 4 of ADR 0095 apply to them unchanged: the issued mark before the final HEAD, cancellation between members, one outcome per member, and a lost record stopping admission.
7. **The terminal state counts the segments, and so does the container deletion.**
   The state is derived as ADR 0095 decision 5 derives it, over every member of the job, segments included: a segment refused, changed since its manifest was written, failed or unverified makes the job partial with the breakdown.
   A job that left any large object's segments under decision 4, or whose sweep of a dynamic namespace stopped or did not end empty, is partial too and says how many were left and why the first was.
   The container is deleted only after a complete job (ADR 0095 decision 6), so with the option a container whose objects are all gone still stands when a segment was not deleted or was left, and the job says so; running the deletion again deletes the now-empty container.
   This is the simple rule and the cautious one: one state for the whole job, and no container deleted by a job that did not do everything it was asked.
8. **The dialog says what goes, what is not known, and what it cost.**
   Beside the option: that each large object's segments are deleted after it, the ones a static manifest lists or everything under the prefix a dynamic manifest names; that segments which cannot be bounded safely are kept, as when a dynamic manifest's prefix is wider than what is being deleted, and the job then ends partial and says why; that this application cannot show that no other object uses the same segments, and one that does will lose its data; that how many segments there are is not known until each large object is read, and every segment deleted is listed in the job; and that each segment is compared then deleted like any other object, at the same cost.
   When the container keeps versions it says instead that segments are kept because earlier versions still use them.
   After a job that took segments, the notice about the segments container no longer says the deletion removed nothing from it; it says that container still exists and that the job's records list what was deleted from it.
9. **What follows in the contract.**
   K-SEG-003, K-SEG-004 and K-XFER-014 name the force action as offered from Backstage for one object and as the bulk deletion's option, each with its typed confirmation and the sharing risk disclosed.
   DESIGN §32, §34 and §35, `AGENTS.md`, the release notes' known issue and the test plan follow.
   `DeleteService.StartDelete` keeps its scenario row and its required set; the option's cases are regression tests through the public service.

## What this does not touch

The option is off unless ticked, and with it off no request, record or word differs from today.
One object, one marker and one manifest are deleted as before, and a selected set as [ADR 0093](0093-selected-set-deletion.md) decision 8 now says; one large object is still deleted with its segments from Backstage.
No `?multipart-manifest=delete` and no bulk-delete request is sent.
The segments container is never deleted by this job, whatever it holds afterwards.
K-SEG-005 is unchanged, and so is the valuable-data release hold.

## Required proof before implementation is accepted

Tier 1.
Backend cases run through the public service on the mock and the owned Swift, with namespace snapshots before and after.

- Off: with the option false, a prefix holding a static and a dynamic large object is deleted manifest-only with every segment unchanged, and the confirmation with ` and segments` is refused.
- Happy: with the option, a prefix holding plain objects, a static and a dynamic large object is deleted; each manifest and each of its segments has a deleted record; a neighbouring large object outside the scope and its segments are unchanged; the job is complete. On the owned Swift the same for a container, which is then deleted.
- Consent: the confirmation without ` and segments` is refused for a request with the option, before any job row; a container that keeps versions, in each of the three modes, is refused the option with nothing deleted.
- Left: a static manifest with a ranged entry, one with a nested manifest, one with an entry that records no entity tag beside one that does, one whose entry's tag is empty, a dynamic manifest whose raw prefix reads a name outside its namespace, one whose namespace is wider than the scope, with a name inside that namespace and outside the scope unchanged, one whose segments lie in a live job's stage, and a dynamic one whose segments' container another job is staging into each lose their manifest only, with every segment unchanged, the reason recorded and the job partial.
- Order: a manifest replaced between the graph read and its comparison is refused as changed with every segment unchanged; a manifest whose DELETE reply is lost is unverified with every segment unchanged and its admitted entries settled not attempted.
- Once: a segment two manifests in scope name is requested once and both manifests are deleted; a segment that is also a row of the swept scope is requested once; a dynamic large object whose namespace is the scope itself is deleted with its segments as rows of the scope, each once, the job complete and nothing recorded as left, on a store that holds each segment's DELETE until the manifest's baseline has been read and the manifest's comparison until the segments are gone.
- Segment identity: a static segment replaced since the manifest was written is refused as changed and left, and the job is partial; a segment the manifest records as empty is deleted after one comparison with no read before it, and a recorded empty segment whose name holds a manifest is refused as changed.
- Cancel: a cancellation after a manifest's deletion and before its segments leaves them not attempted and the job partial.
- Container: with a segment refused, the delete-container mode leaves the container standing; with every member deleted it deletes it.
- Lost records: an entry admission that cannot be written stops the job before the manifest is requested; a manifest outcome that cannot be recorded leaves its segments untouched and the job unavailable.
- Dialog: the option is unticked when the dialog opens and after a new preview, is not offered without a possible manifest or where the container keeps versions, changes the text to type, and sends `segments` only when ticked; the notice after the job.
- A browser journey on the mock and the real clouds: a folder with a segmented file is deleted with the option and the segments container lists nothing of that upload afterwards.
- Retained mutants, each failing a case above: the option honoured when the request does not carry it; the plain confirmation accepted with the option; a versioned container admitted; a ranged or nested graph's entries deleted; an entry with no entity tag read again and deleted, or the entries beside it deleted; a recorded empty segment read first as a zero-byte listing row is; the dynamic namespace swept after an outside name was seen; a namespace wider than the scope swept; a manifest made of the scope's rows handled without waiting for the members in flight; segments deleted after an unverified manifest; a held segment admitted or requested again; the segments deleted before the manifest; the container deleted after a segment was refused.

## What this does not establish

It does not prove that no other object uses a deleted segment: a manifest elsewhere that names one loses its data, and the dialog says so.
A dynamic large object's segments are whatever its namespace lists when it is swept; a name the listing omits is left.
The graph is read when the large object's turn comes, not when the operator previewed, so the number of segments is not shown before the job.
It does not delete segments left by earlier manifest-only deletions, whose manifests no longer exist to name them.

## The question left for the operator

Whether the option should start ticked.
That is a decision of the kind K-SEG-005 records, because it would delete segments without a separate act, and it would be a change to the dialog's starting value and to K-SEG-004's wording, with this record's rules unchanged.
