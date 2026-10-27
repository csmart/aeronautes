# ADR 0092 — delete one object the operator was shown

**Status:** Proposed on 27 October 2026 for the operator's decision of the same day, recorded as decision 30 in the [Linux finish line](../audits/2026-09-05/linux-finish-line.md), in their words "yes, we need delete".
Written for review before any implementation, in the shape [ADR 0085](0085-reobserved-destructive-identity.md) took.
Nothing here is built.
It proposes one new bridge action behind the [ADR 0052](0052-v1-capability-gate.md) gate, lifts nothing from that gate, and changes no refusal of [ADR 0020](0020-recovery-containment.md).
**Scope:** One synchronous deletion of one plain object the operator has inspected: `MutateService.DeleteObject`, its guards, its durable facts, its contract row, its gate classification and its interface.
It does not cover manifests, markers, segments, prefixes, containers, symbolic links or a selected set; each is named below with the record that owns it.

## The requirement, and what exists today

[DESIGN §63](../DESIGN.md) lists "delete ordinary objects" as the first item under Delete, and it has never existed.
The [remediation plan](../audits/2026-09-05/remediation-plan.md) carries it as P27 and UI-08: the exact-object delete journey, with the rule that a prefix deletion is never a substitute for it, and a completion test that the plain, empty, link and manifest identity transitions and same-prefix siblings are safe.
The finish line's 14 September re-baseline deferred it with the destructive routes, and the [25 September release plan](../audits/2026-09-25/next-release-plan.md) placed deletion last.
The operator met the gap on the first day of testing the Linux build: an uploaded object could not be deleted, because the only deletion a build can be made to offer is the bulk deletion of a prefix or a container, whose own control says it is not the selected object.
The [road to 1.0.0](../audits/2026-10-27/road-to-1.0.0.md) brings deletion forward on their word and makes this record its first slice.

What a build has, behind the gate, is the shape this record extends.
`Session.DeleteManifestObject` HEADs the name, refuses a plain object, refuses when the class, length and entity tag differ from the identity the inspector showed, deletes with nothing of ours between the HEAD and the DELETE, and verifies by a read that finds nothing.
`MutateService.DeleteManifest` wraps it with the gate, the writer gate and an audit event whose summary withholds the name and carries both identities on a conflict.
That is ADR 0085's narrowest window, and it exists for manifests only.

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
   `MutateService.DeleteObject(ctx, scope ListingScopeDTO, container, name, contentFingerprint string) (ObjectDetailDTO, error)`.
   The scope is the portrait the inspector's detail was read under, passed as ADR 0079 decision 4 has the frontend pass it, and the call is refused with `ErrMutationScopeChanged` before any intent or request when that portrait is empty, names another bookmark or has moved a generation.
   The fingerprint is the `ContentFingerprint` the inspector's HEAD returned for this object, handed back the way `DeleteManifest` and the metadata editor hand back theirs; an empty one is refused, because a delete that cannot say what it meant to destroy is refused.
   Unlike the two deletions ADR 0079 left unbound, this action is born bound; it has no callers to keep a signature for.

2. **The session guards are ADR 0085's three, with the class guard turned the other way.**
   A fresh HEAD of the name immediately before the DELETE is the final comparison.
   A name that holds a dynamic or static large object is refused and the operator is pointed at the manifest deletion, so the manifest-only semantics stay with the record that owns them and the two paths never overlap.
   A HEAD that followed a symbolic link is refused: the identity the operator was shown is the target's, a DELETE at the name would remove the link rather than the thing shown, and deleting a link needs its own identity read (`Session.SymlinkGet`) under a record of its own.
   A class, length or entity tag that differs from the fingerprint refuses with `IdentityConflictError`, carrying both identities, and deletes nothing.
   Only then is the DELETE sent, with nothing of ours between the HEAD's reply and it.
   A zero-byte object is a plain object and is deleted like any other; a directory marker reached as an object row is the object it is, its children are untouched and the prefix they imply remains, and the dialog says so.

3. **Verification is a HEAD of the exact object, and only absence is success.**
   A read that reports not found records `applied`.
   A read that finds anything, whether the same identity or another, records `unverified` and names what it found, because either the DELETE has not taken effect yet or another writer has already placed something at the name, and this record cannot tell which.
   A read that fails records `unverified` with the DELETE's transaction identifier.

