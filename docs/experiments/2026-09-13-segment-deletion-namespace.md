# Segment deletion acts on a string prefix, not a namespace — 13 September 2026

A dynamic large object's segment deletion sweeps the raw manifest prefix while the ownership proof shown beside it covers the slash-terminated namespace.
The effect is wider than the proof, and it destroys other manifests' backing data.
This record carries the reproduction, the contract in [ADR 0044](../adr/0044-segment-deletion-namespace.md), and what it does not establish.

## Why the two differ

`X-Object-Manifest` names a container and a prefix, and Swift reads the object as every object in the container whose name begins with that string.
`DeleteManifestWithSegments` passes that prefix to the sweep unchanged, so the destructive set is the same raw string match.

`HasExclusiveOwnership` sends its argument through `canonicalPrefix`, which appends a trailing slash when one is missing.
The proof therefore answers a question about `job1/` while the deletion acts on `job1*`.

The static large object half of the same function already applies the boundary rule: `ownedRefs` matches a segment only against a slash-terminated proof, with a comment naming this exact hazard.
The dynamic half does not, which is the dual-path shape this repository keeps producing.

The pinned engine writes the header without a trailing slash.
That is recorded here already, in the crash-sweep production slash pair, so the vulnerable shape comes out of this project's own transfers rather than only from foreign tools.

## The reproduction

Through `CheckSegmentOwnership` and `DeleteManifestWithSegments` against the two-account fixture, not against a helper.

A subject manifest carries an unslashed header and two segments under `job1/`.
Three neighbours share the string and lie outside the namespace: `job1-archive/00000000`, `job1keep.bin`, and `job10/00000000`, which is the live backing of a second manifest.
A recorded ownership row over `job1/` makes the dialog report the segments as proven.

The dialog reported `class=dlo target="<segments>/job1" proven=true`.
After the forced, typed-confirmed deletion the whole-account snapshot showed four changes beyond the approved set:

    object <segments>/job1-archive/00000000
    object <segments>/job10/00000000
    object <segments>/job1keep.bin
    object <aliases>/sibling.bin

The first three were destroyed.
The fourth is the second manifest, which now reads as different content because its backing segment is gone.

The controls in the same run passed: the subject's own two segments and its manifest were removed, so the collateral is scope widening rather than a dead fixture.

## What the contract changes

[ADR 0044](../adr/0044-segment-deletion-namespace.md) authorises the sweep over the namespace boundary only, refuses before deleting anything when a preflight listing observes names outside that boundary, refuses an empty manifest prefix, and reports the boundary in the dialog instead of the raw header.

Three statements carry different weight and are kept apart.
The invariant is unconditional: no delete escapes the canonical namespace, which rests on the sweep's argument and not on a listing.
The refusal is conditional on observation.
Absence is not established, because K-CON-001 records listings as advisory and eventually consistent, so a complete successful listing may omit an existing outside name.
An unobserved outside name costs the refusal rather than the containment: the manifest is deleted and those bytes are orphaned rather than destroyed.

A manifest whose segments sit under its own namespace, which is what the engine writes, is unaffected.
A manifest that genuinely spans outside names loses this route, which is a deliberate withdrawal: the route cannot distinguish such a name from another manifest's backing data, and the probe shows it destroying exactly that.

## The delivered correction

The boundary is resolved once and used for both the dialog and the sweep.
The preflight lists the raw prefix, refuses on any observed outside name before the manifest is deleted, and refuses when the listing itself fails, so a refusal leaves the graph exactly as it was.

Four regressions replace the probe: the refusal with its boundary and sample, the engine's own unslashed shape deleting exactly its own graph, a slash-terminated header sparing a name that shares its stem, and a whole-container manifest refusing without issuing any delete.

Two further cases separate the containment from the preflight, because an invariant that only holds when the listing was complete is not the invariant this decision states.
A recording front seeds a name outside the namespace at the first delete, which is strictly after the preflight has been consumed, so the refusal legitimately does not fire; the late name survives and no delete is issued outside the namespace.
A second case fails the preflight listing and asserts the refusal with no delete issued at all.

Nine broken-guard controls were built by one producer, each unpacking the committed tree, applying one removal and discarding the copy, so the commit each result records is the exact source it compiled.
Every control is caught by its own named cases.

