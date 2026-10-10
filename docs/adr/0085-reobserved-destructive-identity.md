# ADR 0085 — compare a name's identity at the last observation before destroying what it holds

**Status:** Proposed. The operator asked for this protocol on 7 October 2026, in these words: re-HEAD
just before the delete, and if the hash still matches delete it, otherwise fail. It is written for
review before any implementation.
**Scope:** An identity comparison at the last observation each destructive path already makes, for
Aeronautes-native manifest deletion, prefix deletion, and the upload overwrite
[ADR 0081](0081-upload-overwrite-acknowledgement.md) already accepts. It admits no capability, admits
no Mirror, and does not lift any other [ADR 0020](0020-recovery-containment.md) refusal.

## Problem

Nothing on this store binds a mutation to the object the operator looked at:

| Precondition | On | Measured result |
| --- | --- | --- |
| `If-Match` with a wrong entity tag | object PUT | ignored, the write applied |
| `If-Match` with the current entity tag | object PUT | 201, the sent bytes |
| `If-Match`, `If-None-Match`, `If-Unmodified-Since` | object POST | each ignored |
| `If-Match` | DELETE | ignored |
| `If-None-Match: *` | object PUT | honoured — create only |

ADR 0081 states the consequence: replace-if-unchanged is not available. So a destructive path can
observe a name and then destroy something else, and no header prevents it.

## What this decision is, and is not

It adds one comparison: at the last observation a destructive path already makes, compare the name's
identity against the identity that path's decision rests on, and proceed only on a match.

**There are two observations here, not one, and an earlier draft wrote as though there were one.**
The *baseline* is what the path's decision rests on — a listing row, a resolving HEAD, the identity an
acknowledgement captured — and it stays where it is. The *final comparison* is a fresh observation
taken immediately before the effect. A difference between them refuses. What is left exposed begins
**after the final comparison**, not after the baseline, and an earlier draft said the opposite in two
places.

**It does not bound that remaining interval, and an earlier draft claimed it did.** It does not make
the window one round trip. What remains inside it differs per path and is stated per path below,
because a reader who takes "re-check immediately before" to mean "nothing can intervene" has the wrong
model. In every case the request still has to reach the store and be applied, and a request carrying a
body is not applied until the body has arrived.

**It is not proof of anything continuous.** A match says the compared attributes agreed at that
observation. It does not say the object was unchanged from listing to effect, that no writer exists,
or that the effect landed on the object the operator saw. ADR 0020's prohibition stands: no interface
text, log line, audit event or record may call a match exclusive ownership, a lock, a reservation, or
proof that nobody else is writing.

## Decision

### Native manifest deletion — the narrowest window available

`Session.DeleteManifestObject` already HEADs the name, refuses a plain object, and then deletes, with
nothing between the HEAD and the DELETE. It is reachable by the operator today: the inspector offers
it when `marker-manifest-delete` is enabled, and the selected container and name cross the bridge to
`MutateService.DeleteManifest`. An earlier draft of this decision said no operator-facing named
deletion existed, which was wrong.

That HEAD becomes the comparison point. It already reads class; it should also carry length and entity
tag and require all three to equal what the inspector showed the operator for that object. On any
difference, refuse and delete nothing.

The interval that remains is the DELETE's own transport and application. This is the smallest window
any path here can offer, and it is the only one where nothing of ours runs inside it.

### Prefix deletion — the baseline is a row this job read, not something a person approved

`DeleteRequestDTO` carries a container and a prefix; `PreviewDelete` returns four aggregate numbers.
The operator approves a prefix, so no per-object expectation of theirs exists to compare against. The
baseline is therefore the listing row this job read, and for a zero-byte row — which may be a manifest
whose logical content the listing does not show — the baseline is a HEAD this job takes for that row,
which is a **later** baseline than the listing.

Two separate statements follow, and they must not be run together.

A replacement landing between a name's baseline and its final comparison HEAD **is refused**: that is
precisely what the comparison is for, and for a prefix deletion that interval is the bulk of the
elapsed time, because it spans listing, planning and queueing.

A replacement landing **after** the final comparison HEAD is still deleted, and nothing here detects
it. That is the exposure this protocol accepts.

The zero-byte row carries its own separate limitation, in the other direction: its baseline is a HEAD
this job takes, so a replacement that landed before that HEAD is adopted as the baseline and then
deleted. The listing row it came from is not the baseline for such a name.

