# ADR 0087 — copy may replace an existing destination on the terms upload already has

**Status:** Accepted on 29 October 2026, reviewed before implementation, as the P14 deliverable the DS-02/DS-03 row names for copy's overwrite: the supported server primitives, the exclusive-writer assumption (none is made), and the precise refusal and conflict behaviour. It amends [ADR 0081](0081-upload-overwrite-acknowledgement.md)'s **route scope** — which lifted [ADR 0020](0020-recovery-containment.md)'s transfer-overwrite refusal for the upload route alone — to include the copy route on the same acknowledged, bounded, compared terms, in the shape [ADR 0084](0084-held-manifest-copy.md) took as an amendment to ADR 0083. The protocol is accepted here and its 0.3.0 implementation is complete: the copy route replaces an acknowledged destination on these terms (`internal/transfer/copy/overwrite.go`), and copy was returned to the release on 31 October 2026 by [ADR 0052](0052-v1-capability-gate.md) decision 12. The per-shape question of whether an existing destination read moves to the fixed point or a read is added there (decisions 2 and 3) is left to the implementation that resolves it against the executor.
**Scope:** The copy executor's two existing-destination verdicts — `VerdictUnknown`, which refuses after reading both sides, and `VerdictChanged`, which refuses without reading anything. It does not propose anything about Mirror, whose own record ([ADR 0086](0086-mirror-deletes-what-the-source-does-not-have.md)) depends on this one; it does not touch the source-content bindings of [ADR 0042](0042-source-content-ladder.md), ADR 0083 or ADR 0084; and it **excludes** segment reclamation.

## The operator's decision

Asked on 16 October 2026 how a copy's overwrite should be protected, given that no conditional write
exists on this store, the operator chose the **same accepted exposure as upload** — the exposure they
had accepted in their own words for the upload route three weeks earlier, and which ADR 0085 then
narrowed with an identity comparison.

That is a decision about *exposure*, and it is not by itself a protocol. This record is the protocol
it implies, and the part of it the operator has not yet been asked for is marked.

## What the remediation plan asks for before anything is restored

The DS-02 / DS-03 row contains copy's overwrite today, and states the condition on restoring it:
P14 "may restore only operations with a reviewed enforceable protocol after P11/P12". The
external-writer row names the deliverable precisely: "An ADR naming supported server/workflow
primitives, exclusive-writer assumptions if any, and precise refusal/conflict behaviour".

So three things are owed, and this record supplies them:

**The supported primitives.** Measured on this store: `If-Match` is ignored on PUT, on POST and on
DELETE; `If-None-Match: *` is honoured and is create-only. The
[conditional evaluation record](../experiments/2026-10-10-conditional-evaluation-point.md) adds that
the create conditional is evaluated **before the request body completes**, and that a 201 is **not**
evidence the sent bytes are at the name. There is no replace-if-unchanged primitive to build on.

**The exclusive-writer assumption: none is made.** This protocol assumes no exclusivity, claims none,
and would be unsound if it did. What it does instead is compare, refuse on a difference, and report.

**The refusal and conflict behaviour** is decided below, per path.

## What the copy route already does, which is more than the refusal suggests

The executor's verdict dispatch already separates the cases, and the two existing-destination paths
are **not** in the same position:

| Verdict | Today | Requests it already makes |
| --- | --- | --- |
| `VerdictSame` | recorded as same | none |
| `VerdictNew` | create | the publication's own |
| `VerdictUnknown` | `runUnknown`: HEAD source, HEAD destination; skip if proven equal; create if the destination has gone; **otherwise refuse** | **both HEADs** |
| `VerdictChanged` | `refuseOverwrite`: refused outright | **none** |
| `VerdictDestOnly` | ignored | none |

The refusal's own machinery exists too, and is not a stub: `ResultRefused` increments
`OverwritesRefused`, the item outcome is `overwrite-refused`, a private refusal record is appended,
and `internal/app` already reports "N overwrites refused" with the transfer dialog surfacing it.

**That asymmetry matters for the baseline, and two earlier drafts of this record got it wrong in
opposite directions.** The first was about to call the baseline free on both paths. The second called
it free on `VerdictUnknown` without noticing **when** that read happens — which is the finding that
matters more, and which decision 2 settles.

- On `VerdictUnknown` the destination's HEAD-grade identity **is already read and then discarded**, so
  there is a read to retain rather than add — the same observation ADR 0085 made when it found
  `FragmentPlan` already HEADs every name an upload would replace. But that read happens **in the
  launched worker**, after the job is created, audited and started, so it is on the wrong side of the
  operator's answer and cannot be the baseline where it currently sits.
