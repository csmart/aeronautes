# ADR 0045 — bind the remaining transfer routes to their source's content

**Status:** Accepted for the local download route, which is delivered with this decision; the native copy route, the native upload route and the staged upload commit remain in the sequence.
It does not close L05: missing and reordered segments, and whole-job source and publication substitution, remain open, as does the release hold on valuable data.
The native-download remainder of those cases is [ADR 0060](0060-native-download-content-binding.md).
**Scope:** Content verification on the native copy route, the local upload route and the local download route, within DS-04/P16 and finish-line item L05.

## Problem

[ADR 0042](0042-source-content-ladder.md) built a source's content expectation as an ordered ladder of length-and-MD5 runs, and compared the assembled staged bytes against it before publication.
It says in terms that it delivers the staged copy route alone, and that the native copy route and the local upload and download routes remain in the sequence.
This decision is that sequence's next step.

The [route content record](../experiments/2026-09-14-route-content-binding.md) reproduces what those routes accept today, on the real fixture, with a proxy that alters bytes in flight and preserves their length exactly.
Four cases publish or place different bytes of the same length and report success.

`nativeCopy` compares logical length always, and compares entity tags only when the source and the destination are both plain.
A segmented source under the single-PUT limit streams into a plain destination, so the classes differ, the tag comparison is skipped, and the copy is verified on logical length alone.

`verifyUpload` compares the live object's logical length against the local file's size and nothing else.
No digest of the local file is taken anywhere on that route.
`commitStagedUpload` likewise admits a staged object on `staged.ContentLength != item.Size`, so a staged-to-published substitution passes.

The download route compares the written or staged file's size against the object's size and nothing else, on both its paths: `finishNativeDownload` after `io.Copy`, and `placeDownloaded` for a file the engine staged.

## Decision

### The expectation is the ladder that already exists

Every route builds its content expectation with the ladder ADR 0042 defined, and refuses when no ladder can be built, with the same classifications.
Nothing about the ladder, its admission rules, its ceilings or its bounded manifest reader changes here.
What changes is which byte streams are compared against it.

### Listing evidence is still not content evidence

ADR 0042 rejected a fast path that let a segment listing reproducing the ladder settle the question without reading anything, because a same-size segment replacement can leave the container row at its previous length and entity tag while the aggregate derived from those rows agrees too.
That reasoning is not weakened by moving to another route.

A staged upload that publishes an engine-segmented live object therefore cannot be verified from a remote segment listing recombined against the live aggregate, however well that listing matches the local file.
Its two admissible outcomes are the complete remote content read ADR 0042 already specifies, or an explicit `unverified` result with the staged artefacts and recovery facts retained.
This decision takes the read, so that a segmented upload publishes on the same evidence a segmented copy does.

### Where the bytes already are

Three of the four covered cases never need an extra read, because the bytes pass through the process on their way to their destination:

**Native copy.** The source body already streams into the destination PUT.
Digest it in flight along the source's ladder, and separately accumulate the MD5 of the whole body as it is sent.
Accumulating is not the comparison: after the PUT, require the live object's own HEAD to report a plain object whose length and entity tag equal the length and accumulated MD5 that were sent.

**Native upload.** The local file already streams into the destination PUT.
Digest it in flight the same way, and require the live object's HEAD to report a plain object whose length and entity tag equal the local file's length and digest.

**Native download.** The response body already streams into the temporary file through `io.Copy`.
Digest it in flight along the source's ladder.

**Engine download.** The bytes are on local disk, so a staged file could be read and digested against the source's ladder before publication, for one local read.
That comparison is not part of this decision's first delivery, for an empirical reason recorded with it: on the pinned engine, with the fixtures used, an altered body ends with no retained staged file and nothing published, so a comparison added there could not be shown to do anything.
What was observed is the state after the failure; it does not establish that no local effect occurred during the attempt.
Unproven code in a data-safety path invites a later reader to trust it, so it is not added until a fixture exists that alters the staged file on local disk after the engine has written it.

**Staged upload commit.** The staged object's entity tag establishes the stage, not the live name.
`commitStagedUpload` reads the stage and sends a separate live PUT, and the reproduced fourth case alters exactly that PUT while leaving the stage intact, so any comparison that stops at the stage is answering the wrong question.
For a plain live object, the local file's length and digest must equal what the final live HEAD reports, and no staged comparison substitutes for it.
For a segmented live object the complete assembled staged content is read and compared against the local file's ladder, and that read must succeed **before** the committing recovery fact, the live manifest PUT and the committed recovery fact, exactly as ADR 0042 orders them.
A read taken after publication would let an unverified manifest go live first, which is the ordering this decision refuses.

