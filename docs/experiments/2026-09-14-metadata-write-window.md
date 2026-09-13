# Object metadata: what a successful save establishes

Date: 14 September 2026.
Source: `3f663a7aae5b5f275af29fd7e3d3d9b0daa5d5c8`.
Finding area: P26 and the §18 stale-write boundary, under the read-mostly release scope.

## Why this was examined

Object metadata editing is the one mutation on the intended read-mostly release path, so what it claims about its own writes carries more weight than it did when it sat beside Copy, Mirror and bulk deletion.

## What was already decided, and is not disputed here

[DESIGN §18](../DESIGN.md#18-best-effort-stale-write-protection) states the flow and says Aeronautes must not imply an atomic compare-and-swap guarantee.
`K-META-003` requires the same.
`editableFingerprint` in `internal/swiftcontrol/headers.go` already carries that limit in its own comment.
None of that is a defect and none of it is restated here as a discovery.

## What was measured

### The window is reachable and its cost is a destroyed edit

`Session.UpdateObjectMetadata` reads the object, compares the caller's base fingerprint against that read, builds a replace-all header set from it, and writes.
A second editor's complete save landing between the read and the write is overwritten.

The probe holds the preflight HEAD's response at a proxy in front of the store, completes a second editor's save directly against the store while it is held, then releases.
Against the real fixture, through `MutateService`:

| Case | Reported outcome | Mismatch | Final metadata |
| --- | --- | --- | --- |
| Concurrent save inside the window | `applied` | empty | `Alpha:1 Beta:2` |

The second editor's `Gamma:3` is gone, and the product reported an unqualified success.

Two healthy controls pass in the same run, so the proxy, the barrier and the competing writer are not themselves the cause:

| Control | Result |
| --- | --- |
| Undisturbed edit through the same proxy and barrier | `applied`, intended metadata present |
| Competing save landing *before* the preflight read | `conflict`, nothing written, the other editor's state intact |

The second control is the load-bearing one: the existing fingerprint check works, and what the probe reproduces is the interval it does not cover rather than a broken check.

### The verification cannot detect this, by construction

`verifyObjectOutcome(before, edit, after)` asserts that the state after the write equals the intended edit.
A successful overwrite of a concurrent editor satisfies that assertion exactly, so no re-read can distinguish a clean write from a destroyed one.
This is a property of the comparison, not a gap in its implementation.

### No conditional metadata write is available to build on

Measured directly against the fixture, each with a condition that should fail:

| Request header | Status | Metadata write applied |
| --- | --- | --- |
| `If-Match` with a wrong entity tag | 202 | yes |
| `If-None-Match: *` on an existing object | 202 | yes |
| `If-Unmodified-Since` at the epoch | 202 | yes |

All three are ignored on the tested fixture.
Even were `If-Match` honoured it could not carry this check, because the entity tag is the content digest and a metadata POST does not change it.
No conditional write is therefore available on the supported path, measured here rather than taken from documentation.
This is a measurement of that fixture: three ignored headers do not establish that no Swift deployment anywhere honours a precondition, and it is sufficient only to decide what this product may claim about itself.

### The empty base fingerprint is deliberate, and is not this finding

`ObjectEditPanel` and `ContainerEditPanel` pass an empty base only through the conflict banner's explicit "Apply mine anyway" action, after the user has been shown that their edit was not written.
That is an informed override.
A caller reaching the bridge directly can supply an empty base without a conflict having been shown, which is the same class already recorded for the public-ACL acknowledgement and needs the same server-issued one-shot capability to close.
No change is proposed for it here.

## What the correction can and cannot be

It cannot be detection.
There is no conditional write to make the comparison atomic, and no post-write read can recover the state that our own write replaced.
Narrowing the interval between the read and the write shortens the exposure without bounding it.
No such narrowing is included: it improves nothing measurable and would invite a later reader to believe the interval is handled.

What is wrong today is therefore not that the window exists — that is decided and recorded — but that a write landing inside it is reported as a plain success.
The correction is to make the reported outcome state what it establishes and what it does not, so that neither the record, the audit entry nor the editor presents a completed save as evidence that no concurrent edit was overwritten.

## What was delivered

The statement travels with the result, the audit row and the editor.
It is set once the write has happened, so a refused save carries none and invents no effect.
The `applied` outcome is unchanged, because it already meant written and verified rather than exclusive, and a new failure state would claim a detection that does not exist.
The editor's notice outlives its own immediate close on success.
Four maintained regressions assert the reported shape: an undisturbed save still states its limit, a refused save states none and changes nothing, a save that overwrites a concurrent edit states its limit and leaves content byte-identical, and the audit row carries the same sentence.

## What this does not establish

It does not make metadata editing safe under concurrent writers.
It does not close P26, it grants no release acceptance, and it leaves the valuable-data hold unchanged.
The container edit path shares this shape and is not examined here.

## Evidence

Durable private packet `/home/csmart/code/.aeronautes-work/20260913-claude/meta/`, with digests in `SHA256SUMS` beside the files.
The contract-phase manifest digest is `288736de73323f25998b40d75c55caaa0a71744060b9d9d920a56dbf9a66d296`; the delivered-phase manifest is resealed with this range and its digest is given in the review request.
`probe-before-verbose.log` is the reproduction and its two controls in one run.
`conditional-post.log` is the conditional-write measurement.
`metadata_race_probe_test.go.txt` is the probe source, retained outside the repository because it asserts current behaviour rather than the intended contract.
