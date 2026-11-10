# ADR 0094 — make marker and manifest deletion return-ready

**Status:** Accepted and delivered: both actions were returned to the release on 1 November 2026 by [ADR 0052](0052-v1-capability-gate.md) decision 13, and the `marker-manifest-delete` token named below no longer exists. Proposed on 27 October 2026 for the 0.2.0 step of the [road to 1.0.0](../audits/2026-10-27/road-to-1.0.0.md) that returns marker and manifest deletion, the pair [ADR 0052](0052-v1-capability-gate.md) decision 7 deferred with the destructive work.
Written for review before any implementation, and implemented on 27 October 2026 behind the `marker-manifest-delete` token: the shared session protocol, the bound services with their ledger, the browser-owned dialogs and the public proof on both fixtures are in the range that carries this sentence, with the capability renamed and still deferred.
This record itself returns no capability: the return was the operator's word, recorded in ADR 0052 decision 13, as the earlier returns were.
This record decides what the two actions must be before that word can be asked for.
**Scope:** `MutateService.DeleteMarker` and `MutateService.DeleteManifest`, the session protocol behind each, the interface that offers them behind the `marker-manifest-delete` token, and their public proof.
Deleting a manifest together with its segments stays force-only with bulk deletion; selected sets are [ADR 0093](0093-selected-set-deletion.md); prefixes, containers and symbolic links keep their own records.

## What exists today, and what is missing

When this record was written both actions were built and gated: the deferred-table entries and the token, the frontend flags, the inspector's buttons, a manifest dialog and the services with their unit tests all existed, and [ADR 0085](0085-reobserved-destructive-identity.md) had bound the manifest deletion to the identity the operator was shown.
Four things were missing, and each was something [ADR 0092](0092-exact-object-deletion.md) built for the plain object that these two older paths predated.

- Neither action takes the caller's scope under [ADR 0079](0079-caller-named-mutation-binding.md); both act on the active session.
- Neither records the durable facts of [ADR 0058](0058-object-metadata-durable-facts.md); decision 7 of that record left them on best-effort audit, and the marker path discards an audit failure.
- The marker deletion compares no identity: it checks that the name holds a zero-byte `application/directory` object, and nothing else, and the inspector offers it as a one-click button with no confirmation, which §4.4 of the design forbids for any deletion.
- The manifest protocol lacks three things the plain-object protocol has: one credentials snapshot around the comparison, the DELETE and the verifying read; the DELETE's own transaction identifier in the record; and an unverified outcome, so a manifest still observed afterwards is reported as a failure although nothing says whether the DELETE took effect, and a predecessor restored by versioning reads as the delete having failed.

Neither action had a producer in the scenario inventory, so their six required cells were uncredited and unprotected.

## The behaviour oracle

- ADR 0085: a comparison immediately before the effect, nothing of ours between them, a difference refuses with both identities, the exposure after the final comparison is stated and never closed.
- ADR 0092 decisions 2, 3 and 4 and its session layer: classification before comparison, symbolic links refused in either representation, one credentials snapshot, no repeated DELETE, only a 2xx accepts, the literal verifying read with its outcome words, the ledger with intent before effect and residue when a DELETE may have landed unrecorded.
- ADR 0079: a destructive request names the portrait its view was built under.
- ADR 0058: intent before the effect, outcome and audit in one transaction, `unverified` is never written as `failed`.
- [ADR 0039](0039-delete-request-authority.md): one destructive request per name.
- The [measurement of 27 October 2026](../experiments/2026-10-27-delete-under-versioning.md): what a DELETE does under each versioning mode, which holds for a marker and a manifest as for any object, because the store does not know the difference.
- DESIGN §4.4: the Delete key opens a safe delete flow, never an immediate deletion; §14.1: a marker is an explicit zero-byte `application/directory` object and its children are independent objects; §60 Phase 4: manifest-only deletion leaves segments by construction.

## Decision