So the cost claim is narrower than a blanket one: the native copy, native upload, native download and engine download cases add no object bytes to the network, and only a segmented staged upload pays a content read.

### Publication is bound to the bytes that were verified

The download route publishes by a no-replace link through held directory authority and then requires the published entry to name the same file that was verified, which `publishVerified` already does with `os.SameFile`.
The digest must be bound to that same file: taken from the stream written into the temporary on the native path, and from a read of the staged file whose `os.FileInfo` is then passed to publication on the engine path.
The held directory authority is preserved unchanged; no path is resolved by name for any effect.

A download comparison that fails publishes nothing, leaves the temporary as retained residue, and reports `unverified`.
That is a true pre-publication gate.

How this sits beside the cancelled-download delta contract in [ADR 0043](0043-cancellation-delta-authority.md) is worth stating, because "exactly the accepted delta" and "a retained temporary" read as a contradiction otherwise.

They are not mutually exclusive, and any claim that they are is wrong.
Cancellation is observed at specific boundaries — before the body is opened, before the temporary is created, and as a read error while copying — and a cancel arriving after the last of them does not unwind work already done.
A context cancelled during a successful `Sync`, or during `Close` after the comparison has already run, leaves a detected mismatch detected: the item is reported failed and `unverified` with an unresolved effect, and its rejected bytes are retained.
So a single item can be both cancelled and a content mismatch.

That is consistent rather than conflicting, because ADR 0043 scopes its delta to its own staging boundaries and already allows surviving residue to remain unresolved.
What this decision adds is another way to arrive at retained, unresolved residue; it does not widen or contradict the delta ADR 0043 accounts for.

An identity that could not be established retains on every path, cancellation included, because not knowing what occupies a name can never license unlinking it.

What preserves a rejected native temporary is this route's own deferred cleanup and nothing else.
The native temporary is a sibling of its target, chosen by `downloadTemporaryPath` and written through the held target parent, so it is not in an engine stage and no stage cleanup governs it: `finishNativeDownload` simply does not reach `removeNativeTemporary` when the content was rejected or the identity is unknown.
A later change that removed that guard, or that reached for a recursive removal on this path, would silently defeat the promise.

A stronger identity is available and is recommended as separate work rather than folded in here.
Holding the temporary's descriptor open across cleanup prevents the inode being freed at all, because the kernel cannot recycle it while a reference exists, which makes the comparison sound rather than probabilistic.
That is a POSIX-general property; what differs on Windows is the identity model, not the pinning.
It touches the settled close ordering, so it is a follow-up and not part of this decision.

Two boundaries on that promise, both learned from getting it wrong.

Retention covers a comparison that failed, whatever else went wrong afterwards, including a close failure.
An earlier draft of this decision made a close failure keep the existing cleanup contract and remove the temporary anyway.
Continuous integration disproved that: on the close-failure path the removal reaches an identity check that compares device and inode, and a replacement created at the same name after an unlink can be handed the freed inode, so the check reports "still ours" for a file that is not.
That reuse is observable on the authoring host and was reproduced independently in review; the integration runner hit it in both of its jobs.
Those are observations that the condition occurs, not estimates of how often it will.
Never reaching that check is the only version of this that cannot be reopened later by a filesystem, by a change to the comparison, or by a reordering of the closes.

The cost is real and is accepted deliberately.
A source that serves fewer bytes than it advertises now leaves its partial temporary on the operator's disk, reported as unresolved residue, where that fault previously cleaned it up.
Leftover bytes the operator can see and remove are recoverable; another writer's destroyed file is not, and this project's stated order of priorities puts data safety first.

Retention is separate from identity, and identity is not conditional.
The identity of the bytes this call wrote is captured on every path, successful or not, so cleanup can refuse to unlink an entry another writer has since put at the temporary's name.
Preserving an existing rule that cleans up our own failed temporary does not permit deleting somebody else's file, and no property of the source — its stated length, or whether that length agrees with the listing — may be the condition for protecting another writer.
This decision's first implementations got that wrong three times before it was caught in review, which is why the rule is stated here rather than left implicit in the code.

An identity that could not be established is not a weaker identity, it is none, and it is never authority to unlink.
When the temporary's identity cannot be read, the file is retained and reported as unresolved residue, and the failure is joined with any earlier one rather than replacing it.
Cleanup must never read a missing identity as permission to remove whatever regular file happens to hold the name.

