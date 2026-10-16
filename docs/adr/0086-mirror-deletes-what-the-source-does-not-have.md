# ADR 0086 — a mirror deletes what the source does not have, and says what it cannot hold

**Status:** Proposed. Written for review before any implementation, as ADR 0085 was. Nothing here is built: `TransferService.StartMirror` is a stub that refuses, and this record does not change that. Enabling Mirror is the operator's decision and this is the statement they would be deciding on.
**Scope:** What a mirror does to a destination scope, and what it must refuse. Its three effects do not stand on the same ground, and this summary says which is which because it is the first thing an operator reads.
Its **creates** are a copy route's, and both routes are already bound and implemented: [ADR 0042](0042-source-content-ladder.md) for the staged route, [ADR 0083](0083-native-copy-content-binding.md) for the native one as amended by [ADR 0084](0084-held-manifest-copy.md).
Its **replacements** have no accepted path at all — Copy's overwrite is refused today — so this record **proposes new route authority** for them, which is the part that is not yet anyone's decision.
Its **deletions** inherit [ADR 0085](0085-reobserved-destructive-identity.md)'s prefix protocol unchanged, and this record adds exactly one question none of those paths asks.
It admits no other capability, lifts no [ADR 0020](0020-recovery-containment.md) refusal — including the one that keeps Mirror itself refused — and excludes the upload-only fragment reclamation of [ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 5.

## The requirement, in the operator's words

On 16 October 2026, asked whether a mirror that only ever adds would do:

> sync has to make exactly what's on the source be at the destination - if there are files in the dest that are not in the source, they should be deleted. if tehre are files in the source not in the dest, they should be copied. if there are newer or older files in the dest (files that don't match checksum), they shodul be overridden. if we don't do this, is't not a sync, it's a copy, right?

It is. An operation that copies what is missing and overrides what differs, but leaves whatever the
source no longer has, does not make the destination match the source — it makes the destination a
superset of it. Calling that a sync misnames it in the one direction that matters, because the
operator's reason for running it is to be able to trust the destination afterwards.

**This project had already ruled out the additive version, in the same words.** ADR 0020's own table
lists, as a REJECTED alternative beside the Mirror refusal, "run a partial copy and then silently
omit deletion, or delete a later replacement". So the operator's correction and ADR 0020 agree, and
the author's suggestion of an additive mirror disagreed with both.

## Why Mirror refuses today, and what that refusal is waiting for

`StartMirror` returns "Mirror is unavailable while concurrent changes cannot be protected" before any
transfer or deletion, deliberately NOT through the [ADR 0052](0052-v1-capability-gate.md) capability
gate, so that narrowing the gate's table can never enable it by accident. ADR 0020 states the
condition on the refusal precisely: it stands "while its **external-writer/source-absence** guarantee
is unsupported".

Those are two guarantees, and only one of them is the familiar one.

## The problem: a deletion justified by an absence

Every destructive path this project has already decided rests on a **presence**. Manifest deletion
compares what is at a name against what the operator was shown there. An upload overwrite compares
what is at a name against the identity its acknowledgement captured. ADR 0085's prefix deletion
compares what is at a name against the listing row that put it in the plan. In each case the thing
being destroyed is the thing being compared, and an identity can be taken of it.

A mirror deletion is different in kind. The reason for deleting `dest/X` is that **`source/X` does
not exist**, and an absence has no identity. There is nothing to take a digest of, nothing to
compare, and no header on this store that makes a read of nothing durable for even one round trip. An
absence observed when the source was listed and acted on when the destination is deleted is the one
thing this store offers no way to hold.

That is what ADR 0020 means by the source-absence guarantee, and it is why a mirror is not simply a
copy job with a delete pass bolted on.

## What the store actually permits, measured

| Precondition | On | Measured result |
| --- | --- | --- |
| `If-Match` with a wrong entity tag | object PUT | ignored, the write applied |
| `If-Match`, `If-None-Match`, `If-Unmodified-Since` | object POST | each ignored |
| `If-Match` | DELETE | ignored |
| `If-None-Match: *` | object PUT | honoured — create only |

The [conditional evaluation record](../experiments/2026-10-10-conditional-evaluation-point.md) adds
two findings that bound every claim below: the create conditional is evaluated **before the request
body completes**, and a 201 is **not** evidence that the sent bytes are at the name.

So there is no delete-if-unchanged and no delete-if-still-absent. Nothing in this record is atomic,
and no part of it prevents a concurrent writer. Each part narrows an interval and reports.

## Decision

1. **A mirror deletes what the destination holds and the source does not.** The alternative is a
   copy, which this project already has two of. The operator's requirement is the decision.

2. **A mirror is one-way and its direction is explicit.** Source scope and destination scope, named
   separately, with deletion applying to the destination alone. No two-way reconciliation is proposed
   here and none should be inferred: a two-way sync has to decide which side wins per name, and that
   is a different decision with a different acknowledgement.

