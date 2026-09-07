# Find worker and record lifetimes

This records P08's third implementation under §65.11, §16 and the scan-retention part of audit PERF-01.
The separately reviewed timing and shutdown contract is the behaviour oracle.
Pagination and frontend Find lifetimes were delivered in PRs #40 and #41; this record covers the Go service and its harness integration.

## Result and scope

Find now admits at most four actual workers and keeps at most sixteen records, including running scans.
At the record limit, admission evicts the oldest completed record, ordered by completion time and then admission order.
Worker overload refuses admission before evicting anything.
Every record has one five-minute idle timer, renewed by a successful poll before its deadline.
Expiry retires the record without another API call; a poll at or after the deadline cannot revive it.
Cancellation, expiry and eviction release the match buffer and stop its timer, but a worker still returning from a cancelled operation retains its slot.
A private clock makes deadline races deterministic without exposing timing policy to the renderer.

The Wails shutdown hook closes admission, cancels all service-owned workers and releases retained records.
It waits up to five seconds for actual worker exit and reports an error if they remain.
Repeated shutdown is safe; neither a cancellation request nor forgetting a record counts as worker exit.
The separate worker registry includes workers whose records are already absent.
The existing three Find methods, DTOs, 1,000-match limit, literal prefix and case-insensitive substring semantics remain the contract.
Scans remain read-only and ephemeral.

## Reproduction and deterministic checks

Two public-entry-point tests were preserved before changing production code.
On the parent service, the fifth held worker was admitted and the seventeenth completed scan left the oldest record available.
Both failed their intended assertions, compiled successfully and released their fixture gates.
The same two tests pass with the bounds applied.
This parent pairing covers those two defects; it does not claim that the later private-clock tests compile on the parent.

The worker gate uses the existing session admission callback around real Find listing work, preserving the captured bookmark-generation check.
It can hold the actual worker after its context observes cancellation.
The following maintained checks cover the new lifetime contract:

| Behaviour | Evidence |
| --- | --- |
| Worker and retained-record bounds | Fifth concurrent worker refused; seventeen completed scans retain the newest sixteen |
| Running records count; eviction is completed-only | Mixed completed/running scans retain all running IDs; overload preserves existing results |
| Stable completion order | A first-admitted worker completes last; equal completion times use admission order |
| Idle expiry without another API call | Running and completed records, match buffers and timers disappear before a subsequent status call |
| Lease renewal and deadline race | Repeated polls keep one timer, renew beyond old deadlines and refuse a poll whose deadline already passed |
| Queued callbacks | A callback queued while a poll owns the service cannot undo renewal; an old callback cannot retire a replacement record with a reused ID |
| Cancellation and actual exit | Cancel, expiry and shutdown retain all four held worker slots; cleanup checks real exit |
| Bounded shutdown | A forgotten worker held past five virtual seconds causes an error; early success and reopened admission fail |
| Current results | 1,005 matching objects retain 1,000, preserve names/order and report truncation plus the full scanned count |
| Status integrity | Caller edits cannot change retained results; listing errors remain visible and completed throughput stops changing |
| Cleanup | Checked shutdown runs before Find fixture purges and database teardown; repeated shutdown and absent cancellation are safe |

The reused-ID test completes real scans, then forces the possible post-retirement ID reuse under the service lock.
It tests callback identity without replacing the random-ID source or crediting the forced identity as a renderer scenario.
The final focused app suite contains fifteen top-level tests and eighteen leaf cases, including the existing Find test.

## Two-account scenarios and receipt rules

The same three scenario bodies run against independent mock account state and the owned SAIO fixture.
Success verifies literal prefix scope, exact names, sizes and case-insensitive matching.
Refusal verifies a blank query and an unknown scan ID.
Cancellation holds the first exact listing GET response after Swift returns HTTP 200, then calls CancelFind and observes that selected request terminate because its context was cancelled.
The test rejects a timeout, response loss or retry as cancellation evidence and checks worker drain before taking the final snapshots.
All three scenarios compare both owned account namespaces before and after and require no collateral change.
Mock account state separation is a fixture witness, not a claim about authentication isolation.

