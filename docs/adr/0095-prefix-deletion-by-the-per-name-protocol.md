# ADR 0095 — delete a prefix or a container by the verified per-name protocol

**Status:** Proposed on 28 October 2026 for the 0.2.x step of the [road to 1.0.0](../audits/2026-10-27/road-to-1.0.0.md), the bulk deletion of a prefix or a container, whose protocol [ADR 0085](0085-reobserved-destructive-identity.md) decided and recorded as owed and which [ADR 0093](0093-selected-set-deletion.md) decision 7 named as the next record.
Written for review before any implementation.
Nothing here is built, and nothing here returns a capability: the actions stay under "Bulk delete" with the `bulk-delete` token until the operator's word, recorded in [ADR 0052](0052-v1-capability-gate.md) when given.
**Scope:** `DeleteService.StartDelete` in its three modes, a prefix, a container's contents and a container, `DeleteService.PreviewDelete`, and the two segment branches of `DeleteService.DeleteManifestWithSegments` that run the same sweeps.
Benchmark, which writes objects and sweeps them away, follows this record and is not decided here; Mirror is not admitted and is not a step closer.

## The requirement, and what existed when this was written

The three bulk actions are built and gated.
`StartDelete` lists the scope page by page and deletes as it streams, either through bulk-delete requests of up to a thousand names, bounded by what the store advertises, or through a pool of eight to sixty-four workers issuing plain DELETEs; it runs up to three passes and stops when a pass issues nothing new; it keeps a private accounting index per job, discarded with the job, that records every name as issued before its request leaves so no name is requested twice ([ADR 0039](0039-delete-request-authority.md)), and that retains failed and unresolved names up to two thousand per facet with a spill file for the rest; it verifies the scope with a one-row listing after bounded retries (DESIGN §32); the empty mode compares the container's configuration before and after (DESIGN §33); and the delete-container mode deletes the container only after a sweep with no problem.
`PreviewDelete` counts the objects and bytes under the scope, counts the rows listed as zero bytes or as a static large object as possible manifests, and names the strategy the run would take.
The confirmation is the container name, typed.
`DeleteManifestWithSegments` is force-only with a typed confirmation and advisory ownership; its dynamic branch sweeps the manifest's namespace through the same prefix sweep ([ADR 0044](0044-segment-deletion-namespace.md)), and its static branch deletes the manifest's exact segment list through the same executors.

What is missing is the comparison.
A plain DELETE deletes whatever stands at the name when it arrives, and a bulk request carries no per-name precondition at all, so a replacement written between the listing and the request is deleted with no record that it was not what the operator approved.
ADR 0085 decided the rules for this path and stated their cost: the baseline for an ordinary row is the listing row, a zero-byte or SLO-listed row takes a HEAD of its own as a later baseline, one DELETE is issued per name immediately after that name's own HEAD, bulk delete is given up, and for N names of which Z take the later baseline the object requests are 2N + Z.
ADR 0093 then built that protocol for a selected set: `Session.DeleteMember` takes a listing row, fixes its baseline, compares under one credentials snapshot, issues the DELETE immediately after the comparison HEAD, reads the name literally afterwards and reports the outcome in the words of [ADR 0092](0092-exact-object-deletion.md) decision 3 and [ADR 0094](0094-marker-and-manifest-deletion-return-ready.md) decision 3.
The remediation register asks two things of this path: L03, that first-pass and later-pass replacements follow an accepted allowed-effect contract with unchanged collateral, and L04, that the deletion failure streams retain every known and unknown identity beyond the display caps and across a restart.

## Why the selected set's protocol over a listing, and not a new one

The per-name protocol does not care where its row came from.
A prefix sweep's unit is the same listing row the working list carries, so the change to the executor is to hand each row to `DeleteMember` instead of to a plain DELETE or a bulk batch; what is new is the membership, a listing pass rather than a selection, and the volume, which can be millions of names.
A second protocol would have to re-decide every rule ADR 0085 and ADR 0093 settled and would then need its own proof; reusing the one that exists keeps one set of words for what a deletion did.

