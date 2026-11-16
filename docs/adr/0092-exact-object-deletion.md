# ADR 0092 — delete one object the operator was shown

**Status:** Accepted (2026-10-27) with the replacement baseline it authorises, built from candidate run 40516591112 as the [baseline record](../experiments/2026-10-27-object-delete-baseline.md) describes and reviewed in the same change as the contract row, as [ADR 0069](0069-preserve-covered-scenario-cells.md) requires; proposed earlier the same day for the operator's decision, recorded as decision 30 in the [Linux finish line](../audits/2026-09-05/linux-finish-line.md), in their words "yes, we need delete".
Written for review before any implementation, in the shape [ADR 0085](0085-reobserved-destructive-identity.md) took, and implemented on 27 October 2026 behind the `object-delete` token: the session, service and interface are built in the range that carries this sentence, with the public-service, owned-Swift, durable-fact and browser proof listed below; the capability was returned to the release on 29 October 2026 by [ADR 0052](0052-v1-capability-gate.md) decision 11, and the preservation baseline was admitted through the drift route from that range's candidate run, 40516591112.
It proposes one new bridge action behind the [ADR 0052](0052-v1-capability-gate.md) gate, lifts nothing from that gate, and changes no refusal of [ADR 0020](0020-recovery-containment.md).
Amended on 16 November 2026 by [ADR 0122](0122-the-stores-answer-is-the-result.md) decision 6: the object is deleted by one DELETE of its name and the store's answer is the result, so the comparison with the identity the operator was shown, the refusal of a manifest or a link, and the verifying read with the successor it reported are no longer made; the binding to the scope the detail was read under, the durable intent and outcome, and the confirmation stay.
**Scope:** One synchronous deletion of one plain object the operator has inspected: `MutateService.DeleteObject`, its guards, its durable facts, its contract row, its gate classification and its interface.
It does not cover manifests, the marker journey on a prefix row, segments, prefixes, containers, symbolic links or a selected set; each is named below with the record that owns it, and decision 2 says what it does with a marker reached as an object.

## The requirement, and what exists today

[DESIGN §63](../DESIGN.md) lists "delete ordinary objects" as the first item under Delete, and it had never existed when this record was written.
The [remediation plan](../audits/2026-09-05/remediation-plan.md) carries it as P27 and UI-08: the exact-object delete journey, with the rule that a prefix deletion is never a substitute for it, and a completion test that the plain, empty, link and manifest identity transitions and same-prefix siblings are safe.
The finish line's 14 September re-baseline deferred it with the destructive routes, and the [25 September release plan](../audits/2026-09-25/next-release-plan.md) placed deletion last.
The operator met the gap on the first day of testing the Linux build: an uploaded object could not be deleted, because the only deletion a build can be made to offer is the bulk deletion of a prefix or a container, whose own control says it is not the selected object.
The [road to 1.0.0](../audits/2026-10-27/road-to-1.0.0.md) brings deletion forward on their word and makes this record its first slice.

What a build has, behind the gate, is the shape this record extends.
`Session.DeleteManifestObject` HEADs the name, refuses a plain object, refuses when the class, length and entity tag differ from the identity the inspector showed, deletes with nothing of ours between the HEAD and the DELETE, and verifies by a read that finds nothing.
`MutateService.DeleteManifest` wraps it with the gate, the writer gate and an audit event whose summary withholds the name and carries both identities on a conflict.
That was ADR 0085's narrowest window when this record was written, and it existed for manifests only; the same shape now serves plain objects under this record.

## The behaviour oracle

- [ADR 0085](0085-reobserved-destructive-identity.md): two observations, a baseline the decision rests on and a final comparison immediately before the effect; a difference refuses; what remains exposed begins after the final comparison and is stated, not closed.
- [ADR 0079](0079-caller-named-mutation-binding.md): a mutation carries the portrait its view was built under and is refused before any intent when that portrait no longer names the active connection at its generation.
- [ADR 0058](0058-object-metadata-durable-facts.md): an intent is durable before the store can be asked, the outcome and the audit event commit in one transaction, an effect whose outcome cannot be proven is `unverified` and never `failed`, and a landed effect whose outcome cannot be recorded is residue named by its identity.
- [ADR 0020](0020-recovery-containment.md): no text may call a matching comparison exclusive ownership, a lock or proof that nobody else is writing.
- [DESIGN §4.4](../DESIGN.md): the Delete key opens a safe delete flow, never an immediate deletion; and §32: a single-object mutation is verified by a HEAD of the exact object.
- [REQUIREMENTS](../REQUIREMENTS.md) K-XFER-014: destruction is Aeronautes-native, with an authoritative HEAD before any mutation whose semantics depend on the object's current identity.
- P27's completion test, quoted above, is the test matrix.

