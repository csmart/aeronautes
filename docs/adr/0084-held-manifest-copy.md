# ADR 0084: hold a small manifest source, and refuse only what cannot be held

**Status:** Proposed as an amendment to [ADR 0083](0083-native-copy-content-binding.md) decision 2, which was wrong. The contract it amends is accepted and merged; this records what that decision cost and corrects it rather than rewriting it.
**Scope:** `nativeCopy` in `internal/transfer/copy/executor.go`, for a manifest source. Finish-line item L05, gates P16 and P21.

## What ADR 0083 decision 2 got wrong

That decision refuses every manifest source on the native copy route, on the reasoning that the comparison cannot be built: a manifest's entity tag is an aggregate over its segments' tags, while the plain destination this route creates has the MD5 of the assembled bytes, so there is nothing to declare. The reasoning holds for a source whose bytes cannot be held. It does not hold for a small one, and the cost argument was wrong in a way a maintained test caught immediately.

`TestNativeDLOMaterializesSmallLogical` asserts that a dynamic large object of seventeen logical bytes, named `weird//dlo`, is materialised by this route as a plain object. Its comment states the intent: the native path serves the assembled stream and lands a plain object, as logical replication with re-segmentation to one inline segment. Implementing decision 2 failed that test with the refusal's own reason.

The deeper error is in the cost. ADR 0083 argued that the affected set is the intersection of two narrow ones — an engine-hostile name and a manifest source — which is true. What it did not say is that for that intersection there is **no other route**. `plan.route` picks the native route only for names the pinned engine would silently drop, so refusing a manifest source there does not defer the work to the engine: it makes those objects uncopyable. A narrowing that removes a tested capability with no alternative path is not the same as one that declines an unproven path, and the contract should have weighed it as the former.

## Decision

### 1. A manifest source at or below a bound is held, COMPARED AGAINST ITS OWN LADDER, and only then declared

The first draft of this decision hashed whatever the GET delivered and declared that hash. That is not validation of the source's content, and review was right to call it a P1: a path that serves same-length bytes other than the ones the manifest describes is hashed, declared, and accepted by an enforcing store, because the body genuinely does hash to what was declared. A declaration binds the transport — what arrived is what was sent — and nothing more. For a plain source decision 1 declares the source's OWN header tag, which is an independent expectation; a manifest has no such header, and the first draft dropped the independence without noticing the difference.

So the held bytes are compared against an expectation built independently of the stream that delivered them:

1. The source's content ladder is built by the machinery [ADR 0042](0042-source-content-ladder.md) already uses for the staged route, and the two manifest classes draw their expectation from different places, which this decision does not blur. A dynamic large object's runs come from a listing of the segments under the prefix its manifest names, in listing order. A static large object's come from its OWN recorded manifest, through the bounded reader, because an SLO reports the entity tag its manifest recorded rather than one derived from whatever segments are there now, so a listing cannot be bound to it — and that manifest also carries recorded order and inline-data entries a listing has no way to supply. Its admission rules are ADR 0042's unchanged: only an entry stating a whole segment's own content MD5 qualifies, a ranged entry records the containing segment's tag rather than the range's, a nested manifest records an aggregate rather than content, and each of those refuses the covered path instead of being guessed at. Either way the expectation comes from the manifest's own account of itself and not from the assembled body.
2. An unusable expectation refuses: a manifest whose segment list cannot be read, or whose runs cannot be assembled into a ladder, is not attempted. Length alone is never the comparison, which is the rule ADR 0042 already states.
3. The assembled stream is held under an enforced cap, and the hold must COMPLETE before anything is published: the read reaches EOF, the held length equals the ladder's total, and the body closes without error. `OpenObject` performs its own length check on close, so a close deferred past the publication would be a check that cannot refuse anything; it is required before the PUT instead. A truncated read, an overflow past the cap, or a failing close publishes nothing.
4. The held bytes are compared with the ladder. A mismatch is not attempted, with the source content named; nothing is published, so there is no residue.
5. Only then is the MD5 of those verified bytes declared on the PUT, so the transport is bound as well as the content.

What this does not catch is a path that lies consistently in both places — serving a segment listing and an assembled body that agree with each other and not with what the operator's source held. That is the same limit ADR 0042 carries for the staged route, and it is stated here rather than implied.

The destination is still a plain object. ADR 0083 says this decision makes no claim about shape, and that is unchanged: the recorded oracle permits re-segmentation, and §25's logical replication is what the existing test asserts.

### 2. Above the bound, the source is not attempted

In ADR 0083 decision 2's shape — a source-read failure, code unsupported, effect not attempted — with the reason naming both the logical length and the bound, so the operator is told why this object and not others. Nothing is written, so there is no residue.

### 3. The bound is a 16 MiB HELD-PAYLOAD ceiling, which is not the same as 16 MiB of memory

Sixteen mebibytes is the value this application already names for a per-transfer buffer, `DefaultBufferSize`, chosen there so that transfers times buffer is the child memory the record shows (ADR 0074 decision 3). Reusing it keeps one number in the operator's head rather than two.

What it bounds is the held payload, and the claim is kept to that. The executor runs its items one at a time and has no goroutine of its own, so there is one live held payload per executor rather than one per item — but that is not a statement that this adds exactly sixteen mebibytes of heap or resident set. Allocation growth while reading, the transport's own buffers and the ladder's metadata each cost something separately, and none of them is measured by this decision. A stronger memory claim would have to be substantiated by measurement in the implementation, not asserted here.

It is a fixed constant in this decision, not a setting. If an operator needs a different ceiling, ADR 0074's pattern — an operator setting with the cost recorded — is how to raise it, and that is a separate decision.

## Consequences

- The tested materialisation of a small manifest source keeps working, and is now verified against the source's own ladder rather than merely sized — a stronger guarantee than the route had before this sequence began, where a manifest-to-plain copy was checked on length alone.
- A manifest source larger than the bound becomes uncopyable on this route, where today it is copied unchecked. That is the narrowing this amendment still accepts, and it is the honest one: those bytes cannot be held, so they cannot be bound before publication, and the alternative is publishing bytes nobody compared.
- Copy is deferred and refused BY DEFAULT under [ADR 0052](0052-v1-capability-gate.md), which is not the same as unreachable: [ADR 0082](0082-operator-enabled-unproven-capabilities.md) lets an operator set `AERONAUTES_ENABLE=copy` in the same shipped artifact, and the capability is then offered as unproven. So an operator who opts in reaches this narrowing, and the honest statement of the cost is that it applies to them rather than to nobody. An earlier draft said nothing shipped changes either way, which was wrong, and the correction was already asked for once before this round.

## What this does not claim

- It does not close L05: a manifest source above the bound is refused rather than verified.
- It makes no claim about replication shape, and none about a store or a path that drops the declared tag — ADR 0083's caveats carry over unchanged.
- It establishes nothing about memory behaviour under concurrency on this route beyond the sequential execution the current executor performs; a future concurrent native route would have to re-derive the peak.