4. **Durable facts in the ADR 0058 shape, with the operation `object.delete`.**
   The intent commits before the HEAD; a failed intent sends nothing.
   The outcome and the audit event commit in one transaction after the attempt: `applied`, `conflict` for the identity refusal, `failed` for a refusal or a store error that provably destroyed nothing, and `unverified` as decision 3 says.
   A DELETE that landed and an outcome transaction that failed is residue, returned as a named error carrying the identity, and that identity is not `applied` on reopen.
   The audit summary withholds the object name as the manifest deletion's does, because a name can be a secret, and carries the two identities, the class, the byte count and the entity tag on a conflict.
   Returning `DeleteManifest` to a build is a separate change; whether it joins this ledger then is decided there, not here.

5. **What the operator is told before and after.**
   The dialog binds the exact identity it opened for, as the manifest and bulk dialogs do, and acts on nothing else.
   It names the object, says the data at this name is destroyed, and states the container's versioning mode and archive as the inspector knows them.
   What a DELETE does to a versioned container is not what an operator expects and is not recorded in this repository, so the implementation slice measures the effect under no versioning, a versions location and a history location against the owned Swift and records it before the dialog's sentence is written; the sentence states a measurement, not a reading of the documentation.
   A conflict is shown with both identities, so an operator learns that something else is writing to their account rather than only that a delete was refused.
   After a success the working list refreshes and the inspector clears, as they do after a manifest deletion.

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
   Its own record decides whether a set of N names is N synchronous calls or a job, and what one confirmation names.

## What this does not touch

Manifests stay with `DeleteManifest` and ADR 0085; markers with `DeleteMarker`; segments with the force-only `DeleteManifestWithSegments`, classified with bulk delete; prefixes and containers with `DeleteService`, whose protocol ADR 0085 records as owed; symbolic links with a record of their own.
Automatic reclamation stays refused under ADR 0020 apart from [ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 5.
Nothing here is a lock, a reservation or exclusive ownership.

## Required proof before implementation is accepted

Tier 1 under [DESIGN §65.12](../DESIGN.md): an authority change on a destructive path.
Backend cases run through the public service on the mock and the owned Swift, with namespace snapshots before and after so collateral is compared and not assumed.

- Happy: a plain object the inspector read is deleted; the after-HEAD finds nothing; the fact is `applied` with the DELETE's transaction identifier and the audit event is present; the siblings `prefix/name2` and `prefix/name/child` and the same name in the other account are unchanged.
- Refusals, each with no DELETE sent and no intent where the record says so: a dynamic and a static large object at the name; a followed symbolic link; an empty fingerprint; a scope naming the other account's portrait while the first is active, with no intent and no request; the same bookmark at an earlier generation after a key change; a process with the capability not enabled, refusing through the public method with no request to the store.
- Conflict: a replacement of a different length landing after the inspector's HEAD and before the call; a replacement of the same length with a different entity tag; each refused, nothing deleted, both identities in the fact and the audit event, credited `conflict`.
- Failure streams: a DELETE the store refuses leaves the object and records `failed`; a lost reply to the DELETE records `unverified`; a verify read that fails records `unverified` with the transaction identifier; an after-HEAD that still finds the object records `unverified` naming it; an outcome transaction failure after a landed DELETE returns the residue error and the identity is not `applied` after reopen; an intent failure sends nothing.
- The held-request exposure is stated, not claimed closed: a replacement landing after the final HEAD is deleted, as ADR 0085 says for every path, and no case may be written to pass as if that were prevented.
- Frontend, around the real `BrowserState`: the dialog binds the identity it opened for and closes when the selection or scope moves; the Delete key opens it and deletes nothing; no affordance without the capability; a conflict shows both identities; a success refreshes the list and clears the inspector.
- Retained mutants, each failing a case above: the final HEAD removed; the listing row used as the baseline instead of the fingerprint; the scope check removed; the outcome recorded before the DELETE; a failed verify read recorded as `failed`; the class guard removed; the symbolic link guard removed; the affordance shown from the frontend's defaults.
- The candidate run's drift diagnostic retained, the replacement baseline built from its reports, and the join preserving every previously protected pair plus this action's credited pairs.
- The product-level smoke extended with one case: a process started with `object-delete` names "Object deletion" as unproven and offered.

## What this does not establish

It does not prove that nobody else writes to the account, bound the interval after the final HEAD, or make any deletion safe against a writer that lands inside it.
It does not lift the gate, change what a release offers, close P27 or UI-08 as a whole, or affect the valuable-data release hold.
It does not decide the selected set, symbolic link deletion, the return of the manifest and marker deletions, or the prefix protocol bulk deletion owes.