- On `VerdictChanged` nothing is read at all, so a baseline there is a **new request per name**, and
  the planner's verdict is not a substitute for it: ADR 0042's counterexample is that a same-size
  segment replacement can leave a container row at its previous length and entity tag, and the
  planner's Changed verdict is derived from listing rows.
- Neither of these is the **final comparison**, which is a separate request from the baseline on
  every shape and which the Consequences section accounts for on its own.

## Decision

1. **ADR 0020's transfer-overwrite refusal is lifted for the copy route**, on the same terms it was
   lifted for upload and no others: an acknowledged, bounded, compared replacement. This is the
   amendment to ADR 0081's scope that the operator's decision calls for, and like that record it
   lifts nothing else.

2. **The baseline is a HEAD-grade identity of the destination, per name, captured BEFORE the
   operator answers.** ADR 0085 decided that for upload in those terms: the identity belongs to the
   acknowledgement, not to execution. Neither copy path may use a listing row as the baseline, and
   **no later observation may be adopted as one**.

   **An earlier draft of this record got that wrong in a way worth stating, because the mistake is
   invisible if you only count requests.** It said the baseline is the HEAD `runUnknown` already
   takes — which is free, and which happens in the launched worker, after `StartCopy` has created
   the job, written its start audit and returned. So the operator's answer would come first and the
   "baseline" second, and the race that opens is the one this whole protocol exists to close: the
   question is asked about N names while `A` is live, `A` is replaced by `B`, the worker then HEADs
   the name and adopts `B`, the comparison passes `B` against `B`, and `B` — which the operator was
   never shown — is overwritten. A free read at the wrong time is not a baseline.
   So the baseline is taken in the plan preparation that composes the question. Whether
   `runUnknown`'s existing read MOVES into that preparation or a read is added there is an
   implementation question this record deliberately leaves open; what it fixes is that the identity
   the comparison rests on is the one the acknowledgement carried.

3. **The final comparison is ADR 0085's, at the fixed point — and the copy route has FOUR
   publication shapes, not the upload route's two.** ADR 0085 excludes preparation from the accepted
   interval, so the comparison goes after everything a shape prepares and immediately before the
   effect. An earlier draft of this record reused the upload wording and so described only two
   shapes and omitted the native route entirely. It then said three while listing four, which is the
   fifth count in this night's work that I typed instead of taking from the thing being counted; the
   packet's checker now compares the stated number against the bullets rather than trusting either.

   - **Native** (`nativeCopy`): the source is opened, a manifest source's bounded ladder and held
     content are read, closed and digested, and only then is the destination compared — immediately
     before the PUT. The destination read that exists on this path today sits **after** the PUT and
     is verification; it is not and cannot be the comparison.
   - **Staged plain or static materialisation**: as upload does it — after the staged reader is
     open, immediately before the live PUT, closing that reader on a refusal.
   - **Staged dynamic manifest**: after the assembled-content check and before the committing fact,
     as ADR 0042's ordering requires — **and the preparation that currently follows that fact has to
     move ahead of the comparison.** `metaBudgetFor` can call `FetchCapabilities`, which is a
     request, so leaving it where it is would put a round trip inside the interval this protocol
     claims to have narrowed.
   - **Symlink**: this shape already reads the destination before its PUT, so its comparison can sit
     at that read's position provided nothing else prepares after it. An implementation must say
     whether that holds rather than assume it.

   A difference refuses the name and never prompts.

4. **The acknowledgement is ADR 0081 decision 2's**, raised by the backend on a surface the page
   cannot draw, with the request's field an accident guard rather than the authorisation. It names
   **both accounts** as the backend resolved them, because a copy has two and ADR 0073's
   counterexample applies to each end; ADR 0086 reaches the same conclusion for mirror and the two
   should use one statement. The answer authorises one plan, and a start whose scope, either account,
   either generation or whose collision set differs is refused rather than carried.

5. **The source binding is untouched.** A replacement still declares what ADR 0083 and ADR 0084
   require — a plain source's own entity tag, a manifest source's ladder-verified digest — and a
   staged commit still performs ADR 0042's content comparison. Being allowed to replace does not
   relax what may be published.

6. **Segment reclamation is excluded.** ADR 0081 decision 5 reclaims a replaced large object's
   recorded segments by default **on the upload route**, where the operator's acknowledgement and the
   segments are on the same account. On a copy the predecessor's segments are on the **destination**
   account, so an answer of yes would be covering objects the operator was never shown on an account
   other than the one they chose the source from. ADR 0086 excludes it for mirror for the same reason
   and the two records agree. A replaced large object's parts are therefore **retained** on this
   route, which is what ADR 0020 asks for by default, and anyone who wants otherwise needs a separate
   decision.

