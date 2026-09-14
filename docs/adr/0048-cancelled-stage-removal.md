# 0048. Removing a cancelled download's own stage

**Status:** Accepted for the cancelled download's own stage, which is delivered with this decision.
Residue from a killed run remains a separate operator-facing action, and P20 does not close.

## Context

[ADR 0043](0043-cancellation-delta-authority.md) decision 7 recorded an open product choice and left it to the operator: whether a graceful cancel removes the retained stage.
The operator has now answered it.
A graceful cancel removes the partial files the job created and says so; a crash or a kill retains them and that is reported accurately; there is no setting and no first-run prompt.

The operator's reasoning, recorded because it is the part a later reader needs: these bytes are the application's own incomplete work product, written under a name the application invented into a directory the application created, rather than the operator's data.
Deleting your own scratch output when told to stop is what comparable tools do, and with no resume feature a retained fragment cannot be continued, only deleted by hand.
The crash path is genuinely different because the application cannot clean up, so residue exists and must be reported rather than denied.

This is the opposite of the recommendation ADR 0043 offered, which was to retain deliberately.
That recommendation rested on the staged bytes being the operator's data.
Where the bytes actually sit decides that question, and the answer was established rather than assumed: the engine stage is `.aeronautes-stage-<job>` inside the operator's picked directory, and a native temporary is `.aeronautes-part-<random>` beside its eventual target, both named and placed by this executor.

The [cancelled stage record](../experiments/2026-09-14-cancelled-stage-removal.md) reproduces current behaviour on both routes through `Download` and `nativeDownloadBody`.
A cancel on the engine route leaves the staged files of every unplaced item in the picked directory, reports `Issues` of one with `rmdir ...: directory not empty`, and leaves `StageCleanupResult.Retained` false while the stage is in fact retained.
A cancel on the native route removes its temporary under an identity check and leaves nothing.
So the two routes already disagree, the engine route retains by failure rather than by decision, and the field that names the condition denies it.

## Decision

1. A cancelled download removes the files it planned into its own stage, then the planned directories beneath it, then the stage itself.
   This extends the cleanup authority of [ADR 0035](0035-download-root-authority.md) decision 7, which removes only empty planned directories; that decision is otherwise unchanged.
2. Removal is bounded by the names this job planned, never by listing the stage.
   Cleanup already derives its directory set from the job's items rather than from the directory; files follow the same rule, so a name this job did not plan is never a candidate for removal.
3. Every unlink is issued through the held stage directory handle, which this executor opened and holds open for the life of the job.
   No removal is issued through a pathname, so a rename of the stage cannot redirect one.
4. Only an ordinary file at a planned name is removed.
   A symbolic link, a directory, a device or anything else at a planned name is retained and reported, never followed and never unlinked.
5. This does not establish that the bytes at a planned name are the ones the engine wrote.
   The engine writes those files as a child process and this executor never holds a descriptor to them, so there is no identity to compare and none is claimed.
   What is bounded is the directory and the set of names, not the content.
   A substitution inside a hidden, randomly named, job-scoped directory during the job's own run would not be detected, and that is stated as a limit rather than covered by a weaker check.
6. The native route is unchanged.
   It already removes its temporary only while the entry at that name is still the file it created, comparing an identity captured while its own handle was open, and it already retains and reports a substituted entry.
7. Retention after a failure to record is unchanged and stays deliberate.
   When required item recording fails the run stops, cleanup is not attempted at all, and the stage is retained so the evidence of what happened is not destroyed along with the record that failed.
8. Two independent facts get two fields, rather than one flag carrying both.
   `StageCleanupResult.Attempted` says whether cleanup ran at all, and is false only under the recording-failure rule of decision 7.
   `StageCleanupResult.Retained` says whether anything remains under the stage afterwards, whatever kept it there: a refusal at a planned name, a removal that failed, or cleanup never having run.
   The two are independent in both directions — cleanup can run and still leave residue, and it can be skipped with nothing on disk — so one boolean cannot carry both without a reader having to know which era it was written in.
   Adding a field rather than redefining one is also the only version where a reader that is missed keeps its old meaning instead of silently acquiring a new one.
