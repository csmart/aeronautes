# ADR 0060 — bind native download to missing segments and whole-job substitution

**Status:** Proposed.
Contract only; nothing in it is implemented yet.
This does not close L05, DS-04 or the valuable-data hold.

**Scope:** The remaining L05 cases on the native download route only: missing and reordered segments, and whole-job source and publication substitution.
The engine download path stays unbound.
Copy, native upload and staged upload commit stay the named deferred gap in [ADR 0045](0045-route-content-binding.md).
L07 cancellation work in the same executor file is out of scope.

## Problem

ADR 0045 compares a native GET against the source ladder built at execution, and publishes only the inode it verified.
That closes same-size corruption on this route.
It does not close L05.

The [remainder record](../experiments/2026-09-16-native-download-content-binding.md) states the three leftover cases from the current source:

1. A segmented native GET is untested.
   The digest would refuse a GET whose runs do not match the ladder, but every maintained case is a one-run plain object.
2. The transfer set stores no source identity, so a same-size replacement after Start is downloaded as the new source and verified.
3. Verification is recorded in the same observation as publication, and that row cannot be rewritten, so a published name substituted later in the job stays verified.

The engine path is still size-only.
A comparison is not added there without a fixture that can show the comparison doing work.

## Decision

### The ladder still compares the GET

Native download keeps the ADR 0042 ladder and the in-flight digest ADR 0045 already rides.
What changes is that a segmented GET is a required case, not an implication.

A native download of a dynamic or static large object refuses before publishing, retains the temporary, and reports `unverified`, when the assembled GET is any of:

- a same-size permutation of the ladder's runs;
- a missing run replaced by a duplicate of another so the total length matches;
- a body that ends at a run boundary short of the ladder.

A matching control publishes.
A source that was legitimately resegmented, whose listing and GET agree on the new runs of the same bytes, publishes.
A ladder that cannot be built stays `incomplete`, which is ADR 0045's classification for an unusable expectation, not this comparison.

If `OpenObject` does not present the assembled content the digest can compare, that is a defect this slice must close rather than a reason to skip the cases.

These are the ADR 0042 fixture family applied to the native GET.
They are not a listing-recombination fast path.

### Source identity is captured with the transfer set, not on the bridge

Start already rebuilds the transfer set with `PreflightDownload`.
That walk is the whole-job source observation.

Capture identity there, internally, for native file items only:

1. A zero-byte listing row already HEADs, to distinguish a folder marker from a large object.
   Capture that HEAD's class, logical length, trimmed entity tag and manifest.
2. A non-zero listing row is not HEADed.
   K-CON-001 forbids HEADing every listing row.
   Capture listing bytes and the trimmed listing entity tag.
   If the row is `SLOListed` and carries `SLOETag`, capture that too.
3. Directory markers have no content identity.

The identity is not a JSON field on `DownloadItem`.
The generated binding and the bridge view stay as they are.
ADR 0045 already refused putting a digest on that DTO.

At `OpenObject`, before creating a temporary, the live HEAD must match what was captured:

- Logical length equals the captured length.
- A plain object: trimmed entity tag equals the captured listing tag.
- A DLO or SLO captured from the zero-byte HEAD: class, trimmed entity tag, length and manifest equal that HEAD.
- An SLO captured from a listing: trimmed live entity tag equals `SLOETag` when that value was present.

A disagreement, or a class change, is source substitution.
Close the body, create no temporary, publish nothing, report `unverified` with effect `unresolved`, because the GET was issued.
Do not classify it `incomplete`: that code remains the unusable-ladder case.

This is identity of the planned object, not content evidence.
The GET still has to match the execution-time ladder.
A listing tag is not a substitute for that digest.

Limit: an SLO listed with bytes and no `SLOETag` has only size as captured identity.
A same-size substitution of that object is not claimed from the listing.
The ladder versus GET still binds whatever the live HEAD describes.

### Publication identity is confirmed before verification is recorded

`publishVerified` stays the per-item no-replace link and `os.SameFile` check.

`job_item_observations` accepts one verification row.
A second different verification is a conflict, so a later substitution cannot un-verify a row already written.

Therefore a native file's verification observation is recorded only after a confirmation pass that the published name still identifies the verified inode.

Execution is still recorded when the helper returns, as ADR 0033 already requires before later work.
The confirmation runs after the native item loop, through the held destination parent, using the `FileInfo` taken while the writer handle was open.
`SameFile` success records the same verified observation the helper records today.
`SameFile` failure records `unverified` with effect `unresolved`, publishes nothing further for that name, and does not unlink the occupant: an identity we do not hold is not authority to delete, which is the ADR 0045 cleanup rule applied to the published name.

A crash before the confirmation already has the missing-verification facet ADR 0033 named.
This slice does not invent a second verification row.

Engine placement keeps its current execute-and-verify timing, because this decision does not bind that path.

### Engine download stays unbound

No digest, no source-identity check, and no publication confirmation is added to `placeDownloaded`.
The matching-content engine case stays a control that matching bytes still arrive.
The earlier observation that an altered engine GET ended with nothing staged is still an observation about one engine version and one fixture set, not a contract.

If a later slice can prove a comparison on that path, it needs a fixture that fails when the comparison is deleted.
Until then the engine download path is unbound by this project's evidence, including missing and reordered segments and substitution.

### Nothing else changes

Existing same-size native refusals, retained temporaries, and the close-failure identity rule stay.
No overwrite, Mirror start, source deletion, automatic segment cleanup or credential reclamation is authorised.
The bridge surface does not widen.
L07 cancellation checks are not moved, added or removed.
`mutateservice.go`, `containerupdate.go` and `browserState.svelte.ts` are not this slice.

## Cost

Segmented native GETs add no object bytes: the digest already rides the body.
Source identity uses the Start listing and the HEADs preflight already takes for zero-byte rows, plus the execution `OpenObject` HEAD the helper already has.
Publication confirmation is one `Lstat` per published native name after the native loop.

Rejected alternatives: HEADing every file at Start, which is the listing rule this project does not break; putting entity tags on `DownloadItem`, which widens the bridge ADR 0045 kept closed; treating a listing recombination as content evidence, which ADR 0042 disproved; rewriting a verified observation after substitution, which the item store refuses; and adding an unproven engine comparison.

## Consequences

Native download refuses the L05 cases it can see on its own route: a GET that does not match the ladder's runs, a source that is no longer the object the transfer set named, and a published name that no longer names the inode that was verified.

Refusals become possible where a segmented native download, a replaced source, or a replaced publication previously succeeded.

The engine path, Copy, native upload and staged upload commit remain unbound by this decision.
L05 stays open.

## Limits

MD5 is the store's integrity expectation for an honest provider, not authenticity against forged or colliding evidence.

A listing tag compared at `OpenObject` can refuse a stale listing that disagrees with a live HEAD without any substitution having occurred.
That is conservative and is accepted: a disagreement is not proof of current content, and it is not authority to publish.

This decision does not freeze the source after `OpenObject`, and it does not freeze the published name after the confirmation pass.
It does not audit the engine.

It does not close L05, DS-04, or the valuable-data release hold.
