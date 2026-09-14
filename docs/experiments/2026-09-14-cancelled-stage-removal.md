# What a cancelled download leaves behind — 14 September 2026

This records what a graceful cancel leaves on disk on each download route, and what the report says about it, so [ADR 0048](../adr/0048-cancelled-stage-removal.md) is written against observed behaviour rather than against a reading of the code.
It is a reproduction and a contract; no correction is delivered by it.

## Why it was measured

[ADR 0043](../adr/0043-cancellation-delta-authority.md) decision 7 left one product choice to the operator and described current behaviour from the source.
The operator has settled it, and the answer turns on where the bytes actually are, so the description was checked rather than carried forward.

## Where the files are

Two shapes, both inside the directory the operator picked.

The engine route builds its stage as `filepath.Join(destDir, ".aeronautes-stage-" + stageID)`, where `destDir` is the picked destination and `stageID` is the job identifier.
The native route places `.aeronautes-part-<sixteen random bytes>` as a sibling of the file's eventual target, not inside a stage directory at all.
So a download can leave a hidden directory holding partial files, or hidden partial files spread beside their targets, or both.

## What a cancel does today

One probe, run through `Download` for the engine route and `nativeDownloadBody` for the native route, against the local mock session.
Three objects are seeded and the cancel is taken at the first verification, so one item is placed and the rest are staged and unplaced.

| Route | Residue under the picked directory | `StageCleanup` |
| --- | --- | --- |
| Engine | `.aeronautes-stage-probe/` holding `b` and `c`, seven bytes each | `Retained=false`, `Issues=1`, `rmdir ...: directory not empty` |
| Native | none; the temporary is gone | no issues |

The placed file `a` is not residue and is not counted as such.

The engine stage is therefore retained by a failed `rmdir` rather than by a decision, and `Retained` reads false while the stage is in fact retained.
What the operator sees is a cleanup issue naming an `rmdir` error, which describes the code path that was reached rather than the state of their directory.

The native route already removes its own temporary, under an identity captured while its own handle was open, and already retains and reports an entry that was substituted.
So the operator's decision is already met on one route and not on the other.

Probe and run are in the private packet at `~/code/.aeronautes-work/20260914-claude/cancelclean/probe`, `cancel_stage_probe_test.go` with SHA-256 `2fbc8066276d4d4124355c73cfd5db035276a76439c0144e1190b2e4de9314bc` and `probe-run.log` with SHA-256 `f3fb23d01045c5220a0a4fa27c85458274955f784c3a2c0d333d6c6e33e134ee`.
The probe is inert and is not carried in the repository.
It ran at load average 0.74 on twenty-four cores with no other suite on the host.

## What the contract will change

Removal of the files a job planned into its own stage, bounded by planned names and issued through the held stage handle, with anything that is not an ordinary file at a planned name retained and reported.
`Retained` becomes a statement about the disk rather than about one code path.
The decisions and their limits are in ADR 0048.

Eight maintained test cases change, across six test functions, not the two first estimated.
The unit is the failing test case; ADR 0048 lists all eight and marks the one that carries two assertions.
The list came from running the package against the implementation rather than from reading it, and the correction is recorded rather than absorbed, because the first figure was carried into a contract review and the second was stated without saying what it counted.
Five cases that do not move are the evidence that the bound holds: every foreign entry in the stage is still retained and reported, because only names this job planned are candidates.

## The correction, and what its controls establish

Cleanup now removes the files the job planned into its own stage before the directories above them, through the held stage handle.
`Retained` is taken from whether the stage survived rather than from the issue count.

Five deliberately broken controls, built by one producer into throwaway copies of the delivered source, each caught by its own named case:

| Control | Guard removed | Caught by |
| --- | --- | --- |
| g01 | the ordinary-file check at a planned name | `TestDownloadStageRemovesOnlyPlannedFiles` |
| g02 | `Retained` taken from the stage, replaced by the issue count | `TestDownloadStageRetainedIsNotTheIssueCount` |
| g03 | the file-removal pass itself | `TestDownloadCancelledPlacementRecords` |
| g04 | the scope check on the planned name itself | `TestDownloadStageRefusesAnEscapingNameItself` |
| g05 | the displacement check before any removal | `TestDownloadStageLeavesAMovedStageAlone`, both cases |

The producer credits a control only when the package compiled with its tests and a named case failed, and `go vet` is used for that because `go build` does not compile test files.
A retained self-test inserts a deliberate syntax error and confirms the producer reports a build failure and refuses to credit it.