3. **A name the destination lacks is a create on a copy route, and both copy routes are already
   bound.** [ADR 0042](0042-source-content-ladder.md) bound the staged route.
   [ADR 0083](0083-native-copy-content-binding.md) bound the native one and is **accepted and
   merged** — ADR 0084's status line says so in those words — so `nativeCopy` declares a plain
   source's own entity tag, which for a plain object is the MD5 of its content, and for a manifest
   source [ADR 0084](0084-held-manifest-copy.md) compares the held content against the source's own
   ladder first and declares the digest of those verified bytes. What remains on that route is the
   set of caveats ADR 0083 and ADR 0084 state for themselves, not an absence.
   A mirror's create side therefore composes, and it inherits those caveats rather than escaping
   them.

   **Two earlier drafts of this record were wrong here and the corrections are kept rather than
   tidied away.** The first reached for ADR 0080's declared digest, which is `nativeUpload` and
   sends local bytes; a mirror sends bytes already on a store. The second read ADR 0083's own
   Status line, which still says Proposed, and concluded the native route was unimplemented — then
   offered the operator a choice between waiting for it and staging every object. Both halves of
   that were false. The route is implemented, and staging everything was never available anyway:
   `plan.route` returns the native route **only** for names the pinned engine would silently drop,
   so for those names there is no staged alternative at all, which is the cost ADR 0084 decision 2
   was amended for getting wrong.

4. **A name whose content differs is a replacement, and this record PROPOSES that route rather than
   composing an existing one.** Copy's overwrite is refused today: ADR 0020's table refuses it, the
   remediation plan's DS-02 / DS-03 row asks for a reviewed enforceable protocol before P14 restores
   anything, and ADR 0085 says in its own voice that it does not supply one and must not be read as
   satisfying that row for Copy. ADR 0081's lifting of the transfer-overwrite refusal was **for the
   upload route alone**.
   So there is no unchanged replacement path for a mirror to reuse, and the proposal is explicit:
   a mirror's override of a changed destination name would need **new route authority**, resting on
   the acknowledgement shape of ADR 0081 decision 2 and the fixed-point comparison of ADR 0085, and
   on the operator's decision of 16 October 2026 that copy's overwrite may rest on the same accepted
   exposure as upload's. **That amendment to ADR 0081 is owed and unwritten.** Until it exists, this
   record proposes a mirror whose override side has no authority, and the refusal stands.
   **The upload-only fragment reclamation of ADR 0081 decision 5 is excluded, not inherited.**
   Removing a replaced large object's recorded segments on a copy or mirror route is not proposed
   here and remains refused under ADR 0020; if it is ever wanted it needs its own decision, because
   an answer of yes on this surface would be covering objects on a second account.

5. **A name the source lacks is a deletion** and takes ADR 0085's prefix-deletion protocol,
   including its baseline rule for a zero-byte row and its abandonment of `BulkDelete`. Nothing here
   weakens it.

6. **One question is new, and it gets one request: immediately before a deletion's final comparison,
   the source is asked for that name.** If the source now holds it, the name is **not deleted**. It
   is reported as a name that reappeared, and it is a create or a replacement candidate for a later
   pass rather than something this pass acts on, because the plan the operator authorised did not
   include writing it.
   The order is: source HEAD, then the destination's own final comparison HEAD, then the DELETE. The
   comparison stays closest to the effect, as ADR 0085 requires, and the absence is re-observed as
   late as one request allows.

7. **A mirror whose source scope lists nothing refuses, before any effect.** The arithmetic of a
   sync makes an empty source mean "delete everything at the destination", and that is also exactly
   what a mistyped prefix, a wrong container or a listing that failed in a way the client reported as
   empty produces. The destructive reading of an empty source is indistinguishable from the mistaken
   one, so it is refused rather than guessed at. An operator who genuinely wants a destination
   emptied has bulk delete for that, with its own confirmation.
   This is not a heuristic about how much is being deleted: a source that lists objects may
   legitimately require deleting every name at the destination, and that is allowed.

8. **One acknowledgement authorises the whole plan, raised by the backend on the native surface**, in
   ADR 0081 decision 2's shape. `MirrorRequestDTO` already carries a `Confirm` string today, and it
   must not be mistaken for that authorisation: like `acknowledgeOverwrite`, a field on the request
   is supplied by the page and guards against an operator's accident, not against a hostile webview
   that can set it itself. It states the counts of each of the three effects, and it names
   **both** accounts as the backend resolved them, because a mirror is the first operation in this
   project with two of them and [ADR 0073](0073-selected-resource-acknowledgement.md)'s
   counterexample therefore applies twice: a page that can display one account while another is
   connected can misrepresent either end.
   The answer is bound to that plan. A start whose source scope, destination scope, either account,
   either generation, or whose planned deletion set differs from the one confirmed is refused rather
   than carried by the earlier answer, as ADR 0081 decision 4 already requires of an overwrite.