The item ledger is not reused for the sweep's members.
It admits items in pages and reads them back by page for a set the operator chose, and a page-per-member record of a million-name sweep would make the drawer the slowest part of the job.
The sweep already has a private per-job index with a bounded display cap and a spill that keeps every name, which is what L04 asks for; it gains the deletion words rather than being replaced.

## The behaviour oracle

- ADR 0085: the listing row as the baseline for an ordinary row, a later HEAD for a zero-byte or SLO-listed row, one DELETE per name immediately after its own HEAD, no bulk request, 2N + Z, and the interval after the final comparison stated and never closed.
- ADR 0093 decisions 3 and 4: the member protocol with its outcome words, the issued mark before the final HEAD, cancellation between members, a terminal state derived from the members by whether any DELETE was issued, and a refused member carrying its kind.
- ADR 0092 decision 2 and ADR 0094 decision 3: a link in either representation is never deleted, and a manifest is deleted manifest-only and judged by the manifest words.
- ADR 0039: one destructive request per name per job, recorded as issued before it leaves; a pass that issues nothing new ends the sweep; names still listed after the only request are counted, not requested again; a name first listed in a later pass is requested because the prefix scope is the operator's consent; the operator re-runs deliberately for fresh authority.
- [ADR 0043](0043-cancellation-delta-authority.md): a cancellation produces exactly the accepted delta and a truthful retained outcome.
- [ADR 0020](0020-recovery-containment.md) and ADR 0044: manifest-only by construction, no automatic reclamation, the segment sweep bounded by the manifest's namespace, and ownership advisory.
- DESIGN §32 and §33: a bounded streaming pipeline, an operation-specific postcondition stated in the job's effective configuration, cancellation at safe boundaries, exact failed names retained, and the empty mode preserving the container's configuration.
- L03 and L04 of the remediation register, as stated above.

## Decision

1. **Membership is a listing pass, and every object row listed is a member.**
   The sweep lists the scope deeply, page by page, as it does today, and each object row becomes a member row with its name, listed bytes, listed entity tag and static large-object mark, handed to the per-name protocol as the selected set hands its rows.
   An ordinary row is compared against its listing; a row listed as zero bytes or as a static large object takes a HEAD of its own first, which fixes its class and refuses a link at once; classification comes before the identity comparison; any difference between the baseline and the comparison HEAD refuses the name as changed since listed with both identities recorded; a matching plain object is deleted by one plain DELETE issued immediately after its comparison HEAD; a matching manifest is deleted manifest-only, its segments untouched; a directory marker listed as an object row is a literal object and is deleted as one; and each DELETE is followed by the literal verifying read with the plain or the manifest words.
   No bulk request is sent on any branch, and `BulkDelete` leaves these actions' strategies; the strategy named in the job's effective configuration is the verified per-name one.
2. **One request attempt per name per job, marked before the final HEAD.**
   ADR 0039 decision 1 is kept and moved to where the protocol needs it: a name is recorded as issued in the job's accounting before its final HEAD, not before a batch, so nothing of ours, local or remote, runs between that HEAD and the DELETE, and a name recorded as issued is never requested again by the same job in any later pass.
   That covers a name refused or found changed since listed as much as one deleted: the job does not try such a name again with a fresher row, because the comparison that refused it is the protocol's answer for this job, and the operator re-runs the deletion deliberately to obtain fresh authority for whatever is still listed, as ADR 0039 decision 4 records.
   A later pass therefore requests only names not yet issued, which are the names first listed in that pass, each against its own listed facts, because the prefix scope is the operator's consent; a pass that issues nothing new ends the sweep, and a pass that lists nothing ends it as before.
   This is L03's allowed-effect contract for this path: a replacement at an issued name is never requested by the job that issued it, and a name first listed in a later pass is deleted only if it still matches what that pass listed.