7. **Only the refusing branches change.** `skippableEqual`'s proven-equality skip is unchanged, so is
   `runUnknown`'s create transition when the destination has gone, and so is `VerdictNew`. An
   unacknowledged copy behaves exactly as it does today, which is ADR 0081 decision 1's shape.

8. **`OverwritesRefused` keeps its meaning and is not repurposed.** It counts refusals, and after this
   it counts two kinds — a replacement nobody acknowledged, and one whose comparison refused — which
   the item outcome and the audit record distinguish. A counter that silently changed what it counts
   would make every historical job's number mean something else.

## What this record does NOT discharge

**DS-02 / DS-03 gates restoration on P05, P11 and P12, and what those owe for THIS operation is
narrower than an earlier draft of this record claimed.** That draft called L02's forged-force half of
P11 and L04's segment-action and deletion-failure-stream halves of P12 "this surface", which would
make sibling operations this record excludes into prerequisites for it. They are not.
`CopyRequestDTO` carries a source scope, a destination scope, a profile and settings — **no force
flag and no segment-delete authority**; those fields belong to `SegmentsDeleteRequestDTO`, and ADR
0020 keeps force cleanup as a separately confirmed action in its own right. A segment-retaining copy
overwrite cannot reach either.

What this operation does owe, and what should be in front of the operator before P14 acts on it:

- **The acknowledgement itself**, which does not exist for copy and is decisions 1 and 4 above.
- **The immutable plan and account/generation binding** that P11 requires of an enabled mutation, in
  the half L02 records as implemented under ADR 0073 option B since 24 September 2026 — so this is a
  matter of applying it to a copy's two accounts rather than building it.
- **The durable publication and outcome facts** P12 and P13 require, in the halves L04 records as
  required and applying, extended to a replacement's own refusal and conflict records.
- **Copy's own staging cleanup obligations**, which a refused replacement must not leave behind and
  which are this route's rather than any deletion surface's.

Force cleanup, segment deletion and bulk deletion stay deferred and are **not** prerequisites for
this. Nothing here declares P11 or P12 globally complete, and nothing here touches Copy's separate
capability gate.

## Consequences

**The costs separate into four, and an earlier draft of this record collapsed them into one.** ADR
0085 made and corrected the same mistake for upload in its own words — "an earlier draft wrongly
claimed it cost none" — and this record repeated it for copy, which is the clearest argument there is
for reading the executor rather than the records.

**Baseline acquisition**, per replaced name, in the preparation that composes the question.
`VerdictUnknown` already HEADs the destination, so if that read moves into the preparation this is
free there; `VerdictChanged` reads nothing today, so it is one HEAD per candidate either way.

**The final comparison**, per replaced name, which is a **separate** request from the baseline and is
not satisfied by it. On the native route it is always an added HEAD, because that route has no
pre-PUT destination read at all. On the staged route it depends on whether the existing pre-PUT
destination HEAD can be relocated to the fixed point, and on the symlink shape likewise — following
ADR 0085's own rule for this case, an implementation must say which rather than assume.

**The acknowledgement's plan reading**, once per job rather than per name.

**Transfer, publication and verification**, which are the route's existing costs and are not changed
by this record. No total is claimed.

Mirror's override side becomes possible, which is the one of its three effects ADR 0086 records as
having no authority. Nothing else about ADR 0086 follows from this: its deletions and its
source-absence exposure remain its own and remain unaccepted.

Acknowledgements taken before this record is implemented carry no identity for a copy, because no
copy has ever taken one.

## Limits

**Nothing here is atomic**, and no part of it prevents a concurrent writer. There is no
replace-if-unchanged on this store; the comparison narrows the interval before a replacement and
cannot close it, and the measured evaluation point means what remains after it includes the request's
own body transport and the server's application of it.

**Everything ADR 0081 and ADR 0085 accept for upload is accepted here**, per name, including that a
writer landing after the final comparison is published over and that a 201 is not evidence the sent
bytes are at the name. The route's own verification afterwards is what reports.

**A store that misreports a HEAD defeats it**, as it defeats every check in this project.

**This record does not establish that copy should be enabled.** Copy is a deferred capability under
[ADR 0052](0052-v1-capability-gate.md) with its own gate, and lifting the overwrite refusal decides
what copy may do when it is enabled, not when that happens. L05 remains open, though **not** for the
native route's binding: that row said so until 16 October 2026 and the claim was false, which the
correction on master records in the row itself.
