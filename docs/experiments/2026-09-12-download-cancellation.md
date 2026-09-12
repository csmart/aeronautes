# Download cancellation: one truthful record per unplaced item

## Scope and oracle

This experiment records download cancellation through the service on Linux amd64 for DS-07 and SEC-06 under P20: what the job, its item records and the selected directory show after a cancel that lands at each reachable point, before and after the correction.
The oracle is the cancellable job contract, DESIGN section 26.4 and section 32.1, the DS-07 acceptance row (cancel before staging, after staging, between placements, during native copy and during cleanup; only pre-cancel committed names may exist; the durable job state must match the filesystem snapshot) and ADR 0020's rule that nothing is reclaimed automatically.
Three product choices were settled by the operator on 12 September 2026 and are applied here as decided: a cancelled download ends partial when anything was placed and cancelled when nothing was; staged-but-unplaced bytes stay retained in the named stage after a cancel, and whether a graceful cancel should remove them is an open decision recorded under remaining limits; a cancel that lands inside a native body copy is classified as cancelled for that item, with the temporary removed and nothing published.
Nothing here establishes child quiescence after the cancel grace period, parent death, cancellation during stage cleanup or directory materialisation, or any behaviour on macOS or Windows; those are the next slice and GitHub issue 103.
The [JSON twin](2026-09-12-download-cancellation-probes.json) carries every observation with the digest of its raw file.

## Source finding

Line numbers are for master `0b5079ab`.
`DownloadInto` in `internal/transfer/local/download_execute.go` checks the context before each item (141), after each native or directory outcome (174), before the engine route (181) and before each placement of a staged file (263), and returns the context error after the placement loop (273).
The placement loop at 262 to 272 returns at the first cancelled check without recording an observation for the remaining staged items, whereas a cancel caught inside `placeDownloaded` (379 and 387) records failed, unresolved, commit, cancelled with the label place.
`finishNativeDownload` in `download_close.go` (24 to 68) copies the body with no context check of its own; a cancel that ends the read surfaces as a source-read failure with the effect unresolved (52), although `removeNativeTemporary` (73 to 85) removes the temporary while it is still the file the executor created and nothing is published.
`runLocal` in `internal/app/localtransferservice.go` maps a run error with copied items or any failure to partial and a bare context cancellation to cancelled; a cancelled native item is recorded as a failure, so a cancel that lands on a native item ends partial with nothing placed while a cancel after engine exit ends cancelled.
`itemTotals` in `internal/jobs/item_reads.go` (142 to 157) counts an admitted item without an execution record as unresolved and only a recorded diagnostic as a problem, so the same staged-but-unplaced item counts as one unresolved name at one cancel point and as one failed name, one unresolved name and one problem at the other.

## Deterministic boundary

Service level, on the owned mock and the owned real fixture: (a) the native GET held before its response by the interruption proxy's barrier; (b) the native body held by a front proxy after 65,536 of the object's 262,144 bytes reached the client, with the cancel issued only once the executor's temporary held those bytes; (c) the stage boundary between engine exit and placement; (d) a queued job cancelled before its slot frees, as the control.
Executor level, mock only: the placement loop's check after the first placement, reached by a verification hook that cancels, and `placeDownloaded` called with an already cancelled context on a staged file.
A cancel between two placements through the service is not reachable at this base and needs the placement boundary this slice adds beside the stage boundary; its before evidence is the executor-level loop check.
The [service probe](probes/2026-09-12-download-cancellation-service-probe_test.go.txt) and the [executor probe](probes/2026-09-12-download-cancellation-executor-probe_test.go.txt) are archived; each ran once per provider and wrote the observations the twin carries.

## Before evidence on the executor

At the loop check, item a is placed and verified (copied with the reported-applied effect, then verified), item b has no execution record, the stage retains b as a regular file, stage cleanup reports one issue (the stage directory is not empty), the report lists no failure and the run returns context canceled.
Inside placement, `placeDownloaded` with a cancelled context on a staged 7-byte file returns failed, unresolved, commit, cancelled with the label place and places nothing.

## Before evidence on the service

Both providers gave the same observations for every case.

| Case | State and message | Item records | Totals | Local delta |
| --- | --- | --- | --- | --- |
| native GET held | partial; `transfer failures (first: scope/n／native.bin: Get …: read bookmark generation: context canceled); context canceled` | native item failed, not_attempted, source_read, cancelled; both engine items not_attempted, not_attempted, engine, cancelled | 1 failed, 0 unresolved, 3 with problems | none; five requests, no write |
| native body interrupted | partial; `transfer failures (first: scope/n／native.bin: context canceled); context canceled` | native item failed, unresolved, source_read, cancelled; engine item not_attempted, engine, cancelled | 1 failed, 1 unresolved, 2 with problems | none: the temporary that held 65,536 bytes was removed |
| stage boundary | cancelled; `download stage cleanup: 2 issues (first: rmdir …/nested: directory not empty)` | both items admitted with their staging locations and no execution record | 0 failed, 2 unresolved, 0 with problems | the stage directory with a.bin (7 bytes), nested and nested/b.bin (7 bytes); no write |
| queued cancel | cancelled with an empty message | no item record | all zero | none; the slot-owning job completes |

