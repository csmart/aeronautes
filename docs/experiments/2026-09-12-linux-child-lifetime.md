# Linux download child lifetime

## Scope and oracle

This slice addresses the Linux amd64 part of P30 by observing an actual service parent around pinned rclone v1.74.3 at an anchored download write boundary.
The oracles are DESIGN sections 26.4, 32.1 and 65.10, ADR 0020, and the [download cancellation contract](2026-09-12-download-cancellation.md#contract).
Graceful cancellation, forced cancellation and parent death must stop the child before recovery and retain truthful job and item outcomes.
A cancellation with no placed item and no independent failure ends cancelled; another failure or a placed item still makes the outcome partial.
No change here decides whether the application should delete complete staged files on graceful cancellation.

## Before probe

The production base is `3c5f7f7fe61bedc8199c72e8439400181643058a`.
The run used plan-only head `14d229f7eb01eb12ef1dba88fd75a9a5a8319568`, which changes DESIGN only.
The [archived probe](probes/2026-09-12-linux-child-lifetime-probe_test.go.txt) ran as a Go overlay against the owned HTTP fixture with the race detector.
The [evidence twin](2026-09-12-linux-child-lifetime-probes.json) records the exact inputs, terminal leaf results and raw stream digests.
No real Swift deployment was contacted by this before probe.

The controller starts another copy of the Go test binary, which acquires the application instance lock, creates its own database, wires the production transfer service, chooses its local directory and calls StartDownload.
The actual engine reads a 311,296-byte object through the production gateway and a response barrier that releases only 65,536 bytes.
The controller waits for those exact bytes in the engine's partial file before signalling anything.
It discovers the direct rclone child through the parent's thread child lists and opens a Linux pidfd before any signal, so PID reuse cannot redirect signals or satisfy a liveness check.
For ordinary cancellation and forced escalation, the service parent stays alive after writing the terminal record until the controller proves the child exited; parent-death signalling therefore cannot hide an early service result.
The initial stopped-child case was intended to distinguish parent-death signalling from gateway EOF; the later negative control below shows why that assertion alone was insufficient.
A second actual process acquires the same instance lock, reads the database, calls FailInterrupted twice and records the retained state.
Remote snapshots on both accounts and local snapshots remain separate witnesses.

An initial probe failed because its own exit-status assertion used `unix.WaitStatus` where os/exec returns `syscall.WaitStatus`.
That failed run is retained and supplies no parent-death acceptance.
The corrected observational run passed all four original modes.
The strengthened five-mode run passed completion and both parent-death modes, and failed exactly the two assertions requiring a cancelled state and cancelled diagnostic after CancelJob.

| Mode | Child result | Local result | Durable result |
| --- | --- | --- | --- |
| completion control | exit 0, not cancelled | the complete file is placed; stage removed | complete; one copied and verified item |
| graceful CancelJob | exit 130, cancelled | rclone removes its 65,536-byte partial; no final file | partial; failed, unresolved, engine, unknown |
| stopped child then CancelJob | exit -1, cancelled, after the existing ten-second grace | the exact 65,536-byte partial remains in the stage; no final file | partial; failed, unresolved, engine, unknown; stage cleanup names the retained directory |
| parent SIGKILL | child exits; partial remains unchanged | exact partial and stage retained; no final file | running before restart; failed after FailInterrupted, with the admitted item and no invented execution |
| stopped child then parent SIGKILL | stopped child exits; partial remains unchanged | same exact retained partial and stage | same interrupted result; repeated FailInterrupted changes zero jobs |

The actual graceful and escalated runner results both have no collected errors, an empty LogStreamErr and zero Stats.Errors.
The runner observation overlay serialises the Result after child reaping and before returning it, without changing the signal, gateway, context or transfer decisions.
Graceful cancellation has no cleanup issue and no independent failure in its record.
The forced-cancellation cleanup issue is the existing refusal to remove a nonempty stage, not evidence of a separate engine failure.
Neither mode issues a remote write or changes collateral data.

## Source finding and correction contract

DownloadInto converts ResultFailure's plain `engine child cancelled` error into an engine diagnostic with the unknown code for every engine item, then returns a nil run error.
That loses the cancellation classification and bypasses runLocal's existing cancellation-only terminal rule.
This differs from a cancelled native read or a cancellation after staging, even though none of those cases places the selected file.

The correction is limited to the download engine result boundary in `internal/transfer/local/download_execute.go` and dedicated executor and system regressions.
It does not change ResultFailure for Copy or Upload, the runner, the ten-second grace, the gateway or stage cleanup.
Classify an engine result as cancellation-only when `errors.Is(ctx.Err(), context.Canceled)` holds, the engine call itself returned no error, Cancelled is true, ExitCode is 0, 130 or -1, LogStreamErr is empty, there are no collected errors and Stats.Errors is zero.
The recognised exits represent a child that finished during cancellation, an interrupt exit, or an uncatchable kill; any other exit remains a generic failure.
Record failed with the unresolved effect and cancelled code at the engine stage for each admitted engine item, and return the context cancellation to the existing service terminal rule.
Unresolved remains conservative: interruption establishes no successful publication or complete staged-object verification.
Do not classify a call error, missing result, uncancelled context, deadline expiry, unexpected exit, incomplete log or independently reported engine error as cancellation-only.
The existing retained-stage message stays visible, and retained bytes are not reclaimed.

## Required proof

The maintained Linux system tests must repeat completion, graceful cancellation, forced escalation and both parent-death modes against the owned mock and real Swift fixture.
They must assert the exact local files and bytes, both accounts' collateral snapshots, absence of remote writes, child exit before the still-live parent reports completion, exact job and item records, instance-lock exclusion and release, and idempotent recovery in another process.
The after-recovery snapshot must remain identical to the post-child snapshot.
The executor table must preserve non-cancellation errors, including cancellation accompanied by a call error, an incomplete log, collected errors, counted errors or an unexpected exit.
Deliberately broken controls must remove parent-death coupling and return before reaping, then fail their specific liveness assertions while explicitly terminating any test-owned survivor.
The classification control must retain the existing wrong mapping and fail both real-service cancellation assertions.
Every command retains its actual exit status and complete output; failing harness drafts are kept separate from product regressions.

## Maintained proof and causal controls

The production correction follows the reviewed predicate above without changing the child lifecycle code.
The executor table covers the three recognised cancellation exits and nine independent-failure or context controls, including an actual deadline.
The first maintained before run failed the three positive executor cases and both cancellation modes on each provider, with fifteen other leaves passing.
The first after run passed all twenty-two leaves with the same test inputs and only the production boundary changed.
A second after run strengthened process identity, terminal audit and recovery assertions and again passed all twenty-two leaves against mock and real Swift.
Those two successful after runs are not by themselves causal proof of the parent-death signal.

Removing Pdeathsig from the initial maintained harness unexpectedly passed all four selected completion and stopped-child parent-kill leaves on mock and real Swift.
That ineffective control is retained as `control_no_pdeath`, with actual exit zero; it supplies no Pdeathsig acceptance.
Linux sends SIGHUP and SIGCONT when a stopped process group becomes orphaned, so the stopped-child liveness check had an alternate termination path ([setpgid](https://man7.org/linux/man-pages/man2/setpgid.2.html)).
The corrected harness runs each mode in its own controller process and uses PR_SET_CHILD_SUBREAPER so that controller adopts and waits for the direct engine after the service parent dies ([subreaper interface](https://man7.org/linux/man-pages/man2/PR_SET_CHILD_SUBREAPER.2const.html)).
This keeps subreaping out of the rest of the test suite and makes the child's actual termination signal observable.
The production service parent, engine, gateway and signal policy stay unchanged.
The controller's process identity, pidfd liveness, same executable, direct parent and separate process group checks remain in place.
After killing the parent, both running-child and stopped-child cases require an adopted-child wait status of SIGKILL before recovery begins.
Every owned child is explicitly killed and, if adopted, reaped during failure cleanup.
The stopped-child case is now named `stopped_child_parent_kill` to identify which process was stopped.

The first private subreaper probe passed completion and both parent-kill modes, each observing SIGKILL.
Its Pdeathsig control failed the ordinary parent-kill case with SIGPIPE and left the stopped child alive until cleanup.
Its reexec selector initially allowed the `parent_kill` suffix to select `stopped_parent_kill` too; that raw run is retained and not used as an exact one-controller-per-leaf inventory.
The maintained selector anchors each slash-separated test component, and the exact maintained controls below use the corrected selector.

On the maintained subreaper source, the mock plus executor run passed seventeen leaves with the race detector.
Removing only Pdeathsig left completion green, made the running child fail with SIGPIPE instead of SIGKILL, and left the stopped child alive until explicit cleanup.
Returning at the existing ten-second escalation deadline without killing and reaping the child left completion green and failed `terminal job left its child alive` while the service parent was still alive.
Neither control shortened the production grace or any assertion deadline, and neither reported a cleanup or race failure.
Restoring the old download result mapping is a separate classification control, keeping all process safeguards in place.
It fails the three positive executor cases and both mock service cancellation cases, while the nine exclusion cases and completion pass.
Exact commands, statuses, sources and stream hashes are recorded in the evidence twin.

The earlier real Swift mapping result remains valid for its recorded source; it is not presented as the final process-signal proof.
The final provider run followed the outcome-reporting correction below.
Focused `go vet` on both changed packages and formatting checks pass.
Final-base package verification, full-suite reconciliation, independent review and hosted delivery remain separate gates.

## Controller outcome reporting

The independent review's fast no-fixture run exposed a separate reporting defect in the reexec wrapper.
At `14ef1d922e5fc660`, removing the Swift auth URL made every inner provider case skip, but each outer case still reported pass because a skipped Go test exits zero.
The retained `no_fixture_before` run therefore has five falsely passed outer leaves and supplies no Swift evidence.
The wrapper now requires an explicit outcome file written after the inner case's cleanup, propagates skip to its own test, and rejects a missing or unexpected outcome.
A nonzero child exit remains an outer failure.
The corrected no-fixture run has exactly five skipped leaves and no passed or failed leaves.
A fresh mock plus executor run still passes seventeen leaves, and the repeated Pdeathsig control passes completion while failing both parent-kill leaves at their intended signal/liveness checks.
These outcomes cover actual pass, skip and failure propagation with the final wrapper.
The reporting correction changes only the dedicated system test and its evidence, with the production source unchanged.

## Final two-account Swift proof

The final source at `00eff59cb944eb9bcc62b7a1be98394340347a2f` ran in the coordinator's exclusive Swift fixture slot with both accounts required.
The selected engine was rclone v1.74.3 with SHA-256 `9700aa1273ac73d6d0833c43ba63fe830516422cb131960b8c1a24ced789cba0` on Linux x86_64, kernel `7.1.8-200.fc44.x86_64`.
The source hashes were unchanged throughout all four commands: system test `0afaa447…`, executor table `0c2bdb41…` and production boundary `be807668…`; the twin contains the full hashes and actual commands.
The normal run executed all five selected cases with the race detector and no skips.

| Run | Go exit | Passed leaves | Failed leaves | Skipped leaves | Elapsed |
| --- | --- | --- | --- | --- | --- |
| final Swift | 0 | 5 | 0 | 0 | 30.556 s |
| remove Pdeathsig | 1 | 1 | 2 | 0 | 8.674 s |
| omit kill and reap at the existing grace deadline | 1 | 1 | 1 | 0 | 17.832 s |
| restore old cancellation mapping | 1 | 1 | 2 | 0 | 22.882 s |

Completion placed and verified all 311,296 bytes, and graceful cancellation placed nothing and removed the engine partial.
Forced cancellation retained exactly the 65,536-byte partial and its stage, reported the retained path, and ended cancelled with one failed, unresolved engine item carrying the cancelled diagnostic.
Both parent-kill cases produced an adopted-child SIGKILL status before recovery and retained the exact partial and stage.
The next process acquired the released instance lock and changed the running job to interrupted failure exactly once, without fabricating execution or verification for the admitted item.
All cases retained the sentinel, preserved both accounts' snapshots, issued no remote writes and left the post-child filesystem unchanged through recovery.

The Pdeathsig control's running child died from SIGPIPE, so it failed the required SIGKILL assertion; its stopped child survived until the controller's explicit cleanup.
The omitted-kill control failed because the job was terminal while its child remained alive, with the service parent intentionally kept alive.
The old-mapping control failed both cancellation record assertions while completion remained green.
The actual Go exits are retained as failures, and no control reported a race or owned-process cleanup failure.
Together with the final seventeen-leaf mock/executor run, this supplies twenty-two passing scoped cases on the final test and production sources.
The coordinator's combined final-base suite, independent range review and all five PR and resulting-master jobs remain required before delivery.

## Remaining limits

The final causal evidence covers native Linux amd64 direct-child processes against both the owned HTTP fixture and a local two-account Swift fixture.
This is not a production desktop journey.
It proves the lifetime of the observed direct engine, not arbitrary detached grandchildren, a cross-platform shutdown coordinator or OS keychain release.
The parent-kill record retains the admitted staging location; it does not invent an execution observation for the partially written engine temporary.
The tests do not establish power-loss durability, atomic publication across every filesystem, cancellation during directory creation or cleanup, or the retained-stage removal product decision.
Other platforms and architectures remain under GitHub issue 103.
DS-07, SEC-06, P20, P30 and the valuable-data hold remain open.

## Addendum, 14 September 2026: the running-child signal is a race

The resulting-master run for `e053cb73d6cc61e97ca23dc4478568ac71a9193d` failed one leaf.
`TestChildLifetimeRealSwift/parent_kill` reaped its adopted child with status 13, SIGPIPE, where the assertion requires status 9.
The same run recorded status 9 for `TestChildLifetimeMock/parent_kill`, `TestChildLifetimeMock/stopped_child_parent_kill` and `TestChildLifetimeRealSwift/stopped_child_parent_kill`, and the package completed in 1171.054 seconds rather than timing out.
The same leaf had failed the same way earlier that day on the mock provider, and an attribution to host load was rejected as unestablished.

A standalone probe outside this repository isolates the mechanism.
A parent sets `Setpgid` and `Pdeathsig` exactly as `internal/transfer/rclone/childproc_linux.go` does, gives its child a stderr pipe it drains, and a subreaper kills that parent and records which signal reaped the orphan.
With a child saturating its stderr pipe, two runs of two hundred trials each reaped 8 and 5 children with SIGPIPE and the remainder with SIGKILL, a combined 13 of 400.
With the child stopped before the parent is killed, two hundred trials reaped two hundred children with SIGKILL and none with SIGPIPE.
The host was 24 cores at load average 3.48 with other suites present, and the rate is for a saturating writer rather than for the engine's actual stderr volume.

A child that is inside a write to that pipe when the parent dies can be reaped by SIGPIPE before the parent-death signal reaches it, which is what the trials observe; the ordering inside the kernel is not established here.
A stopped child cannot execute a write, so it cannot take SIGPIPE, and only the parent-death signal can reap it.
That asymmetry explains why the stopped mode has never produced this failure and the running mode does.

The guarantee is intact.
`Pdeathsig: syscall.SIGKILL` is set, no child survived its parent in any trial, and both signals are consequences of the parent's death.
What the assertion actually rests on is narrower than it reads.
The control recorded above this addendum states that removing the coupling makes the running child die of SIGPIPE, and that is true but incomplete: SIGPIPE is also a few-per-cent outcome with the coupling present.
A reader relying on that sentence alone would conclude from a SIGPIPE reaping that the coupling had been removed, which is why this looked like a product defect for a day.
The status alone does not distinguish the two.

The proposed correction accepts either SIGKILL or SIGPIPE in `parent_kill`, keeps the exact SIGKILL requirement in `stopped_child_parent_kill`, and states that the stopped mode is the retained proof of parent-death coupling.
This narrows that proof from two modes to one and is the cost of the change.
It is acceptable only if the stopped mode still fails when the coupling is removed, which the earlier control recorded and which must be rebuilt and reproved on current source before the correction is implemented, not taken from this record.
Every other assertion in both modes, including the exact retained partial, the stage, both accounts' collateral, the absence of remote writes and the recovery records, is unchanged.

Holding the child's stderr read end open in the controller was considered and rejected.
It would remove the race and keep the exact assertion in both modes, but the parent owning that pipe and its EOF when the group dies is the production wiring this test exists to exercise, and a controller that holds it open no longer observes what production does.

These observations show that SIGPIPE is compatible with configured parent-death coupling.
They do not prove a universal lifetime guarantee, and they establish no ordering inside the kernel beyond what the trials show, and the probe's rate describes a saturating writer rather than any rate of failure on this suite or on master.
The documented signal is tied to the creating parent thread rather than to the parent process, which matters when describing the exit of a multithreaded parent.
This addendum grants no native, scenario or release acceptance.

The precondition is met on current source.
Removing only `Pdeathsig` from `childSysProcAttr` and running the stopped mode against the mock fixture fails `child still alive after parent terminal` at `child_lifetime_linux_test.go:507`, which is the named liveness assertion and fires before cleanup kills the child.
The delivered assertion therefore keeps the exact requirement in the stopped mode and accepts SIGKILL or SIGPIPE in the running mode, with the accepted list closed so an unexpected crash or a normal exit still fails.

Three controls built by one producer at the delivered head, each credited only because its package built and its named case failed:

| Control | Removed | Case | Assertion that fired |
| --- | --- | --- | --- |
| `g01_no_pdeath` | parent-death coupling | stopped child, parent kill | child still alive after parent terminal |
| `g02_other_signal` | the closed list, by reaping SIGTERM | running child, parent kill | child did not terminate from parent death by SIGKILL or SIGPIPE |
| `g03_normal_exit` | the closed list, by reaping a normal exit | running child, parent kill | child did not terminate from parent death by SIGKILL or SIGPIPE |

`g01` is the retained coupling proof and fires before cleanup kills the child.
It also shows the cost: with the coupling removed the running mode passes, so the stopped mode is the only mode that still detects a missing `Pdeathsig`, and deleting it as duplicate coverage would remove the proof rather than a duplicate.
`g02` and `g03` show the running mode's relaxation did not become an acceptance of any termination.
All ten leaves of both providers pass at the delivered head under the race detector, on a host at load average 0.69 with no other suite running.

The independent review packet for the delivered range is `/home/csmart/code/.aeronautes-work/reviews/20260914-db503-c9e8a21e`, whose 35-file manifest has SHA-256 `d3dcda0eb1df54428f257e0209f98961e26ee7b1f2aaf47d4f587ebefc155cc7`.
