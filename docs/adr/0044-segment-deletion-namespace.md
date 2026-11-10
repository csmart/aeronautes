# ADR 0044 — authorise segment deletion over a namespace, not a string prefix

**Status:** Accepted for the dynamic large object segment sweep, which is delivered with this decision. Static large object collateral, active-job staging protection and the final-hop concurrent-writer protocol remain in the sequence, and P15 does not close.
Active-job staging protection is closed for this application's own jobs by [ADR 0105](0105-concurrent-jobs.md) decision 3; a writer outside the application stays as this record leaves it.
**Scope:** The destructive scope of `DeleteManifestWithSegments` for a dynamic large object, within P15 and finish-line item L03.

## Problem

A dynamic large object's `X-Object-Manifest` names a container and a prefix, and Swift reads the object as every object in that container whose name begins with that string.
The prefix is a raw string match, so `segments/job1` reads `job1/0000`, and also `job10/0000`, `job1-archive/0000` and `job1keep.bin`.

`DeleteManifestWithSegments` passes that raw prefix to the prefix sweep, so the destructive effect is the same raw string match.
The ownership evidence displayed beside the decision does not describe that set.
`HasExclusiveOwnership` canonicalises its argument through `canonicalPrefix`, which appends a trailing slash when one is missing, so the proof answers a question about `job1/` while the deletion acts on `job1*`.
The proof is strictly narrower than the effect, and the dialog can report the segments as proven exclusively ours while the sweep removes objects no proof ever covered.

This is not a foreign-tool hazard.
The pinned engine writes the header without a trailing slash, which this repository already records as the production form in the crash-sweep slash pair, so the vulnerable shape is produced by the project's own transfers.

The [archived probe](../experiments/2026-09-13-segment-deletion-namespace.md#the-reproduction) reproduces it against the real fixture through the service.
A subject manifest with an unslashed header and its two segments under `job1/` is deleted with the force and typed confirmation, with a recorded proof over `job1/` reported as proven.
The sweep additionally destroys `job1-archive/0000`, `job1keep.bin`, and `job10/0000`, which is the live backing of a second manifest; that second manifest is left reading as different content.
The operator-facing prefix delete already refuses a prefix without a trailing slash and has a maintained regression proving it spares a name that merely shares the string.
The segment path takes its prefix from a header instead of the operator and had neither.

## Decision

### The unit of destructive authority is a namespace

Segment deletion for a dynamic large object is authorised over the manifest prefix's namespace boundary.
The namespace is the manifest prefix when it already ends in a slash, and the prefix followed by a slash when it does not.

The sweep acts on that namespace and on nothing else.
A name that the manifest reads but that lies outside the namespace is never destroyed on this authority.

This aligns the effect with the proof.
`HasExclusiveOwnership` already canonicalises to the same boundary, so after this decision the set the operator is shown and the set the sweep acts on are the same set.

### Content outside the namespace is reported, not destroyed and not ignored

Before deleting anything, the route lists the raw manifest prefix.
Names under the raw prefix that lie outside the namespace are counted and named.

When any are observed, the operation refuses before deleting the manifest.
The refusal names the boundary, the count and a bounded sample, and says that other objects may reference those names.
Nothing is removed, so a refused attempt leaves the graph exactly as it was and the operator can act on the named objects deliberately.

The listing must be consumed completely before any delete, and a listing that fails or is cancelled refuses.
The bounded sample is a display limit on the message and never a limit on the decision: the decision reads every listed name.

The alternative of sweeping the namespace and reporting the remainder as residue was rejected.
It destroys the manifest that gives those bytes their meaning while leaving the bytes, which converts a refusable situation into unrecoverable orphans.

### What the preflight does and does not establish

Three separate statements, because they carry different weight.

The invariant is unconditional: no SEGMENT delete issued by this route escapes the canonical namespace.
It rests on the sweep's own argument and not on any listing, so no listing behaviour can weaken it.
The explicitly selected manifest is a separate authorised deletion, named and typed by the operator, and it normally sits outside that namespace; the invariant governs the segments the manifest points at, not the manifest itself.

The refusal is conditional on observation: when the preflight observes a name outside the namespace, the route refuses.

The absence of such names is not established.
K-CON-001 records that listings are advisory and eventually consistent, so a complete and successful listing may still omit an existing outside name, and the route can then proceed in a case this decision would refuse had the name been visible.
An advisory listing cannot supply absence, and this decision does not claim it does.

The consequence is deliberate.
The invariant already prevents the destruction the probe reproduced, because the widened set is exactly the set outside the namespace.
What an unobserved outside name costs is the refusal, not containment: the manifest is deleted and its outside-name content is left as orphaned bytes rather than destroyed.
Refusal for every such graph would need an authority stronger than a listing, which does not exist here; the valuable-data release hold and the standing force and typed confirmation remain, and this decision does not lift them.

### An empty manifest prefix refuses

A manifest of the form `container/` states a container and no prefix.
Its namespace would be the whole container, which no manifest's authority extends to, so it refuses.

This store rejects that header at creation with HTTP 400, so the case is latent rather than reproducible here.
It is a guard against a store that does not, and it is recorded as defence in depth rather than as a fixed defect.

### The dialog states the boundary

`SegmentOwnershipDTO.Target` reports the namespace that would be swept rather than the raw header, so the operator is shown the set the operation will act on.

## Consequences

A dynamic large object whose segments sit under its own namespace, which is what the engine writes, deletes exactly as before.

A manifest that genuinely spans names outside its namespace can no longer have its segments deleted through this route.
That is a withdrawal, and it is deliberate: the route cannot tell such a name from another manifest's backing data, and the probe shows it destroying exactly that.
The refusal names the objects, so the operator retains a deliberate path, and it applies to the graphs the preflight observes rather than to every graph that has one.

The proof and the effect now describe the same set, which removes the reading in which recorded evidence appears to authorise destruction beyond its own scope.
It does not make the evidence a proof of exclusivity: no live reverse-reference scan exists, so the force and typed confirmation remain required for every segment deletion, and creation provenance still never proves exclusivity.

This decision covers the dynamic large object sweep only.
Static large object nested and shared collateral, another job's active staging, transfer-cleanup authority, first and later-pass replacements, and the concurrent-writer protocol are unchanged and open, and L03 does not close with it.