3. **Workers run members concurrently, each member whole.**
   The bounded worker pool stays, and each worker runs one member's issued mark, baseline, comparison, DELETE and verifying read as one unit under one credentials snapshot, exactly as the selected set runs one member; the workers share the job's accounting and nothing else.
   Cancellation lands between members on every worker: a member in flight finishes its own requests and records its outcome, no later member starts, and the listing stops being consumed, so the delta is exact per name (ADR 0043) and the boundary is the one DESIGN §32.1 asks for.
4. **Every outcome is retained per name, with its identities, in the job's own accounting.**
   The private index gains the deletion words beside the issued mark: deleted, with the successor the verifying read observed when something stood at the name; unverified, with what the read observed; changed since listed, with the identity expected and the identity found; refused, with its kind; failed at the request, with the store's status; and unsettled when the process ended between the issued mark and the outcome.
   Names and identities are retained up to the display cap and spilled beyond it to the job's retained file, which survives a restart, so no identity is lost to a cap (L04); the job carries exact counts of planned, issued, deleted, unverified, changed, refused, failed and unsettled names, and the drawer shows them with the spill's location.
   An accounting write that fails stops the sweep as it does today, with no later request and the job's status unavailable, because a name whose outcome cannot be recorded cannot be counted.
5. **The terminal state follows the members and the postcondition.**
   A sweep is complete only when every member it issued was deleted and the one-row listing of the scope is empty after bounded retries; partial whenever a DELETE was issued and the job is not complete, which covers a member refused or changed beside the deleted ones, every reply lost, a cancellation after a DELETE, names still listed after the job's only request for them, and recording that became unavailable after a DELETE; cancelled when cancellation landed before any DELETE was issued; and failed when nothing was sent, because every listed member was refused or changed since listed, or because the listing or the accounting could not be read before any request.
   The postcondition and the pass count stay in the job's effective configuration, so the verification is never a silent shortcut.
6. **The empty and delete-container modes keep their contracts.**
   Emptying a container compares its configuration before and after and reports a difference as a problem, unchanged.
   Deleting a container issues the container DELETE only after a complete sweep with an empty postcondition; a partial, cancelled or failed sweep never deletes the container, which is then reported with what the sweep left.
7. **The preview states the cost, and the confirmation says what the protocol does.**
   `PreviewDelete` keeps its counts and adds the request cost: N objects, of which Z are listed as zero bytes or as a static large object and take a later baseline, cost 2N + Z object requests before listing, retries or verification; the strategy it names is the verified per-name one.
   The dialog states the scope, the counts, the cost, the container's versioning consequence in the sentence ADR 0092 settled, that an object listed with a size is deleted only if it still matches its listing and one that changed is left and recorded, that a zero-byte or SLO-listed row is read again first and what that read finds is what is deleted, a marker or a manifest included and a link refused, that each name is requested once by this job, and that a manifest's segments stay; the confirmation is still the container name, typed (DESIGN §28.2).
8. **The segment branches run the same protocol over their own membership.**
   The dynamic branch of `DeleteManifestWithSegments` sweeps the manifest's canonical namespace through this sweep, with ADR 0044's refusal of names outside the namespace unchanged.
   The static branch's members are the manifest's segment entries, each with the size and entity tag the manifest records as its listed facts, so a plain segment is compared against what the manifest said it was; an entry without both facts takes a later baseline as a zero-byte row does.
   Both stay force-only with the typed confirmation, recorded ownership stays advisory, nothing reclaims a segment on its own authority (ADR 0020), and the manifest's own deletion keeps its place in each branch's order.
9. **The gate and the contract.**
   The three actions stay deferred under "Bulk delete" and return together on the operator's word.
   `DeleteService.StartDelete` is a job row and gains the conflict scenario, a name refused as changed since listed, which is the real contract of a sweep that compares; the changed required set is admitted through the [ADR 0069](0069-preserve-covered-scenario-cells.md) drift route from the candidate run, as the selected set's row was.
   `DeleteService.DeleteManifestWithSegments` keeps its row.
