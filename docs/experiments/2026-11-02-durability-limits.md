# Durability limits: crash recovery and power loss

This record states what Aeronautes guarantees for the durability of its valuable local state — the SQLite database of bookmarks, jobs, audit events and recovery records — across an abrupt crash and a power loss, and the boundary of that guarantee.
It is the durability residual the [register reconciliation](../audits/2026-10-18/register-reconciliation.md) names (DS-01) and the durable-recovery gate of the valuable-data hold.
It consolidates evidence that already exists rather than adding new mechanisms or tests, and re-runs that evidence on current master to confirm it still holds.

## What is guaranteed

**A committed write is durable across power loss.**
Every connection opens the database in WAL mode with `synchronous=FULL` (`internal/db/db.go`), so a commit is not acknowledged until SQLite has flushed the write-ahead log to disk with `fsync`.
A commit that was acknowledged therefore survives a later power loss, and recovery validates each commit frame and its pages with the WAL salts and cumulative checksums, so the recovered state is a complete committed state — never a torn or partially-applied transaction.
A power loss *during* a commit, before it was acknowledged, leaves that transaction wholly present or wholly absent; its outcome is not promised either way, but it is never partial.
Writes are serialised through one writer (`WriteTx`) while many readers run concurrently under WAL, so there is no second, unsynchronised write path.
`internal/db/durability_test.go` holds this as a contract: `TestConnectionsUseDurableWAL` requires `journal_mode=wal` and `synchronous=FULL` on every pooled and replacement connection, and `TestConnectionRefusesIneffectiveDurability` refuses to open with `synchronous=NORMAL`, `synchronous=OFF`, or a non-WAL journal — any of which would weaken power-loss durability.

**An abrupt in-flight crash retains its data and recovery records, and the recovery logic recovers them idempotently.**
`internal/systemtest/child_lifetime_linux_test.go` (real-Swift variant `TestChildLifetimeRealSwift`) runs a transfer against Swift, `SIGKILL`s the process mid-flight, and verifies the retained partial is exactly the object's leading bytes.
A separate process — the test helper `TestLifetimeParentHelper`, not the desktop shell — then acquires the same instance lock the shell takes at startup, opens the same `state.sqlite`, and runs `jobs.Store.FailInterrupted`, the recovery `main.go` runs on every launch.
It asserts the interrupted job becomes `failed`, the recovery is counted once and is idempotent (`Interrupted=1`, `Repeated=0`), the item, totals and audit rows are unchanged, the retained filesystem is unchanged, no execution or verification outcome is invented, and the error begins "interrupted by shutdown; data and recovery records retained" (a download adds that its remote residue is unverified).
Because `FailInterrupted` records `interrupted_at`, a recovered job is surfaced by `jobs.Store.InterruptedDisclosures` (ADR 0088) — asserted directly by `TestInterruptedDisclosuresSelectsUndisclosedNewestFirst` and `TestInterruptedDisclosuresReportsPerEntryFacts` in `internal/jobs`, which query the store rather than start the shell.
The startup wiring that joins these is present — `main.go` runs `FailInterrupted` at launch and the shell reads `InterruptedDisclosures` — but no test here drives the real desktop process through an abrupt crash and its next start end to end; that end-to-end desktop-start measurement is the one part of the residual that remains unmeasured (see below).

## The boundary

- **fsync honesty.** The power-loss guarantee assumes the operating system, filesystem and drive honour `fsync` — that a returned `fsync` means the bytes are on stable media.
  On storage that does not (a write-back cache without barriers, or a device that lies about flush), neither Aeronautes nor any database can guarantee power-loss durability.
  This is a documented limit, not a defect, and it is the operator's environment to control.
- **Non-Linux targets.** The abrupt-crash recovery above is measured on Linux (the Tier-1 platform).
  macOS and Windows startup recovery are unmeasured and belong to the platform work in 0.7.0.
- **Scope.** This covers the valuable local state in SQLite. Credentials live in the OS keychain, not here (K-SEC-003); an in-flight keychain write interrupted by a crash is governed by ADR 0055's bounded-operation contract and is re-enterable, so it is not valuable-data loss.

## Evidence

Re-run on 3 November 2026 against master `7afbca61` and the Linux SAIO fixture (two TempAuth accounts, `AERONAUTES_REQUIRE_TWO_ACCOUNTS=1`):

- **Power-loss contract.** `go test ./internal/db -run 'TestConnectionsUseDurableWAL|TestConnectionRefusesIneffectiveDurability'` passes: WAL with `synchronous=FULL` on both the initial and a replacement connection, and open refused for `synchronous=NORMAL`, `synchronous=OFF` and a `DELETE` journal.
- **Recovery and disclosure.** `go test ./internal/jobs -run 'TestFailInterrupted|TestInterruptedDisclosures'` passes: a job driven to `running`, recovered once and idempotently by `FailInterrupted`, then returned by `InterruptedDisclosures`.
- **Abrupt crash against real Swift.** `go test ./internal/systemtest -run '^TestChildLifetimeRealSwift$'` passes all five modes. In `parent_kill` and `stopped_child_parent_kill` a download is `SIGKILL`ed mid-flight — the job is `running` with a 64 KiB leading partial retained in its stage — and a separate helper process (not the desktop shell) reopens the same state under the instance lock and reports the job `failed` with `Interrupted=1`, `Repeated=0`, the item and totals intact, and the interruption error above.

## For the valuable-data hold

The valuable-data release hold is held by two things ([road to 1.0.0](../audits/2026-10-27/road-to-1.0.0.md)): the artefact and deployment acceptance evidence that remediation P31 to P35 require, and this durability residual.
This record consolidates the residual and bounds it. The power-loss contract (WAL + `synchronous=FULL`) and the abrupt-crash retention, idempotent recovery and disclosure components are evidenced on Linux, within the fsync-honesty and non-Linux boundaries above.
What remains of the residual is the end-to-end measurement the register named: the real desktop process starting after an abrupt in-flight crash and surfacing the recovered interruption through its own startup, rather than through the component tests and the helper reopen that stand in for it here. That is an acceptance measurement, not an unbuilt mechanism.
So the residual is narrowed, not discharged; the hold stays in force and is lifted at 1.0.0 on the acceptance evidence those gates require.
