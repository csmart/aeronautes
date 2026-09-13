# ADR 0045 — bind the remaining transfer routes to their source's content

**Status:** Proposed.
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

**Engine download.** The bytes are on local disk, so the staged file is read and digested against the source's ladder before publication: a local read, no network content.

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

A download comparison that fails publishes nothing, leaves the temporary or staged file as retained residue, and reports `unverified`.
That is a true pre-publication gate.

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

Download refuses before publishing.
Native copy and native upload can only report an unresolved live name.
Routing those two behind a staged name would let them refuse too, and is recommended as separate work; it is not decided here.

## Limits

MD5 is the store's integrity expectation for an honest provider, not authenticity against forged or colliding evidence.

The ladder is bound to an observation taken at execution time, not to current segment contents; ADR 0042's limits on that binding are unchanged and apply here.

Nothing here audits the engine's internal behaviour.

This decision covers same-size content binding for the enabled routes.
It does not close L05: missing and reordered segments, and whole-job source and publication substitution, remain open, as does the release hold on valuable data.
