# Swift fixture readiness, 14 September 2026

A heavy-lane job failed in a documentation-only pull request, and the failure was a harness seed rather than an assertion.
Reading it established that the readiness gate proves a weaker property than the tests need.
This record states what was observed, what was not, and the contract that follows.

## What was observed

Run 34829207753, job 103928287854, head `3e5a514003a2f934`, pull request 122, which changes one Markdown file and no source.

    --- FAIL: TestCopyCompletionRealSwift (115.24s)
        --- FAIL: TestCopyCompletionRealSwift/control (0.82s)
        --- PASS: TestCopyCompletionRealSwift/mixed (7.15s)

The failing output is not an expectation:

    harness.go:477: seed PUT /st45AFDQEBYHRYHKMB43F4DYWMZB-completion-src-1:
      HTTP 404 <html><h1>Not Found</h1><p>The resource could not be found.</p></html>

`Harness.Container` creates a throwaway container with a raw PUT, and `rawPutQuery` calls `Fatalf` on any non-2xx.
That refusal is deliberate and is recorded in the source: an unchecked seed leaves an empty container and the matrix would then assert a completed copy against source objects that are not there.
The test therefore never reached its own logic.

Timestamps from the same job:

| Event | Time |
| --- | --- |
| `Swift is up` | 09:41:15 |
| First test event in the package | 09:42:13.370 |
| `control` starts | 09:42:13.381 |
| Seed PUT answers 404 | 09:42:14.031 |

`control` began eleven milliseconds after the package's first test event, so it was the first case to write to the fixture, and it failed 0.65 seconds later.

## What was ruled out

**Fault injection leaking from a sibling case.**
`copyCompletionCases` iterates a slice literal, so case order is fixed and `control` is first; nothing ran before it.
A `queued_unavailable` line appears above the `control` failure in the log, but `go test` streams `=== RUN` as it goes and prints the `--- FAIL` summary after the parent completes, so that ordering is not chronology.
The `terminal outcome was not saved` message quoted alongside it belongs to `queued_unavailable`, which injects that fault deliberately.

**A concurrency increase from the lane rebalance.**
Both lanes run `-parallel=3`, so moving twelve tests to the heavy fixture did not raise the number of harnesses running at once.
What it did change is which test holds the first-writer position on that fixture.
That is a real mechanism and it is not established as the cause.

## What was not established

The cause of the 404 is unknown.
An account layer not yet usable is plausible: Swift answers 404 to a container PUT when the account does not exist, `scripts/saio-start.py` creates no accounts, and authentication returns a token and storage URL without creating one.
It is not proven, and fifty-nine seconds elapsed between `/info` answering and the failure, which is a long readiness window.
An account reaped between runs, or a fault specific to one account, are not excluded.

Reproducing it needs a cold fixture restart.
The shared fixture on loopback port 18080 was carrying other authors' suites throughout, so no restart was taken; that is the coordinator's to schedule rather than an author's to take.

## The gap, stated on its own terms

The pipeline gates on `/info` answering.
The harness gates on nothing: it authenticates and the first test write is a raw container PUT.

A test needs an authenticated client to be able to create a container on the account it was given.
The recorded run shows the existing gate passing at 09:41:15 while that client write did not succeed at 09:42:14 on the same fixture.
Whether or not the account layer explains that particular 404, the gate demonstrably does not establish the property the suite depends on, and correcting it stands on that observation rather than on a diagnosis.

## Contract

[ADR 0050](../adr/0050-swift-fixture-readiness.md) records the decision: gate on an authenticated container PUT and DELETE round trip on every required account, retried to a stated deadline, reported with the last status and body when the deadline passes, placed in the harness so a workstation run proves what the pipeline proves, and probing under the harness namespace so it cannot collide with a concurrent suite or leave residue.

## What this record does not claim

It does not claim to fix the observed failure, and no later green run may be cited as having closed it.
It establishes a gap the failure exposed.
No product source, assertion, deadline, lane membership, gate or release disposition changes, and the valuable-data hold is untouched.
