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

On the final maintained source, the mock plus executor run passed seventeen leaves with the race detector.
Removing only Pdeathsig left completion green, made the running child fail with SIGPIPE instead of SIGKILL, and left the stopped child alive until explicit cleanup.
Returning at the existing ten-second escalation deadline without killing and reaping the child left completion green and failed `terminal job left its child alive` while the service parent was still alive.
Neither control shortened the production grace or any assertion deadline, and neither reported a cleanup or race failure.
Restoring the old download result mapping is a separate classification control, keeping all process safeguards in place.
It fails the three positive executor cases and both mock service cancellation cases, while the nine exclusion cases and completion pass.
Exact commands, statuses, sources and stream hashes are recorded in the evidence twin.

The final maintained subreaper harness still requires its real Swift rerun and causal controls in the coordinator's exclusive fixture slot before publication.
The earlier real Swift mapping result remains valid for its recorded source; it is not presented as the final process-signal proof.
Focused `go vet` on both changed packages and formatting checks pass.
Final-base package verification, full-suite reconciliation, independent review and hosted delivery remain separate gates.

## Remaining limits

The final causal evidence here is a native Linux amd64 process test against an owned HTTP fixture; the earlier maintained runs also used real Swift, but the final harness still needs that provider rerun.
This is not a production desktop journey.
It proves the lifetime of the observed direct engine, not arbitrary detached grandchildren, a cross-platform shutdown coordinator or OS keychain release.
The parent-kill record retains the admitted staging location; it does not invent an execution observation for the partially written engine temporary.
The tests do not establish power-loss durability, atomic publication across every filesystem, cancellation during directory creation or cleanup, or the retained-stage removal product decision.
Other platforms and architectures remain under GitHub issue 103.
DS-07, SEC-06, P20, P30 and the valuable-data hold remain open.
