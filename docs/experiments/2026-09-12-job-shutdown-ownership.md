# Whole-job shutdown ownership

Date: 2026-09-12.
Status: contract independently approved; the bounded implementation and focused evidence await independent implementation review.
Implementation base: `836ad0d2138f51741c65de948bab5c5edeb2f3f8`.

## Oracle and scope

DESIGN §§26.4, 32.1, 38 and 46 require explicit cancellation and honest job outcomes.
The SEC-06/P30 shutdown requirement needs owned job bodies and their children to finish before shell-owned state is closed and restart recovery can enter.
DESIGN §65.10 requires real process evidence for native interruption claims.
ADR 0020 keeps uncertain stages, segments and recovery records; this work grants no cleanup authority.
The existing Linux direct-child evidence establishes a child runner boundary, not application shutdown ownership.

The bounded implementation covers `internal/app/jobrunner.go` and admission, cancellation ownership, handoff and refusal cleanup in the five public Start paths in `localtransferservice.go`, `transferservice.go`, `deleteservice.go` and `benchmarkservice.go`.
Dedicated application lifetime and public-service tests and this experiment record are in scope.
`main.go`, Find, synchronous bridge/native-request drain, Connections and SecretStore remain read-only until a separate consumer contract is reviewed and assigned.
Existing terminal outcomes, audit failure handling, credential generations, concurrent-job budget, caller detachment, child grace and retained-stage behaviour are preserved.

**This is a prerequisite that cannot ship alone.**
The actual shell caller and safe resource-order consumer must receive their own reviewed contract and accompany this prerequisite in the same eventual delivery.
The prerequisite's unit and service tests cannot prove that an unmodified shell calls its drain.
Neither the prerequisite nor a job-only shell hook closes DS-07, SEC-06, P20/P30, native-platform acceptance or the valuable-data hold.

## Preserved before observations

The archived [probe](probes/2026-09-12-job-shutdown-ownership-probe_test.go.txt) uses the existing runner and actual download service with owned SQLite databases, a literal-name HTTP mock, deterministic barriers and a separate recovery process.
The [evidence twin](probes/2026-09-12-job-shutdown-ownership-before.json) records all three attempts, their return codes and exact leaf outcomes, source hashes and stream hashes.
These runs used frozen head `1f57000cf0a6f3788ae8e8436fd57a4a6ffd008f` with `go test -race -count=1 -timeout=60s -json -overlay <probe> -run '^TestPrivateShutdown(ResourceOrder|PublicContext)$' ./internal/app`.
No shared Swift fixture or native child engine was used.

The resource-order model deliberately holds one runner boundary while applying the current shell database-close and instance-lock-release order.
It keeps the first process alive to inspect that boundary and invokes the production `FailInterrupted` recovery in a separate process after releasing the lock.
This is a runner/resource-order model, not a native Wails quit, proof that a process survived return from `main`, or evidence of remote corruption.

| Held owner | Active slots | Cancel registrations | DB close returned | Second process acquired released lock | Recovery while owner remained held |
| --- | ---: | ---: | --- | --- | --- |
| Running job body | 1 | 1 | Yes | Yes | One running row became failed; repeat count 0 |
| Terminal body cleanup | 1 | 0 | Yes | Yes | Cancelled row remained cancelled; count 0 |
| Final pin-release callback | 0 | 0 | Yes | Yes | Cancelled row remained cancelled; count 0 |

All three model observations passed on every attempt.
Their success exposes the insufficiency of those signals as a join; it is not passing shutdown safety acceptance.

The public-service comparison cancels the caller context at an acknowledged mock download engine barrier and invokes the existing Find shutdown hook.
Without `CancelJob`, the detached job still completes, places all 17 bytes and records one copied and verified item.
With the production `CancelJob` call at the same barrier, it ends cancelled with zero placed or verified files.
This preserves the intended asynchronous caller contract and shows why application cancellation needs its own explicit ownership.

The first two aggregate runs failed the public completion control because the reused `httpEngine` concatenated an empty prefix into `GET shutdown-probe//file`, which the literal-name mock correctly refused with HTTP 404.
The second run exposed the complete result; the final probe uses the established `scope/file` and `scope/` fixture convention without changing production or the completion/cancellation assertions.
Both initial exit-1 results remain recorded and supply no successful public completion evidence.
The corrected final attempt exits 0 with five passing leaves, no failure or skip and 5.538 seconds in the Go package, or 7.621 seconds including tool overhead.