9. **The deletion pass is entered only after the selected copy result is verified**, which
   [DESIGN §28.4](../DESIGN.md) already requires of a mirror in those terms: the selected transfer set
   is forced, and the result is verified **before any Mirror deletion**. This record cites that
   prerequisite rather than inventing one, and adds only the ordering consequence — writes first,
   deletions last.
   **An earlier draft justified that ordering with a claim that is simply false**, and it is
   corrected here rather than removed, because the ordering is right and the reason given for it was
   not. The claim was that an interrupted mirror leaves a superset of the source. It does not: for a
   source holding `a` and `b` and a destination holding `old`, an interruption after `a` is copied
   leaves `old` and `a`, which is not a superset of the SOURCE — it is one of the destination's
   former contents, which is a different and much weaker thing to be able to say. What the ordering actually buys is
   narrower and still worth having — **no name is deleted on the strength of a copy that did not
   verify**, so an interruption or a quota failure during the writes leaves an incomplete copy with
   the destination's own names still there, rather than a destination missing both what it had and
   what it was going to get.
   This is not a new all-writes gate: the prerequisite is the existing verification of the selected
   result, not a demand that every write in the job succeed.

10. **The preview states what it counted and that it is a reading.** Three counts, from two listings,
   each of which is stale the moment it is taken. The preview must not describe the deletion count as
   what will be deleted: it is what would be deleted if nothing changes, and decision 6 exists
   because things change.

## Consequences

**The deletion pass costs 3K + Z object requests**, for K deletion candidates of which Z are
zero-byte or SLO-listed rows. ADR 0085 accounts for prefix deletion at 2N + Z and this adds one
source HEAD per candidate. **ADR 0085's exclusions carry over and an earlier draft of this record
dropped them**: that figure is before listing, planning, verification or retry overhead, and it is
the deletion pass alone. Ten thousand names to delete need thirty thousand requests where a bulk
delete needed ten.

**`BulkDelete` is unavailable here for the reason ADR 0085 gives:** a header applies to a whole
request, so a batched name list can carry no per-name precondition, and the first name in a thousand
would get the widest window of all.

**The copy and replacement passes cost what those routes cost, and this record does not total them
or compare them per name with the deletion pass** — one route's figure establishes neither. A
healthy staged plain commit alone takes a source HEAD, a
stage HEAD, a destination HEAD, a staged GET, the live PUT and a live HEAD — six object requests in
`commitStaged` before the engine's own transfer and the stage cleanup. An earlier draft of this record claimed a
mirror takes between two and four requests for every name it changes, which is false by a factor on
that route alone. No total is claimed for a mirror, and none should be, because it would be a figure
nobody measured: what is stated is the deletion pass's own arithmetic, which is this record's to
account for.

**A mirror is not a cheap operation and should not be described as one.** It reads both sides
entirely before it changes anything.

**Acknowledgements taken before a plan changes authorise nothing**, which means a mirror of a busy
namespace may be refused repeatedly at the start rather than part way through. That is the intended
direction.

## Limits

**The absence cannot be held, and that is this protocol's own exposure.** A name created at the
source after its source HEAD and before the destination's DELETE is deleted at the destination
anyway, and nothing here detects it. The window is one request plus the destination's own comparison
and delete, which is the narrowest this store allows and is not zero. An operator mirroring from a
source that is being written to will lose those writes at the destination, and the mirror will report
having deleted names it believed absent.

**Everything ADR 0085 accepts is accepted here too**, per name: a replacement landing after a
deletion's final comparison HEAD is still deleted; a zero-byte row's baseline is a HEAD this job
takes, so a replacement that landed before it is adopted as the baseline and then deleted; and a
replacement's own window is the one ADR 0081 and ADR 0085 already describe.

**No part of this is atomic.** There is no delete-if-unchanged, no delete-if-absent and no
replace-if-unchanged on this store. Every claim above is about one observation and names which.

**A store that misreports a HEAD or a listing defeats it**, as it defeats every check in this
project. An empty-looking listing in particular is why decision 7 refuses rather than acts.

**A mirror's override side has no authority today**, and that is the sequencing fact in front of the
operator rather than the copy binding an earlier draft named. Copy's overwrite is refused, the
amendment that would rest it on the exposure the operator accepted for upload is owed and unwritten,
and until it exists a mirror could create and delete but not override. That is the one of the three
effects a sync cannot do without, so this record is not implementable in order as it stands.

**ADR 0083's own Status line read Proposed although the contract was accepted and merged**, and it
misled the author of this record into telling the operator the native copy route did not exist. ADR
0084 is the authority that it does. That status line, and the finish line's L05 wording that had the
same error from the day the binding landed, were corrected in the slice that followed this one; this
note is kept because an operator reading this record is entitled to know it was drafted on a false
premise, and it is now past tense rather than an owed item.

**This record does not establish that Mirror should be enabled.** It states what a mirror would do
and what it would not be able to promise, so that decision can be made on something specific. ADR
0020's refusal stands until the operator lifts it, and the external-writer half of its condition is
not satisfied by this protocol — it is accepted, in the same way ADR 0081's exposure was accepted,
and the operator has not been asked for that acceptance yet.

**Nothing here grants authority over anything a deleted name refers to.** ADR 0020's reclamation
rules are untouched: a verified mirror deletion is not an `ExclusiveOwnershipProof` for a segment, and
a large object's parts are not removed by it.
