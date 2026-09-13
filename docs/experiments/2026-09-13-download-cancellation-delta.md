# Download cancellation leaves two different deltas — 13 September 2026

Finish-line item L07, P20 directory materialisation and stage cleanup under cancellation.
This record is the reproduction and the contract proposal in [ADR 0043](../adr/0043-cancellation-delta-authority.md).
No correction is implemented by this record.

## What was already covered

Cancellation already has maintained regressions at four boundaries and a queued case:
held native headers, mid native body, the stage boundary and the placement boundary, in
`internal/systemtest/download_cancellation_test.go`, with package twins in
`internal/transfer/local/download_cancellation_test.go`.
Those cover what a cancel does to *files*.
None of them observes what a cancel does to *directories*, which is the half L07 names.

## The reproduction

One selection, one held body, twelve runs of the unchanged fixture through the
bridge-facing `PickDownloadDir`, `StartDownload` and `CancelJob`.

The selection is a directory marker `scope/aaa/` and one native-routed object nested at
`scope/deep/inner/n／native.bin`, so the marker materialises first and the native item has
ancestors that do not exist yet.
A reverse proxy in front of the primary account holds the object body after 64 KiB, the
cancel is issued while the body is held, and the hold is then released.

Twelve runs produced three outcomes:

| Runs | Directories in the picked directory | Item record | `ObjectsFailed` |
| --- | --- | --- | --- |
| 7 | `aaa`, `deep`, `deep/inner` | `failed`, effect `unresolved`, stage `staging`, code `cancelled` | 1 |
| 4 | `aaa` only | `not_attempted`, effect `not_attempted`, stage `source_read`, code `cancelled` | 0 |
| 1 | `aaa` only | `failed`, effect `not_attempted`, stage `source_read`, code `cancelled` | 1 |

The third outcome stops *earlier* than the other two, not later: the source read itself
fails on a cancelled read before the ancestor walk is reached, so it creates nothing and
its effect is correctly `not_attempted`.
It is counted here because it is part of the same twelve runs and because an earlier
version of this record described it as a run whose cancel landed later still, which was
wrong.

Two cancellation checks sit either side of the ancestor walk in
`internal/transfer/local/download_execute.go`; the walk creates absent components.
Whichever check observes the cancel first decides both what is created and what is
recorded, and nothing chooses between them.

## Why this is a defect and not just variation

A cancel is asynchronous, so which check observes it is not fixed and cannot be made
fixed.
Independent review established that directly: removing the check between the ancestor
walk and the temporary does not collapse the orderings, because cancellation still
arrives before or after the remaining check, and it permits a temporary to be created
after cancellation where today none is.
So the variation in *when* the executor stops is not itself the defect, and an earlier
version of this record was wrong to propose one deciding boundary.

The defect is that the permitted effects for each ordering are unstated, and that the
records describe the wrong thing.

The truthfulness failure runs the wrong way round.
In the seven runs that left real directories behind, the item is recorded `unresolved` —
which tells the operator that residue may exist and they should go and look — about a file
that was certainly never created, because the check that fired precedes the temporary's
exclusive creation.
The directories that certainly were created are recorded nowhere.
The five runs that created nothing record `not_attempted`, which is correct, and which is
also what the upload twin and both neighbouring download checks already record.

## Limits of this reproduction

Mock Swift only so far; the real Swift fixture repeats the same two outcomes and is
recorded with the implementation rather than here.
The split between the outcomes is a scheduling race and its ratio is not a stable figure;
the finding is that each outcome occurs, not how often.
The held proxy response establishes that the server has made progress, not that the
executor has reached a fixed boundary, so it selects the region the cancel lands in and
not the instruction.
Nothing here establishes anything about the engine route's own cancellation, which keeps
its existing coverage, nor about whether a graceful cancel should remove the retained
stage, which ADR 0043 decision 7 leaves to the operator.

## The correction

Nothing was removed. Both cancellation checks either side of the ancestor walk stay,
because each prevents work the other cannot and a cancel cannot be made to land in one
place.

An item cancelled between the walk and the temporary is now recorded not attempted at the
source read rather than failed and unresolved at staging: the temporary's exclusive
creation is below that check, so no file and no temporary exists for the item and the
effect is certain.
That is what the check above it and the upload twin already record.
The diagnostic code was already cancelled in both, because classification maps a cancelled
error to it whatever the call site passes; the result, the effect and the stage are what
change.

