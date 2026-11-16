# ADR 0083: bind the native copy route to its source's content

**Status:** Accepted and implemented on 6 October 2026, in commit `10a98f6a`, with decision 2 amended by [ADR 0084](0084-held-manifest-copy.md) — which records that decision as wrong and says in its own status line that the contract it amends is accepted and merged. Reviewed before implementation, which followed as its own commit, as [ADR 0080](0080-upload-content-binding.md) was.
This line read "Proposed" until 16 October 2026, ten days after the implementation landed, and that staleness was not harmless: it was read as establishing that the route did not exist, twice — once into the finish line's L05 row and once into a draft of [ADR 0086](0086-mirror-deletes-what-the-source-does-not-have.md), which told the operator a mirror would have to stage every object or wait for a binding that was already there.
This is the last member of the sequence [ADR 0045](0045-route-content-binding.md) opened: that decision delivered the local download route and named the native copy route, the native upload route and the staged upload commit as remaining; ADR 0080 took the two upload paths and handed this one back in terms.
**Scope:** `nativeCopy` in `internal/transfer/copy/executor.go`. Finish-line item L05, gates P16 and P21. Nothing about the staged copy route changes; [ADR 0042](0042-source-content-ladder.md) bound that one.
Amended on 16 November 2026 by [ADR 0122](0122-the-stores-answer-is-the-result.md) decision 4: the application's own copy still declares a plain source's entity tag on its one PUT, and the store's check of it is that route's outcome; the read after the PUT is no longer made.

## Problem

`nativeCopy` streams a source object through the client into a destination PUT, stamps its own fingerprint as user metadata, asks for a conditional create, and then reads the destination's detail and calls `verifyCopied`. That comparison checks logical length always, and entity tags **only when the source and the destination are both plain**.

Measured on current master, head `4c4c7a55`, by rerunning the probe
`docs/experiments/2026-09-14-route-content-binding.md` retains — the same bytes,
digest `aea3910c2011ff61bd6ae9f160216963e235b1fc8ffd681dd60893d38ca31b61`, checked
against that record before the run and removed from the tree afterwards: a
segmented source copied through a proxy that inverts every byte while preserving
length lands as a destination holding different bytes of the same 8192, and the
job counts it 1 copied. Its identity control, the same proxy passing bytes
through, copies correctly.

Why it passes is not an oversight in the comparison, it is the comparison being impossible as written. A manifest source streamed through the client arrives at the destination as a plain object, whose entity tag is the MD5 of the assembled bytes; the source's entity tag is an aggregate over its segments' tags. The two are not the same function of the same bytes, so the code compares what it can, which is length.

That the destination stops being a manifest is a shape change, and this decision makes no claim about it either way: the recorded oracle permits a destination to be re-segmented, so a flattened copy is not by itself a fidelity defect. What this decision is about is narrower — bytes nobody compared should not be published, whatever shape they land in.

## Context the measurements supply

ADR 0080's [upload protocol measurements](../experiments/2026-09-25-upload-protocol-measurements.md) established that a PUT carrying a client-declared `ETag` that disagrees with its body is refused with 422 and no object exists at that name, and that a declared `ETag` and `If-None-Match: *` combine on one request. Both were re-measured against this workstation's cluster while preparing this decision: a PUT declaring a wrong entity tag answered 422 and the name answered 404 afterwards, while the same body with the right declaration answered 201.

So a route that knows its source's MD5 before it sends the body does not need to catch a difference after publishing. It can make the store refuse.

## Decision

### 1. A plain source's copy declares the source's entity tag on the PUT

The source's detail already carries it, and for a plain object Swift's entity tag is the MD5 of the content. `nativeCopy` declares it on the destination PUT beside the conditional create it already sends. A body that does not hash to it is refused by the store, so a same-size difference in flight publishes **nothing** instead of being reported afterwards.

