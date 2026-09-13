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
stage, which ADR 0043 decision 5 leaves to the operator.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/l07/`, digests in
`SHA256SUMS` beside the files, that manifest's own SHA-256 being
`5a41f3c90e0b3b42c45f17d49f63a4e33a7dea0174f4f721ed9e0c5c5bc9e231`.
`before-probe-mock.log` is the twelve-run reproduction;
`probe_system_dir_cancel_test.go.txt` and `probe_local_dir_cancel_test.go.txt` are the
probes that produced it, archived rather than committed because they assert nothing.