An earlier draft promised that nothing would be destroyed whose content changed between listing and
deletion. That overstated the guarantee in one direction and understated the comparison in the other,
and both are corrected above.

### Prefix deletion gives up bulk delete, and the cost is not only the DELETEs

`BulkDelete` writes a newline-separated name list into one request body, and a header applies to the
whole request, so no per-name precondition can ride it. Reading a batch of HEADs and then issuing one
bulk request would give the first name in a thousand the widest window of all.

A verified deletion therefore issues one DELETE per name immediately after that name's own HEAD, and
takes its throughput from the worker pool `deletePass` already has.

The cost, stated so a long job does not discover it: for N names of which Z are zero-byte or
SLO-listed rows, the object requests are 2N + Z, before listing, planning, verification or retry
overhead. Ten thousand plain objects need twenty thousand requests where bulk delete needed ten; ten
thousand zero-byte rows need thirty thousand.

### Upload overwrite — already accepted, and the comparison is the amendment

ADR 0081 is the governing contract here, not this one. The operator accepted the overwrite exposure
for the upload route explicitly, in their own recorded words, and ADR 0081 lifted ADR 0020's
transfer-overwrite refusal **for that route alone**. So overwrite is not becoming possible here; this
decision amends an accepted exception by adding an identity comparison to it.

Copy's overwrite stays refused. ADR 0020's table still refuses it, and the remediation plan's DS-02 /
DS-03 row still asks for a reviewed enforceable protocol before P14 restores anything. This decision
does not supply one — it supplies an accepted narrowing for paths an operator has already accepted —
so it must not be read as satisfying that row for Copy or for anything else.

`observePredecessor` reads what is at the name and records class, length, entity tag, last-modified
and the replaced manifest's fragment inventory, for ADR 0081 decision 7's ledger. It is the obvious
comparison point and it is **not** the last observation before the effect. After it, the native route
hashes the whole local file; the staged route HEADs the stage and the destination, hashes the file,
may read the whole assembled staged content, and writes recovery facts. Then a body streams.

**This decision fixes the comparison point rather than leaving it open.** The comparison is made after
all preparatory work, immediately before the publishing request is issued. Where ADR 0042's ordering
applies, it runs before the committing fact, because a fact recorded for a publication that is then
refused is a fact about nothing — so the committing fact's own write sits between the comparison and
the request.

The alternative was to reuse `observePredecessor`'s earlier observation and leave the preparation
inside the window. That is rejected, and the reason is the whole point of the protocol: preparation can
take a long time — hashing a large local file, or reading an entire assembled stage — and a writer
arriving during it would not be compared against anything. A comparison that permits that is not the
comparison the operator asked for. Leaving the choice to an implementation would have left the safety
property undecided, which is not something a contract may do.

What remains inside the interval is not one thing, and it differs by publication shape:

- **The committing fact, where ADR 0042's ordering applies.** It must exist before the publication it
  protects and after the comparison, so its write is inside the window by construction.
- **The final request's body, where there is one.** The native PUT streams the local file. A staged
  plain or static materialisation streams the staged object. A staged **dynamic** manifest PUT sends
  an empty body — its assembled-content read is preparation, not the final request — so that
  publication has no body transfer in its window at all.
- **Transport and server application**, which no path avoids.

So replacing a one-gigabyte object by the native route leaves a window as long as that upload takes,
while committing a dynamic manifest over a name leaves recording plus one small request. Calling both
"immediately before the publication" is accurate and tells an operator nothing useful, which is why
the decision separates them.

That fixed point costs a request, and an earlier draft wrongly claimed it cost none. On the native
route the only execution-time destination HEAD is `observePredecessor`'s, and its result feeds the
fragment inventory and the durable predecessor record, so it cannot be moved to the comparison point
and cannot serve as a fresh observation there. The comparison therefore adds **one HEAD per replaced
name** on that route. The staged route already HEADs the destination later in its sequence, so whether
it adds a request depends on where that read sits relative to the fixed point, and an implementation
must say which rather than assume.

What the comparison compares against is a problem the code does not currently solve.
`UploadItem.Replaces` is a boolean: the preflight records that a name was live and acknowledged, not
what was at it. ADR 0045 keeps content identities off `UploadItem` and `DownloadItem` and changes no
generated binding, so the captured identity belongs in ADR 0081 decision 7's replacement ledger,
keyed by job and name. An acknowledgement that captured no identity cannot overwrite under this
protocol, and an acknowledgement taken before this change captured none, so it must be re-taken.

