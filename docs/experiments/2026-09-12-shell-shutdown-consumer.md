# Shell consumer for owned job and Find shutdown

The whole-job ownership prerequisite is now present, but the shell must consume its drain before closing the state database or releasing the instance lock.
This bounded correction covers the five job families and Find.
It does not establish ownership of every synchronous bridge or native request.

## Oracle and before proof

The oracle is DESIGN §§26.4, 32.1, 38, 46, 55 and 65.10, the approved whole-job prerequisite, Find's existing reserved lifecycle hook, and ADR 0020 retention.
The source base is `997e7f0220e9b2b553116bf0ede52e50a7840e81`.
The pinned Wails version is `v3.0.0-beta.16`.

The private probe executes the actual `main` function in a subprocess.
Four narrow interpositions replace the native application constructor with a controlled runtime, supply an isolated session secret store, and observe the unchanged database-close and lock-release calls.
The controlled runtime receives the actual `application.Options` and its twelve actual bound service instances.
The probe changes no admission, cancellation, drain, terminal-state or cleanup-order guard.
The final probe source is preserved in [the source twin](probes/2026-09-12-shell-consumer-probe_test.go.txt), and the three executed packets are reconciled in [the before evidence](probes/2026-09-12-shell-consumer-before.json).

The final race-enabled run executes four leaf cases: one healthy control passes and three assertions fail.
No case skips, fails setup, panics or exceeds its test deadline.

| Case | Observed result |
| --- | --- |
| Normal return without work | Actual shell assembly contains twelve services, no shutdown callback, and closes the database before releasing the lock; healthy control passes |
| Normal return with held work | Actual Find and Delete starts reach two acknowledged HTTP boundaries and survive caller cancellation; actual main then closes the DB and releases the lock while both requests remain uncancelled; a separate process acquires the lock |
| Runtime Run error | The injected runtime error takes the actual `log.Fatal` path; neither resource defer runs |
| Database-close error | A fault returns an error while the real database remains readable; actual main ignores it and releases the lock, which a separate process acquires |

The held-job cleanup subsequently logs the expected closed-database persistence failure.
That is preserved evidence of the failure, not a successful job outcome.
The first packet contains the healthy control and the first two failures.
The second adds the database-close failure, and the third replaces same-process independent lock opens with a separate lock-check process.
All three packets remain recorded; the final packet is the cross-process lock evidence.

These are actual shell assembly and control-flow tests with an injected runtime.
They are not native GTK window-close, Wails `Quit`, signal-driven exit, real rclone, or real Swift acceptance.
The frontend was installed from its lockfile and built before compiling the root package; missing assets were not replaced with a placeholder.
Xvfb was unavailable for a native headless quit probe.

## Correction contract

1. Extract only Find's existing stop-under-mutex operation into a private helper returning its existing drained channel.
   Preserve its admission closure, cancellation, worker ownership and the existing `ServiceShutdown() error` hook's five-second clock-based wait.
   Add no renderer method and no Wails dependency inside `internal/app`.
2. Add the concrete package function `ShutdownWork(ctx, runner, find) error`.
   Stop and cancel Find before waiting, then invoke the approved runner shutdown and wait for the same actual Find drained channel.
   Both kinds of admission stay closed if a waiter is cancelled or its deadline expires.
   Return the wrapped context error on an incomplete wait; a failed wait never authorises resource release.
   An already-complete drain may succeed even when the waiter's context is no longer usable.
   Add no total shutdown deadline.
3. Retain the actual Find instance when assembling the twelve bound services, and wire the same complete drain into the real `application.Options.OnShutdown` callback and the post-Run cleanup path.
   Keep one concrete shell owner, with an idempotent drain shared by repeated or concurrent callbacks and final cleanup.
   The shell uses a fresh background context, independent of the cancelled Wails or browser context.
   A held job or Find worker keeps shutdown blocked with the DB and lock held.
   The current drain returns only context-wait errors, so this background caller cannot time out.
   If a future or injected drain nevertheless returns an error, report the error and remain blocked with resources held; logging is not evidence of a successful drain.