Every case was reopened in a second process and the durable records equalled the ones read before the reopen.

## What the evidence establishes

Nothing is placed after a cancel at any probed point, no remote write occurs, placed items keep their copied and verified records, and staged bytes stay in the named stage.
The record of a staged-but-unplaced item depends on where the cancel lands: absent when the placement loop sees it, failed, unresolved, commit, cancelled when the placement itself sees it, with the item totals and the audit summary differing accordingly.
The job's state depends on where the cancel lands although nothing was placed in either case: partial when the cancelled item is native, cancelled when the cancel lands after engine exit.
A native item interrupted while its body is read is recorded with the effect unresolved although its temporary was removed and its target was never touched.

## What the evidence does not establish

It does not observe a cancel between two placements through the service, during stage cleanup or directory materialisation, the engine child's quiescence after the grace period, a parent that dies, or any platform other than Linux amd64.
It does not decide whether a graceful cancel should remove the retained stage.

## Contract

After a cancel, no further placement, native write or child write occurs; every placed item keeps its copied and verified records; the retained stage and its bytes are not touched.
Every staged-but-unplaced item carries exactly one execution record, failed with the effect unresolved at the commit stage and the cancelled code beside its recorded staging location, whether the placement loop or the placement itself sees the cancel.
A native item interrupted while its body is read records failed with no effect at its target and the cancelled code at the source-read stage once its temporary is removed, and unresolved with a cleanup issue when the temporary cannot be removed.
A cancelled download ends cancelled when nothing was placed and no failure other than a cancellation occurred, and partial when anything was placed or any other failure occurred; in both cases the message names the cancellation and the retained stage.
The filesystem delta is exactly the placed files, their planned directories, and the retained stage with its contents.
The durable job, item and audit records equal the filesystem after a second-process reopen.
Every delivered cancellation check, accounting behaviour and root-authority effect stays as it is.

## Required proof

Executor tests: the same observation and retained staging at both cancel points; the native mid-body record with the temporary removed, and the unresolved record with a cleanup issue when the temporary was replaced; the existing engine cancellation boundaries test updated to expect the uniform record instead of no record.
System tests on both providers for the held native GET, the interrupted native body, the stage boundary, the placement boundary between two placements and the queued cancel, each asserting the exact state, message fragments, counters, item records, totals, audit summary, local delta, absence of remote writes and the second-process reopen.
Two deliberately broken controls as overlays, one that places after the cancel and one that records a placed item as unplaced, each failing only its own cases.
The full race suite with the real fixture reconciled on the range head.

## Proposed shared-document text

For DESIGN, after the deletion request authority paragraph: the [download cancellation correction](experiments/2026-09-12-download-cancellation.md) delivered in PRNN gives every staged-but-unplaced item one record after a cancel, failed with an unresolved effect at the commit stage and the cancelled code beside its retained staging location, whether the placement loop or the placement itself sees the cancel; a native item interrupted while its body is read records no effect at its target once its temporary is removed, and unresolved with a cleanup issue when it cannot be; a cancelled download ends cancelled when nothing was placed and no failure other than a cancellation occurred, and partial otherwise, with the cancellation and the retained stage named in either case; removal of the retained stage on a graceful cancel, cancellation during stage cleanup and directory materialisation, child quiescence, parent death and the native platforms remain open under DS-07 and SEC-06.
For the DS-07 disposition in the data-safety register, before its closing sentence: PRNN then records one truthful outcome per staged-but-unplaced item after a cancel at every cancel point, classifies a native item interrupted mid-body as cancelled with no local residue, and ends a cancelled job cancelled when nothing was placed and partial when anything was, on both providers with a second-process reopen; and the closing sentence names what stays untouched: cancellation during stage cleanup and directory materialisation, removal of the retained stage on a graceful cancel, child quiescence and the native parent-kill evidence on all six targets.
For the DS-07 / SEC-06 remediation-plan row: PRNN establishes cancellation at native reads and placements with exact partial outcomes on Linux, one record per unplaced staged item, a cancelled classification for an interrupted native body and a cancelled state only when nothing was placed; P20/P30 must still establish cancellation during stage cleanup and directory materialisation and child quiescence before recovery.

## Correction delivered

