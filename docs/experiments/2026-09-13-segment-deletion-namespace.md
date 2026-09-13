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

[ADR 0044](../adr/0044-segment-deletion-namespace.md) is Proposed and nothing in it is implemented yet.
It authorises the sweep over the namespace boundary only, refuses before deleting anything when a preflight listing observes names outside that boundary, refuses an empty manifest prefix, and reports the boundary in the dialog instead of the raw header.

Three statements carry different weight and are kept apart.
The invariant is unconditional: no delete escapes the canonical namespace, which rests on the sweep's argument and not on a listing.
The refusal is conditional on observation.
Absence is not established, because K-CON-001 records listings as advisory and eventually consistent, so a complete successful listing may omit an existing outside name.
An unobserved outside name costs the refusal rather than the containment: the manifest is deleted and those bytes are orphaned rather than destroyed.

A manifest whose segments sit under its own namespace, which is what the engine writes, is unaffected.
A manifest that genuinely spans outside names loses this route, which is a deliberate withdrawal: the route cannot distinguish such a name from another manifest's backing data, and the probe shows it destroying exactly that.

## What this does not establish

The empty-prefix guard is latent rather than reproduced.
This store refuses `X-Object-Manifest: <container>/` at creation with HTTP 400 `X-Object-Manifest must be in the format container/prefix`, so the guard is defence in depth against a store that does not, and that refusal is the store's protection rather than this project's.

Nothing here makes recorded evidence a proof of exclusivity.
No live reverse-reference scan exists, so the Backstage force and typed confirmation remain required for every segment deletion, and creation provenance still never proves exclusivity.

The static large object graph is untouched by this slice.
Nested and shared collateral, another job's active staging, transfer-cleanup authority, first and later-pass replacements, and the final-hop concurrent-writer protocol are unchanged and open.
L03 does not close with this slice, and no release acceptance, scenario credit or gate disposition changes.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/l03/packet/`, holding the probe source, its raw failing run, the additive harness patch and a statement of the source base.

| Artefact | SHA-256 |
| --- | --- |
| `before-run-76c30e90.log` | `4fd3699cac9168899272d10db119b8c73cc8c38f8b79e87931e77e5f3b9e04c0` |
| `segment_scope_probe_test-76c30e90.go.txt` | `baa48f607ad4bc690ade9641b175a8afae21883e4dd84684e39b48ce78f672a8` |
| `harness-evidence-field-76c30e90.patch` | `f3c06ae271f23d97067709921505825991b0092eb4e84fdbc1c37b031d0551e2` |
| `SOURCE-BASE.txt` | `dccf35786dd1f7bcb6ed6cc610e4a4baf0424dd715394ee0ce3d95e36de5fa15` |

The packet's own `SHA256SUMS` manifest is `3e592e809db4820e33213bd9a86b48b6939ff789abaaf210a647db20efc6b2b5`.
Each filename carries the source base the artefact was produced against, `76c30e900e1e1c27`, which is unchanged source rather than the contract commit, because the probe reproduces before any correction.
The review request and progress notes are kept outside the sealed directory so that writing them cannot change the digest this record cites.