## Decision

1. **One action, one object, bound to the view it was chosen from.**
   `MutateService.DeleteObject(ctx, scope ListingScopeDTO, container, name, contentFingerprint string) (MutationResultDTO, error)`.
   The result carries the outcome word, the transaction identifiers and, as `ObjectAfter`, the literal successor when the verifying read of decision 3 observed one, so that what was found at the name reaches the interface and not only the ledger.
   The scope is the portrait the inspector's detail was read under, passed as ADR 0079 decision 4 has the frontend pass it, and the call is refused with `ErrMutationScopeChanged` before any intent or request when that portrait is empty, names another bookmark or has moved a generation.
   The fingerprint is the `ContentFingerprint` the inspector's HEAD returned for this object, handed back the way `DeleteManifest` and the metadata editor hand back theirs; an empty one is refused, because a delete that cannot say what it meant to destroy is refused.
   Unlike the two deletions ADR 0079 left unbound, this action is born bound; it has no callers to keep a signature for.

2. **The session guards are ADR 0085's three, with the class guard turned the other way.**
   A fresh HEAD of the name immediately before the DELETE is the final comparison.
   A name that holds a dynamic or static large object is refused and the operator is pointed at the manifest deletion, so the manifest-only semantics stay with the record that owns them and the two paths never overlap.
   A symbolic link in either representation is refused, with the two-part test the metadata editor already applies: a HEAD that followed a link (`FollowedSymlink`), or one that carries the link's target without following it (`SymlinkTarget`), which is how a cross-account link is represented.
   The identity the operator was shown is the target's, a DELETE at the name would remove the link rather than the thing shown, and deleting a link needs its own identity read (`Session.SymlinkGet`) under a record of its own.
   The unfollowed representation is what the supported mock and the DTO contract establish for a cross-account link; a real Swift follows such a link or answers with its own error, so the mock exercises the unfollowed form, the owned Swift exercises its followed or error form, and both must witness the refusal at the public service.
   A class, length or entity tag that differs from the fingerprint refuses with `IdentityConflictError`, carrying both identities, and deletes nothing.
   Only then is the DELETE sent, with nothing of ours between the HEAD's reply and it.
   A zero-byte object is a plain object and is deleted like any other.
   A directory marker reached as an object row is deleted as the literal object it is, not through the marker journey of `DeleteMarker`: its children are untouched, the prefix they imply remains, and the dialog says so.

3. **Verification is a literal read of the exact name, and success means the identity that was shown is no longer observed there.**
   The read is a HEAD of the name with both expansions suppressed, `?symlink=get&multipart-manifest=get`, because a writer can place at the name, after the DELETE, a dangling link or a large-object manifest whose segments do not exist, and a read that follows or expands reports the missing target as not found while an object still stands at the name; `Session.SymlinkGet` suppresses only the link, so the implementation adds the manifest control beside it, and a plain object answers that read as itself.
   The literal observation must be comparable with the inspector's identity: its class, length and entity tag come from the literal response's own headers, `X-Symlink-Target`, `X-Object-Manifest`, `X-Static-Large-Object`, `Content-Length` and `Etag`, because `SymlinkGet`, as it stood when this record was written, built its detail from headers without the length and entity tag the inspector's read fills in, and comparing those two results would have made an unchanged object look different; the session slice gave the raw read those fields.
   Classification comes before comparison: a found link or manifest is a successor of another kind whatever its length and entity tag, since a literal link and an empty plain object share the class, the zero length and the empty entity tag that `ContentFingerprint` hashes; only a found plain object is compared by identity.
   After a DELETE the store accepted: a read that reports not found records `applied`; a successor of another kind, or a plain object of a different identity, records `applied` with the successor observed; a plain object of the same identity records `unverified`, because the DELETE may not have taken effect yet and a rewrite of the same content cannot be told from that.
   After a DELETE whose reply was lost, the read's finding is recorded but the outcome is `unverified` whatever it finds, because nothing proves the store applied this request; a read that fails, or whose fields cannot be compared, records `unverified` with the DELETE's transaction identifier.
   What `applied` claims is exactly this: after a DELETE the store accepted, the plain-object identity the operator was shown was no longer observed at the name.
   It does not claim that the bytes are gone, because a link or a manifest placed at the name can serve the same bytes, and it does not claim that an observed successor is the archived predecessor rather than a concurrent write, because a versions location makes the predecessor expected and proves nothing about what was found; the successor is described as observed, possibly restored from the archive or written concurrently.