The component walk reports the components it created, and the decision is a named
predicate rather than a condition buried in the walk: a component counts only when the
entry was absent when inspected and this call's own `Mkdir` succeeded.
A directory that already existed, or one another writer won in the same window and whose
`Mkdir` therefore returned `ErrExist`, is never counted.
Every return after the walk carries what the walk made, whichever way the item ends,
because the directories exist either way.
Ancestors are counted for an item that did not copy.
An item that copied has the directories it needed as its intended outcome and its own
record already carries the placed file, so counting them again would describe the result
rather than what a stopped job left behind: the distinction is intended placement against
residue.
That per-item scope sits beside the job-level one — the report counts them with a bounded
sample that never truncates the count, and the service names them in the terminal message
and audit summary of a job that did not complete.

## What the controls found

Seven independently broken controls, each removing one guard in a scratch copy of the
committed tree, all caught by their own named case.
Three of them found real weaknesses in the first version of these tests rather than
confirming it, and all three are worth recording.

`g02` and `g03` were undetected at first: the creation decision was a condition inside the
walk, and no test could reach the raced `ErrExist` branch without a filesystem race.
Extracting `componentCreated` as a predicate made the property testable directly, which is
why it is a named function rather than an inline condition.

`g07` removes the guard that a walk told not to create never creates.
It was undetected because nothing asserted that at all, and the stage cleanup walks that
way — a cleanup that materialised the directory it was inspecting would leave the operator
a directory nobody asked for.
That is now a test.

`g06` removes the operator-facing sentence, and it was caught, then undetected, then
caught again.
The middle state was the honest one: the system regression only asserted the reporting
invariant on runs where the walk happened to create directories, so a run that cancelled
earlier passed it vacuously.
The first repair repeated the scenario until it reached that case, which independent
review rejected: every early cancellation it retried past is permitted by this contract,
so a test that fails after eight of them reports a scheduling outcome as a defect.
The reporting case now waits for the walk's own effect before cancelling, and early
cancellation is a separate mode asserted in its own right.

`g04` was reported as caught and was not: it removed the propagation in a way that left an
unused variable, so the package did not compile and no assertion ran. A control that does
not build proves nothing, and the producer now says so — a build failure is recorded as a
build failure and can never read as caught. The control is now a compilable omission, and
the maintained case catches it with `created ancestors were not reported in order: []`.

## Verification on the delivered head

Whole module with the race detector, the two-account Swift fixture on loopback and the
pinned official rclone: exit 0, 22 packages passing with none failing, 4,609 distinct test
names, 4,226 leaves of which 4,225 pass, 4,608 passing events including parents, and the
only skip the expected `TestRcloneMatrix`.
Empty standard error.
No other suite ran alongside it at the start and two did by the end, with the load average
moving from 3.86 to 7.68 on 24 cores; nothing timing-sensitive failed, so no case needed
an isolated re-run.

The system regression runs against both mock Swift and the real fixture, in two modes.
The reporting mode waits until the ancestor walk has actually created its directories
before cancelling, because the proxy releasing bytes says the server made progress and not
that the executor reached any particular point; observing the walk's own effect makes the
case deterministic instead of hoping the scheduler obliges.
The early mode cancels as soon as the body is held, which is an equally permitted ordering
that must leave nothing behind and claim nothing.
Both modes assert the durable job message and the audit summary against what is on disk,
and require the cancelled item's own record to exist.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/l07/`, digests in
`SHA256SUMS` beside the files, that manifest's own SHA-256 being
`fab6439818aaed9f10c9830c37d668b665c56450dd7a362a79e46a0ad40b610e`.
`before-probe-mock.log` is the twelve-run reproduction;
`probe_system_dir_cancel_test.go.txt` and `probe_local_dir_cancel_test.go.txt` are the
probes that produced it, archived rather than committed because they assert nothing.
`build_guards.py` is the single producer and `guards.json` its results with the head each
control was built from; `full-suite.jsonl`, `full-suite.meta` and `full-suite.stderr` are
the delivered-head run and the load and concurrency recorded around it.
The controls were built from the code at the delivered head, which `guards.json` records;
the only change after that is the line naming this manifest, because sealing the packet
changes the digest the line cites.
