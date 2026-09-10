# Copy binding release under a slow body

## Scope and oracle

This plans the bounded copy investigation required by the [intermittent failure record](2026-09-08-integration-time-budget.md#planned-investigation-for-the-two-intermittent-assertions).
The bounded question is only this: which phases does the copy item-record assertion actually observe, and which of them can be told apart by direct observation.

The oracle is the job runner's stated contract that the binding survives until the whole launched goroutine returns, the job state machine's terminal states, and a successful bookmark save as the release acknowledgement.
That successful save stays the release oracle throughout, and every identity, record and collateral assertion in the affected tests stays exactly as it is.
This plan proposes no production change, no test change and no retry; it decides nothing until the evidence exists.

## The recorded failure

A master run failed `TestCopyItemRecordsMock/failed_401` with the copy body not releasing its binding, and the same tree passed on the retry.
The failing subtest elapsed 31.49 seconds, which is consistent with one thirty-second deadline expiring rather than two.
Nothing about the cause is established, and the failing process is gone, so no experiment can recover it.

## What the source shows

The case starts the copy asynchronously and calls the wait immediately afterwards, so the wait spans everything the job does.
The runner releases the binding in a deferred call wrapping the whole launched goroutine, so the release follows the body's return, after the separate slot release.
The wait polls a bookmark save every five milliseconds against a thirty-second deadline, for the source account and then the destination account in turn.

Three source facts bound part of the space of explanations, and each is stated with the limit of what it excludes.
Each harness builds its own job runner over its own temporary database, the active-job ceiling is two, and this case starts one job before the wait.
Together those exclude waiting behind a competing occupant for a capacity slot in this fixture.
They do not exclude delayed scheduling of the launched goroutine before it reaches the body, which stays a live candidate.

Three further facts constrain what any observation can mean, and they are the reason this plan does not simply time the existing wait.
A job is created in the queued state, and the transition to planning happens inside the copy body, so a state change is not an exact timestamp for entering that body.
Terminal publication is not the body's return: the terminal outcome is published inside the body, which then still runs its deferred executor close, after which the runner releases the slot and only then the binding.
An interval measured from terminal state to a successful save therefore contains remaining cleanup, goroutine scheduling and the save path's own latency, and is not time spent releasing a binding.

The existing helper also begins each account's deadline after listing bookmarks, and checks the source account before the destination.
An interval measured from the start of the copy is therefore not the same interval as the helper's own deadline, and a destination observation can be censored by the source wait having already failed.

The upload item-record case awaits the terminal state before the same release wait, with a comment that hundreds of checked commits may outlast the separate cleanup wait.
That is surrounding evidence about how long these bodies run, not proof that the copy case needs the same edit.

## What will be observed

The private producer wraps the launched body to take its own timestamps, rather than inferring phases from published state.

1. Body entry, taken as the launched body is entered.
2. Body return, taken as that body returns, before the runner's slot and binding releases.
3. Terminal publication, read from the durable job record, which is expected to precede body return.
4. The first successful bookmark save for the source account and for the destination account, observed independently of one another so that neither observation is censored by the other.

All four are expressed from one shared origin taken as the copy is started, and the per-account acknowledgements are additionally expressed from the helper's own origin so the two intervals are never conflated.
The interval between body return and an acknowledged save remains a bound rather than an exact release duration, because it necessarily contains the runner's deferred releases, scheduling and the save path's latency.
A body still held after terminal publication and before its return must not be classified as a missing release merely because the bookmark remains in use.
Any observation that is missing or arrives late is recorded as unresolved, and never filled in from an assumed event order.

## Producers and controls

The fixture is torn down by a failing guard, because the guard is fatal and the proxies and database are closed by registered cleanups.
Proof that the guard rejects a delay and observation of what the job does afterwards therefore cannot come from one run, and are separated into two producers.

**Guard rejection, unsupervised.** The unchanged test runs against an overlay that holds the body past the deadline.
The expected result is the real guard failing with the binding message; the exit is classified against that specific failure, and a setup, teardown or unrelated failure is not counted as the expected result.
This producer shows only that the current guard rejects a held body.

**Supervised observation.** A private producer owns its fixture and drives the same operation without a fatal at the deadline.
It records the deadline crossing, then keeps the servers, database, namespace and observers alive until body entry, body return, terminal publication and both per-account acknowledgements have been collected, under a separate bounded abort of its own.
The hold placed after body entry is released by the producer and is bounded so that a stuck producer cannot hang.
The run finishes by checking the original identity, record and collateral outcomes, which must hold exactly as they do today.

**Retained guarantee.** A second overlay removes the release while leaving the body fast.
That control must observe its body completing, and must then observe refusal for both accounts, so that a generic non-zero exit is never accepted as evidence that the guard was retained.

**Timing sample.** Deliberate processor contention is recorded as a separately labelled sample of how long the phases take under load.
It cannot by itself establish which phase was delayed, and is not used to make that claim.

Both overlays are private producers, are never committed, and the retained evidence records their exact replaced source, the commands and the actual exits.

## Fixed before execution

These are fixed and retained before any producer runs, so the sample cannot be chosen after seeing it.
The sample counts and the per-run and total time bounds; the contention settings; the exact source revision and overlay identities with their digests; the expected exit classification for each producer; and the abort bounds for the supervised run.
The actual baseline conditions of the host are recorded rather than assumed idle, and the load is bounded and scheduled so that it does not overlap the other active investigations.

## Limits

The runs cover a Linux host and the mock harness; the real-service variants need the containerised service and are outside this slice.
The experiment establishes which phases the current assertion conflates and which of them direct observation separates.
It cannot establish the cause of the recorded failure, and no result here may be reported as that cause.
Delayed scheduling before body entry is measured but not controlled, so it is reported as an observation rather than excluded.
Slot admission, cancellation, the recovery paths and the upload attempt-count question are all outside this slice.

## Decision to follow

The result is recorded first, including an explicitly unresolved outcome if the evidence does not separate the phases.
Only then, and only as its own reviewed commit, may any test change be proposed, with the release oracle and every identity, record and collateral assertion preserved.
No blind retry is introduced anywhere, and no deadline is relaxed merely to absorb variance.

## Result

Five private producers ran on one Linux host against the mock harness, at the planned source revision.
The parameters were fixed beforehand, with one exception recorded here rather than presented otherwise.
The contention reduction, to eight busy loops and three runs because another investigation had system-test processes on the same host, was registered before that producer ran.
The missing-release window, aligned to the real thirty-second deadline and so observing to sixty seconds, was registered after those three runs had already completed, which makes that producer's parameters retrospective provenance rather than pre-registration; its value is visible in the retained marks, and the original timestamps are left unchanged.

### Marker semantics

The seam markers are named for where they are emitted, and the intervals below are marker to marker.
The entry marker is emitted at the launch seam before the bounded hold and before the body call, so it is not entry into the copy body.
The return marker is emitted immediately after the body call returns, so the body's own deferred cleanup has completed.
The release marker is emitted inside the deferred release wrapper before the release runs, and is emitted even when the release is suppressed, so it marks the call site and not a completed release.
A successful bookmark save is the only evidence of an actual release, and remains the release oracle.
No origin was recorded at the start of the copy, so no interval here measures all scheduling before the body.

### The interval the unheld runs measure

In the five idle runs, with retained one-minute load averages of 0.26 to 0.66 taken immediately before each run, the interval from the entry marker to the return marker was 9,538 to 10,400 milliseconds, a mean of 9,776.
The interval from entering the wait to the entry marker was 0.0 to 0.1 milliseconds, which bounds the seam handoff only and does not establish that scheduling played no part.
The return marker was followed by the release marker within 0.1 milliseconds in each of the fourteen runs that retained both markers.
The three guard-rejection runs retain only the entry and hold-start markers, because the unchanged guard fails before the hold ends, so their return-to-release interval is unobserved.
The account polled first is acknowledged as that interval ends, and the second within about fifteen milliseconds.
The worst idle run consumed 34.7 per cent of the thirty-second deadline.

In three runs with eight busy loops running throughout, the same interval was 11,774 to 12,181 milliseconds, a mean of 1.23 times the idle mean.
The retained one-minute load averages, sampled three seconds after the busy loops started and therefore lagging the instantaneous load, were 1.26, 3.44 and 5.01.
Reaching the deadline from the idle mean would require about 3.07 times that interval, which this sample did not reach.

For the unheld runs, then, what the wait measures is dominated by the body: the whole interval from entering the wait to the release marker equals the entry-to-return interval to within a tenth of a millisecond.
That is a statement about these runs, not about every expiry, because the suppressed-release control below expires after a body that completed quickly.

### The two situations are separable

The unchanged guard was proved to reject a held body: with the launch seam holding for thirty-five seconds, each of three runs produced one failing leaf carrying the binding message, with no build, setup or teardown error counted as that result.

The supervised runs then observed what the same held job does afterwards, with the fixture kept alive past the deadline.
Both accounts crossed their own thirty-second deadline, at 30,000 to 30,006 milliseconds from their own origins.
The entry-to-return interval was 45,418 to 45,968 milliseconds, which contains the thirty-five second hold and is therefore not thirty-five seconds of copy work.
The release marker followed the return marker within 0.1 milliseconds, and both accounts were acknowledged 22 to 45 milliseconds after that marker.
Every original identity, record and collateral assertion in the case still held.

The suppressed-release control removed the release and left the body unheld.
The entry-to-return interval was 10,105 to 10,255 milliseconds, the return marker was observed, and both accounts crossed the deadline and were still refused when observation ended at about sixty seconds.

The deadline crossing alone therefore does not distinguish the two situations, because it is identical in both.
What distinguishes them is whether an acknowledgement follows the return marker: within forty-five milliseconds when the launch seam had been held, and absent through sixty seconds when the release was removed.
That is a narrow distinction between a supervised delayed launch that eventually releases and an intentionally suppressed release after body return, and it is the distinction the current single wait cannot draw.

### One planned observation was not collected

The plan listed terminal publication as a fourth observation, and these producers did not collect it.
The instrumentation marks the launch seam, the body call's return and the release call site, and the wait marks the two acknowledgements; nothing timestamped the terminal row.
It is recorded here as not collected rather than inferred from the order of the other marks.
The separation reported above does not rest on it, because the distinguishing observable is whether an acknowledgement follows the return marker.

### What is eliminated and what is not

Competing occupants of a capacity slot are excluded for this fixture, because the harness builds its own runner over its own database, the ceiling is two, and the case starts one job.
Scheduling before the body is not excluded: no origin was taken at the start of the copy, and the measured seam handoff bounds only the step from entering the wait to the seam marker.
The cause of the recorded hosted failure is not established and cannot be, because its process is gone, and that failure remains unclassified between the two situations above.
Nothing here shows that the hosted body reached thirty seconds, and no result in this document may be reported as that failure's cause.

### Limits on these numbers

The instrumentation writes one short line per event and the host was shared with another investigation, so both bound the precision of the timings rather than being corrected for.
Per-run wrapper exit codes were not written to disk in this set, and the retained streams carry the pass and fail events.
The invocation record retained beside them is a reconstructed template rather than a capture of the executed command lines: it states the common command shape and per-producer environment, and it omits the bound substituted into each timeout, the execution directory, the per-run log paths and redirections, and the source switch that restored the unchanged guard for the rejection producer.
The hold sits at the launch seam immediately before the body call, so during it the job holds its slot and its binding but has not entered the copy body.
From the binding's perspective that is indistinguishable from a slow body, and it is not slow copy work.
These runs cover a Linux host and the mock harness; the real-service variants need the containerised service and are outside this slice.

### Indicated direction, not validated here

Waiting explicitly for a terminal state before waiting for the release, which is the shape the upload item-record case already uses, remains a hypothesis worth its own slice.
This sample neither measured a terminal-to-release interval nor validated that boundary, because terminal publication was not observed.
Any such change would come as its own reviewed commit with its own evidence, keeping the successful save as the release acknowledgement, every existing assertion unchanged, no relaxed deadline and no retry.
