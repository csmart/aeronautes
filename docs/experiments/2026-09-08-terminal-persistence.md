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

## Maintained implementation and evidence

The local slice implements ADR 0024 in the shared jobs/audit stores and the upload/download runner.
Final state, counters, error and finish time share one transaction with the terminal audit and its existing retention policy.
Rejected completion writes latch a fixed non-secret diagnostic, refuse new job records and make job Get/List unavailable.
Planning, running, start-audit and upload recovery-admission failures stop before execution; a refused verification transition cannot produce a clean completion.
Queued local cancellation uses the same atomic completion path and releases its cancellation registration and credential pins.
Other queued job families retain their prior cancellation accounting, but every queued execution body checks the shared availability latch.
The collapsed drawer reports unavailable status instead of repeating a stale active-job count or saying there are no active transfers.

The maintained proof includes:

- `internal/jobs/completion_test.go`: independent durable-state reads after refusal of each final field, audit append or retention; audit validation, illegal/missing/closed-store cases, the matching successful snapshot and prevention of terminal replacement.
- `internal/app/localcompletion_test.go`: local admission and verification refusals, shared-latch queue admission, cancellation deregistration and six actual upload/download queued-cancellation cases with acknowledged credential-pin release.
- `internal/systemtest/local_completion_test.go`: forty leaf cases through actual upload/download services on mock Swift and two-account SAIO, including plain and DLO controls, admission refusals, terminal-write failures and refused subsequent starts.
- Complete independent account and local-file snapshots, exact plain/DLO bytes and backing identities, untouched other-account/other-job/segment-store sentinels, exact recovery identities and terminal audit bookmark/job/result checks.
- A separate process reads the exact persisted state, applies the existing interrupted-job handling and returns both snapshots for comparison, with no new service storage requests or collateral changes.
  The injected refusal is removed before the checked close/reopen; this models a usable database at restart, not successful repair after a real disk failure.
  Missing terminal observations remain missing; restart preserves advisory counters and retained facts rather than inventing a completed outcome.
- Two frontend regressions prove visible unavailability with either an empty or stale active-job snapshot, including the collapsed drawer.

The final full internal run passes 601 top-level tests and 1,508 leaf cases in twenty packages, with no failures or skips and 85.2% statement coverage.
It uses the pinned engine, required keychain fixture and two-account SAIO under Go 1.27.1 with `-race -count=1`.
All tested Go source hashes match the implementation after that run.
Scenario reconciliation is valid at schema 3 and remains 32/110 cells, with 38/49 actions having gaps; these regressions do not invent additional scenario credit.
All 852 frontend tests pass across 22 files, at 79.33% statement coverage; Svelte reports zero errors and warnings.
Frontend build, Go vet, Linux native build, the CGO-disabled internal build, patched Swift source checks and upstream Swift core race tests pass.

The parent local-caller overlay fails all 34 injected service-fault cases and four of six no-fault cases that expose its existing job-before-audit visibility gap; two no-fault cases pass.
This restores the exact old local caller while retaining the new primitive and test APIs, and is not described as a full parent-tree build.
Deliberately broken variants also catch lost final counters/error, omitted or separately committed audits, missing/unsafe diagnostics, ignored admission and admitted new/queued work, and a hidden UI error.
The original direct-finaliser probes and current-master upload probes remain separately retained as pre-change observations.
Raw runs, source/overlay hashes, checks, negative controls and the valid scenario report are retained locally under `/home/csmart/code/.aeronautes-work/20260908/terminal-*`.

Earlier failures hidden by later errors/cancellation, sibling Copy completion, per-identity durable intents/outcomes, real crash/fsync/lost-history evidence and other mutation families remain open.
This bounded summary implementation does not authorise retry, cleanup, overwrite/Mirror re-enablement or valuable-data use.
