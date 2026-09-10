# Change detection for segmented destinations

## What this slice claims, and what it does not

This is a bounded prerequisite within the DS-04 copy verification finding in [the data safety audit](../audits/2026-09-05/data-safety.md) and its [remediation row](../audits/2026-09-05/remediation-plan.md).
It does not settle DS-04 and it does not establish equality.

The distinction is the whole point of the slice.
Comparing a destination against an identity recorded when this executor committed it detects change since that observation.
It cannot supply the observation that the committed bytes were ever correct, because the staged commit path still accepts a large object on logical size alone.
If wrong bytes were committed, an unchanged wrong destination matches both the source fingerprint and its own recorded identity, and would still be reported the same.
So the rule proposed here may only ever refuse more than today; it grants no destination any new equality authority, and the staged-content work remains a prerequisite for any claim that a skip means equal to the source.
A stale segment row can also conceal a substitution, so even the detection claim is bounded: a skip says that no aggregate difference was observed, not that nothing changed.

The applicable contract is the executor-grade equality test in DESIGN §28.4 and the advisory-listing rule K-CON-001.

## Before evidence

The archived reproduction `TestAuditSkipRejectsChangedDestinationSegments` still fails on master at 08b0d125ace4944c702720f4c08336253127a36c, and was run for this plan on unchanged production sources.
It copies an eight-byte object, replaces the first segment of the committed destination with four different bytes, and runs an unknown-verdict item over the same name.
The report reads SkippedEqual 1.

`provenEqual` in `internal/transfer/copy/executor.go` decides a non-plain pair from the destination carrying this executor's fingerprint metadata for the source's length and entity tag, together with equal live logical length.
The destination's own aggregate identity is never consulted, so a substitution that preserves total length leaves both facts true.

## Observed behaviour of the deployed fixture

These observations were taken against the containerized Swift deployment used by the integration lane, for this plan.
They are observations of that deployment at that moment, not a guarantee of the protocol.

A dynamic large object read reported an aggregate entity tag equal to the MD5 of the concatenated entity tag strings of its segments.
With segments holding `GOOD` and `DATA` it reported `79153ca8aad1663e8c6e18831ea0f4bd` at content length 8; after one four-byte segment was replaced with four different bytes it reported `80996eef7443fd3011ef305d10573d08` at the same length, and the assembled body changed accordingly.
Both values were recomputed independently from the segment entity tags.

A static large object behaved differently under the same substitution.
Its entity tag stayed at the value recorded when the manifest was created, and the deployment refused the assembled read with 409 Conflict instead.

One limit of that evidence is decisive for the design.
The upstream dynamic large object middleware builds this aggregate from a container listing of the segment rows rather than by reading the segment bodies, so the aggregate inherits that listing's consistency and a stale segment row can leave the aggregate unchanged after a substitution.
An immediate update on one deployment does not establish otherwise.
The aggregate is therefore treated here as evidence of change, never as proof of equality, and no part of this slice depends on it being authoritative.

## Proposed correction

1. Carry the stamp in the existing conditional publishing PUT, beside the fingerprint that write already sends, so no new mutation of a live object is introduced.
   Its value is the aggregate identity observed on the staged manifest before publication, and the stamp says exactly that: it identifies an earlier staged observation, not the live object's identity, and it asserts nothing about segment membership being frozen by publication or about the content being verified.
   A value observed after the publishing PUT cannot be carried by that write, and persisting one would require a further live mutation with its own authority, concurrency, metadata-preservation and response-loss protocol; this slice deliberately does not open that.
2. In `provenEqual`, a destination may be reported the same only when it is a dynamic large object, the fingerprint matches, the live logical lengths match, and the live aggregate identity equals the stamp.
   Every other non-plain destination, including any static large object and anything carrying no stamp, takes the existing unknown path, so the overwrite is refused rather than skipped.
3. The plain pair path is unchanged, no existing refusal is removed, and no new refusal policy for names is introduced.