4. **Durable facts in the ADR 0058 shape, with the operation `object.delete`.**
   The intent commits before the HEAD; a failed intent sends nothing.
   The outcome and the audit event commit in one transaction after the attempt: `applied`, `conflict` for the identity refusal, `failed` for a refusal or a store error that provably destroyed nothing, and `unverified` as decision 3 says.
   A DELETE that landed, or that may have landed because its reply was lost, followed by an outcome transaction that failed, is residue: the call returns a named error carrying the identity and the warning that the deletion may have occurred, the only durable row is the intent, and on reopen that identity is neither `applied` nor any other terminal outcome, which is how a possibly posted metadata write is already treated.
   The audit summary withholds the object name as the manifest deletion's does, because a name can be a secret, and carries the two identities, the class, the byte count and the entity tag on a conflict.
   Returning `DeleteManifest` to a build is a separate change; whether it joins this ledger then is decided there, not here.

5. **What the operator is told before and after.**
   The dialog binds the exact identity it opened for, as the manifest and bulk dialogs do, and acts on nothing else.
   It names the object, says the data at this name is destroyed, and states the container's versioning mode and archive as the inspector knows them.
   What a DELETE does to a versioned container is not what an operator expects, and the [measurement of 27 October 2026](../experiments/2026-10-27-delete-under-versioning.md) is what the dialog states: with no versioning the content is destroyed; with a versions location the content is destroyed and never archived, and if the archive holds a predecessor the name reverts to it and the archive gives it up; with a history location the content moves to the archive with a deletion marker and is recoverable from there.
   The sentence names the archive container the inspector already knows.
   A conflict is shown with both identities, so an operator learns that something else is writing to their account rather than only that a delete was refused.
   After a success the working list refreshes and the inspector clears, as they do after a manifest deletion.
   When the verifying read observed a successor, the interface says so beside the success from the `ObjectAfter` the action returned, naming its kind, length and entity tag and that it may be the archived predecessor or a concurrent write; a success is never shown as a plain deletion when something was observed at the name, and the ledger's summary carries the same observation.

6. **The Delete key opens the same dialog on a selected object row and never deletes.**
   Backspace keeps its meaning; the two keys sit together and §4.4 names the hazard.
   A prefix row's Delete key does nothing in this record, because a prefix deletion is bulk deletion and has its own dialog.

7. **A new deferred capability, off by default.**
   The action joins `v1DeferredActions` under the capability "Object deletion", with the token `object-delete` added to ADR 0082 decision 3's list and the affordance `objectDelete` added to the frontend's flags, off until the process reports the capability enabled.
   The operator trials it with the token in a shipped build; returning it is one name out of the table on their word, recorded under ADR 0052 as the earlier returns were.
   Decision 23 of the finish line is why: built and checked, disabled by default until they are ready.

8. **The contract row is Destructive and its baseline takes the drift route.**
   `"MutateService.DeleteObject": {Kind: Destructive}` requires happy, refusal and conflict, each produced on the mock and the owned Swift through a harness wrapper that binds credit to this action's receipt.
   The row changes the contract hash, so the preservation baseline is replaced from a candidate run's evidence under [ADR 0069](0069-preserve-covered-scenario-cells.md), as the verify action's was, and reviewed with the row.

9. **A selected set is the next record, not this one.**
   Multi-selection of loaded rows is the rule [ADR 0072](0072-large-namespace-handling.md) decision 3 records, and a set deletion composes this action's shape, one name with its own identity per deletion; where no inspector HEAD exists for a row, the baseline is the listing row and ADR 0085's prefix rules apply.
   Its own record, [ADR 0093](0093-selected-set-deletion.md), decides whether a set of N names is N synchronous calls or a job, and what one confirmation names.

## What this does not touch

