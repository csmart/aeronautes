# Checked download item records

## Scope and oracle

This implements the independently approved [ADR 0033](../adr/0033-download-item-recording.md) contract after the delivered captured-root, engine-stage and native-closure prerequisites in [ADR 0032](../adr/0032-download-placement-prerequisites.md).
Independent implementation and integrated-range review passed, followed by [protected PR75 delivery](#protected-delivery).
The original [download archive](2026-09-09-download-boundaries.md) remains unchanged: it demonstrates the delivered version-zero caller truncating 201 and 401 failed names to 200 on both execution routes and owned providers.

The oracle is the exact preflight selection, independently read destination content and complete allowed filesystem/remote delta, compared with the actual item rows, public pages and matching terminal job/audit facts.
The existing [item-store contract](../adr/0028-transfer-item-records.md) defines available, unavailable and historical unrecorded reads, phase ordering, typed diagnostics and the six overlapping facets.
Recorded paths and listed lengths are observations; they confer no stable filesystem identity, strong content verification or cleanup authority.

## Implementation

The [download executor](../../internal/transfer/local/download_execute.go) accepts an optional complete [recorder](../../internal/transfer/local/download_recording.go).
The [application adapter](../../internal/app/download_items.go) records the exact source bookmark/container/full object name, unchanged selected destination and actual planned temporary location.
It validates the constructed identity and intent before attempting a transaction.
Native temporary names are selected in memory, acknowledged, then exclusively created at that exact path with private permissions.
An admitted collision is retained and no alternate temporary name is attempted.
Directory markers have no invented staging file; successful materialisation requires an observed directory and contributes zero copied bytes.
Every engine member is admitted before exclusive group-stage creation or child dispatch.

The executor preserves helper-witnessed placement, bytes, closure checks and permitted item cleanup before recording execution, then checks execution acknowledgement before recording verification.
A required record refusal stops subsequent reads, placements, retries and deferred group cleanup.
A deliberately retained group stage is reported separately from actual cleanup issue counts, including when the count is zero.
Known cancellation is checked before dependent work; an already started child can still have effects and does not acquire a fabricated no-effect result.
The native closure helper preserves independent read and close errors, failed verification actually attempted, copied bytes after placement and separate cleanup problems.

New application downloads use record version one throughout admission, queued cancellation and completion.
The shared atomic completion transaction derives distinct failed names and all item facets from the ledger, while copied/verified/byte counters retain helper-witnessed results.
This deliberately permits a witnessed verified counter beside a missing durable verification observation, an unverified facet and unavailable public reads after its write is refused.
Live download progress counts placed items; attempted failures do not become copied objects.
The sampled error label is now `transfer failures` for both recorded local directions.
Historical jobs retain their unrecorded status, and direct executor callers can still omit the recorder.

## Maintained actual-service evidence

The [complete identity cases](../../internal/systemtest/download_records_test.go) run through the application service on mock Swift and two-account SAIO.
Each provider exercises both native and engine routes with 0, 1, 201 and 401 problematic ordinary local filenames plus one successful file.
The native cases retain a newline in the remote prefix without turning it into an unsupported local name.
Actual native source-open refusal is a failed name with positive knowledge of no local effect; an engine final-target race is a conflict with unresolved staged effects, not another failed-name count.
The [assertions](../../internal/systemtest/download_records_assertions_test.go) read every item through bounded pages, compare exact source/destination/staging identities, verify independent file content, and require exact terminal/audit facts with no missing or duplicate keys.
They retain unrelated local stages and owned shared DLO/SLO collateral and compare source/destination account snapshots.

The [record-refusal cases](../../internal/systemtest/download_records_faults_test.go) install real SQL triggers before starting the service.
The selected trigger refuses admission or an execution/verification insert at the actual store boundary; filesystem effects and selected-object GETs are independently inspected after completion.
This proves the real service/SQL/effect sequence, not a separate observer paused between the final link and the refused insert.
Both providers cover native files, engine batches and directory markers, first/second admission refusal, execution refusal, verification refusal and writable controls.
A mixed engine/directory case refuses the later marker admission while the earlier engine member is only buffered: no child or local effect occurs and that earlier item records `not_attempted` when storage permits.
After an engine observation refusal, the already downloaded batch retains unplaced members and group cleanup is explicitly unattempted with zero invented issues.
Native helper cleanup has already finished before its execution acknowledgement can fail.
Actual public reads become unavailable, all eight job families sharing the store refuse new work, earlier rows remain inspectable in the database and orderly child reopening preserves every retained and missing phase.

The [empty and marker cases](../../internal/systemtest/download_records_empty_test.go) distinguish a new available empty ledger from a historical version-zero job.
Root, nested and pre-existing directory markers keep their exact nonempty remote identities, map to the captured local paths, record no temporary location and count zero bytes.
They preserve existing directories and collateral files, require no selected-object body GETs, and retain the complete record/audit facts across orderly reopening.
Local-history rerun remains refused.

Existing [native closure](../../internal/systemtest/download_close_test.go), [engine-stage](../../internal/systemtest/download_stage_test.go) and [local-completion](../../internal/systemtest/local_completion_test.go) regressions now also assert the new version-one records and facets.
Their existing real effects, closure/cleanup faults, retained foreign entries and audit assertions remain in place.
The [captured-root cases](../../internal/systemtest/download_root_guard_test.go) replace the former unrecorded-history expectation with exact available version-one records, preserving original-root, source-content, collateral and rerun-refusal checks.
The [queued Copy cases](../../internal/systemtest/copy_completion_test.go) also inspect the exact admitted history of the download holding their runner slot.
When the queued Copy's completion refuses a required store write, the already admitted download may retain its witnessed copied and verified facts, while the shared latch makes its public item reads unavailable and prevents an unqualified complete outcome.
The existing version-one completion path preserves that original refusal in a partial job and matching audit; the tests now require those facts rather than the former version-zero complete result.
Copy's own queued outcome, fault trigger, source and destination checks, and reopen assertions remain unchanged.
The existing upload, recovery and interruption assertions change only the neutral sampled-failure label where needed.

## Focused boundaries and evidence limits

The [local recorder tests](../../internal/transfer/local/download_recording_test.go) cover acknowledgement ordering, admitted temporary file/directory/symlink collisions, exact name/prefix/relative-path mapping, known cancellation and placed-only progress.
Constructed selections with case-fold collisions or file/ancestor conflicts, including a non-adjacent ancestor, initially reached admission in four failing regressions.
The corrected executor refuses them before any admission or transfer effect, retaining the existing namespace policy without claiming the wider P19 filesystem rules.
Directory observation faults use actual directory/file/missing-path objects through the observation helper; they are narrower than an interposed whole-application directory replacement race.
The independent body-close failure after known cancellation uses a real opened mock HTTP body with an added close error at the private post-open helper boundary.
Engine cancellation cases stop before child dispatch, after the actual test engine writes the stage and between placements, preserving the first copied file and missing later execution facts where appropriate.
They do not establish an atomic scheduling interlock or orphan-child quiescence.
The [additional boundary tests](../../internal/transfer/local/download_recording_boundaries_test.go) refuse partial recorder configurations before any marker effect and stop between native items while retaining the earlier file and all three acknowledged facts.
Their partial-child fixture fetches one selected file through the actual mock session, truncates that staged file to three bytes and returns an unsuccessful test-engine result before the second selected read.
Both admitted names retain failed unresolved execution without invented verification, the partial bytes remain staged and no final file appears.
This is executor/test-engine evidence, not an interrupted pinned-rclone process or a pause immediately between group-directory creation and child dispatch.

The [adapter tests](../../internal/app/download_items_test.go) reject invalid representations before an armed SQL trigger without latching healthy storage and count a name with both failed execution and verification only once.
Their 201-item transaction measurement performs 603 acknowledged writes in each route representation.
The observed engine-shaped run took 4,448.840 ms plus 45.588 ms for completion; native-shaped admission took 5,359.364 ms plus 45.862 ms for completion.
These are local store measurements, not network throughput or power-loss durability evidence.

The [job-drawer tests](../../frontend/src/features/jobs/itemRecords.test.ts) exercise complete download pages, quoted paths, missing/overlapping phases, available empty and unavailable records, and local rerun refusal.
The focused file passes 31 tests, including the new download cases.
These are DOM rendering and interaction checks; native dialogs, installed application lifecycle and accessibility remain separate.

## Deliberate broken controls

Private Go overlays change one required behaviour at a time without editing the maintained source.
Each producer retains the original and overlay hashes, raw events and exact failing assertion; compilation failures are not counted as detected behaviours.
The following controls pass on unchanged code and fail when their corresponding rule is removed.

| Broken behaviour | Observed failure | Preserved control |
| --- | --- | --- |
| Sum failed execution and verification as separate names | The one-name adapter case reports two failures | Invalid-identity refusals still pass |
| Remove the native cancellation check before final link | A cancelled helper places the final target and returns success | An uncancelled transfer succeeds |
| Drop the independent failed-close verification after a failed read | The combined read/close case loses its attempted verification | Read-only failure and successful transfer retain their distinct results |
| Discard file/source close errors from the primary result | Four individual/combined closure cases lose required errors or incorrectly permit placement | Seven named controls still pass |
| Drop an actual cleanup issue from the item result | Three cleanup-refusal cases lose the cleanup error beside success or a primary refusal | Twelve named controls still pass |
| Discard the continuation cursor for a bounded item page | The actual mock-service 201-name case cannot retrieve every retained identity | The zero-failure service case remains complete |
| Ignore refused admission | Both routes reject the resulting read/placement without acknowledged admission | The other eleven selected local cases still pass |
| Call verification before execution | Six route/outcome cases reject phase reordering | The other seven selected local cases still pass |
| Drop sticky refusal state or permit deferred cleanup after refusal | Each variant causes actual group cleanup and loses explicit retention in both engine observation-failure cases | Each variant retains eleven passing controls |
| Use an unadmitted temporary path or remove exclusive creation | Each variant fails file, directory and symlink collision cases | Each variant retains ten passing controls |

The initial tests did not detect calling verification after a refused execution callback and only then returning the original refusal.
That preserved variant passed all thirteen originally selected leaves because the assertion observed invocation order without tracking successful acknowledgement.
The corrected test records acknowledgement only when execution returns successfully and requires that fact before verification.
The exact preserved variant then fails the native and engine execution-refusal cases; fourteen other leaf controls in the expanded boundary selection pass.
No production change was inferred from this test gap.

These are bounded witnesses, not evidence that every guard is individually indispensable or that the entire fault/barrier harness is complete.

## Verification and delivery

Focused producers retain their exact source hashes, command, raw events, stderr, exit code and all failures outside the repository.
Counts below include named parent tests; leaf counts are supplied separately to avoid presenting them as disjoint scenario totals.
These overlapping runs preceded final verification and are not a claim that every final source byte has passed the complete suite.

| Focused producer | Named tests | Leaf cases | Package time |
| --- | ---: | ---: | ---: |
| Closure/cancellation/progress follow-ups | 18 | 15 | 1.090 s |
| Adapter identity, accounting and transaction cost | 51 | 49 | 20.218 s |
| Service identities plus existing closure/stage/completion | 94 | 86 | 147.955 s |
| Actual SQL record refusal and writable controls | 32 | 30 | 40.464 s |
| Paired-name refusal and unchanged controls | 27 | 24 | 1.062 s |
| Directory observation and engine cancellation | 10 | 8 | 1.026 s |
| Empty/marker and mixed admission service cases | 10 | 6 | 6.813 s |

The expanded acknowledgement/configuration/cancellation/partial-child selection passes nineteen named tests on unchanged implementation code.
All 883 frontend tests on this pre-integration branch pass, with 81.42% statement coverage, clean typechecking and production frontend build.
Native Go build, Go vet, the no-CGO internal build and Swift source verification pass; regenerating all twenty-two binding files produces identical bytes.
The first complete internal race run retained 2,603 named passes and 42 failing leaves with eight failing parents.
It exposed the old sampled labels and version-zero history/completion expectations in the four compatibility files described above; all 336 Go/SQL input hashes remained unchanged through that run.
After those test-only corrections, all 62 focused compatibility checks pass and the second full internal race run passes 2,653 named tests across all twenty packages, with zero failures or skips.
Final readback verifies all 336 Go/SQL input hashes, every one of the 2,486 prior terminal names and exactly 167 additions, with no lost or duplicate tests.
The first and second full runs contain the same complete named inventory; only one generated absolute-expiry name is normalised after its value is checked against that run's one-hour expiry calculation.
The passing run includes 740 top-level tests, 87.1% statement coverage and a 630.205-second system package.
All twenty-seven prior scheduled groups remain, with the six new record/refusal/empty groups under the unchanged `-parallel=3` limit.
Scenario reconciliation remains valid at schema version three with 40/116 required cells covered and thirty-eight actions with gaps; it remains incomplete coverage.
Independent implementation review passed for source `021d3e17f761345135a37de2e668f343198fcf5d`.
The reviewer independently reproduced all 2,653 named passes across twenty packages without failures or skips, inspected the actual-service and SQL-refusal evidence, and verified the acknowledged-order and identity negative controls.
Seven of eight additional reviewer variants failed their intended cases; removing the post-`MkdirAll` directory observation remained undetected because that guard addresses a replacement race, as the focused evidence limits above already state.
The resulting integration later passed [independent review and protected PR/master delivery](#protected-delivery).
No CI limits, assertions, historical archives or audit finding dispositions are weakened by this migration.
Full per-effect history, stronger source/content identity, stable local roots and path authority, cancellation interlocks, crash recovery, native acceptance, retention/export and the [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remain open.

## Current-base integration

The four independently reviewed delivery/contract/implementation commits were rebased once onto protected master `a8ece3429c39468c3da8a4343bbfa7cf8226efce`, incorporating PR71–74.
Original source `021d3e17` is preserved, and all four stable patch identities and commit messages match the resulting source `cafe784aee6aa8f4dd2dfa2c5dca03b8a7f5631a`.
Its tree `18227478bca7aeb4565c2de4b9cd4289c0c056ba` matches the previously checked prospective integration.

The fresh complete internal race run passes 2,694 named tests, including 743 top-level tests, across twenty packages without failures or skips.
All 343 Go/SQL input hashes match the immutable integrated source before and after the run.
The complete inventory is exactly the union of the earlier download run and delivered upload-admission baseline: forty-one additions over the former and 167 over the latter, with no missing or duplicate names.
The two hosted optional skips run and pass in this configured local environment.
The system package takes 630.907 seconds and coverage is 7,615 of 8,732 statements, reported as 87.2%.
All earlier scheduled groups remain: the merged upload test adds one to the previous thirty-three, while its environment-changing mock counterpart stays serial.
All serial system-test groups finish before the parallel phase; the configured worker bound stays three.
An observed peak of four open JSON test records reflects slot release before parent reporting, not proof of four simultaneous workers.
The raw 13,107-event stream has SHA-256 `644c25a6d65e5b9a34ede1767bcf305e015ee932ece72ca3b7a3bf4be2ca82ed`.
Schema-three scenario reconciliation remains valid but incomplete at 40/116 required cells and thirty-eight actions with gaps.

Fresh frontend validation passes all 971 tests, with no failures or skips, and reports 88.53% statement coverage.
The full 883-name pre-integration inventory remains, with exactly thirty-three keyboard, fifty-one Preview and four About-identity cases added.
All ninety-five frontend inputs and twenty-two regenerated binding files remain unchanged through typechecking, frontend build/tests, vet, native production build, root/build-identity/command tests, no-CGO internal build and Swift source verification.
The native executable has SHA-256 `bb272224b1fb01a1f055dce0243d8dd33b95a24693be3ff5a2e8b7750abae6aa`.
Its observed `--version` output at 16:39:43 UTC on 9 September 2026 reports version `dev`, source revision `cafe784aee6aa8f4dd2dfa2c5dca03b8a7f5631a` and `sourceDirty=clean`.
This attributes the existing local binary; byte-for-byte build reproducibility and hosted artifact provenance were not established by that observation.
This build and component evidence does not replace native acceptance or valuable-data validation.

## Protected delivery

Independent review approved the integrated source `f88114958c7f5211b8953e0c757687146a4c4f5d` on base `a8ece3429c39468c3da8a4343bbfa7cf8226efce`.
[PR75](https://github.com/csmart/aeronautes/pull/75) merged through the normal protected rebase procedure on 9 September 2026 at 17:03:41 UTC, producing master `4feaaeacb0f7093e47ed8d7c37bcd08d6305be2e`.
Both trees are exactly `325766fcd7705fecf97261356965dcb1863f8f10`: all 645 tracked files and 343 Go/SQL inputs match, as do all five binary patches, stable patch identities and complete commit messages.
The PR checkout `28fe4f5f695abab662681f191c1ba5fc1a492201` has that same tree and the exact source/base parents.

Both actual pipelines passed all five required jobs and all 67 steps, without a rerun or changed CI limit.
Each full SAIO log contains exactly the same 13,095 events as its own downloaded artifact.
Both retain the complete 2,694-name inventory across twenty passing packages: 2,692 named passes, no failures, and only `TestRcloneMatrix` and `TestKeychainRoundTrip` skipped with their source-bound optional-environment diagnostics.
The 743 top-level names comprise 741 passes and those two skips.
The complete names and outcomes match between PR and master after separately checking each run's generated absolute-expiry test name against its one-hour calculation.
The configured local run's two optional tests passed; its 2,694-pass result remains a separate producer.

| Producer | Actual Go | System package | Suite step | SAIO job | Reported statements |
| --- | --- | ---: | ---: | ---: | ---: |
| [PR75 run 34377926698](https://github.com/csmart/aeronautes/actions/runs/34377926698), attempt 1 | 1.27.1 | 980.437 s | 1,170 s | 1,222 s | 87.2% |
| [Master run 34380591757](https://github.com/csmart/aeronautes/actions/runs/34380591757), attempt 1 | 1.27.1 | 931.617 s | 1,120 s | 1,183 s | 87.2% |

The reports retain all thirty-four scheduled groups and 123 serial system-test groups under the unchanged `-parallel=3` setting.
All serial groups terminate before the parallel phase; four open JSON records do not establish four simultaneous workers.
The observed minimum margins are 219.563 seconds against the twenty-minute package limit and 278 seconds against the twenty-five-minute job limit; these are measurements of these runs, not a worst-case performance guarantee.
The hosted Go 1.27.1 witnesses do not change earlier producers' recorded Go versions.

Schema-three scenario reconciliation remains valid but incomplete at 40/116 required cells, with thirty-eight actions having gaps and contract hash `6c1e4d947471b8b5`.
The fifty-one raw PR/master report differences comprise one checkout SHA and fifty evidence strings with consistently mapped generated job, scan or fixture identifiers.
All remaining fields, test witnesses, outcomes, object names and evidence meaning match.
Separate broken-reader controls reject changed coverage, a changed job outcome, inconsistent scan identity, a changed object name and a missing witness.
The full PR log has SHA-256 `e8c7dc2083622ef4f059c046a256b84da04e64237148fc86885f91a555495d95`; its scenario artifact `10115447008` has verified ZIP SHA-256 `59a591ed1b3e45ee7c115076b16da45c42531fc6015d5aaa01ca2763385cd7f2`.
The full master log has SHA-256 `4f78297259f8e86a35a2f952b5e02acf6d1a8548ea34100c9fc5583ca0bfe0d2`; its scenario artifact `10116446144` has verified ZIP SHA-256 `a2cba0b4b49ded471f3c73e12994529cd41a4f9d4a015c404350eb378cb267e1`.
All twenty-seven pinned historical artifacts and the complete PR evidence remained unchanged after master validation.

This delivery establishes the checked download-recording migration and preserves its original failed probes and later corrections.
It does not close DS-08/SEC-08, stronger content identity, stable filesystem authority, crash recovery, native acceptance or any valuable-data release gate.