4. Keep resource cleanup after Run returns, after the shared drain has completed and the remaining Wails service hooks have had their existing opportunity to run.
   Call database Close exactly once and release the instance lock only when that call returns nil.
   A Close error is not proof of closure: wrap and latch it, preserve it alongside any Run error, and do not explicitly release the lock or retry Close to obtain an idempotent nil.
   Keep the concrete lock owner reachable through the top-level error exit, including garbage collection during failure reporting.
   The top-level main may retain its existing nonzero error exit after owned work has drained, with kernel process exit providing the final release after a database-close failure.
   This is not a graceful-close success or a quit-while-work-is-active policy.
5. Route post-lock startup errors and Run errors through one checked return/cleanup path.
   Keep top-level version output and pre-acquisition failures unchanged in meaning.
   A failure before a database handle is returned has no admitted work to drain and still releases the acquired instance lock.
   Use one shared build-and-run function called by main and maintained tests, with narrow native-launch and secret-backend constructor inputs for isolated tests.
   Tests must receive the exact Options and service instances built for the real caller; do not recreate their assembly or resource ordering in a test-only model.
6. Keep all existing job outcomes, persistence failure latches, caller detachment, child grace, retained-stage policy and reserved Find hook semantics.
   A successful ownership drain does not prove durable job persistence.
   Do not expand this correction into synchronous bridge admission, Connections or SecretStore lifecycle, descendant containment, or another native platform's exit policy.

## Maintained evidence and negative controls

Use table-driven race tests through the shared shell caller for normal Run return, Run failure, repeated and concurrent callbacks, no-work cleanup, startup refusal, held jobs, held Find workers and database-close failure.
While either owner remains held, require a usable first-process database and refusal by a separate process to acquire its state lock.
Observe the actual final job pin/handle release and Find worker exit before checked database close and lock release.
For the Close-error control, keep the real database readable, force garbage collection while the error exit is held, and require the lock to remain unavailable until that process exits.
Keep the error visible and distinguish kernel release on failure exit from successful explicit release.

Test `ShutdownWork` cancellation and deadline failures separately from the background production callback.
Both admissions remain closed and a later valid waiter must still observe the real complete drain.
Keep Find's existing deterministic five-second reserved-hook timeout tests passing.
Independently remove the shell hook, Find drain, whole-job drain, resource-order guard, close-error refusal and complete-callback wait; each relevant maintained assertion must fail with healthy controls passing.

Add a Linux system test around the same `ShutdownWork` function with the pinned real direct child at an acknowledged write boundary.
Exercise graceful cancellation and the existing ten-second forced escalation.
Require real child exit/reaping, stable later filesystem state, unchanged remote namespaces, exact terminal or persistence-failure records and separate-process lock/recovery checks.
Retain the distinction between this real-child application-consumer test and the actual shared shell wiring tests; neither alone supplies native window-quit proof.
Run owned mock and actual two-account Swift cases separately, propagate skipped inner cases, and record selected executed, skipped and failed leaves.
The bound-service inventory has a closed action/scenario vocabulary and is unchanged by this lifecycle correction.
Keep that inventory byte-identical and claim no new action-coverage cell, including no invented Application shutdown or CancelJob credit.
Coordinate the fixture and full-suite slots before acceptance.

| Lifecycle scenario | Required evidence |
| --- | --- |
| Normal Run return | Actual shared shell caller drains held jobs and Find before DB close and lock release |
| Run error | The same caller drains and closes safely while preserving the runtime error |
| Graceful child cancellation | Actual pinned child exits and is reaped before the shared consumer permits resource release |
| Ten-second escalation | The existing grace is respected, the held direct child is killed and reaped, and retained state is unchanged by later recovery |
| Database-close failure | Real DB remains readable under the fault, failure remains visible, and no explicit or GC-driven lock release occurs while the failing process is held |
| Lock and recovery | Another process is refused while ownership remains, then can lock and perform idempotent recovery after the permitted release or failure exit |

## Scope and delivery limit

