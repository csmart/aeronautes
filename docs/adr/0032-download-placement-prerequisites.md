# ADR 0032 — download placement prerequisites

**Status:** Accepted after independent local review under the authorised audit remediation plan.
**Scope:** Bounded pathname, placement and staging corrections before the separate download item-record migration.
The reviewed prerequisite sequence governs the separate implementation changes below.

## Problem and oracle

The [reviewed download archive](../experiments/2026-09-09-download-boundaries.md) reproduces writes to a different directory from the captured selection and deletion of another writer's staging file through actual services on both owned providers.
It also reproduces a native stream-close corruption error hidden behind a complete job, copied bytes omitted after a successful link and refused unlink, and failure identities truncated beyond 200 samples.
Independent review reproduced all seventeen failing leaves and seventeen controls on unchanged application sources.

The behaviour oracle is the unchanged backend-captured path, independently read full content, exact permitted filesystem and remote deltas, and the actual placement, stream-closure and cleanup effects.
The local completion rules in [ADRs 0023](0023-upload-post-commit-errors.md), [0024](0024-local-job-completion.md) and [0025](0025-local-interruption-outcomes.md) require successful copies to remain visible alongside later issues and reasons.
The original before-fix archives stay unchanged.

Prioritise the wrong-directory write and recursive deletion because they affect data directly.
Complete these bounded corrections before enabling download record version one.
The later item-record contract must separately define native temporary-file admission, directory markers, group cleanup observations, checked store failures and exact terminal totals; this decision does not invent those mappings.

## Captured destination spelling

Require a valid UTF-8 absolute destination path whose spelling is unchanged by the host's `filepath.Clean`.
Check it at download preflight entry before any destination stat, path join or source listing, and again at executor entry before any local write or engine invocation.
Return one rejecting preflight problem with a constant destination label and safe constant explanation; do not render an invalid path lossily or call every refusal a collision.
Reject the whole selection when the guard fails, including an empty remote scope.
Do not clean the path, resolve symlinks, normalise Unicode or silently choose another execution route to accept it.

Preserve accepted spelling, including case, valid replacement characters and supported Unicode, throughout preflight and execution.
A fresh selection through a supported absolute spelling is the recovery path.
This conservative compatibility restriction intentionally refuses relative, dot-component, repeated-separator and single trailing-separator spellings even when a particular spelling would happen to resolve harmlessly.
An absolute filesystem root whose spelling is already clean remains eligible.
It does not bind the selected directory's inode, exclude symlink replacement or certify a native picker on any platform.
P18/P19/P11 remain responsible for those wider boundaries.

## Stage creation and conservative cleanup

An engine stage must be created as a new directory for this invocation; do not reuse an existing directory through `MkdirAll`.
An existing file, directory or symlink at the intended stage path refuses engine dispatch and is retained unchanged.
The backend-generated job identifier remains the source of the stage name; recorded or displayed paths do not grant cleanup or retry authority.

Remove the recursive stage deletion.
After the existing successful no-replace placement, cleanup may unlink that item's staged pathname as before, but its result must be checked and reported separately from the already witnessed placement.
On an unsuccessful or interrupted engine invocation, retain partial or uncertain staged files instead of recursively deleting them.
Do not discover arbitrary staging children and delete them merely because they are under a generated directory name.

After selected-file handling, attempt removal only of empty directories corresponding to the stage root and the selected relative paths' ancestor directories, deepest first.
Do not recursively traverse unrelated children or remove a non-directory as a substitute for an expected directory.
A nonempty directory, unexpected entry or removal refusal is retained and reported as a cleanup issue, while any earlier completed copies and failures remain visible.
Normal nested downloads must still remove their now-empty selected stage directories.
An unrelated file, directory, symlink or replacement at an unselected name must remain untouched.

The group cleanup result must be explicit in the returned outcome and final job/audit reason; do not fabricate a failed object identity to represent an issue affecting the whole stage.
Keep issue counts separate from copied objects, copied bytes and failed-object samples.
A later group cleanup refusal cannot turn an earlier failed or partially copied job into complete, and cannot erase its earlier reason.
Retaining uncertain residue is deliberate; this change does not add automatic recovery reclamation.

This bounds the cleanup scope and prevents the archived unrelated-file deletion.
Pathname checks and empty-directory removal do not prove stable root identity or exclusive authority against concurrent replacement of selected paths.
Those stronger P15/P18 boundaries remain open, including the existing selected staged-file unlink race.

