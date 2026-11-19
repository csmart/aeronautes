# Download stage lifecycle — 9 September 2026

## Scope and oracle

This implements the engine-stage prerequisite in [ADR 0032](../adr/0032-download-placement-prerequisites.md).
The [unchanged before-fix archive](2026-09-09-download-boundaries.md) shows recursive cleanup deleting an unrelated writer's file through actual services, and a smaller POSIX probe shows copied bytes disappearing from the report after successful placement and refused unlink.
The oracle is independently read full content, actual no-replace placement, checked cleanup effects, complete permitted local and remote deltas, and matching public/durable job and audit records.
The separately [delivered destination guard](2026-09-09-download-root.md#delivery) remains unchanged.

## Correction

[Download execution](../../internal/transfer/local/local.go) creates its stage with exclusive `Mkdir` rather than reusing an existing directory.
An existing file, directory or symlink refuses engine dispatch and remains unchanged.
An eligible destination with a nil internal plan now returns a refusal before stage creation, completing the earlier review's entry-contract follow-up.

Recursive deletion is removed.
[Group cleanup](https://github.com/csmart/aeronautes/blob/856b7ee862a3b3822f57e0ef0798a8c5591c9441/internal/transfer/local/download_stage.go) considers only the stage root and selected relative paths' ancestor directories, deepest first.
It checks expected directories without following symlinks and uses directory-only removal, which cannot unlink a file substituted for the directory.
It does not discover or delete arbitrary children.
Nonempty or refused directories and unrelated entries remain, while ordinary empty selected ancestors are removed.
The report has an explicit group issue count and first error, without inventing a failed object name.

A successful final `Link` contributes the copied object and bytes even when unlinking its staged pathname fails afterwards.
That refusal is a separate post-placement issue.
The [local service](../../internal/app/localtransferservice.go) appends group cleanup issues to the earlier job reason and prevents an otherwise complete job from hiding retained staging data.
It preserves earlier failed, partial and cancelled states and their reasons.
The matching terminal audit receives a bounded cleanup summary; raw filesystem errors stay in the job detail.
No schema or frontend DTO change is needed, and downloads remain record version zero.

## Maintained effect tests

The [actual-service cases](https://github.com/csmart/aeronautes/blob/856b7ee862a3b3822f57e0ef0798a8c5591c9441/internal/systemtest/download_stage_test.go) use pinned rclone with owned mock Swift and two-account SAIO fixtures.
Ten leaves per provider cover ordinary nested placement, refused staged unlink, an injected post-child error, four unrelated entry types, and three pre-existing stage types.
The unrelated entries are a file, a populated directory, an empty directory and a symlink.
Each is introduced and acknowledged while the selected GET is held at the existing response barrier.
Completion must preserve that exact entry and the complete owned filesystem, allowing only the selected full output and specifically required stage residue.

For existing-stage refusal, a first real download holds the single active-job slot.
The second job is observed queued before its exact generated stage path is populated with a file, directory or symlink.
After release, only the first child may complete; the second job fails without changing its existing stage.
The first job's full bytes and counters are checked too.

A narrow [per-invocation boundary](https://github.com/csmart/aeronautes/blob/856b7ee862a3b3822f57e0ef0798a8c5591c9441/internal/app/download_stage.go), configured through backend wiring outside the frontend bridge, acknowledges the return of the actual child before placement.
The permission fixture first reads all 21 selected staged bytes and proves the final target is absent.
It then removes write permission from the owned staged parent directory.
After release, the final and staged files must identify the same actual hard link, while an independent unlink attempt must return permission denied.
The partial job retains one copied and verified object, 21 copied bytes, zero failed objects, a post-placement issue and two group cleanup issues.
The normal control removes the complete nested stage and finishes without a cleanup warning.

The post-child refusal fixture preserves the actual child's complete staged bytes after injecting an error before placement.
It proves that this uncertain stage is retained beside the original failure reason and explicit cleanup result; it does not represent a child crash or a truncated network transfer.
The [executor tests](https://github.com/csmart/aeronautes/blob/856b7ee862a3b3822f57e0ef0798a8c5591c9441/internal/transfer/local/download_stage_test.go) separately preserve partial bytes, non-directory replacements and an outside empty directory behind a selected-ancestor symlink.
The original smaller [permission fixture](https://github.com/csmart/aeronautes/blob/856b7ee862a3b3822f57e0ef0798a8c5591c9441/internal/transfer/local/download_placement_test.go) is maintained alongside the broader service evidence.
The [completion tests](https://github.com/csmart/aeronautes/blob/856b7ee862a3b3822f57e0ef0798a8c5591c9441/internal/app/download_stage_test.go) cover eight combinations of earlier copies, failures, cancellation and later cleanup, and preserve simultaneous engine and injected boundary errors.

Every service leaf checks exact public/durable counters, state and reason, the matching terminal audit and its durable bookmark identity, and complete owned local and remote collateral.
History reruns remain refused, and an orderly fresh-process reopen preserves the complete saved state without storage requests or automatic cleanup.
These fixtures use ordinary remote objects and second-account sentinels; they do not establish shared DLO/SLO backing-data behaviour.

## Deliberate broken controls

Seven production overlays are rejected by maintained behavioural assertions, with no compiler failures or skips.
Across the runs, 23 intended failing leaves are accompanied by thirteen passing controls.

| Broken behaviour | Failing leaves |
| --- | ---: |
| Reuse existing stage directories or links | 4 service leaves |
| Restore recursive cleanup | 8 service leaves |
| Hide a copy after successful link and refused unlink | 2 service leaves |
| Discard the group cleanup outcome | 2 service leaves |
| Replace the earlier failure reason with the cleanup reason | 4 service leaves |
| Omit the audit cleanup reason | 2 service leaves |
| Follow a selected ancestor symlink during cleanup | 1 executor leaf |

Each run preserves its raw event stream, exit, exact production overlay and source hashes separately from the original archives.

## Verification and limits

The focused producer passes 51 named tests across the executor, app and system packages.
The complete `download-stage-full-v2` race/coverage producer passes 2,453 named tests across twenty packages with zero failures or skips and 86.8% statement coverage.
All 2,402 prior named tests are preserved alongside the 51 new tests, including independently checked expiry values for the one existing clock-generated subtest name.
Its 321 captured Go/SQL file hashes identify the executed uncommitted source at recorded HEAD `7ad1fb81707d472b6cb34ab6d10c67fb4747e854`.
The approved delivery documentation was fast-forwarded during that run without changing any of those sources; the recorded producer HEAD is not relabelled.
The system package takes 703.234 seconds with thirteen independent real-Swift groups under the unchanged parallel limit of three and no sequential fixture overlapping them.
Scenario reconciliation accepts the complete stream, actual zero exit and captured owned SAIO configuration; coverage remains 40/116 cells with 38/52 actions having gaps.
The 1,200-second package and 1,500-second hosted job limits remain unchanged; the [delivery evidence](#delivery) records actual PR/master timings.
Go vet, production native build, the internal build without CGO, frontend asset build and vendored Swift source/checker tests pass.
All 22 regenerated bridge files are unchanged.
The first service attempt failed compilation because a test expected a bookmark field in the UI audit DTO; the corrected test reads the durable audit table.
An earlier executor fixture attempt failed because its nested fixture parents had not been created; the corrected fixtures and their original failed streams remain separate.
An initial full-suite attempt omitted cross-package coverage instrumentation and was interrupted; it is not a baseline or passing evidence.
Independent local review and protected PR/master delivery passed as recorded below.

With owned SAIO and both accounts configured, verified rclone on PATH and `AERONAUTES_OFFICIAL_RCLONE_DIR` pointing to it, the focused replay is:

```sh
go test -race -count=1 -parallel=3 -timeout=20m \
  -run '^Test(DownloadStage|DownloadPlacementOutcome|DownloadPreservesExistingStage|DownloadRequiresPlanBeforeStage)' \
  ./internal/transfer/local ./internal/app ./internal/systemtest
```

The existing length check remains production verification; full test-body comparisons do not upgrade it to a strong checksum contract.
Pathname checks and directory-only removal do not bind a stable inode or exclude raced selected-path and ancestor replacement.
The selected staged-file unlink race remains part of P15/P18, and this change adds no automatic recovery reclamation.
Orderly reopen is not power-loss recovery or full native-application restart evidence.
Native source/file closure, temporary-file cleanup, directory-marker identity, exact download history and six-platform behaviour remain separate work.
No complete audit package, scenario gap or valuable-data gate is closed.
The [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.

## Delivery

[PR68](https://github.com/csmart/aeronautes/pull/68) merged through the normal protected rebase procedure on 9 September 2026 after independent local approval and all five successful latest-head checks against current master.
The reviewed source `e6ec3404f2b1e5e8d58803381a3555c10b7a615b` and resulting master `856b7ee862a3b3822f57e0ef0798a8c5591c9441` share exact tree `45dac841bc822b478f76476692765358e6d254df`.
Both logical patches are preserved: the prior destination-delivery record and the staging correction.
The original reviewed implementation remains separately preserved at `478c711342a584ca3bc3d605029d1dad593d5082`; the final source only corrects commit-message wrapping, with the same parent, tree and message words, and received another independent review and focused replay.

All five jobs passed in [PR run 34326590483](https://github.com/csmart/aeronautes/actions/runs/34326590483) and [master run 34328690498](https://github.com/csmart/aeronautes/actions/runs/34328690498).
The complete integration logs preserve all 2,453 local named tests: 2,451 pass and the same two explicitly optional tests skip in each hosted run.
All twenty packages pass, no test fails, and the single clock-generated expiry name is independently checked against each run's timestamps.

| Run | System package | Entire integration job |
| --- | ---: | ---: |
| PR | 996.216 seconds | 1,220 seconds |
| Master | 948.892 seconds | 1,192 seconds |

Both measurements remain below the existing finite limits, leaving at least 203.784 seconds at package scope and 280 seconds at job scope.
Continue measuring each added fault-test slice rather than assuming that this margin will remain constant.
The archived full PR log hashes to `18e0caad7999b64cc060cdd318731b080d1327c19dcad137a4c21f686ce028bb`; the master log hashes to `b5ad49df173134872c9b9505fa9172f965a411b0eac65eebe307e17511242792`.
This delivery establishes the bounded engine-stage prerequisite and leaves native closure, download identity recording and all wider valuable-data gates separate.
