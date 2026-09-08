# Terminal persistence reproductions — 2026-09-08

## Source and method

Source was current master `d2659015259f5cfda64c360bd9eb13af5fa66bce`, in an isolated clean worktree.
Temporary Go overlays added probes without changing application code.
Go 1.27.1 ran each selected test once under the race detector, using the pinned rclone 1.74.3 and Aeronautes-owned mock/SAIO fixtures.
The pure service probe supplied reports directly to the actual local finaliser; it is not transfer, cancellation, crash or power-loss evidence.
The second probe called the real picker and StartUpload service, using actual engine/native writes and independent whole-account and local-file snapshots on both providers.
SAIO used two separate test accounts; both account sentinels and unselected local state remained unchanged.
Each selected write refusal used an SQLite abort trigger in the test database.
These are rejected SQL writes, not simulated disk corruption, failed fsync or lost commit acknowledgement.

## Observed results before remediation

| Refused write or supplied outcome | Persisted result |
| --- | --- |
| Final counters | Complete job and applied audit, with advisory copied/byte totals and zero verified objects instead of the final snapshot |
| Error text | Partial job with one counted failure and an empty error field |
| Terminal state | Non-terminal verifying job, with a separate complete/applied audit |
| Terminal audit | Complete job with no terminal audit event |
| Planning state | Execution body ran; job remained queued while the audit said complete |
| Running state | Execution body ran and the job later became complete despite rejected admission |
| Upload start audit | Actual upload completed despite rejected start accounting |
| Earlier failure, later error | Failure count survived; only the later error remained in the error text |
| Earlier failure, later cancellation | Failure count survived; cancelled state and empty error text hid the earlier failure |

The finaliser probe completed nine cases plus its parent, with no failed or skipped tests.
It recorded the inconsistent current behaviour rather than asserting that behaviour was safe.
All cases released their cancellation registration.
The actual upload probe completed seven cases per provider, fourteen leaf cases and two parents, with no failures or skips.
The final `terminal-system-probe-baseline-v2` run observed the runner's active-body count under its mutex through a temporary read-only overlay before reading final state.
An initial run sampled some separate audit appends too early after the terminal job row appeared; its timing-dependent audit counts are not used as final-state evidence.
Every upload, including each rejected admission case, created the selected live object with the exact expected bytes.
Only that live identity changed in the primary account; the complete secondary account and local input snapshot remained unchanged.
The actual upload probe did not cover error-text refusal or earlier-failure/later-error/cancellation combinations; those remain direct-finaliser observations until maintained service regressions are added.

## Evidence and next step

Raw commands, exact source/overlay hashes, JSON test events, exit codes and extracted observations are retained locally under `/home/csmart/code/.aeronautes-work/20260908/terminal-*probe-*`.
The system-test observation parser needed to join Go test output chunks before decoding a long JSON line; that extraction correction did not change the probes or their successful producer exits.
[ADR 0024](../adr/0024-local-job-completion.md) defines the bounded atomic-summary and admission follow-up.
Its first implementation does not close P12/P13, establish per-identity outcomes or lift the valuable-data release hold.
