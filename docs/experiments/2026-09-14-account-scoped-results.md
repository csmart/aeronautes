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

## Evidence

Durable private packet `~/code/.aeronautes-work/20260914-claude/l11/`, digests in `SHA256SUMS` beside the files.
`probe/before-run.log` is the recorded run and `probe/l11_probe_test.go.txt` the probe source, archived rather than committed because it asserts the current behaviour and is replaced by maintained regressions when the correction lands.