The correction is deliberately one-directional, and that property is what the slice must prove.
The permitted-skip set is a strict subset of today's: a destination may be skipped only if it would be skipped today and it is additionally a dynamic large object whose live aggregate matches the recorded staged observation.
Nothing that is refused today becomes skipped, and no new write is introduced, so no current outcome becomes less safe.
A legitimately resegmented copy is unaffected, because the comparison is against the observation this executor recorded for that same destination rather than against the source's segment structure.

## Fixture correction

The mock serves a manifest object's own stored entity tag, which represents neither observed behaviour, so it is corrected rather than designed around.
A dynamic large object read in the mock derives its aggregate entity tag from the same segment rows the mock lists, which is the shape of the upstream computation.
Neither the mock nor the observations above model a lagging segment listing; that dimension is named here as unmodelled, and a fault-injection switch for it belongs with the staged-content work rather than this slice.
The static large object behaviour, both the frozen entity tag and the refusal of a mismatched assembled read, remains unmodelled, which is exactly why this slice refuses static large object destinations instead of reasoning about them.

The correction was measured before proposing it: with it applied, the swifttest, copy, plan, app, evidence, deletion, systemtest, swiftcontrol and integration packages all pass unchanged, so no existing test observes different behaviour.

## Required proof

The archived probe is kept as before evidence, and the maintained regression asserts the precise refusal outcome for the changed destination, that no overwrite occurred, the exact recorded item and audit facts, and complete unchanged collateral in both namespaces.
It must distinguish an authoritative answer from a transport or permission failure.

What this slice fixes, and must prove it fixes:

- a dynamic large object destination carrying a stamp, whose segments were substituted after publication so that its live aggregate differs, is refused instead of skipped, with the exact item and audit outcome, no overwrite and unchanged collateral;
- a static large object destination is refused rather than skipped, whether or not its segments were substituted, because the evidence for it is insufficient;
- a destination carrying no stamp is refused rather than skipped.

What this slice does not fix, and must retain as explicitly unresolved DS-04 reproductions recording their actual outcomes rather than a required refusal:

- a wrong-at-stamp-time destination, whose committed bytes never matched the source, still matches the fingerprint and its own stamp and is still reported the same; this is the dependency on staged verification, and the reproduction records that outcome as unresolved;
- a stale segment row that leaves the aggregate unchanged after a substitution still permits the skip, because the aggregate is derived from that listing; the reproduction records that outcome as unresolved.

Neither unresolved case may be turned green by weakening an assertion or by restating the same outcome under a new description.

Controls that must keep their current outcomes, each with its stamp and class precondition stated: an unchanged stamped dynamic large object destination, a legitimately resegmented stamped destination including the four and four to three, three and two case, and a plain pair.

Deliberately broken variants must each fail their own boundary and no other: one reverting the decision to fingerprint and length, one stamping the identity but never checking it, and one admitting static large object destinations to the stamped path.

The full race suite runs with inventory reconciliation against the delivered name count, alongside vet, gofmt, the Windows and macOS vets and internal builds, and module tidiness.

## The staged half, and its dependency

The other half of DS-04 is that a staged large object is accepted on logical size alone, so a same-size substitution between the engine's exit and the commit is copied and reported as copied.
That half is a prerequisite for this one: until it lands, a skip says only that no aggregate difference was observed against the staged observation this executor recorded, and never that the destination equals the source.

Comparing source and destination segment structure is not available as a remedy, because a copy may legitimately resegment and requiring identical inventories would refuse a correct copy.
The only evidence that survives resegmentation is logical content.
For a plain source that means one full read of the staged object, digested against the source entity tag; for a segmented source the source entity tag is itself derived from its segments, so the comparison requires reading both sides.
That is a cost and policy decision per source and destination class, and it belongs in its own reviewed sequence with its own measurements.

Nothing here re-enables destructive follow-up: Mirror starts are refused today under ADR 0020, and stronger change detection grants no new authority.
