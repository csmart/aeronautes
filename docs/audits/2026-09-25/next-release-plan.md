# Next release plan — upload

Written on 25 September 2026, after the operator accepted the author's sequencing and named the first capability, in their words: "let's just get started on the next release as per your plan".
Linux only, as v1 is. The release after 0.1.0 is 0.2.0 and its capability is upload.

## The principle this sequence follows

None of the deferred capabilities is unbuilt. [ADR 0052](../../adr/0052-v1-capability-gate.md) refuses each one at the bridge boundary from a single table and hides its affordances, and it left the code, the tests and the refusals untouched so that they work when the gate lifts. Returning a capability is deleting names from that table. The work is never the lifting; it is the safety proof the deferral stood in for.

So the releases are ordered by what each proof costs, not by how large the feature looks.

## 0.2.0, upload

Already built: file picking, a preflight that reports collisions, duplicates and unreadable files, chunk sizing against the destination's advertised maximum, both the engine and native routes, item records, recovery records, and post-commit error reporting under [ADR 0023](../../adr/0023-upload-post-commit-errors.md). The transfer dialog serves upload mode; only its opener is withdrawn.

Owed, in this order:

1. [ADR 0082](../../adr/0082-operator-enabled-unproven-capabilities.md), the out-of-band switch that lets a built application offer an unproven capability deliberately, off by default. Independent of the rest, and what makes the steps below testable in the product rather than only in a test binary.
2. [ADR 0080](../../adr/0080-upload-content-binding.md), binding both upload routes to their source's content. This is [ADR 0045](../../adr/0045-route-content-binding.md)'s next step and the substance of the release.
3. [ADR 0081](../../adr/0081-upload-overwrite-acknowledgement.md), replacing an existing object on an acknowledged, bounded overwrite.
4. Lifting the gate for upload: three names out of the table, the affordance flags fed from the reported set, the capability-gate record, and upload steps added to the release test plan.

The protocol facts all three rest on are measured in the [upload protocol measurements](../../experiments/2026-09-25-upload-protocol-measurements.md).

## What follows, and why in this order

**0.3.0, copy and job re-run.** Copy needs the native copy route bound, which is the last route in ADR 0045's sequence, and it needs P17 replication fidelity, finish-line item L06: metadata, content headers, expiry and link targets must not be reported as equivalent replication when they differ. The executor, its batching and its recorders already exist.

**0.4.0, deletion.** The heaviest and the most dangerous. It needs an accepted allowed-effect contract for destructive paths, finish-line item L03, the deletion failure streams of L04, and the exact-object journey of P27. It carries the DS-01 hazard, where a recovery sweep deletes segments an unrelated live manifest still references. It wants the most decisions from the operator and it should not be folded into an earlier release.

**Not scheduled.** Mirror stays refused under ADR 0020, which refuses it independently of the capability gate so that lifting the gate cannot enable it, and which needs an external-writer and source-absence guarantee that nothing here supports. Benchmark follows deletion, because it writes real objects and bulk-deletes them. A native uploader and SLO creation are non-goals under DESIGN section 62.

**Fits alongside any of them.** Pause and resume, which the operator placed at v1.x on 24 September and which the ADR 0074 batching makes feasible; the retired-key removal action, waiting on a bridge-action admission under [ADR 0069](../../adr/0069-preserve-covered-scenario-cells.md); clearing residue from a killed run, which P20 leaves open; and raising the default active-job budget, which [ADR 0078](../../adr/0078-overlapping-download-destinations.md) made safe.

## What this plan does not do

It does not change v1, whose scope and release hold stand as the [finish line](../2026-09-05/linux-finish-line.md) records them. It accepts no capability and lifts no refusal by itself: each of the records above carries its own decision and its own proof. It fixes no dates.