9. The service says which of three things happened, and never asserts the other two.
   Cleanup was not attempted because item recording failed; or cleanup ran and left residue, with the count and the first reason; or cleanup ran and left nothing.
   Before this record the operator-facing message and the audit summary were selected by `Retained` alone, which meant only the first of those, so redefining that flag without changing its reader would have told an operator that recording had failed and cleanup had been skipped whenever cleanup ran and deliberately left files behind.
   Both halves of that sentence would have been false, in operator-facing text and in a durable audit row, on a path that removes files from the operator's own directory.
10. A crash or a kill runs no cleanup at all and is out of scope for removal.
   Residue from such a run, and from versions before this record, is reported rather than denied; an action for the operator to remove it is separate work and is not authorised here.

## Consequences

A cancelled engine download now leaves the picked directory as it found it, apart from placed files and any ancestor directories the run created, which ADR 0043 decision 6 already refuses to remove.

Fourteen maintained test cases changed, across ten test functions in three packages.
The eight in `internal/transfer/local` are listed below; the other six are `TestDownloadStage`, `TestDownloadCancellation`, `TestDownloadRecords` and `TestDownloadRootAuthority` in `internal/systemtest`, each in both its mock and real-Swift form, and `TestDesktopHeldDownload` in the root package.
The first count named only the package being edited, which is where the flag was produced rather than where it was read.
The unit of that count is the test case that fails today against the implementation, not the assertion and not the file; one of the eight carries two distinct assertions and is marked below.
The list was produced by running the package against the implementation rather than by reading it, after a first estimate of two proved wrong, and it is complete.

1. `TestDownloadCancelledPlacementRecords/loop`, two assertions: that the staged bytes of an unplaced item survive a cancel, and that the report carries one cleanup issue.
2. `TestDownloadEngineCancellationBoundaries/after_child`.
3. `TestDownloadEngineCancellationBoundaries/between_placements`, which with the previous case asserts the same survival under the message that cancellation must not reclaim uncertain child data, and pins the issue count.
4. `TestDownloadPlacementOutcome/fault_true`, which pins the issue count when a permission fault prevents removal; the removal attempt now fails first and is reported.
5. `TestDownloadRecordsPartialChildEffects`, which pins one cleanup issue for a partial child.
6. `TestDownloadRootAuthority/engine_parent_after_stage`.
7. `TestDownloadRootAuthority/engine_existing_parent_after_stage`, which with the previous case carries an expectation of issues for a case that no longer leaves residue.
8. `TestDownloadStageCleanupPreservesOtherEntries/partial_file`, which writes the planned file itself and asserts it is preserved.

Every one of them pins the behaviour being replaced rather than an unrelated property.

The invariant `TestDownloadRootAuthority` actually protects is unchanged and is strengthened: residue implies a retained or failed report, and no residue implies no report.
That holds consistently only because `Retained` now describes the disk, so the two halves of the invariant cannot disagree.

Separately from the count, five cases in `TestDownloadStageCleanupPreservesOtherEntries` do **not** move, and they are the evidence that the bound holds: `foreign_file`, `foreign_directory`, `foreign_empty_directory`, `foreign_symlink` and `stage_file` all still retain and report, because a name this job did not plan is never a candidate.
Within that test only `partial_file`, entry 8 above, changes.

An item's staged bytes are no longer available for inspection after a cancelled engine download.
This matters for work that does not exist yet rather than for anything today: content binding on the engine route is recorded as an open gap in [ADR 0045](0045-route-content-binding.md), and if a future slice retains rejected engine bytes for the operator to see, as the native route already does for a content mismatch, it must exempt them from this removal or this removal will delete the evidence it wants to keep.

Removal applies to a job that ends, not only to one that is cancelled.
The operator settled the cancel case; treating an ordinary failure differently would leave partial files behind for one kind of ending and not another, which is not explainable to an operator and is not a distinction the stage's purpose supports.
The source of a download still holds the data in either case, so nothing is lost by removing a partial copy.
This is an extension beyond the literal decision and is flagged for the reviewer to accept or reject rather than absorbed.

No existing refusal is relaxed, the selected-root authority is unchanged, no destination ancestor becomes removable, and no scenario, audit, native-platform or valuable-data acceptance is granted.
