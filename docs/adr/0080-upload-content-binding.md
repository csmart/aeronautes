# ADR 0080: bind the upload routes to their source's content

**Status:** Accepted. Proposed on 25 September 2026 as the next step in the sequence [ADR 0045](0045-route-content-binding.md) left open, for the operator's decision of the same day that upload is the next release's capability; revised on 25 September 2026 after review; reviewed before its implementation, which followed as its own commit and is on master. This record is the content-binding half of the evidence the operator returned upload on, on 16 October 2026 ([ADR 0052](0052-v1-capability-gate.md) decision 9); the retained cases are `internal/transfer/local/upload_content_binding_test.go`.
Amended on 16 November 2026 by [ADR 0122](0122-the-stores-answer-is-the-result.md) decision 3: an upload has no staged object to bind and no live name to read back, so the staged comparison and the verifying read of this record are no longer made; the digest the application's own PUT declares stays, and the store's check of it is that route's outcome.
**Scope:** The two publishing paths in `internal/transfer/local/upload.go`: `nativeUpload`, which publishes at the live name in one request, and `commitStagedUpload`, which publishes a staged plain object or a staged large object's manifest. The native copy route stays in ADR 0045's sequence with Copy. Finish-line item L05, gates P16 and P21.

## Problem

Both upload paths verify a publication by reading the live object's detail and comparing its `ContentLength` with the local file's size (`verifyUpload`). Length is the only thing compared, so bytes that differ from the source while keeping its length are reported as copied and verified. That is the same hole [ADR 0042](0042-source-content-ladder.md) closed for the staged copy route and [ADR 0060](0060-native-download-content-binding.md) and [ADR 0070](0070-engine-download-content-binding.md) closed for the two download routes, still open on the two routes an upload release needs.

The staged commit adds a second gap of its own. It compares the staged object's length with the local file's length, then publishes a manifest naming the staging prefix. Nothing compares the staged content with the source, so a segment that is missing, duplicated, reordered or left by another run is published as the operator's object.

[DESIGN §65.11](../DESIGN.md) anticipated that a route publishing at the live name in one request could at best report an unresolved effect with retained residue, because a fingerprint dropped from the initial PUT cannot be added afterwards: the available metadata update is a POST that rewrites headers.

## Context the measurements supply

The [upload protocol measurements](../experiments/2026-09-25-upload-protocol-measurements.md) of 25 September 2026 change what the native route can promise.

- A PUT carrying a client-declared `ETag` that disagrees with the body is refused with 422 and no object exists at that name, including when the body is a same-length corruption of the declared content.
- A declared `ETag` and `If-None-Match: *` combine on one request, so declaring a digest does not cost the conditional create the route already relies on.
- An SLO manifest naming a segment whose entity tag does not match is refused with 400 and the manifest does not exist.

So the fingerprint does not have to be added after publication. It can be declared in the request, and the store refuses to publish anything else.

## Decision

1. **The native route declares the digest it intends.** `nativeUpload` computes the file's MD5, then sends that value as `ETag` on the PUT alongside the existing `If-None-Match: *`.
2. **The digest and the body come from one held descriptor.** The route is already handed an open file, and it keeps it: the digest is computed by reading that descriptor and seeking back, not by opening the pathname again. So a pathname replaced under a running job does not change what is sent and does not refuse it, because the descriptor still names the file the job opened. What is refused is the file whose content changes in place between the digest and the body, which the store rejects against the declared digest: an upload reports what it can prove, and a file moving under the read cannot be proved.
3. **The staged commit of a large object reads the staged content once and compares it against the source.** Before the manifest PUT, the route builds the local file's whole-stream expectation, one run of its length and MD5, reads the staged object's assembled bytes through the client, and folds them through that expectation. This is the comparison ADR 0042 built and the shape the staged copy route already performs, one staged content read per publication.
4. **Listing evidence is not accepted in place of that read.** ADR 0042 rejected exactly that fast path, because a same-size segment replacement can leave the container row at its previous length and entity tag while the stored bytes differ, and the aggregate a dynamic large object reports derives from those same rows. Its counterexample is retained as `TestStaleStagedListingCannotStandInForContent` in `internal/transfer/copy`. An earlier draft of this decision proposed the rejected path; this revision does not.
5. **A staged plain object is bound by its own entity tag.** ADR 0042 names plain objects as the one pair it does not read, because their entity tags come from their own object HEAD and state stored content rather than container metadata. The commit compares that entity tag with the local file's MD5 and declares the same value as `ETag` on the live PUT, so the publication is bound to the bytes that were compared without a second transfer.
6. **The later-publication boundary stays deferred and is stated here rather than promised away.** `X-Object-Manifest` names a prefix, not the segments that were read, so a segment replaced between the comparison and the manifest PUT is not caught by this decision and is not claimed to be. That is the publication race this project has already deferred; it is unchanged, and nothing in this record closes it. A static large object is a **candidate** for binding a large upload through publication, not a solution this record can offer: the probe only saw a manifest rejected because a segment already carried a different tag, and it exercised no change between that validation and the publication. The store validates the named segments when it stores the manifest and checks them again when the object is read, so a change between those two points can leave a published manifest that cannot be read whole. That is an inference from two separate operations rather than an outcome anything here measured, so SLO would need its own reviewed concurrency proof, and no change of manifest kind is proposed in this slice.
7. **Failure is a refusal, not a repair, in the classification the contract already has.** A staged comparison that fails is a staging `unverified` failure whose effect is unresolved, exactly as the copy route's `stage-content` refusal is: the engine's remote writes already happened and their residue is retained, so nothing here claims that no external mutation was invoked. A staged expectation that cannot be built is a staging `incomplete` failure. A native PUT refused with 422 is a commit `unverified` failure whose effect is unresolved, because a PUT was invoked and this contract records what was invoked rather than what one fixture's response implies. Distinguishing a confirmed refusal from an unobserved outcome would be a change to [ADR 0028](0028-transfer-item-records.md), and this record does not make it.
8. **Retained residue is governed by its own contract.** Staged bytes left behind by a refused publication are remote objects under the staging prefix, retained under [ADR 0020](0020-recovery-containment.md) and reclaimed by nothing this decision adds.