Production scope is `internal/app/findservice.go`, new `internal/app/shutdown.go`, `main.go` and a concrete root `shutdown.go`.
Dedicated maintained tests belong in `internal/app/shutdown_work_test.go`, root shutdown test files and `internal/systemtest/shutdown_linux_test.go`.
Only the bounded DESIGN sequence, this experiment and dedicated evidence twins accompany the code.
Shared bound-action scenario metadata remains byte-identical; the table above records the non-credited lifecycle scenarios.

The ownership prerequisite cannot ship alone and must accompany this independently reviewed consumer in the eventual delivery.
No consumer implementation approval is implied by this contract.
Root delivery still requires final-base verification, independent review, all five PR jobs, protected merge and all five resulting-master jobs.
Synchronous bridge/native-request ownership, Connections/SecretStore closure, retained-stage removal, non-Linux Run-not-return paths and full native shutdown acceptance remain open.
DS-07, SEC-06, P20/P30 and the valuable-data release hold remain open.

## Implementation evidence

The implemented caller now uses the same `ShutdownWork` drain from its actual shutdown callback and its checked post-Run cleanup.
The maintained root tests receive the twelve service instances from that same assembly.
The final focused race baseline passes 39 leaves: 21 new app/root cases and 18 existing Find cases, including the original reserved-hook behaviour.
Four isolated root cases hold a real download after its engine finishes, cancel through the actual shell callback or Run return, and check the live database, cross-process lock, final bookmark pin and native directory handles.
The held stage deliberately survives cancellation, so its separate nonempty-directory cleanup failure produces the existing partial outcome with zero placed objects and one failed item.
The first four assertions incorrectly expected cancelled; that failed packet and source remain preserved, and the corrected assertion requires the exact existing counters and both error reasons.
Production cancellation outcomes and retained-stage policy were not changed.

The two new real-child mock cases pass, and the two selected actual-Swift cases pass with no failures or skips.
The actual fixture run requires both owned accounts and completes in 17.534 seconds: graceful cancellation takes 3.27 seconds and forced escalation takes 13.25 seconds.
The parent uses the same `ShutdownWork` function, then proves the pinned child has already been reaped by observing `ECHILD` from `Wait4` before exposing the drain result.
The checks preserve the exact 64 KiB write boundary, ten-second grace, terminal records, later filesystem state, both remote namespaces and idempotent recovery.
The separate root tests establish the shared shell wiring; these native-child checks do not run a GTK window or Wails `Quit`.

The private actual-main close-error proof has four narrow interpositions, documented in [the evidence twin](probes/2026-09-12-shell-consumer-after.json) and [its probe source](probes/2026-09-12-shell-consumer-gc_test.go.txt).
During actual `log.Fatal` reporting, five completed GC/finalizer barriers leave the real database readable and refuse another process the instance lock.
After the failing process exits with status one, the kernel releases the lock.
Removing only the deferred `KeepAlive` and its unused import makes all five lock probes acquire while the database remains readable, and the assertion fails.
The maintained close-error case separately checks the latched original error, one Close attempt and a live database and lock.
The maintained unexpected-drain-error subprocess verifies that the actual caller remains blocked with its resources held after logging the error.

All nine omission controls fail their intended assertions without a panic, race report, setup failure or test timeout.
Their exact selected leaves, healthy controls, source hashes and stdout/stderr hashes are recorded in the evidence twin.
They independently remove the actual callback, Find stop, Find join, job join, drain-before-close ordering, failed-Close unlock refusal, concurrent callback join, unexpected-error block and GC owner lifetime.
Focused coverage reports 100 percent for `ShutdownWork`, Find stop and reserved hook, and checked shell Close; 95.3 percent for the shared caller; and 66.7 percent for shell wait because child-process coverage is not merged into the profile.
The blocked-error and actual-main paths have separate behavioural evidence; native launch and window code remain untested here.
`go vet` on the three affected packages, `gofmt`, `goimports` and `git diff --check` pass.
`staticcheck` was not available and was not run.
The bound-service action inventory remains byte-identical, and no action-coverage cell or broader shutdown clearance is claimed.
Independent implementation approval and the root's final combined delivery gates are still required.