Find receipts bind the returned scan ID to the same harness and matching status or cancellation invocation.
An asynchronous StartFind ID cannot receive ordinary completed-read credit.
Completion requires its successful terminal status, captured independently of the caller's mutable DTO.
Cancellation requires its matching successful cancellation, observed request termination and absence of the cancelled ID.
The scenario also checks actual service drain.
Twenty-three malformed-receipt subprocess probes verify refusals without emitting scenario credit; these are harness guard tests, not twenty-three product scenarios.
Receipts remain cooperating-author evidence and do not create an anti-forgery boundary.

The action inventory exempts only FindService.ServiceShutdown with the exact expected method signature.
Pinned Wails beta.15 binding generation succeeded with twelve services and forty-nine renderer methods, producing no binding or dependency diff.
Shutdown receives no renderer scenario credit.
The contract adds explicit StartFind and FindStatus refusal requirements, bringing the denominator from 106 to 108.

## Independent broken-safeguard controls

Thirty-two isolated copied-tree variants each break one safeguard.
All thirty-two are caught by assertions; no variant is accepted on a compile error, setup failure, panic, data race, skipped test, incomplete test stream or hung fixture.
The unchanged baseline passes twenty-three top-level tests and twenty-six leaf cases across the app and system-test packages.
Twenty-seven variants run that whole selection.
Worker-exit release runs the two public tests with real bounded shutdown; actual-worker counting and the three shutdown controls use their targeted held-worker cases.

The variants cover worker admission, record admission, completed-only eviction, completion ordering, tie ordering, actual-worker accounting, release on worker exit, retirement cancellation/buffer/timer, poll deadline/renewal/context, timer expiry/deadline/identity, stopping superseded timers, match cap/truncation, copied snapshots, completed throughput, listing errors, shutdown admission/timeout/actual drain, asynchronous-read credit, completion receipt ID/done/error, cancellation receipt ID/request termination and receipt snapshot ownership.
They are targeted counterexamples, not exhaustive mutation coverage of every defensive branch.
In particular, the additional worker identity/context check and repeated shutdown cancellation of already-cancelled forgotten workers also have protection in the underlying iterator and retirement paths.

The initial cancellation-fixture attempt failed while saving a storage endpoint override without the required fixture key.
It was corrected to use the owned account's key.
That setup failure is preserved separately and receives no regression or scenario credit.

## Verification and limits

The full internal race run with SAIO and the pinned official rclone passes 504 top-level tests and 979 leaf cases across twenty packages.
The one skipped test is the separately gated rclone compatibility matrix.
Combined internal Go statement coverage is 84.2%.
FindStatus, CancelFind, renewal, retirement and shutdown have 100% statement coverage in this run; StartFind has 88.5%.
These percentages supplement the behaviour and broken-safeguard checks.

The full Go race suite without SAIO also passes, with its environment-dependent skips reported separately.
Go formatting, vet, full build, internal build without CGO and binding generation pass.
Frontend sources and dependencies are unchanged from reviewed PR #41; its built assets were used for the Go embed.
Every PR still requires fresh frontend, Go, SAIO, smoke and aggregate success on the latest head with current master incorporated, followed by protected merge and master verification.

Scenario reconciliation consumes the real successful producer exit and all twenty expected package terminations.
It reports schema 3, valid=true, SAIO configured, 19/108 covered cells and 44/49 actions with gaps.
The six new covered cells are Find StartFind happy/cancel/refusal, FindStatus happy/refusal and CancelFind happy.
Duplicate mock/SAIO witnesses do not multiply credit.
Clock and receipt tests and frontend cases are accounted separately.

Local raw evidence is preserved under /tmp/aeronautes-remediation-20260905 with the p08-scan prefix: public-before/public-after JSON and exit files, app-final, saio-full with coverage, scenario-coverage, suite-summary, bindings-final and controls-v1.
The controls runner and each copied variant retain their mutation, event stream, real exit and validation result.
The full SAIO run includes the reused-ID check; the later repeated-poll timer-count assertion is additionally exercised by the passing controls baseline and its failing renewal-timer-stop variant.

This closes the bounded Find lifetime portion of P08 once independently reviewed and delivered.
P22 listing completeness and total UI rows, P24 wider budgets, P10/P11 authority, P13 durable outcomes, P14 concurrent writers, P15 ownership and the valuable-data release hold remain open.
