# ADR 0052: Deferred capabilities are refused in a v1 build

## Status

Accepted.
Refusal at the service entry point and hiding of deferred affordances are delivered
with this decision.

Two parts remain the lead author's reading awaiting the operator's confirmation, and
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

7. **Operator decision, 22 September 2026.** Container creation is kept: it is
   non-destructive, and a Swift workbench that cannot create a container is close to
   useless.
   Marker deletion and manifest deletion are deferred with the destructive work.
   Marker creation stayed deferred in the gate table until the operator confirmed whether
   it follows container creation; the lead's reading of 20 September deferred it, and
   the author's reading of 22 September would have included it, which is why it was an
   open question rather than a decision.

8. **Operator decision, 23 September 2026.** Marker creation is a v1 capability: the
   operator's words were "include it in v1 then", after the author reported that the code
   path, its tests and a retained patch existed behind the gate and that the work was
   small.
   `MutateService.CreateMarker` leaves the deferred table and joins the kept set; marker
   deletion and manifest deletion stay deferred with the destructive work.

9. **Operator decision, 16 October 2026.** Upload is a v1 capability: the operator's answer
   was "Yes — enable upload", given on what master carries for it rather than on a promise.
   That evidence is three records: both publishing paths bound to their source's content
   under [ADR 0080](0080-upload-content-binding.md), the acknowledged and bounded overwrite of
   [ADR 0081](0081-upload-overwrite-acknowledgement.md) with its fragment accounting, and the
   identity comparison at the fixed point before a replacement under
   [ADR 0085](0085-reobserved-destructive-identity.md).
   It is not a claim that the route is proven, and the exposure ADR 0081 and ADR 0085 state is
   accepted rather than removed: no conditional write exists on this store, so a replacement
   keeps a window its comparison narrows and cannot close.
   `TransferService.PickUploadFiles`, `PreflightUpload` and `StartUpload` leave the deferred
   table and join the kept set. The `upload` token leaves
   [ADR 0082](0082-operator-enabled-unproven-capabilities.md) decision 3's list with them,
   because it now names nothing the switch can admit; by decision 5 of that record an operator
   who still has it in their environment is told it was ignored, rather than left to read the
   silence as confirmation.
   Copy, bulk delete, marker and manifest deletion, benchmark and job re-run stay deferred, and
   Mirror stays refused under [ADR 0020](0020-recovery-containment.md).

10. **ADR 0092, 27 October 2026.** `MutateService.DeleteObject`, the deletion of one plain object the
    operator was shown, joins the deferred table when it is born, under the capability "Object deletion"
    with the token `object-delete`, so a build offers it only while the operator trials it; returning
    it is the operator's word, recorded here when given, as the earlier returns were.

11. **Operator decision, 29 October 2026.** Object deletion is a v1 capability: asked whether it was
    safe to return now, the operator's word was "if object delete is safe now, then lift the band".
    The evidence is [ADR 0092](0092-exact-object-deletion.md), accepted and proven on both fixtures,
    whose delete is bound to the identity the operator saw by the fixed-point comparison of
    [ADR 0085](0085-reobserved-destructive-identity.md) and leaves segments untouched, and
    [ADR 0093](0093-selected-set-deletion.md), the selected objects deleted as one job by the same
    verified per-name protocol; both were built behind the `object-delete` token and reconciled
    through the [ADR 0069](0069-preserve-covered-scenario-cells.md) drift route.
    `MutateService.DeleteObject` and `DeleteService.StartSelectedDelete` leave the deferred table and
    join the kept set. The `object-delete` token leaves
    [ADR 0082](0082-operator-enabled-unproven-capabilities.md) decision 3's list with them, because it
    now names nothing the switch can admit; by decision 5 of that record an operator who still has it
    in their environment is told it was ignored. It is not a claim the route is proven: a delete keeps
    the window its identity comparison narrows and cannot close, exactly as upload does.
    Prefix, container and bulk deletion, the manifest-with-segments job, marker and manifest deletion,
    copy, benchmark and job re-run stay deferred, and Mirror stays refused under
    [ADR 0020](0020-recovery-containment.md).

## What this does not establish

It bounds what a v1 build offers.
It does not make any kept capability proven, close any finding, or affect the
valuable-data release hold.

A refused action is not a safe action: the code behind it is unchanged and its open
findings are unchanged.
This decides only that an operator cannot reach it in a v1 build.