### A failed publication must stay failed downstream

Native copy and native upload publish with a single PUT at the live name, so a store that did not keep what was sent can only be detected after the effect exists.
A post-PUT comparison alone does not contain that.

Native copy currently includes the source fingerprint in the initial PUT, before any content verification.
`verifyItem` and `skippableEqual` can then read that stamp back and treat the name as proven, so a job that correctly reports the object failed can still count it verified and let a later run skip it.

The fingerprint is an assertion that the destination holds the source's content, so it must not be written before that is established.
The initial PUT therefore carries no fingerprint.

Nor is it added afterwards.
Writing the stamp once the comparison succeeds would need that write bound to the object that was checked, and no such binding exists here: `UpdateObjectMetadata` preflights with a HEAD and then performs a replace-all POST, which is not an atomic content condition.
A replacement landing between the successful check and that POST would receive a valid source fingerprint while holding other same-size bytes, which is the false-proof path this decision exists to remove, and a replace-all POST could also displace unrelated metadata.
So this native route publishes unstamped, and its successful comparison is retained as evidence for the current job only.
Later equality and skip decisions on an unstamped object stay conservative, which is the correct outcome rather than a shortfall.
An atomic binding, if one is ever specified and proved, could revisit this; introducing the mutation without one is not permitted by this decision.

Independently of the stamp, a name this executor recorded as a content failure is not eligible for a verified count, a `Same` verdict, or a skip in the same job.
Retention is unchanged: the live name, its bytes and every recorded fact are kept and reported as residue, and nothing is deleted or rewritten to repair it, under [ADR 0020](0020-recovery-containment.md).

### An abandoned request is not a proof of no effect

Detecting a mismatch while a body is still being sent permits abandoning the request, but abandonment does not establish that no object was created.
Only a refusal decided before any byte of the body is sent reports `not attempted`.
A mismatch detected in flight, or after the request was acknowledged, reports an unresolved effect with the live name treated as possible residue.

### Nothing else changes

Existing refusals, counters and their tests keep their current meanings except where this decision names them.
No overwrite, Mirror start, source deletion, automatic segment cleanup or credential reclamation is authorised.
The bridge surface does not widen: no digest is added to `UploadItem` or `DownloadItem`, and no generated binding changes.

Each route takes its expectation from an authoritative observation at execution time.
That is not a defence against a source that changed since preflight: these local preflight items retain no prior content identity, so there is nothing to compare a fresh observation against.
Whole-job source and publication substitution stays deferred and is not claimed here.

## Cost

Native copy, native upload, native download and engine download add no object bytes to the network; their comparisons ride streams the process already carries, or read a file already on local disk.
A staged upload publishing a plain live object adds no content read either.
A staged upload publishing an engine-segmented live object pays one complete read of the staged content before publishing it, the same cost and the same ordering as the delivered staged copy route.

Rejected alternatives: recombining a remote segment listing against the live aggregate in place of a content read, which is the counterexample ADR 0042 disproved; carrying a digest in the preflight DTOs, which widens the bridge surface and pins verification to an observation with no prior identity to compare against; and relying on the engine's own checksumming, which is a property of a pinned third-party binary rather than evidence this project produced, and which does not cover the native routes or the commit copy at all.

## Consequences

Every enabled transfer route carries content evidence rather than a length comparison.

Refusals become possible where transfers previously succeeded: where the far side does not hold the expected bytes, and where a source states no usable expectation.
Each has its own named test.

A native copy no longer stamps its fingerprint at all, so every object it publishes is unstamped where it previously carried a stamp written before any content check.
That is a deliberate loss of a fast-skip opportunity in exchange for not asserting something unproven and not adding an unguarded metadata write.

The native download path refuses before publishing.
The engine download path is left unchanged pending a fixture that can show a comparison there doing work.
Native copy and native upload can only report an unresolved live name.
Routing those two behind a staged name would let them refuse too, and is recommended as separate work; it is not decided here.

## Limits

MD5 is the store's integrity expectation for an honest provider, not authenticity against forged or colliding evidence.

The ladder is bound to an observation taken at execution time, not to current segment contents; ADR 0042's limits on that binding are unchanged and apply here.

Nothing here audits the engine's internal behaviour.

This decision covers same-size content binding for the enabled routes.
It does not close L05: missing and reordered segments, and whole-job source and publication substitution, remain open, as does the release hold on valuable data.
The native-download remainder is [ADR 0060](0060-native-download-content-binding.md).