The correction is one commit on top of the plan, `record every unplaced download item after a cancel`.
The placement loop in `internal/transfer/local/download_execute.go` gives every staged-but-unplaced item the record a cancel seen inside `placeDownloaded` already gave, through one helper both use: failed, unresolved at the commit stage, cancelled, with the label place and the staging location recorded at admission.
`finishNativeDownload` in `download_close.go` treats a read ended by the cancel as an interrupted body: the record has no effect at the target once the temporary is removed, and unresolved with a cleanup issue when the temporary is no longer the file the executor created.
Every recorded failure now carries whether it is the cancellation itself, set from the diagnostic by both local executors, and `runLocal` in `internal/app/localtransferservice.go` ends a cancelled run cancelled when nothing was copied and every failure is the cancellation, partial when anything was copied or any other failure occurred, with the message naming the interrupted item in both cases; the retained stage is appended as before.
`WireDownloadPlacementBoundary` beside the stage boundary lets an owned fixture act after each item outcome, which is between two placements on the engine route; it is composition in `internal/app/wiring.go`, excluded from the bridge like the stage boundary.
Nothing else changed: every existing context check, the retained stage, its cleanup and the root-authority effects are as they were.

## After evidence

The archived probes ran again on the corrected head and the maintained tests assert the same facts on both providers.

| Case | State and message | Item records | Totals | Local delta |
| --- | --- | --- | --- | --- |
| native GET held | cancelled; `transfer failures (first: scope/n／native.bin: Get …: read bookmark generation: context canceled)` | native item failed, not_attempted, source_read, cancelled; both engine items not_attempted, engine, cancelled | 1 failed, 0 unresolved, 3 with problems | none |
| native body interrupted | cancelled; `transfer failures (first: scope/n／native.bin: context canceled)` | native item failed, not_attempted, source_read, cancelled with its temporary removed; engine item not_attempted | 1 failed, 0 unresolved, 2 with problems | none |
| stage boundary | cancelled; `transfer failures (first: scope/a.bin: context canceled); download stage cleanup: 2 issues (first: rmdir …/nested: directory not empty)` | both items failed, unresolved, commit, cancelled beside their staging locations | 2 failed, 2 unresolved, 2 with problems | the stage with a.bin, nested and nested/b.bin |
| placement boundary | partial; `transfer failures (first: scope/b.bin: context canceled); context canceled; download stage cleanup: 1 issues (first: rmdir …: directory not empty)` | a.bin copied and verified; b.bin failed, unresolved, commit, cancelled beside its staging location | 1 failed, 1 unresolved, 1 with problems | a.bin placed; the stage with b.bin |
| queued cancel | cancelled with an empty message | no item record | all zero | none; the slot-owning job completes |

At the executor the loop check and the placement give the same record, the report names the unplaced item once as a cancellation, and the interrupted native body leaves no temporary behind, while a temporary replaced by a directory during the interruption is retained and reported with the effect unresolved.
Every case ends with a second-process reopen that reads the same durable job, item and audit records, and no case writes to either account.

## Deliberately broken controls

Two overlays on the implementation head were run against the executor cancellation tests and the system cancellation tests on both providers, with the head passing all twenty-six cases.
The first control makes the placement loop and the placement ignore the cancel; it fails exactly the loop and placement record tests, the after-child and between-placements boundaries, and the stage-boundary and placement-boundary cases on both providers, where a later placement is observed, and passes every native and queued case.
The second control records the item just placed as unplaced after a cancel; it fails exactly the loop record test, the between-placements boundary and the placement-boundary case on both providers, where a placed item's record must stay copied and verified, and passes every other case.
The twin lists each control's failing and passing cases.

## Verification

Static: gofmt clean; go vet on the local, app and systemtest packages; go build ./...; git diff --check clean.
Packages: the local transfer, app, jobs and systemtest packages pass with the change on both providers, including every earlier interruption, stage and root-authority case.
Full race suite with the real Swift fixture on the range head: on the implementation head, 4,139 distinct names, 3,834 leaves (3,833 passing, 0 failing, 1 skipped: the expected TestRcloneMatrix matrix skip), 22 test packages passing and none failing, run 2026-09-12T08:32:00Z to 08:40:04Z, stream 7,607,932 bytes sha256 a4629aeac43e9036a4ecaecfd11d602ebd7ad03224275fb60446e6137fb1df8a, retained with its reconciled text and checksums in the private evidence root; the record commit that follows changes documents only.

## Remaining limits

Whether a graceful cancel should remove the retained stage is an open operator decision; this slice keeps the stage exactly as before and names it in the message.
A cancel during stage cleanup or during directory materialisation has no seam and is not observed here; the parent open before a native temporary is created still records unresolved at the staging stage, which may over-report a created parent directory.
Child quiescence after the cancel grace period and parent death on Linux are the next slice, with a real engine child held at a gateway barrier.
Every platform other than Linux amd64, the native parent-kill evidence on all six targets and the Windows and macOS lifetime solutions belong to GitHub issue 103.
DS-07, SEC-06, P20, P30 and the valuable-data release hold remain blocking.