1. **Both actions bind the caller's scope and keep the ledger.**
   `MutateService.DeleteMarker(ctx, scope, container, prefix, baseContent)` and `MutateService.DeleteManifest(ctx, scope, container, name, baseContent)` take the `ListingScopeDTO` the view was built under and are refused before any intent and any request when the portrait is empty or has moved, exactly as `DeleteObject` is.
   Each records an intent under its own operation, `marker.delete` or `manifest.delete`, before the store is asked, and its outcome and audit event in one transaction afterwards; a DELETE that landed or may have landed whose outcome cannot be recorded is residue that names the identity.
   The audit summary withholds the object name, as the manifest deletion's already does, and describes a failed read by its kind and status only.

2. **A marker is deleted by the plain-object protocol plus the marker rule.**
   The identity shown for a marker is the prefix detail's own object, a plain object of zero bytes, so `baseContent` is that object's content fingerprint and the comparison is ADR 0092's: class, length and entity tag at the comparison HEAD, under one credentials snapshot, with one DELETE issued immediately after it and never repeated.
   Because every empty object has the same fingerprint, the comparison also requires the marker rule the classification already uses: the object at the name reports `application/directory`; anything else at the name, a real object in a folder's clothing, an implied prefix with no object, a manifest or a link, is refused with both identities and the class found.
   The verifying read and its outcome words are decision 3's; children are never read and never touched, and a marker observed again at the name with the same identity is unverified, not failed.

3. **The manifest protocol becomes the plain-object protocol with the class inverted.**
   The comparison read is the read the inspector's detail was built from, so the identity shown and the identity found are computed the same way; it must show a dynamic or static large object, and a plain object, a marker or a symbolic link in either representation is refused.
   Under one credentials snapshot the DELETE follows that read immediately, as a plain DELETE of the manifest and never the segment-expanding form, and only a 2xx accepts.
   The verifying read is the literal read of ADR 0092 decision 3, with link following and manifest expansion suppressed, and because the comparison read describes the assembled content while the literal read describes the stored manifest, the two are not compared as identities: absence, or a successor that is not a manifest of the same kind, is `applied` with the successor returned and shown, which is how a predecessor restored by legacy versioning appears when it is a plain object; a manifest of the same kind still observed at the name, a lost reply, a server error or a failed read is `unverified`, and a comparable manifest baseline is not taken in this record.
   The DELETE's transaction identifier joins the comparison read's in the fact and the event.
   Segments are never read, listed or touched; where the manifest names them is the one thing the summary keeps, as today.

4. **The capability is one, and says what it is.**
   The two actions stay under one deferred capability and one token, `marker-manifest-delete`, so a trial offers both or neither and the return is one word.
   The capability is renamed from "Marker and manifest editing" to "Marker and manifest deletion", because marker creation returned under ADR 0052 decision 8 and the old name no longer describes what the token admits; the token keeps its name so an operator's environment keeps working.

5. **What the operator is told.**
   The marker gains a dialog in the shape of the object deletion's, owned by the browser state so its outcome survives the refresh that follows: it binds the identity it opened for, says that it removes the marker object only and that the objects under the prefix are untouched, states what the delete does under the container's versioning mode in the sentence settled from the measurement, shows a refusal with both identities while its view remains current, closes when its view moves, and shows an observed successor instead of a plain success.
   Both dialogs share the object deletion's readiness rule: confirmation is allowed only once the versioning read has settled, with the facts known or explicitly unavailable, and the warning that result calls for is shown, the conservative warning on a settled unknown included; a confirmation while the read is pending sends nothing.
   The one-click marker button goes; nothing deletes without that dialog.
   The manifest dialog keeps its shape and gains the same ownership by the browser state, the versioning sentence under that readiness rule, and the successor disclosure; its manifest-only path never calls the segment-ownership check, which is classified with bulk deletion and is consulted only for the with-segments option that bulk deletion offers.
   The Delete key opens the flow that owns the selected row's kind: the object deletion for a plain object, the manifest deletion for a manifest, the marker deletion for a marker-backed prefix, and nothing for a symbolic link or an implied prefix, with the inspector saying why.

