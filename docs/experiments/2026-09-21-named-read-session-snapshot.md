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
