# Account-scoped listing results — 14 September 2026

Finish-line item L11, safety package P28.
The completion test is that identical names in different accounts remain distinguishable, and that results, paging, retry and error state stay bound to the selected account.

## What was asked

Whether a listing result, a page cursor, a retry and an error state are bound to the account they came from, or merely to the most recent request; what happens to a request in flight when the account changes underneath it; and whether identical names in two accounts are distinguishable to the operator at the moment they act.

## Method

Through `BrowseService` against the two-account SAIO fixture on loopback port 18080, not against a mock and not by simulating the second account.
One container name was created in both accounts, holding a different object in each, because container names are per-account and a collision is ordinary rather than contrived.
Two healthy controls run in the same probe so a failure is the defect and not the fixture.

## What reproduced

Exit 1, two intended failures, two controls passing.

| Case | Observed |
| --- | --- |
| A page continuation stays in the account it started in | A first page requested under the primary account returned one row and a marker; after the connection moved, the same caller continued with that marker and received the secondary account's containers |
| Identical requests from different accounts are distinguishable | Two calls with identical arguments for the same container name returned `only-in-primary` and `only-in-secondary`, and neither reply states which account answered |
| Control: each account answers its own listing | Passed |
| Control: a first page is answered by the connected account | Passed |

The distinguishability case inspects the reply type for a field that names an account rather than asserting a constant, so it begins passing by itself once such a field exists.
The absence of that field is a source fact rather than a runtime observation: `ContainerPageDTO` and `ObjectPageDTO` carry rows and a next marker only.

## Why the client's own guard does not cover it

`BrowserState` holds a monotonic scope counter and discards a reply whose captured scope no longer matches.
That is sound for transitions the client makes, and it clears every result, marker, error and selection on connect.
It does not cover a connection change the client did not make, because the counter tracks the client's intent rather than the backend's state, and `BrowseService.Connect` is reachable from the bridge.
It also does not move when the same bookmark identifier is reconnected after that bookmark was re-saved to a different account, because the setter compares identifiers.

## What this establishes and what it does not

It establishes that a listing read is answered by whatever connection is active when the call arrives, and that neither the request nor the reply carries an account, so a continuation can cross accounts and two accounts' answers cannot be told apart from the replies alone.

It does not establish anything about inspector detail reads, audit queries, job rows, find results or the metadata surface, none of which were exercised.
It does not measure how often a connection moves mid-listing in ordinary use; the probe moves it deliberately to show the consequence when it does.
No production source was changed to produce these results.

## Three method faults hit while implementing this

The broken-guard producer first reported all three controls as build failures.
That was the producer and not the controls: it copied four chosen files into each
throwaway worktree, leaving callers disagreeing with the signatures they call, so
every control failed to build for a reason that was not its mutation.
Three identical build failures are a signal about the harness, not three findings about the code.
It now carries the whole working tree, and the deliberate-syntax-error control is retained beside the others to show that a build failure is recorded as one rather than credited as a catch.

A bare `git stash` was used to take a baseline measurement, which this repository's working agreement forbids.
The stash stack is shared across every worktree on this machine and other work was running at the time, so the command could have taken somebody else's changes rather than these, and dropping what looked like the right entry afterwards is how that becomes unrecoverable.
The changes were recovered from the stash list, but the rule names a real trap rather than a theoretical one, and this is an instance of it.

A plan commit and its implementation shipped in one range with both documents still denying the implementation beside them: the design text read that the correction was not yet implemented, and this decision record's status read "Proposed. Nothing in it is implemented yet."
Two independent approvals passed that head without catching it, and it is the third occurrence of the same fault in this repository in a week.
The rule that a plan's tense is corrected before handover therefore works only when somebody runs the check as a step of its own; review has now demonstrably not caught it three times, so it cannot be relied on as the place this is found.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260914-claude/l11/`, digests in `SHA256SUMS` beside the files.
`probe/before-run.log` is the recorded run and `probe/l11_probe_test.go.txt` the probe source, archived rather than committed because it asserts the current behaviour and is replaced by maintained regressions when the correction lands.
