# Named-read session snapshot — 21 September 2026

The oracle is the account-specific result requirement in frozen L11/P28, with the read bindings in [ADR 0049](../adr/0049-account-scoped-reads.md) and [ADR 0059](../adr/0059-account-scoped-remaining-reads.md).
Those earlier decisions explicitly left session capture open.
[ADR 0066](../adr/0066-named-read-session-snapshot.md) proposes that bounded follow-up; this record does not claim it is implemented.

## Source and observation

On `eeac79963318f76b1f032a8c1e2aeebbb8000504`, `internal/app/connections.go:357` validates the caller's bookmark and the generation of `sessionEntryFor`, then calls `Session()` again at line 375.
That second lookup reads whichever connection is now active.
The helper serves two listing methods, five inspector methods and Find start.
An inspector can also record evidence under the returned scope; this is a source-inspected risk, not a separately reproduced durable-ledger result.

The sealed private assessment is `/home/csmart/code/.aeronautes-work/20260921-scoped-session-probe/ASSESSMENT.md`.
Its nineteen-file `SHA256SUMS` manifest hashes to `151edb933b80ad92fe8812a214d9cf3d5deafcf9410a24d77b6762cf3f3213d3`.
It retains the source checkpoint, exact commands, load and concurrency observations, raw Go JSON results and the observation diff.

The controls use unchanged production source and exercise eight reads from each account against mock HTTP and owned two-account Swift: all 32 leaf cases pass.
The scheduled run inserts only a context callback after the generation comparison and before the second session lookup.
While the original read waits there, the test completes public `Connect` to the secondary account and confirms a named read there, then releases the original call.
All sixteen switch-account cases fail: secondary payloads carry the primary bookmark and generation.
The 32 no-switch and same-bookmark reconnect controls pass.
The run used `go test -race -count=1 -json`; there were no skips or race-detector reports.
Independent snapshots found no remote namespace changes from the reads.

An earlier fixture attempt used the wrong metadata-key casing and mock SLO representation; its failure remains in the packet and receives no defect credit.
The corrected controls and scheduling proof use the same corrected probe source.
The owned Swift fixture used a pinned image and a local XFS subdirectory, not CI's dedicated filesystem.

## Proposed correction and limits

Return the validated `entry.session` with its bookmark and generation, preserving the existing refusal and request-admission checks.
Maintain the scheduled test through a fail-closed test-only overlay launched by ordinary system tests, so the narrow gap is exercised without a production testing callback.
Keep uninstrumented controls, independent namespace witnesses and caught behavioural mutants beside that proof.

The overlay makes the interleaving deterministic; it does not establish spontaneous race frequency or reproduce an uninstrumented desktop session.
This slice does not close L11, P28, native acceptance or the valuable-data hold, and it grants no mutation authority.

## Implementation — 22 September 2026

`scopedSession` now returns the validated entry's own session with the scope built from that entry's bookmark and generation; the second `Session()` lookup is gone, and the empty-bookmark, disconnected, other-bookmark and generation-mismatch refusals are unchanged.
The eight callers, their signatures, DTOs, continuation checks and request-time generation admission are untouched.

The maintained proof is `internal/systemtest/named_read_snapshot_test.go` with `named_read_overlay_test.go`.
The controls run in the ordinary test binary: eight named reads from each account with account-distinct payloads under colliding names, against mock HTTP and the owned two-account Swift fixture, with independent namespace snapshots before and after.
The scheduled cases run in an instrumented child: the ordinary entry point copies `internal/app/connections.go`, inserts one `namedReadCheckpoint(ctx)` call after the generation comparison of `scopedSession`, adds a package-app file defining that call as a pause taken from the read's context, and runs `go test -race` for the same test name with a Go build overlay and a child marker in the environment, then reports the child's terminal result and output.
A missing or duplicated insertion anchor, a build failure, a missing result or an unreached barrier fails the parent; a child skip skips it.
The child pauses each of the eight reads after validation, then completes a same-bookmark reconnect, an other-bookmark Connect or a key re-entry on the read's own bookmark before releasing it, beside a no-switch control.
The switch schedule asserts the primary payload under the primary scope; the edit schedule asserts the read is refused by request-time admission ("bookmark changed during the operation") rather than served through the captured session.

On the fixed source all four tests pass on both fixtures, 34 leaves, race on.
Against the retained mutants the scheduled mock test fails as follows: the committed pre-fix source and a restored second `Session()` lookup misattribute all eight switch-account reads; returning the connection active at return time as the reply scope loses the requested scope on seven reads; a no-op request-time admission serves all eight edited-bookmark reads through the stale session.
Removing the generation comparison is refused by the instrumentation rather than caught behaviourally, and no public path distinguishes it from the fixed source: `guardCache` already refuses a cached entry whose generation the store has moved past, and `forgetObsolete` then drops both that entry and the active connection, so a validated entry cannot differ in generation from the active connection; the comparison stays as a defensive check.
The runs, the mutant sources and their streams are retained privately in `.aeronautes-work/20260922-named-read-implementation/` with `SHA256SUMS`.

The child build adds one overlay compile of `internal/app` and the system-test binary per scheduled test; locally that cost about five seconds per test with a warm cache.
No finding closes: L11 and P28 keep their remaining named-read and mutation items, and the valuable-data hold stays on.

## Follow-up, 23 September 2026: the sequential rekey

`internal/systemtest/reader_generation_test.go` (`TestReaderGenerationMock`, `TestReaderGenerationRealSwift`) now maintains the sequential case the schedules above do not: after the connected bookmark is re-keyed, every one of the eight named reads refuses as not connected until the bookmark reconnects, and each then answers the same account under a higher generation.
`TestInspectorReadsAreAccountScoped` in `internal/app` gains the SLO manifest and DLO segment readers for the other-account refusal and the scope the reply states.
This proves the backend half of the six adjacent readers' acceptance basis; the frontend generation acceptance ADR 0067 left source-inspected is still open.
