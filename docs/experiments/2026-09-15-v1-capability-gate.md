# v1 capability gate — 15 September 2026

The operator narrowed v1 to browse, inspect, download, and metadata read and write on
14 September 2026, recorded in the
[finish line](../audits/2026-09-05/linux-finish-line.md).
Copy, mirror, upload and bulk delete are deferred until each is proven.

That record left one thing open, and said so: deferral describes what v1 must prove
rather than what the binary refuses, so whether the deferred routes stay reachable in a
v1 build was undecided.
This record settles what to build and leaves the decision itself to
[ADR 0052](../adr/0052-v1-capability-gate.md).

## What this is not

No slice here has been implemented yet.
This is the contract and the enumeration it rests on; the correction it proposes is not
delivered by this record.

## The surface, derived twice

A list of bridge methods nobody can check is a list somebody will trust wrongly, so the
surface was derived two ways and the results compared.

The first derivation reads the generated TypeScript bindings under `frontend/bindings`,
which are what the webview can actually call.
The second reads the action inventory in `internal/scenariocontract/contract.go`, which
is maintained by hand for the coverage denominator and is independent of the generator.

Both produce **52 actions across twelve services**, and `diff` reports no difference.
Two independently maintained enumerations agreeing is the evidence that neither has
drifted; either one alone would be an assertion.
Both lists and their diff are in the private packet.

## Classification

Thirty-seven actions are kept and fifteen deferred.
The full table is in the packet and every action appears in it exactly once, checked
against the surface list so nothing is unclassified.

Four classifications are judgements rather than readings, and are recorded as such.

`BookmarkService.Delete` removes a stored bookmark and no remote data, refusing while any
job still uses it, so it is account management and is kept.
Blocking it would trap credentials in the application.

`BenchmarkService.StartBenchmark` is deferred although no deferred capability is named
"benchmark".
It writes real objects — its own typed confirmation says so — and bulk-deletes them
afterwards, so it exercises the upload and bulk-delete paths under another name.
A classification by capability name rather than by behaviour would have shipped it.

`TransferService.RerunJob` is deferred for the same reason in reverse: it is named for
none of the deferred capabilities and refuses every job type except copy, so it is a
copy entry point and nothing else.

`DeleteService.PreviewDelete` and `CheckSegmentOwnership` are read-only and are deferred
anyway, because they exist to serve actions a v1 build refuses.
Offering an operator a preview of a deletion they cannot perform is worse than not
offering it.

## What the interface must not assume

`LocalTransferDialog.svelte` serves upload and download from one component, switching on
a mode at the call site.
Hiding it would remove download, which v1 keeps.
The interface change is therefore not "hide the components that call deferred methods";
one of them has to keep working with half its behaviour withdrawn.

## Mirror is already refused, and must stay refused

`TransferService.StartMirror` refuses unconditionally today under
[ADR 0020](../adr/0020-recovery-containment.md), because a fresh HEAD cannot protect a later deletion
from an external writer.
That refusal is not a v1 scoping decision and must not become one.
Whatever the gate does, lifting it must not enable Mirror.

## Open

Whether the operator agrees that disabling is the right reading of deferral, and whether
the marker and manifest methods belong with the destructive work, are theirs to confirm.
Neither is settled here.
