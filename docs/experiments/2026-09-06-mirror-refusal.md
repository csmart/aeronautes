# Mirror refusal

Date: 2026-09-06.
Scope: the first P05 implementation under [ADR 0020](../adr/0020-recovery-containment.md) and DESIGN §65.11, based on master `737397674edab9f762dadeb2bb4ac1d2ac5a7440` and design commit `d9dc5802ff2f419c`.
Copy overwrite refusal is a separate implementation; this slice does not claim it is safe.

## Behaviour and boundary

Every `TransferService.StartMirror` call returns an explicit unavailable error before validating input, reading credential bindings, authenticating, constructing an engine or creating a job.
It creates no pins, recovery records or audit event and performs no Swift work.
The bridge method and request shape remain compatible with older callers, while the unreachable Mirror execution, deletion and delete-spill code is removed.
The dialog retains read-only comparison, labels destination-only objects without promising deletion, removes confirmation and offers no enabled execution action.
Copy remains a separate selectable flow, and the fake API also refuses Mirror without manufacturing a job.
Current availability and future execution gates are reconciled in DESIGN, REQUIREMENTS, README and the project instructions.

## Before and after evidence

The archived `TestAuditOverwritePreservesLastHopConcurrentWriter` and `TestAuditMirrorPreservesLastHopConcurrentWriter` both fail on the pre-containment runtime.
Copy replaces the competing writer's bytes, and Mirror removes its replacement object while reporting completion.
The archived application probe needed only two cached-session field accesses adapted to the current wrapper in a temporary overlay; its initial compilation failure is not reproduction evidence.
These probes require reaching the unsafe final HEAD, so early refusal makes that mechanism unreachable rather than turning the unchanged probes into passing tests.

`TestMirrorRefusesBeforeWork` checks confirmed, wrong-confirmation, empty and cancelled requests against independent source and destination readers.
It compares exact recovery rows, audit history, jobs, engine construction and Swift request counts, then confirms that both credential bindings can be changed because no pins remain held.
Snapshots cover both selected scopes and unrelated sentinels, a shared backing object and an outside DLO alias with readable bytes.
`TestMirrorRefusesWithoutServices` and `TestMirrorRefusesBeforeReadingBindings` require the same refusal with absent services and a closed database, preventing validation or binding access from preceding the gate.

`TestMirrorRefusesAllWorkRealSwift` repeats the public start against owned two-account Swift fixtures.
It compares the complete harness namespace on both accounts, including new and changed source names, destination-only data, a symlink, real DLO/SLO manifests, shared backing data and outside aliases.
The outside DLO and SLO must assemble the expected bytes before their preservation is credited.
It requires no job or audit change and earns only the refusal scenario.
The selected namespace is task-owned; this is not a claim about account-wide listing completeness.

Three dialog/fake-API tests preserve comparison and Copy selection while rejecting any Mirror start, including a forced click event on the disabled control.

## Verification and catching controls

The full internal race suite passes **471 top-level tests across 20 packages, with zero failures or skips**, using Go 1.27.1, two owned SAIO accounts, official rclone 1.74.3 with its matrix enabled and the required private keychain protocol fixture.
Statement coverage is **83.7%**.
The count falls from 474 because six tests of removed Mirror execution/spill machinery are replaced by three application refusal tests; the real-Swift execution test is replaced one-for-one.
Queue, credential-generation, overlap and recovery tests for supported operations remain in the suite.
The scenario report is valid at **12/106**, with **47/49 actions carrying gaps**.
Mirror has refusal credit and retains its happy, conflict, cancel, partial and retry gaps.

The frontend passes **71 tests across 11 files**, with **30.76% statement coverage**.
Svelte checking reports zero errors and warnings; frontend and application builds, vet, the internal build without CGO and diff checks pass.
Wails regenerates the changed Mirror documentation in the existing bindings, and repeating the required generation task leaves those bindings unchanged.
Its pre-existing module tidy changes are inspected and kept outside this slice.

Temporary variants compile and fail the corresponding maintained regressions:

| Restored behaviour | Failing leaf tests |
| --- | ---: |
| Previous Mirror execution, mock and real Swift | 7 |
| Resolve connections before refusing | 6 |
| Create a job before refusing | 1 |
| Append an audit event before refusing | 1 |
| Construct an engine before refusing | 1 |
| Previous dialog | 2 |
| Previous fake API | 1 |

An initial command sandbox denied local sockets; those setup failures are excluded, and the authorised loopback runs supply the evidence above.
No build failure, panic, fixture failure or process timeout is credited as a catching control.
Detailed logs, overlays, the control runner and scenario report are retained under `/tmp/aeronautes-remediation-20260905/p05-mirror-*` for local review.

## Remaining work

This contains Mirror admission; it does not establish the concurrency protocol needed to restore execution.
Copy overwrite refusal and its partial-result accounting are the next P05 commit.
The remaining P11–P16 work, normal transfer-time cleanup, wider fault coverage, native platform validation and valuable-data release hold remain open.
Independent local approval and all five checks on both the PR and merged master remain delivery gates.
