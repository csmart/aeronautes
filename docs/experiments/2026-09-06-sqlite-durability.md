# SQLite durability prerequisite — 6 September 2026

## Scope and oracle

This is the first implementation slice of [ADR 0015](../adr/0015-credential-bindings.md).
External keychain work must not outrun the SQLite record that makes it recoverable.
Require the effective settings `journal_mode=wal` and `synchronous=2` (FULL) before a physical connection can perform application work, including schema migration.
The existing driver applies the requested settings on every connection; a connector wrapper also reads their actual values before admitting that connection to the pool.
An unavailable WAL mode, weaker synchronisation or failed settings query refuses the connection.
The driver, schema migrations, writer serialisation, foreign keys and busy timeout are otherwise unchanged.

[SQLite's synchronous contract](https://www.sqlite.org/pragma.html#pragma_synchronous) distinguishes WAL NORMAL from FULL when an operating system crash or power loss occurs.
[Its WAL documentation](https://www.sqlite.org/wal.html#performance_considerations) explains the extra commit synchronisation and its cost.
These guarantees depend on the filesystem and storage device honouring synchronisation.
This change does not implement the credential journal, make SQLite and a keychain one transaction, or prove the P12/P13 destructive-recovery and audit ordering.

## Regression evidence

`TestConnectionsUseDurableWAL` holds four distinct connections and repeats after closing them with idle reuse disabled.
Before the change, all eight reported `journal_mode=wal synchronous=1` and failed the FULL assertion.
`TestOpenRefusesMemoryJournal` also failed before the change: Open accepted an in-memory database whose journal cannot become WAL.
Both now pass.
`TestConnectionRefusesIneffectiveDurability` uses real SQLite connections configured with WAL/NORMAL, WAL/OFF or DELETE/FULL and requires refusal.
The database race suite also covers foreign-key enforcement, concurrent serialised writers, reopening and upgrades from every historical schema.
The full core race run passed 369 top-level tests in 19 packages with zero skips, using two disposable Swift accounts and the verified official rclone 1.74.3 sidecar, including its explicit matrix lane.
Statement coverage was 82.3%; `go vet ./internal/...` and the whitespace check passed.

These are effective-setting and compatibility tests.
They do not simulate a power cut or claim new action/scenario coverage.
The reconciled scenario report is valid with 8/102 cells covered and gaps in all 47 actions.
Native keychain recovery, process-crash barriers, actual power loss and the six-platform durability evidence remain separate work.

## Measured cost

`BenchmarkWriteTx` inserts one synthetic 1 KiB non-secret record per transaction through the real writer API.
Setup, migrations and close are outside the timed loop.
Each measurement uses a fresh database, 500 commits and five repetitions without the race detector.
The comparison changes the previous NORMAL setting to the new WAL/FULL configuration and connection checks.
The baseline is the pre-implementation code at `4881e023` with the same benchmark added.

The test ran on Linux amd64 with Go 1.27.1 and the pinned modernc SQLite 1.57.0, on XFS over the workstation's encrypted block device.
`TMPDIR` placed the benchmark databases on that XFS source volume, rather than the container's `/tmp` overlay.

| Configuration | Median per commit | Observed range | Allocations per commit |
| --- | ---: | ---: | ---: |
| WAL/NORMAL | 13.0 μs | 10.1–15.4 μs | 15–16 |
| WAL/FULL with effective-setting checks | 5.80 ms | 5.26–7.13 ms | 15–16 |

This substantial local cost is expected to depend on storage latency and workload; it is not a six-platform forecast or an end-to-end transfer benchmark.
Later batching or reduction of redundant writes must retain the durable ordering of safety records before external effects.
Do not recover throughput by silently weakening their synchronisation.

Reproduce the current configuration with a writable temporary directory on the filesystem being measured:

```sh
TMPDIR=/path/on/test/filesystem go test -run '^$' \
  -bench '^BenchmarkWriteTx$' -benchtime=500x -count=5 ./internal/db
```
