# ADR 0066: preserve the validated named-read session

**Status:** Proposed.
Implementation waits for independent contract review.
This is the narrow session-capture follow-up that [ADR 0049](0049-account-scoped-reads.md) and [ADR 0059](0059-account-scoped-remaining-reads.md) explicitly deferred.

## Context

`Connections.scopedSession` validates a cached entry for the caller's bookmark, then discards its session and resolves the global active session again.
An ordinary `BrowseService.Connect` between those steps can replace the session without replacing the reply's earlier bookmark and generation.
The [source-anchored record](../experiments/2026-09-21-named-read-session-snapshot.md) schedules that gap through all eight existing public callers against mock HTTP and owned Swift.
Each returns secondary-account data labelled as primary; a Find scan retains that mismatch for later polls.
The deterministic reproduction uses an observation-only source overlay, not an uninstrumented application run.

## Decision

Keep the validated cached entry's session together with its named bookmark and generation for the read.
`scopedSession` returns `entry.session` and the matching scope instead of consulting the global active session again.
Its eight callers remain `BrowseService.Containers` and `Objects`, `InspectorService.Object`, `Prefix`, `Container`, `SLOManifest` and `DLOSegments`, and `FindService.StartFind`.
Their signatures, DTOs, continuation checks and request-time bookmark-generation admission remain unchanged.

An empty bookmark, an initially disconnected connection, a different initially active bookmark or a changed bookmark generation still refuses under the existing checks.
A later connection change may coexist with the admitted read: that read may finish against its captured account, and its reply must retain that account's scope.
A Find worker retains that same session and scope for its lifetime.
An ordinary Connect does not retarget an in-flight read; bookmark edits still reach the existing generation admission at each remote request.
There is no new lock held across remote I/O.

## Maintained proof

Keep public-service tests with account-distinct payloads under colliding names, independent namespace snapshots and both mock HTTP and owned Swift providers.
Cover ordinary reads from both accounts and the no-switch, same-bookmark reconnect and other-bookmark Connect schedules for all eight callers.
Pause after the generation comparison but before returning the session, then complete the public Connect and a read of the newly active account before releasing the original read.
Assert the original account's payload and scope, including Find polls, rather than accepting refusal as proof of capture.
Retain empty, disconnected, different-bookmark and generation-change refusal coverage and prove an edited bookmark cannot issue a remote request through the captured stale session.

The pause belongs only to tests.
A dedicated system-test helper inserts an observation callback into a temporary Go source overlay and runs the named test in a bounded subprocess.
The ordinary test entry point performs this automatically, preserving race detection and fixture requirements; the child runs the public-service test with the overlay rather than launching another child.
The helper must refuse a missing or ambiguous insertion point, a compilation failure, a missing child result or an unreached barrier.
It preserves all production statements and changes no session, check, return value or HTTP response.
No production callback, exported test API or general fault framework is added.
This maintains an explicit scheduled-interleaving proof, not a claim to have observed the race without instrumentation.

The final maintained tests must catch the original implementation before the correction, and compiled mutants must catch restored active-session resolution, incorrect scope attribution and bypassed generation admission.
An instrumentation or compilation failure is a failed verification run, never behavioural mutant credit.

## Alternatives and limits

Sequential account-switch tests already pass on the faulty implementation because they miss the validation-to-resolution gap.
Random concurrent loops cannot reliably force it.
Holding the connection lock across remote reads would couple unrelated operations and is unnecessary when the validated session already exists.
A production callback solely to schedule the test would add a runtime seam to every read; the test-only overlay avoids that cost, at the expense of source-coupled test instrumentation and a bounded nested Go test invocation.
Source drift must make that instrumentation fail visibly.

This proves attribution to the captured local bookmark binding, not authentication of the account the operator approved or atomicity with every later connection, bookmark or remote change.
It neither changes mutation authority nor expands browser generation-provenance guarantees.
Other users of `Session()`, account portraits, audit reads and writes, schemas, native acceptance and engine behaviour are outside this slice.
L11, P28 and the valuable-data hold remain open.
