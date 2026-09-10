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
