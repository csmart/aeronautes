# ADR 0042 — verify copies against the source's content ladder

**Status:** Proposed, revised after review; the staged copy route is proposed here, and the native copy route and the local upload and download routes remain in the sequence.
**Scope:** Content verification of a staged copy before publication, for every source class the route accepts, within DS-04/P16 and finish-line item L05.

## Problem

[ADR 0037](0037-staged-content-read.md) established one complete staged content read for a plain source and a staged dynamic large object, and left segmented sources out because the source's entity tag was read as an unusable expectation for them.
The [change detection slice](../experiments/2026-09-11-copy-verification-strength.md) reached the same conclusion and recorded that comparing source and destination segment structure is not a remedy, because a correct copy may legitimately resegment.

The consequence is that a segmented source publishes with no content evidence at all.
`commitStaged` compares logical length, sums the staged segment bytes, and skips the content comparison because the source is not plain; the live name is then stamped with `fingerprint(src.ContentLength, src.ETag)` from the commit-time HEAD, so a source that changed while the engine was reading is recorded as the identity of bytes the destination does not hold, and every later skip decision trusts that stamp.
The same gap covers a segmented source materialised as a plain staged object, which is checked on logical length alone.

## Decision

### The ladder is the expectation

A source's content expectation is an ordered ladder of runs, each a byte length and a thirty-two hex digit MD5.

A plain source yields one run: its logical length and its entity tag.

A dynamic large object source yields one run per segment, from an ordered listing of its manifest prefix.
The ladder is admitted only when every entry states a complete MD5, the run lengths sum to the source's reported logical length, and the MD5 of the concatenated entity tags equals the source's reported entity tag.

A static large object source yields its manifest's recorded ladder: each entry states the length and MD5 the object was created from, and their recombination is the entity tag it reports.
Only entries stating a whole segment's own content MD5 qualify — a ranged entry records the containing segment's tag rather than the range's, a nested manifest records an aggregate, and an unrecognised entry states nothing.

A source whose class cannot be established, whose segments are themselves large objects, or which declares more segments than one comparison holds, yields no ladder.
No ladder means no publication for that name.

### The staged bytes are read

The complete assembled staged content is read once, exactly as ADR 0037 reads it, and digested along the ladder's boundaries.
Each run's byte count and MD5 must match in order.
The staged object's own boundaries never enter the comparison, so legitimate resegmentation passes.
The check applies to a plain staged object as well as a segmented one, which closes the large-source to plain-destination case.

Two plain objects are the one pair that is not read.
Their entity tags come from their own object HEADs, which state stored content rather than container metadata, and `verifyCopied` has already compared them.

**Listing evidence is not accepted in place of the read.**
An earlier draft of this decision let a staged segment listing that reproduced the ladder settle the question without reading anything.
The review of that draft built the counterexample: a same-size segment replacement can leave the container row at its previous length and entity tag, and the aggregate a dynamic large object reports is derived from those same rows, so both agree with the source while the stored bytes do not.
That would have been an initial-content regression against the guard ADR 0037 already provides, not merely the later-publication race this project has already deferred.
The fast path is therefore not part of this decision, and the case is a maintained regression.

### Failure is a refusal, not a repair

A comparison that fails is a staging `unverified` failure; a ladder that cannot be built is a staging `incomplete` failure, which is the classification an unavailable or malformed expectation already carries.
Both have unresolved effects, retain the staged manifest, its backing segments and every recovery fact, add no committing or committed fact, perform no live PUT and no new cleanup, and report the retained staged name as residue.
Nothing is deleted or rewritten to make the destination match.

### Nothing else changes

The published aggregate stamp, `provenEqual` and `skippableEqual` keep their current meanings and their current tests.
No overwrite, Mirror start, source deletion, automatic segment cleanup or credential reclamation is authorised.

## Cost

Per covered candidate, over what ADR 0037 already accepted for a plain source: one additional listing of the source's segment prefix, or one small manifest fetch for a static large object, and one complete read of the assembled staged object.

That read is not server-local.
`OpenObjectContent` streams the assembled bytes to the application through the Go client, so a covered copy moves the object's bytes over the network a second time — once into the destination by the engine, once back out to verify.
The cost is therefore proportional to the data copied, not to its metadata, and it applies to exactly the large objects where the bytes are.
This is the content-read cost DS-04 deferred to a separate reviewed decision, and this is that decision: for a route that publishes other people's data at a new name, an unverified publication is not an acceptable saving.

Rejected alternatives: comparing whole-object digests across both sides, which would re-read the source and double the transfer; requiring identical segment structure, which refuses correct copies; and accepting listing evidence in place of the read, which the review of this decision disproved.

## Consequences

A copy that publishes now carries evidence that its bytes matched the source expectation the executor built at commit, for every source class the staged route accepts.

Refusals become possible where copies previously succeeded: where the destination did not hold the source's bytes, and where a source states no usable expectation — a ranged or nested manifest entry, a segment without a complete MD5, a listing that does not recombine, or more segments than one comparison holds.
Each has its own named test.

## Limits

MD5 is Swift's integrity expectation for an honest provider, not authenticity against forged or colliding evidence.

The ladder is bound to an observation, not to current segment contents.
A dynamic large object's reported aggregate is itself derived from a container listing, so a source listing that has not caught up can agree with a source HEAD that has not caught up either; the recombination check establishes that the ladder agrees with the identity the HEAD reported, and nothing stronger.
Reading the staged bytes is what makes the comparison meaningful, and it compares them against that observation.

The source listing and HEAD are taken at commit, so a source that changes after both is outside this contract; source and publication substitutions remain open L05 work after the local routes land.
The staged read does not freeze manifest membership through publication, and the stale later listing limitation recorded in `segment_change_detection_test.go` is unchanged.
The local upload and download routes and the native copy route are governed by the same expectation in the delivery sequence, and are not delivered by this decision alone.
DS-04 and the valuable-data release hold stay open until they are.