### A destination that disappeared must regain its create conditional

ADR 0081's route records an absent predecessor and continues, because publishing over nothing is what
a create does. But `item.Replaces` stays true and `publishHeaders` adds `If-None-Match: *` only when
it is false, so a name **recreated** between that observation and the publication is overwritten with
no conditional and no acknowledgement of that object. An earlier draft of this decision asserted the
create's own conditional already protected this case. It does not.

This decision proposes the explicit change: when the predecessor is observed absent, every
publication route restores the conditional create for that name, so a name the store finds live when
it evaluates that conditional is refused instead of overwritten. No maintained test covered that
transition when this was written; the implementation supplies one.

**That sentence originally said a recreated name conflicts instead of being overwritten, without
qualification, and the measurement refutes it.**
[The evaluation point](../experiments/2026-10-10-conditional-evaluation-point.md) was measured after
the implementation shipped: a name created while the conditional PUT's body was still arriving was
never refused, twelve observations of twelve across both HTTP framings, with each of the two controls
holding twelve of twelve. So the conditional is evaluated before the body completes. What this
change removes is the dependence on the age of the route's own observation; the interval after the
store has evaluated the conditional stays exposed, and on the native route, which streams the whole
local file afterwards, that interval is the length of the upload. This transition narrows like every
other one in this decision — it does not close.

The same measurement found that in eleven of those twelve the conditional creator was answered 201
while the other writer's object remained at the name. A 201 is therefore not evidence that a
publication's bytes are live at its name, and nothing in this decision may be read as making it one;
the route's own live read after the publication is what reports that case.

### A difference refuses the name and never prompts

A mismatch fails that one name, retains it, and reports it; execution continues. Nothing is prompted
mid-pass, because a dialog held open while other destructive effects proceed is a hazard rather than a
safeguard. Every refusal records both identities — expected and found — so an operator learns that
something else is writing to their account rather than only that a request was refused.

This is per name and says nothing about a set. A prefix deletion that refuses one name and deletes a
thousand others has done exactly that, with no claim that the set was consistent at any instant.

### Mirror is not admitted

Mirror deletes names for being absent from the source, so there is no expected identity to compare
against. The case that destroys data is a file created after the listing was taken: it is unchanged
since, it was never in the source, this comparison passes it, and it is deleted. Mirror keeps having
no capability token, as [ADR 0052](0052-v1-capability-gate.md) decision 5 requires, and this decision
is not a step toward admitting it.

### History versioning: only what was measured

On a container carrying `X-History-Location`, two writes to one name left an archive container holding
one object whose listing name carried a timestamp suffix. That is the whole of the measurement. No
archived bytes were read, nothing was restored, no versioned DELETE was measured, and ADR 0081
decision 8 records the same limit.

So this decision notes that such a container records a previous version and recommends nothing beyond
what that supports. It makes no claim that a lost race becomes recoverable, and the application may
not present it as a safety net.

## Consequences

Every verified destructive effect keeps a window between its final comparison and its application,
and what sits inside it differs: one request's transport for a manifest deletion; the same for a
prefix deletion, whose earlier listing-to-comparison interval the check does cover; recording plus a
body for a native or materialising overwrite; recording plus a small request for a dynamic manifest
commit. Accepting this protocol is accepting those, and an interface should not describe them with one
phrase.

Prefix deletion costs 2N + Z object requests and loses bulk delete.

Upload overwrite gains the comparison at one HEAD per replaced name on the native route, and gains a
conditional create on a disappeared destination.

Acknowledgements taken before this change carry no identity and must be re-taken.

Copy overwrite and Mirror are unchanged and still refused.

## Limits

No measured replace-if-unchanged or delete-if-unchanged primitive exists on this store, so nothing
here is atomic. Conditional *creation* does work and is relied on, both by every create and by the
absent-predecessor case above.

A writer landing inside any of the windows above is undetectable by this protocol: the observation saw
one object and the effect applied to another, and neither request reports anything unusual.

A store that misreports a HEAD defeats it, as it defeats every check in this project.

Nothing here grants authority over segments or any other object a named one refers to. ADR 0020's
reclamation rules are untouched, and a verified deletion is not an `ExclusiveOwnershipProof` for
anything it points at.

Nothing here establishes continuous identity. Each claim is about one observation, and the decision
names which.