The inspected lifecycle sources are identical at the implementation base except for the independently approved Copy `NoGzipEncoding` constructor option in `transferservice.go`.
That option does not change Start ownership; the before run is still attributed to its actual frozen head rather than relabelled as complete combined-base validation.

## Concrete ownership contract

1. A start must atomically acquire a runner-owned lifetime before credential pinning, session acquisition, preflight, engine construction, durable job creation or registration.
   The same lock serialises acquisition with the first stop, so a start is either admitted and included in the drain or refused explicitly without these effects.
   Ownership of an admitted synchronous start continues until every acquired resource has been released on refusal or transferred to its queued/running job.
2. Each owner has an explicit cancellation source independent of the request caller and cancellation registry.
   During synchronous startup, both caller cancellation and owner cancellation reach startup I/O.
   After successful asynchronous handoff, ordinary caller cancellation or expiry remains detached as before; explicit `CancelJob` and runner shutdown cancel the job.
   Cancellation callbacks are invoked outside the runner mutex, and all context callbacks and ownership state have a final cleanup path.
3. The owner survives cancellation deregistration, a terminal row, terminal persistence failure, active-slot release, child/body return and final pin or local-directory-handle cleanup.
   Ownership is released only after the complete final cleanup callback returns, including queued cancellation and refusal after partial resource acquisition.
   No new terminal-writing shutdown loop is introduced; existing job execution and unavailable-store rules remain responsible for outcomes.
4. Runner `Shutdown(context.Context) error` closes admission, requests cancellation of every admitted start, queued job and running job, then waits for actual drain.
   It introduces no separate total shutdown timeout and does not change the child's existing ten-second interrupt-to-kill grace.
   A successful return means no admitted owner remains and no future owner can enter.
   An already drained runner returns success; otherwise a cancelled or expired waiter returns an error wrapping its context error, while admission stays closed and ownership remains tracked.
   Concurrent and repeated callers are safe, and a later uncancelled waiter can still observe the eventual drain.
5. The internal implementation uses one checked owner per admitted Start and a single closed-admission/drained state under the existing runner mutex.
   A Start's refusal defer must run after its acquired-resource defers, while a launched job releases its owner only after the existing final pin/handle callback.
   Cancellation registrations and slot counts retain their existing public-cancellation and capacity purposes and cannot substitute for ownership.
   The exported shutdown method is on the runner, which is not a bound Wails service; it creates no new frontend action.

The contract does not promise that every pre-existing operation returns immediately after cancellation or invent cleanup authority for uncertain remote effects.
It promises that a still-owned operation prevents a successful drain.
If implementation requires changing terminal semantics, process policy, timeout budgets, another claimed file or the separate shell contract, stop and review that expansion first.

## Maintained verification required

Use table-driven, race-enabled cases for all five actual public Start families after stop and at an observed pre-registration boundary.
Assert refusal before session/preflight/engine/row effects, correct cancellation of admitted startup I/O and complete refusal cleanup before drain.
Cover a queued job, failed starts, late registration/handoff, repeated and concurrent shutdown, cancelled/deadline waiters, ordinary detached completion and explicit owner cancellation.
Hold running-body, post-terminal and final pin/handle cleanup boundaries and require shutdown to remain pending until each owned boundary exits.
Cover terminal persistence failure and queued cancellation while retaining the existing unavailable-store latch and durable records.
Use owned channels and request barriers with bounded test cleanup, not elapsed sleeps as admission evidence.

Independently remove admission closure, startup ownership, owner cancellation and final-cleanup ownership and retain each resulting failure alongside an unrelated healthy control.
Setup failures, skipped wrappers and unchanged control passes cannot count as causal proof.
Keep exact selected leaves, return codes, source and stream digests in the evidence record.
Focused mock verification may precede the consumer; real process/system proof and the full final-base suite, independent range review, five current-head/current-base PR checks and green resulting master remain delivery gates.

## Required separate shell consumer

