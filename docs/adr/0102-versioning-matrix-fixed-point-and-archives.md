# ADR 0102: versioning in 0.5.0: the measured matrix, the mode at the fixed point, and archive containers

**Status:** Accepted for 0.5.0 (9 November 2026).
Remediation item P26 names the acceptance, and the roadmap the operator accepted puts the item in this release.
Decisions 3 and 4 are the author's choices between alternatives the operator has not ruled on; the alternatives are kept below so either can be reversed by a later record without re-deriving them.
**Scope:** What this release claims about container versioning on the stores it has measured, how a replacement that was authorised under one versioning observation behaves when the container reports something else by the time it commits, and what a bulk deletion tells the operator when the container it empties is another container's archive.
It changes neither the container editor's refusals ([ADR 0054](0054-versioning-preflight-authority.md)), the delete dialogs' versioning sentence ([ADR 0092](0092-exact-object-deletion.md)), the overwrite acknowledgement ([ADR 0081](0081-upload-overwrite-acknowledgement.md), [ADR 0087](0087-copy-overwrite-on-upload-terms.md)), nor the v1 capability gate ([ADR 0052](0052-v1-capability-gate.md)).

## Context

P26 asks that the legacy archive and the modern display-only and refusal paths have real Swift evidence, that a preliminary read failure refuses a mutation, that changing versioning invalidates relevant plans, and that hidden versions cannot be silently lost.
ADR 0054 met the second: an unestablished mode refuses the container edit, and the editable fingerprint carries the versioning headers, so a container whose versioning changed under the editor is a conflict.
The first is met now.
The [27 October measurement](../experiments/2026-10-27-delete-under-versioning.md) recorded what a delete does under each legacy mode on the fixture; the [9 November run on a genuine Swift](../experiments/2026-11-09-legacy-versioning-real-swift.md) shows the same matrix through the product's own routes, renewable with `make oracle`; and the [9 November fixture measurement](../experiments/2026-11-09-modern-versioning.md) is the first record of what the modern mode does.

### What the stores do

| Store | `versioned_writes` | `object_versioning` |
| --- | --- | --- |
| the owned fixture (Swift 2.38) | versions and history locations | yes |
| the OpenStack Swift backend of the real-cloud oracle (Swift 2.35) | versions and history locations | no |
| the Ceph RGW backend of the real-cloud oracle (18.2) | no | no |

Under a versions location an overwrite archives the predecessor and a delete archives nothing: the current content is destroyed and the name reverts to an archived predecessor if there is one, which leaves the archive.
Under a history location both archive, and a delete writes a zero-byte marker beside the moved content.
Both are now measured on the fixture and on a real Swift, with the delete route reporting the reverted predecessor as the successor its literal read found.
Modern versioning keeps every version in its own container, a delete writes a marker and keeps the content, and disabling keeps what exists while later writes overwrite one unversioned entry.
A legacy location header POSTed onto a modern-versioned container is accepted by the store, and every later versioning change answers 400 until the header is removed; what happens to the objects differed between the two sequences measured, a history location set before the first write refused to replace or delete that name with 412 while a first write of a new name landed, and a versions location set over versions already written let them be deleted by id, so the record infers no rule beyond the refusal of versioning changes; the reverse is refused with 400.
The archive a legacy location names is a container of the same account: the header carries a container name and nothing else.

### What the routes do today

The upload and copy replacements read the destination container's versioning before the native question and state it in the question as an observation, not a promise of recovery (ADR 0081 decision 8); the reclaim decision reads it again at reclaim time; the fixed-point comparison of [ADR 0085](0085-reobserved-destructive-identity.md) compares the object's identity and nothing about the container.
A container that changes from a versions location to no versioning between the question and the publishing request therefore destroys a predecessor the operator was told would be archived, and the record says the mode was observed, which is true, and nothing more.
The deletion routes know a container's own versioning for the §33 configuration snapshot and the dialog's sentence, and nothing about whether the container is another container's archive.
Emptying, deleting or sweeping an archive container destroys the earlier versions of every object the owning container ever overwrote, and the dialog says only that the archive container itself keeps no versions.

## Decision

1. **The measured matrix is the release's claim.**
   The container editor keeps the legacy modes editable where `/info` allows them, keeps modern versioning display-only and refuses its rewrite, and the three measurement records are the evidence P26 asked for.
   On a store without `versioned_writes` the editor offers no mode, as it does today when the capabilities are known.
   The real-cloud oracle keeps the legacy matrix renewable: `TestLegacyVersioningOracle` asserts the fixture's matrix against each configured backend that allows a mode.
2. **A replacement is committed only under the versioning observation it was authorised under.**
   The route asks, at the fixed point and beside the object comparison, whether the destination container still reports exactly the versioning the question was composed from, the mode and the archive container as read, and the answer is a fresh container read made then, never the observation remembered.
   The two observations are compared as values and never as the sentence the question displayed, because the sentence bounds and sanitises the archive name for display, so two archive names can read alike.
   A container that reports something else, or cannot be read, refuses that one replacement with the live object untouched, as a commit-stage conflict of the kind ADR 0085 records: both observations, the one authorised and the one found, go to the audit trail for the name, stated as the question states them, and the run continues with its other items.
   This applies to upload and to copy, on every publishing route either has; reclaim keeps its own re-read.
   The cost is one container read per replacing item, stated here because the question does not state request costs; a create pays nothing.