Two of these were wrong on the first run and were diagnosed rather than filed.
`g01` and `g02` did not compile, because removing each guard left a variable unused; the mutations were rewritten to keep the code buildable so they test the guard rather than the compiler.
`g04` compiled and was not caught, and the reason is worth recording: `os.Root` refuses an escaping name on its own, so removing this code's own scope check changed the refusal's message without changing its effect.
That made the check defence in depth rather than load-bearing, and the honest response was to pin the property it actually provides — that the refusal is made by this code before any filesystem call, and names the cleanup scope rather than leaking a path-escape error from a lower layer.

## The flag had a consumer that meant something else

A first correction changed `StageCleanupResult.Retained` to describe the disk and left its only consumer untouched.
That consumer selects the operator-facing message and the audit summary, and it read the flag as its original single meaning: that required item recording had failed and cleanup was never attempted.
The result was a product that would tell an operator recording had failed and cleanup had been skipped, whenever cleanup ran and deliberately left files behind — both halves of one sentence false, in operator-facing text and in a durable audit row, on a path that removes files from the operator's own directory.

The whole module was run with the race detector against the real fixture and failed: two packages, thirty-four failing cases.
Every one was caused by that change rather than by load or existing flakiness, which was established by running the same selection at the base in a detached worktree and seeing none of them fail, despite the suite running at load 28 alongside two other worktrees.

The moving expectations had been counted twice, correctly, in the package being edited, and never in the packages that *read* the value being redefined.
Changing what a field means is not the same as changing a field, and the readers are where the meaning lives.
The final count is fourteen cases across ten functions in three packages, against the eight in one package first reported.
The size of that gap is the point rather than the principle behind it: six of the fourteen, and two of the three packages, were invisible to a count taken where the flag was produced instead of where it was read.

The correction is the split in ADR 0048 decisions 8 and 9: one field for whether cleanup ran, another for what remains, because the two facts are independent in both directions and one boolean cannot carry both without a reader having to know which era it was written in.
Adding a field rather than redefining one also leaves any reader that is missed with its old meaning instead of a silently acquired new one.

## A second defect the systemtests caught: a moved stage

The held stage handle follows the directory's identity, not its name.
That property is a guarantee in one direction and a hazard in the other, and only the first was designed for: it is what makes decision 3 work, because a rename cannot redirect a removal, and it is equally why the first implementation removed files from a stage another actor had moved out from under it.
`TestDownloadRootAuthority*/engine_stage_relocated_during_get` caught it: the child's bytes were gone from the relocated stage, where ADR 0035 decision 7 retains and reports them instead.

The same property is a guarantee in one direction and a hazard in the other, and only the second was designed for.
The first fix was narrower than the hazard: it skipped the file pass when displaced and left the directory pass running through the same handle.
With the planned tree already empty there is then no file to make an rmdir fail, so the planned directories are unlinked inside the moved location and nothing reports it.
Independent review caught that, and the first version of the moved-stage regression missed it because it only asserted that a still-present staged file survived.

Nothing is removed once displacement is known, files and directories alike, and the regression now covers an empty planned tree.
The lesson is in how it was found rather than in the fix: a regression that can only pass is the same failure as a control that can only fail, because neither tells you anything and both look like evidence.
A test that does not fail against the defect it was written for is not a test, which is why the narrow guard was restored to prove the new case earns its place.
Restoring the narrow guard demonstrates the difference: the still-present case passes and the empty-tree case fails with a directory removed inside the moved stage.

## Verification on the delivered head

Whole module, race detector, real two-account fixture, pinned engine, frontend built so the root package compiles.
Exit 0; 22 packages pass and `cmd/mockswift` has no tests; 4,650 distinct test names; 4,260 leaves; 4,649 passing events including parents; `TestRcloneMatrix` the only skip; empty stderr.
It ran alone on a 24-core host, load 1.06 at the start and 6.21 at the end, with no other suite present, so the timing-sensitive cancellation, interruption and child-lifetime groups were not competing for the machine.

## What this does not establish

That the bytes at a planned name inside the stage are the ones the engine wrote.
The engine is a child process and this executor never holds a descriptor to the files it writes, so no identity comparison exists to make and none is claimed.

That a crash leaves nothing behind.
No cleanup runs at all in that case; residue from a killed run, or from a version before this record, is unaffected and needs a separate operator-facing action that this record does not authorise.

That anything is established about upload staging, the copy routes, or engine-route content verification, none of which were exercised.