Manifests stay with `DeleteManifest` and ADR 0085; markers with `DeleteMarker`; segments with the force-only `DeleteManifestWithSegments`, classified with bulk delete; prefixes and containers with `DeleteService`, whose protocol ADR 0085 records as owed; symbolic links with a record of their own.
Automatic reclamation stays refused under ADR 0020 apart from [ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 5.
Nothing here is a lock, a reservation or exclusive ownership.

## Required proof before implementation is accepted

Tier 1 under [DESIGN §65.12](../DESIGN.md): an authority change on a destructive path.
Backend cases run through the public service on the mock and the owned Swift, with namespace snapshots before and after so collateral is compared and not assumed.

- Happy: a plain object the inspector read is deleted; the literal after-read finds nothing; the fact is `applied` with the DELETE's transaction identifier and the audit event is present; the siblings `prefix/name2` and `prefix/name/child` and the same name in the other account are unchanged.
- Refusals, each with no DELETE sent and no intent where the record says so: a dynamic and a static large object at the name; a followed symbolic link on both fixtures, the unfollowed cross-account representation on the mock, and the owned Swift's own followed or error response for a cross-account link; an empty fingerprint; a scope naming the other account's portrait while the first is active, with no intent and no request; the same bookmark at an earlier generation after a key change; a process with the capability not enabled, refusing through the public method with no request to the store.
- Literal objects: a zero-byte object and a directory marker reached as an object row each delete; the marker's children survive and the prefix they imply still lists.
- Conflict: a replacement of a different length landing after the inspector's HEAD and before the call; a replacement of the same length with a different entity tag; each refused, nothing deleted, both identities in the fact and the audit event, credited `conflict`.
- The after-read: a dangling link, and a dynamic large object manifest whose segment container does not exist, each placed at the name after the DELETE, are found by the literal read, classified as successors of another kind and recorded as `applied` with the successor observed, where a following or expanding read reports both as absent; an empty plain object replaced by a literal link is classified as a link successor and not as the same identity; under a versions location with an archived predecessor the delete records `applied` with the predecessor observed as the successor and the archive gives it up, as the experiment measured; an unchanged non-empty object, through the public path, records `unverified`, which also shows the literal read's length and entity tag are comparable.
- Failure streams: a DELETE the store refuses leaves the object and records `failed`; a lost reply to the DELETE records `unverified` whatever the after-read finds; a verify read that fails records `unverified` with the transaction identifier; a literal after-read that finds the same identity records `unverified`; an outcome transaction failure after a landed DELETE, and after a DELETE whose reply was lost, each return the residue error with the identity, and after reopen the identity is `intent` with no terminal outcome; an intent failure sends nothing.
- The held-request exposure is stated, not claimed closed: a replacement landing after the final HEAD is deleted, as ADR 0085 says for every path, and no case may be written to pass as if that were prevented.
- Frontend, around the real `BrowserState`: the dialog binds the identity it opened for and closes when the selection or scope moves; the Delete key opens it and deletes nothing; no affordance without the capability; a conflict shows both identities; a success refreshes the list and clears the inspector; a success whose result carries `ObjectAfter` shows the observed successor beside it, and one without shows a plain deletion.
- Retained mutants, each failing a case above: the final HEAD removed; the listing row used as the baseline instead of the fingerprint; the scope check removed; the outcome recorded before the DELETE; a failed verify read recorded as `failed`; the after-read made to follow links or to expand a large object; the literal read's length and entity tag left unpopulated, so an unchanged object compares as different; the successor classified after the comparison, so a literal link reads as the same empty identity; `ObjectAfter` dropped from the result, so the interface shows a plain deletion; the class guard removed; either half of the symbolic link guard removed; the affordance shown from the frontend's defaults.
- The candidate run's drift diagnostic retained, the replacement baseline built from its reports, and the join preserving every previously protected pair plus this action's credited pairs.
- The product-level smoke extended with one case: a process started with `object-delete` names "Object deletion" as unproven and offered.

## What this does not establish

It does not prove that nobody else writes to the account, bound the interval after the final HEAD, or make any deletion safe against a writer that lands inside it.
It does not prove that deleted bytes are gone from the store, and it does not identify an observed successor's origin.
It does not lift the gate, change what a release offers, close P27 or UI-08 as a whole, or affect the valuable-data release hold.
It does not decide the selected set, symbolic link deletion, the return of the manifest and marker deletions, or the prefix protocol bulk deletion owes.