6. **The return stays the operator's word, and nothing here is it.**
   The actions stay in the deferred table under the renamed capability and the token until the operator says they return; when that word comes it is recorded in ADR 0052, the names leave the table, the flags turn on by default and the token leaves ADR 0082's list, as upload's did.
   The contract rows are unchanged, so the baseline takes [ADR 0069](0069-preserve-covered-scenario-cells.md)'s routine update, not the drift route: the producers below credit the six cells, the candidate run passes preservation against the committed baseline because nothing protected is lost, and after the merge the resulting master run's reconciled producer set extends the protected set with every newly credited pair, in a baseline change reviewed with its retained evidence.

## What this does not touch

Deleting a manifest with its segments stays with `DeleteManifestWithSegments`, force-only and classified with bulk deletion; prefixes and containers stay with `DeleteService` and the protocol ADR 0085 records as owed; symbolic links keep their own record; the selected set is ADR 0093.
It does not bound the interval after the comparison read, prove that bytes are gone, identify a successor's origin, or lift the gate, and the valuable-data release hold stays in force.

## Required proof before implementation is accepted

Tier 1 under DESIGN §65.12.
Backend cases run through the public service on the mock and the owned Swift with namespace snapshots before and after, and the controls are retained in full as the object deletion's were.

- Happy, marker: a marker with children is deleted; every child and every sibling, in both accounts, is unchanged; the fact and the event carry the DELETE's transaction identifier; the request log shows one HEAD-then-DELETE pair and the literal read, no listing and no child request.
- Happy, manifest: a DLO and an SLO manifest are deleted manifest-only; every segment and every neighbour is unchanged; the facts carry the DELETE's identifier.
- Refusals before any request: an empty or moved scope; an empty name; each with no intent, no fact, no event and no request.
- Refusals at the comparison: a real object in a folder's clothing at the marker name, an implied prefix, a plain object at the manifest name, a symbolic link in either representation at either name, each with both identities recorded and no DELETE sent.
- Conflict: the marker or manifest replaced after it was shown is refused with both identities and the request log shows no DELETE.
- Outcomes: a store refusal is failed with the object intact; a lost reply is unverified whether the name is empty, holds the same identity or holds a successor; a server error is unverified; a manifest of the same kind still observed after the DELETE is unverified; a plain predecessor restored by legacy versioning is applied with the successor shown and the archive observed emptied, as the measurement recorded; history-location archiving is applied with the archive observed grown.
- Adjacency and exposure on the owned Swift, through a gate that places a replacement at the name at each boundary the protocol has: a changed identity before the comparison read refuses with both identities and no DELETE is sent; a replacement landing after the comparison read and before the DELETE may be deleted, and the case measures that as the exposure this record states rather than requiring a refusal the protocol cannot give; a replacement landing after the DELETE is observed by the literal read and classified by decision 3's outcome words, a plain successor reported as the successor and a manifest of the same kind as unverified; and no authentication and no other request of ours runs between the comparison read and the DELETE.
- Residue: an outcome that cannot be recorded after the DELETE returns the error that names the identity, and the intent is the only durable row.
- Frontend, around the real browser state: the marker dialog binds, states, shows refusal and successor, closes with its view; both dialogs send no mutation while the versioning read is pending and allow confirmation under the shared settled-result rule, the conservative warning on a settled unknown included, as the object deletion's held-facts case already shows; the one-click path is gone; the manifest dialog's manifest-only path sends no ownership check; the Delete key opens each flow for its row kind and nothing for a link; the renamed capability's flags and the smoke's startup line.
- Retained mutants, each failing a case above: the scope check removed; the intent recorded after the DELETE; the marker rule removed, so an empty plain object deletes through the marker path; the content-type check made the only check, so a replaced marker of another identity deletes; a DELETE repeated after a refused token; the segment-expanding DELETE used; the verifying read made to expand or follow; the same identity afterwards classified as failed; the successor dropped; the one-click marker button restored; the ownership check called from the manifest-only path.
- The merged head's reconciled master producer set retained, and the protected set extended from it under the routine update with the old and new baseline reviewed together.