Two rounds of that were wrong before they were right, and both corrections are the point.
The first attempt caught only five of seven: restoring the raw-prefix sweep and ignoring the listing error were both undetected, because every case then reaching the sweep had been filtered by the preflight first.
The two containment cases above were written to close that, and are why the invariant is now tested independently of the observation.
The second attempt credited a control that did not compile, which proves nothing about any assertion; the producer now refuses to credit a control unless its package built and a named case failed, and it records the failing test names and each control's raw output rather than a duration.

That last rule is not specific to this slice and should outlive it.
Any guard producer used here should refuse to credit a control whose package failed to build, record the failing test names rather than their durations, and retain each control's raw output beside the summary, so a claim that a control was caught can be checked rather than taken on trust.
The next author should carry it into the shared producer rather than reimplementing it.

## Ordering the preflight must not disturb

The preflight issues a listing, and a listing placed between the final identity recheck and the DELETE would widen the very window that recheck exists to close.
The first implementation did exactly that, and an independent probe replaced the selected manifest with a plain object during the preflight: the deletion proceeded, removed the replacement and both original segments.
The preflight therefore runs before the recheck, so the order is resolve the destructive scope, refuse when it is wider than the namespace, then re-read identity immediately before deleting.
`TestSegmentDeleteRefusesAnIdentityMovedDuringThePreflight` moves the identity while the preflight is in flight and asserts the refusal with no delete issued.

The preflight also retains a count and at most five sample names rather than every outside name, because a wide namespace would otherwise grow resident data without a bound for a five-name message.
`TestSegmentDeletePreflightCountsEveryOutsideName` seeds twelve outside names and asserts the true count in the refusal, so the memory bound cannot silently truncate the decision.

## What this does not establish

The empty-prefix guard is latent rather than reproduced.
This store refuses `X-Object-Manifest: <container>/` at creation with HTTP 400 `X-Object-Manifest must be in the format container/prefix`, so the guard is defence in depth against a store that does not, and that refusal is the store's protection rather than this project's.

Nothing here makes recorded evidence a proof of exclusivity.
No live reverse-reference scan exists, so the Backstage force and typed confirmation remain required for every segment deletion, and creation provenance still never proves exclusivity.

The static large object graph is untouched by this slice.
Nested and shared collateral, another job's active staging, transfer-cleanup authority, first and later-pass replacements, and the final-hop concurrent-writer protocol are unchanged and open.
L03 does not close with this slice, and no release acceptance, scenario credit or gate disposition changes.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/l03/packet/`, holding the probe source and its raw failing run, the full suite stream, the guard producer and its results, and a statement of the source base each artefact was produced at.

| Artefact | SHA-256 |
| --- | --- |
| `before-run-76c30e90.log` | `4fd3699cac9168899272d10db119b8c73cc8c38f8b79e87931e77e5f3b9e04c0` |
| `segment_scope_probe_test-76c30e90.go.txt` | `baa48f607ad4bc690ade9641b175a8afae21883e4dd84684e39b48ce78f672a8` |
| `harness-evidence-field-76c30e90.patch` | `f3c06ae271f23d97067709921505825991b0092eb4e84fdbc1c37b031d0551e2` |
| `full-suite-375718a1.jsonl` | `3337a6e60b9101604fbcc547f941e2582c4793d904a8b7479cea8b67136ae79a` |
| `guards-375718a1.json` | `d29d06565510a9ca9e80345e45b9a8201a1f802f9a4bde328e9b1579f3f093b0` |
| `build_guards.py` (the producer) | `85ecc4fbd1de19a3699c033f7c56fca751cc9666cabe802284bf5ba72f1505ca` |

The packet's own `SHA256SUMS` manifest is `e73adf877f8ecdb9c23df62e8a5a37254764671ae0453113852cdce60eabfb50`.
Each control's raw output is retained beside the summary, so its named failing cases can be checked rather than taken on trust.
Each filename carries the source base the artefact was produced against, so a later run cannot be mistaken for this one.
The review request and progress notes are kept outside the sealed directory so that writing them cannot change the digest this record cites.

The full run at `375718a1`: exit 0, 22 packages passing with none failing, 4,602 distinct test names, 4,222 leaves of which 4,221 pass, 4,601 passing events including parents, empty standard error, and `TestRcloneMatrix` the only skip.
The 24-core host carried no other suite when it started, at load 1.84, and 18.74 when it finished; nothing timing-sensitive failed, so no case needed an isolated re-run.