Pinned Wails `v3.0.0-beta.16` logs a `ServiceShutdown` error and continues shutdown; its transport stop and window-call cancellation also do not join every bound call.
An error-returning service hook with the current database and lock defers is therefore insufficient.
The separately reviewed consumer must invoke actual owned drain before releasing shared resources, check database closure errors and route `a.Run` errors through cleanup instead of bypassing it with `log.Fatal`.
It must not choose an arbitrary new deadline or interpret a logged timeout as successful drain.
Find and synchronous bridge/native-request lifetimes need their own precise coverage; proof of five job families must remain labelled accordingly.

## Maintained implementation and evidence

The runner now admits each of the five Start families under the same mutex that closes admission.
An owner begins before resource acquisition and survives complete refusal cleanup or the launched job's final pin/handle callback.
Its startup context preserves caller cancellation, while its job context retains the existing asynchronous detachment.
`CancelJob` still cancels only the registered job context, including the narrow interval before Start returns; owner shutdown additionally cancels the admitted synchronous startup.
The final queue wake is synchronised with the condition mutex so cancellation cannot leave a queued owner asleep during shutdown.
No executor result mapping, cleanup policy, process grace, database schema or bound frontend action changes.

The [maintained evidence twin](probes/2026-09-12-job-shutdown-ownership-maintained.json) preserves the initial build failure for the then-absent API, both intermediate successful test sets, the final focused compatibility run, coverage and eight isolated removal controls.
The initial build failure is not credited as a behavioural safety reproduction; the independently reviewed before observations remain the behavioural before evidence.
The final compatibility run executes 62 passing leaves under `go test -race`, comprising 38 new ownership/service/handle cases and 24 existing cancellation, pinning, completion, Copy, upload/download and benchmark cases, with zero failures or skips in 15.106 seconds.
The separate focused coverage run executes the same 38 new leaves successfully and reaches 100% statement coverage in the six added ownership/shutdown functions.
Its whole-package percentage is only the focused selector's 27.0%, not a claim of full application coverage.
`go vet ./internal/app` and `git diff --check` pass; `staticcheck` was not found on PATH or in the local tool cache.

All five public start families refuse new starts after stop, retain ownership through held pre-registration I/O, cancel queued work without starting remote effects and drain failed creation paths.
The held body, terminal cleanup and final pin callback cases show that neither terminal state nor zero slots/registrations permits an early drain.
Cancelled and expired waiters preserve admission closure and ownership, while repeated/concurrent callers can still observe actual completion.
Queued and running completion-write failures keep their previous durable rows and unavailable-store latch rather than inventing a saved cancellation result; ownership still drains and credential pins release.
The Linux descriptor check observes the actual download's derived directory handles and requires them to disappear at drain while the picker-owned handle remains usable.
This is native handle evidence around the actual service and an in-process HTTP engine, not native child-process or Wails shutdown evidence.

| Isolated broken guard | Passing controls | Failing targeted leaves | Failure observed |
| --- | ---: | ---: | --- |
| Do not close admission | 1 | 5 | Every public Start returns a new id after shutdown |
| Omit admitted-owner tracking | 1 | 5 | Held pre-registration starts are reported drained |
| Do not cancel owners | 1 | 2 | Startup I/O remains uncancelled and a running owner cannot drain |
| Release the owner before final cleanup | 1 | 1 | A held final pin callback is reported drained |
| Omit the download destination close | 1 | 1 | One extra native directory descriptor remains after drain |
| Make public job cancellation cancel startup too | 1 | 1 | `CancelJob` unexpectedly cancels the still-owned synchronous start |
| Return success on a failed wait | 1 | 2 | Cancelled and expired waiters report a false drain |
| Detach startup from the caller | 2 | 1 | Caller cancellation no longer reaches admitted startup |

Every control exits 1 with no skipped leaf and retains a passing ordinary detached-download completion control.
The last control also retains the already-cancelled-before-admission refusal.
The source and raw-stream hashes are in the twin, and all mutations run through isolated overlays without modifying the reviewed worktree or its assertions.
The source hashes remained unchanged across final verification and controls.

The implementation is still unable to ship by itself.
The separately assigned actual shell caller, Find drain and checked DB/lock consumer need their own contract, true entrypoint/system proof and independent approval before the same delivery can proceed.
No shared Swift fixture, full Go suite, actual child shutdown, native Wails quit, hosted pipeline or other platform acceptance was run or credited here.
Synchronous bridge/native-request ownership, credential-store lifecycle, retained-stage deletion and the valuable-data hold remain open.