## Required proof before implementation is accepted

Tier 1 under [DESIGN §65.12](../DESIGN.md): before-and-after regressions through the public service on both fixtures, with retained mutants.

Before and after, on the mock and the owned Swift, with a proxy that alters bytes in flight while preserving their length:

- a native upload whose body is altered in flight: before, it publishes and verifies as copied; after, the store refuses it and the item records a commit `unverified` failure with an unresolved effect;
- a native upload whose held file changes in place, between the digest read and the body read, to different bytes of the same length: the store refuses it, which is the case that distinguishes a digest declared before the body from one taken after the fact;
- the control for that case: the file's **pathname** is replaced by a different file of the same length while the job holds the original open. The upload succeeds and publishes the bytes the job opened, because the descriptor is what it sends. An implementation that re-opened the pathname to take its digest would refuse this, and refusing it would be the defect, not the guard;
- a staged large upload whose staged content is altered to the same length after staging and before the commit, including the stale-row case ADR 0042 retains, where the segment's container row still reports its previous length and entity tag: before, the manifest publishes; after, the comparison refuses, the staged bytes are retained and the record names the staging boundary;
- a staged large upload with a segment missing, duplicated, reordered, or replaced by a run of another job, each refused for the same reason;
- a staged plain upload whose staged bytes are altered to the same length before the commit: before, it publishes; after, it refuses;
- controls: an unaltered native upload, an unaltered staged plain upload and an unaltered staged large upload publish, verify and record exactly as they do today, with unchanged counters, and the staged large control performs exactly one staged content read.

Retained mutants, each of which must fail a named case above, and each of which must build: a mutant that does not compile exercises nothing and is not evidence.

- The declared `ETag` is dropped from the native PUT, so an altered body publishes again.
- The staged comparison trusts the segment listing's lengths and entity tags instead of reading what was staged. This is the fast path ADR 0042 rejected, and it must fail the stale-row case specifically, because that is the case listing evidence cannot see.
- The staged comparison is reduced to comparing lengths, so same-size altered content publishes again.
- The staged expectation is taken from the staged object's own length and entity tag rather than from the file, so staged content that is self-consistent but is not the source publishes again.
- A staged refusal is recorded with an effect that claims nothing was attempted.

An order-insensitive variant is deliberately not required: decision 3 compares one whole-stream run, so run order cannot affect it, and a permutation of segments changes that stream's digest anyway. A mutant whose named case cannot distinguish it proves nothing and is worse than no mutant, because it reads as coverage.

## Consequences

The engine route holds no descriptor of its own: the engine opens the local file itself, so the staged comparison can only compare what was staged with what the pathname holds when the commit runs. A file replaced after staging is therefore compared as the new file and refused. That is conservative rather than correct, it refuses instead of publishing, and closing it would need a source identity the preflight does not capture today.

A staged large upload reads its staged object once over the network before publishing it. That is the cost ADR 0042 already accepted for the staged copy route, and it is what reading rather than trusting the listing means. The native route reads its local file twice from local disk. The staged plain commit adds no transfer.

L05 is not closed by this decision. The native copy route remains in ADR 0045's sequence, and Copy is a later release. Upload keeps its own unresolved boundaries besides: the preflight captures a path, a size and a route rather than a source identity, and the final verification still compares length, so a source substituted between planning and sending, or a publication substituted after it, is outside what this decision binds. The valuable-data release hold is unchanged.

## What this does not establish

It establishes nothing about overwriting an existing object; every publication here stays a conditional create, and [ADR 0081](0081-upload-overwrite-acknowledgement.md) is where that changes. It does not close the later-publication window for a large object, it makes no claim about a deployed cluster, a second storage policy or a manifest of more than a thousand segments, and it does not lift the capability gate: upload stays refused in a v1 build until its own decision says otherwise.