`verifyCopied` keeps its comparisons unchanged. They become the second check rather than the only one, exactly as ADR 0070 kept the download size check as the first of several.

### 2. A manifest source is not attempted by this route rather than flattened and published unchecked

While the comparison above cannot be built for a manifest source, this route stops the item before any request that would publish, and it does so in the shape the same function already uses two lines earlier for a source past the single-PUT limit: a failure at the source-read stage, diagnostic code unsupported, **effect not attempted**, with the reason naming the source's class. Nothing is written, so there is no residue to retain.

It deliberately does NOT use the refusal result. `ResultRefused` is not a general-purpose refusal in this route: the recorder maps every one of them into the overwrite-refusal count and its `overwrite-refused` reason, the service then tells the operator that an existing destination could not be overwritten, and the refusal log that verification opens is sized from that same count and rejects a missing entry. A manifest source has nothing to do with an existing destination, so borrowing that result would make the operator's account of the job false and leave the verification bookkeeping inconsistent. Reusing the unsupported-source shape needs no new accounting and says what actually happened.

This is a narrowing of what the route attempts today, and it is deliberate. What it removes is the publication of bytes nobody compared, under a name whose shape also changed. Three facts make the cost acceptable:

- Copy is deferred and refused in a v1 build under [ADR 0052](0052-v1-capability-gate.md), so no shipped capability loses anything.
- The native route exists only for names the pinned engine would silently drop ([`plan.route`](../../internal/transfer/plan/plan.go)), so this is the intersection of two narrow sets: an engine-hostile name AND a manifest source.
- The finish-line record states that several of these areas are expected to complete as accepted refusals rather than implementations, and that such a completion is not a shortfall when the refusal is enforced and recorded.

### 3. One candidate shape for a manifest source is named, and it is not decided here

A native copy that preserved a manifest could copy each segment as its own plain object, each with its own declarable entity tag, and then write a destination manifest naming the copies; every one of those writes would be bound by decision 1's mechanism, and it would create segments under a generated prefix, which brings P15's ownership and cleanup rules with it. That is a candidate requiring its own decision and its own ownership proof, not a consequence of this one.

It is NOT what P17 requires, and an earlier draft of this record said otherwise. The recorded oracle permits the destination to be re-segmented, which `DESIGN.md` and the planner both state, and ADR 0042 binds assembled staged content precisely in the case where a manifest source becomes a plain stage. P17's recorded scope is metadata, content headers, expiry and link target semantics. So the flattening this route performs is not an established fidelity defect; what this decision objects to is publishing bytes nobody compared, whatever shape they land in.

## What this does not claim

- It does not close L05. Decision 2 is a refusal, not a verification, for manifest sources.
- It does not address replication fidelity: a plain source still copies to a plain destination, and a manifest source now copies nowhere.
- It does not change the staged copy route, the engine route, either download route or either upload path.
- It establishes nothing about a store that ignores a declared entity tag, or about a path that removes it. What a stripped declaration costs this route is the PRE-PUBLICATION protection, not the detection: for a plain source `verifyCopied` still compares the destination's entity tag with the source's, so with honest tags a same-length difference is still found and reported unverified — after the bytes exist at the live name. That is a different loss from the two upload paths, whose own later comparison is length-only, and the contract claims only the narrower thing: decision 1 moves the catch from after publication to instead of publication, and a path that removes the declaration moves it back.
- The rerun's other cases are described in its own record and are not evidence about this route. Its two upload cases report unchecked publications because the fixture's PUT branch deletes the declared entity tag that ADR 0080's protection is; its download case failed at a fixture precondition because nothing was placed, the native download route having refused the corrupted bytes as ADR 0060 and ADR 0070 require.

## Evidence

The rerun, its retained probe, both source bookends and the cluster measurements are in `.aeronautes-work/20261004-native-copy-binding/`, with `FINDING-probe-rerun.md` stating what the rerun does and does not settle.