10. **What follows from it.**
    Benchmark's cleanup becomes a verified sweep of the names it wrote, under its own record.
    Mirror stays without a token and is not admitted by anything here, because a name absent from the source has no identity to compare against (ADR 0085).

## What this does not touch

Exact-object, selected-set, marker and manifest deletion keep their records; copy and upload are untouched; automatic segment reclamation stays refused; the valuable-data release hold stays in force.
Nothing here is a lock, a reservation or exclusive ownership, and a member deleted after a match may still have been replaced by a writer landing after its final HEAD, as ADR 0085 states for every path.

## Required proof before implementation is accepted

Tier 1 under DESIGN §65.12.
Backend cases run through the public service on the mock and the owned Swift, with namespace snapshots before and after.

- Happy: a prefix holding plain objects, a zero-byte object, a marker, a static and a dynamic manifest and nested prefixes is deleted; every name's outcome is deleted; the manifests' segments, every unselected neighbour and the other account are unchanged; the job is complete with an empty postcondition; the request log shows one HEAD-then-DELETE pair and one literal read per member, a baseline HEAD only for the zero-byte and SLO-listed rows, no bulk request and no DELETE repeated.
- Refusal: a link under the prefix is refused with its kind, the rest deleted, the job partial, and neither the link nor its target touched.
- Conflict: an ordinary row replaced after the listing, by a plain object, a manifest and a link in turn, is refused as changed since listed with both identities retained, nothing is deleted for it, the rest are deleted, and the job is partial.
- Later pass: a name written after the first pass's listing is deleted in a later pass against its own row; a name refused in the first pass is not requested again in any later pass although it is still listed; a pass that issues nothing new ends the sweep; names still listed after the sweep's only request are counted and the job is partial.
- Cancel: a cancellation during a long sweep ends the job with the members before it deleted, the one in flight on each worker settled, nothing later issued and the counts exact; a cancellation before any DELETE ends it cancelled with nothing deleted.
- Partial through verification: a member whose DELETE reply is lost is unverified, the job partial, and no DELETE is repeated.
- Modes: emptying preserves and compares the container's configuration; deleting a container refuses the container DELETE after a partial sweep and issues it after a complete one.
- Retention: more failed names than the display cap are all retained in the spill with their identities and are readable after the process restarts; the counts on the job are exact.
- Recording failures: an issued mark that cannot be written stops the sweep before that name's HEAD; an outcome that cannot be recorded after a DELETE stops it with the job's status unavailable.
- Preview and dialog: the counts, the cost 2N + Z and the strategy; the dialog's sentences and the typed container name; the segment dialog's membership and boundary.
- Segment branches: the static branch's members compared against the manifest's entries, a changed segment refused; the dynamic branch bounded by the namespace with the outside-name refusal unchanged.
- Adjacency: no authentication and no other request of ours runs between any member's comparison HEAD and its DELETE, under a gate that places a manifest at the name during any such request.
- Retained mutants, each failing a case above: a bulk request used; the listed row ignored in favour of a HEAD baseline for an ordinary row; the replacement adopted as the baseline when an ordinary row's comparison finds a manifest; a DELETE repeated after a lost reply; cancellation honoured inside a member; the issued mark written after the HEAD; a refused name requested again in a later pass; the container deleted after a partial sweep; an identity dropped beyond the display cap; a conflict recorded without its identities; the preview cost counting N alone.
- The candidate run's drift diagnostic retained and the replacement baseline built from its reports.

## What this does not establish

It does not bound the interval after a member's final HEAD, prove that bytes are gone, or identify an observed successor's origin.
A listing is advisory: a name it omits is neither deleted nor reported, and the postcondition's one row is evidence that the scope is empty, not a scan.
It does not lift the gate, decide Benchmark or Mirror, or close the valuable-data release hold.
