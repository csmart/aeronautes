# ADR 0052: Deferred capabilities are refused in a v1 build

## Status

Proposed. Nothing in it is implemented yet.

Two parts of it are the lead author's reading awaiting the operator's confirmation, and
are marked where they appear: that deferral should mean refusal rather than mere absence
of proof, and where the boundary falls inside `MutateService`.

## Context

The operator narrowed v1 to browse, inspect, download, and metadata read and write on
14 September 2026.
Copy, mirror, upload and bulk delete are deferred until each is proven, and the
[finish line](../audits/2026-09-05/linux-finish-line.md) records that deferral describes
what v1 must prove rather than what the binary refuses, leaving reachability undecided.

Leaving them reachable ships unproven transfer and destructive paths in a release whose
stated first priority is data safety.
Every one of those paths carries open findings; several were being actively corrected
during the days before the narrowing, and two defects that destroy data were found in
them in that period.

The surface this applies to is enumerated in the
[capability gate record](../experiments/2026-09-15-v1-capability-gate.md): fifty-two
bridge actions, derived independently from the generated bindings and from the action
inventory, with the two derivations agreeing.

## Decision

1. Deferred capabilities are **refused at the service entry point**, which is the bridge
   boundary, and their affordances are **hidden in the interface**.
   Refusal is the load-bearing half.
   Hiding alone protects nothing, because the bridge is reachable from the webview
   independently of what the interface offers — the connection registry's reachability is
   already recorded as a limit elsewhere in this project for the same reason.
   Hiding exists so an operator is not offered an action that will refuse.

2. The classification lives in **one table naming the deferred actions**, in the shape of
   the section 38 job ceiling: returning a capability is removing a name from that table,
   not editing call sites.

3. A test asserts the table is **exhaustive against the action inventory** — every bridge
   action is classified exactly once — so a new bridge method cannot land unclassified and
   silently reachable.

4. Deferred code, its tests and its refusals are **unchanged**.
   Nothing is deleted.
   Those routes must work when the gate is lifted, and their tests are how anyone will
   know that they do.

5. The refusals under [ADR 0020](0020-recovery-containment.md) are **independent of this
   gate and are checked first**, so lifting the gate cannot enable Mirror.
   That refusal is not a v1 scoping decision and must not become one.

6. `BenchmarkService.StartBenchmark` is deferred, because it writes real objects and bulk
   deletes them; and `TransferService.RerunJob` is deferred, because it refuses every job
   type except copy.
   Neither is named for a deferred capability, and a classification by name would have
   shipped both.

7. **Lead's reading, awaiting the operator.** Container creation is kept, being
   non-destructive and leaving metadata management thin without it.
   Marker creation, marker deletion and manifest deletion are deferred with the
   destructive work.

## What this does not establish

It bounds what a v1 build offers.
It does not make any kept capability proven, close any finding, or affect the
valuable-data release hold.

A refused action is not a safe action: the code behind it is unchanged and its open
findings are unchanged.
This decides only that an operator cannot reach it in a v1 build.