## Placement and closure observations

Distinguish failure before final placement from a cleanup error after `os.Link` has succeeded.
For engine placement, retain the existing staged-size check and final no-replace link.
A successful link contributes one copied object and the witnessed bytes even if unlinking the staged path fails afterwards; record that refusal as a separate post-placement issue.
A failed link contributes no successful final copy and must preserve the existing target.
Do not infer no effect merely from the helper returning an error.

For native download, read into the owned temporary file, check the transferred length and required file sync/close, and explicitly check the source reader's `Close` before attempting the final link.
A read, length, sync, file-close or source-close failure before the link must leave the final target absent or unchanged.
Check cleanup of the temporary pathname on every return path and preserve simultaneous primary and cleanup errors.
After a successful link, retain copied objects and bytes beside any later temporary-file cleanup refusal.
Do not treat successful `io.Copy` as proof that the source reader's final integrity check succeeded.

The existing length checks remain the verification mechanism for this bounded change.
No stronger checksum, stable-inode or physical-durability claim follows from a successful size check, `Sync`, link or orderly database reopen.
Directory-marker accounting, exact identity history and cancellation at every placement boundary remain part of their separately reviewed work.

## Implementation and proof sequence

1. Review this decision and the exact archived before-fix evidence before changing the executor or caller.
   Address the integration scheduling constraint before adding maintained service cases: preserve every test, assertion, race/coverage setting, producer-exit check, scenario reconciliation and all five required jobs.
   Measure bounded overlap of independent fixtures, retain environment-mutating fixtures' sequential execution and keep the existing finite package/job limits.
   Give that test-scheduling change its own implementation commit and independent review.
2. Implement the captured-root guard as its own logical change with preflight/executor and actual-service regressions on both routes and providers.
   Require rejection before a job or child starts, zero local delta, unchanged complete owned remote namespaces and canonical-path controls with full selected bytes.
   Include invalid UTF-8, relative and Clean-changing spellings, and accepted exact Unicode and symlink spellings without claiming stable symlink targets.
3. Replace recursive engine cleanup and unchecked post-link reporting together as one logical stage-lifecycle correction.
   Use actual services and the real child to retain an unrelated file after the acknowledged GET barrier, preserve an existing stage before dispatch, remove ordinary nested empty stages and retain partial stages on failure.
   Extend the smaller permission-denial probe through an acknowledged post-child/pre-placement boundary before claiming actual-service placement evidence.
   Check the actual link identity, refused unlink, complete local and remote deltas, copied-byte facts and explicit final cleanup issue.
   Add a narrow per-invocation fault boundary with its first test if needed; do not add a generic fault framework or expose the hook through the frontend bridge.
4. Check native source/file closure and temporary cleanup as a separate logical change.
   Preserve the real streamed GET/HEAD disagreement on both providers, with an independently witnessed source-close error, no final placement for that fault and a passing ordinary-length control.
   Exercise final-link refusal and simultaneous primary/cleanup errors while retaining complete original and final bytes.
5. For each change, compare full public/durable jobs and matching audits, reject history reruns, and reopen state orderly without HTTP or automatic cleanup.
   Define the exact allowed local/remote delta rather than accepting arbitrary staging residue; the only permitted extra residue must follow the selected fault.
   Preserve shared DLO/SLO backing-data fixtures where applicable and state any narrower fixture scope honestly.
   Remove each relevant guard, closure check, issue observation or cleanup restriction in deliberate broken controls and require the maintained behavioural assertion to fail.
6. Run focused tests first, then the relevant complete race/coverage and build checks against the committed source; verify the full test inventory and scenario report after scheduling changes.
   Require independent local review for every logical commit, all five latest PR checks on current master, protected merge of the approved head and all five resulting master checks.
   Record actual runtime and source/merged tree equality, without widening limits to hide a regression.
7. After these prerequisites pass, review the separate download recording contract and migrate its executor/caller together with complete 0/1/201/401 identity, store-refusal, UI and reopening evidence.
   Downloads remain record version zero until that migration is implemented, reviewed and delivered.

## Limits

This decision addresses the archived bounded placement and cleanup defects; it does not close P13/P15/P18/P19/P20/P28 or any complete audit package.
Stable filesystem capabilities, raced ancestor replacement, full-path collision identity, stronger content verification, concurrency authority, physical durability, native approvals and child quiescence, retention/export and all six platform targets remain separate release work.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused.
The [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.