3. **A bulk deletion says whose archive the container is.**
   The preview of the three bulk modes, the prefix sweep, the empty and the delete-container mode, reads the account's containers for a versions or history location naming the target, one container HEAD each, a bounded number at a time, up to 1,000 containers; an account with more containers than that is not scanned.
   The preview returns the owners it found and, when the scan did not cover the account or a read failed, that the answer is not established and why, in fixed words that carry no identifier of the account.
   A container that is gone by the time it is read is not a failed read: a container that no longer exists archives nowhere, so it is no owner and leaves the answer established.
   The dialog states, beside the versioning sentence ADR 0092 settled, that this container is the archive of the named containers and that the deletion destroys the earlier versions they keep here, or that whether another container keeps its versions here could not be established; the container names are sanitised and bounded where they are interpolated, as the archive name is in the overwrite question.
   The confirmation stays the typed container name, and nothing is bound at start: the disclosure stands as the versioning sentence does, read at preview time.
   The exact-object, selected-set and manifest dialogs are unchanged, because the rows they delete are visible and their versioning sentence already says what a delete does.
4. **Modern versioning management stays out of 0.5.0.**
   Neither real cloud offers it, the fixture does, and setting or clearing `X-Versions-Enabled` from the editor would need the product to own the disable and re-enable transitions the fixture measurement records for one build only.
   The inspector keeps showing the mode, and the editor keeps refusing to rewrite it.

### Why these shapes

A refusal in decision 3, with the owner named and the operator sent to the owning container's editor first, was considered and not taken.
Pruning an archive is ordinary maintenance, and a refusal would push an operator who wants to prune into switching the owner's versioning off first, which loses every overwrite made in the interval; a refusal would also have to decide what to do for an account the scan cannot cover, and either answer there is wrong for someone.
A disclosure the operator reads before typing the container name is the pattern the deletion dialogs already use for the versioning consequence, and it names the one fact the operator cannot see from inside the archive.
The bound is a stop against the pathological, far above any account the product has been pointed at, and a scan that cannot cover the account says so rather than guessing.

Binding the archive relationship at start, as ADR 0081 decision 4 binds the collision set, was considered and not taken.
The deletion routes bind the scope by the typed name and each member by its listing row at its own fixed point, and read the container's configuration for reporting; a container that becomes an archive between the preview and the start is the same exposure the versioning sentence already has, stated below, and closing it would repeat the scan at start for a window no one has observed.

Comparing the sentence the question displayed in decision 2, instead of the observation it was composed from, was considered and not taken.
The sentence bounds and sanitises the archive name for display, so two archive names with one displayed prefix would compare equal and a changed archive would pass; the observation the model already returns, the mode and the archive as read, is exact and costs no second representation, and a changed archive is a change the operator was not told about.

Managing modern versioning in this release, with the fixture measurement as its evidence, was considered and not taken: a transition the product would own on a store no supported real cloud has, measured on one build, is a feature for a release that has such a cloud to measure.

## Maintained proof

- Go, model: the archive scan on the wire fakes finds an owner by a versions location and by a history location, admits unrelated containers, stops without a read for an account above the bound and reports it, reports a failed read as unestablished with the owners it had found, and counts a container gone since the listing as no owner.
- Go, service: the bulk delete preview carries the owners for a container another container names as its archive and nothing for an unrelated one, on the mock where it holds the header and on the owned Swift; the counts and the cost are unchanged.
- Go, transfer: an upload and a copy replacement whose destination container reports a different versioning observation at the fixed point are refused with the live object untouched, both observations recorded for the name, the create items of the same run published and the run's other replacements unaffected; a container whose observation is unchanged replaces as today; an archive that moves to a name the question would display the same way is a change; on the mock and on the owned Swift, with the change made between the question and the publishing request.
- Frontend: the bulk delete dialog states the owners and what the deletion does to their versions, states that the answer is not established when the preview says so, and shows neither for an unrelated container.
- Real cloud: `TestLegacyVersioningOracle` under the `realcloud` tag, run with credentials, asserts the matrix against each backend that allows a mode and skips the modes a backend does not advertise.

## Limits

A container that becomes another's archive after the preview is not seen; the disclosure is read when the preview is, as the versioning sentence is.
A versioning observation that changes after the fixed-point read and before the publishing request lands is not caught, as ADR 0085 already states for the object's identity, and where a route's object read comes first the identity window of that decision grows by the one container read decision 2 adds.
The archive scan reads at most 1,000 containers and says when it could not cover the account; it never says a container is not an archive, only that it found no owner.
The modern mode is measured on one fixture build and on no real cloud, and nothing here manages it.
L09 asked that the supported legacy transfer, delete and cleanup matrix preserve hidden versions; this record narrows the ways a hidden version is lost without a word to the operator, and whether that closes L09 is for the finish-line register at the hold lift, not for this record.
