# Checked upload item recording

## Contract and reproduced failures

[ADR 0031](../adr/0031-upload-item-recording.md) requires checked upload admission and final observations, exact backend-captured identities, conservative stop boundaries and distinct terminal/audit totals.
The [original before-fix archive](2026-09-08-upload-item-boundaries.md) remains unchanged: actual services lost identities and capped 201/401 failed names at 200, while smaller executor probes exposed recovery refusal followed by retry or later commits.
The independently reviewed [source-path correction](2026-09-08-upload-source-paths.md#protected-delivery) landed before this caller migration.
It refuses invalid or lexically changed spellings and preserves accepted paths exactly; it does not establish stable filesystem identity or root authority.

## Implementation and effect boundaries

The [upload recorder](../../internal/transfer/local/upload_recording.go) requires admission, execution and verification callbacks together.
The [application adapter](../../internal/app/upload_items.go) validates exact local, destination and optional actual staging identities before store access, then acknowledges each individual transaction before dependent work.
Invalid caller representations refuse execution without manufacturing a persistence latch.
Every native file open and every buffered engine member requires acknowledged admission.
Execution and attempted live HEAD verification remain separate facts under the original item; there is no extra selection map or new verification pass.

The [executor](../../internal/transfer/local/upload.go) stops this upload's later dependent work after required admission, execution, verification or recovery-record refusal.
A synchronous engine call already in progress may finish writing admitted staging members; the code waits for its return and prevents authentication retry, live commits and later directory groups after a captured-record refusal.
An admitted group that never entered an engine can retain `not_attempted` with a typed reason when storage remains writable.
An engine-started member without a final observation stays missing and unresolved.
The first typed diagnostic and six overlapping facets do not claim complete per-request history or every simultaneous error.

Final observations follow actual native file closure, staged response-body closure and the relevant cleanup decision.
A successful existing live HEAD contributes copied and verified objects and bytes even when a later closure, cleanup or bookkeeping problem occurs.
Simultaneous issues remain separately counted, while one name contributes at most once to each retained facet.
A committed-record refusal retains the affected stage and permits the existing read-only live HEAD witness before stopping.
Ordinary cleanup failures remain explicit and may continue to later items, retaining exact issue totals beyond the display sample limit.
No recovery record is cleared and no additional cleanup authority is granted.

Only new upload jobs opt into version-one item records; historical jobs and new downloads remain version zero.
The [shared local runner](../../internal/app/localtransferservice.go) carries that immutable version through every admission, cancellation and completion path.
Version-one upload completion uses the existing terminal transaction for distinct failed names and all six matching audit facets; samples no longer supply exact failure counts or an apparently exact count in error text.
Existing verified byte counts and captured destination audit authority remain intact.
The completed body releases cancellation registration even when persistence fails.

Required item-write failures preserve the existing shared-store latch: public status/item reads become unavailable and all eight new-job families refuse admission.
Dropping the fixture SQL trigger cannot clear that process-local latch.
Legacy recovery-write failures remain explicit but do not manufacture an item-store latch; that distinction is exercised directly.
Typed item diagnostics exclude raw provider bodies, URLs, credentials and headers; legacy job/audit error text still has its separate SEC-08 obligation.

## Maintained fault and regression matrix

The [identity matrix](../../internal/systemtest/upload_item_records_test.go) exercises 0, 1, 201 and 401 refused live uploads, each alongside one successful engine upload, on mock Swift and two-account SAIO.
Each count runs with native-routed newline names and ordinary local filenames whose conditional live PUT is refused after engine staging.
The latter cases run without the Windows newline-filename exclusion.
All complete item pages retain exact original paths, destination/staging identities, typed execution facts and only the verification actually attempted.
Terminal counters and the transaction-local audit count every failed name, while the independently read full bodies and complete mutation-identity multiset prove the permitted live/staging effects.
Backend rerun refusal creates no new job and performs no HTTP request.

The [SQL refusal matrix](../../internal/systemtest/upload_record_faults_test.go) uses actual services, backend picker tokens, real SQLite triggers and both owned providers.
An acknowledged response barrier holds the successful live PUT response while an independent read verifies the destination and the test installs the selected observation refusal.
The verification refusal applies only after the preceding execution row exists.
Controls use the same boundaries with writable storage.

| Boundary | Permitted effects and retained facts |
| --- | --- |
| Native admission refusal | The first copy may finish; the next source is not opened and no later item is admitted |
| Grouped admission refusal | No child or remote write; the first acknowledged member records not-attempted execution |
| Native execution or verification refusal | Only the first live copy; earlier acknowledged phases survive and the refused or unattempted phase stays absent |
| Staged execution or verification refusal | Three acknowledged members enter the first batch; only its first live commit and ordinary stage cleanup finish, and the other stages remain |
| Captured recovery refusal | The already invoked two-member DLO batch returns; both stages and four backing segments remain, with no live copy, retry or later directory group |
| Committing recovery refusal | Both first-batch DLO stages remain; the first execution fails, the second remains missing/unresolved and the uninvoked third member is explicitly not attempted |
| Committed recovery refusal | Only the first live DLO is copied and verified; its bookkeeping issue, both stages, four segments and earlier recovery records remain, with no later live commit or group |

The [recovery-stop fixtures](../../internal/systemtest/upload_recovery_stops_test.go) use three captured files across two directories, actual pinned rclone and real SQL refusal at each required recovery boundary.
They independently read the full logical bytes, raw DLO manifest bodies, exact segment names, sizes and SHA-256 digests, alongside the complete HTTP mutation multiset and retained recovery identities.
The mock now honours raw DLO manifest reads, with a [separate fixture regression](../../internal/swifttest/dlo_manifest_test.go) distinguishing stored bytes from assembled GET/HEAD results.
The initial real-Swift recovery cases passed while the mock failed specifically because it assembled a raw-manifest request; that failing run remains separate evidence from the corrected replay.

The new identity, SQL-refusal and recovery-stop fixtures preserve complete owned namespaces, derived segment containers, unselected sentinels, another job's staging data, shared DLO/SLO aliases and local selected/unselected files.
Orderly child-process reopening compares complete item identities, both phases, missing facts, facets, job/audit state and recovery records without storage traffic or automatic cleanup.
This is checked database close/reopen evidence, not a native application kill, fsync or power-loss claim.

[Executor admission](../../internal/transfer/local/upload_admission_test.go), [retry](../../internal/transfer/local/upload_retry_test.go), [batch outcome](../../internal/transfer/local/upload_batch_outcome_test.go) and [closure](../../internal/transfer/local/upload_close_test.go) tests exercise partial recorder configurations, pre-open cancellation, undispatched groups, returned engine errors/statuses, cancellation between staged commits and simultaneous close/cleanup/verification issues.
The authentication-retry fixture deliberately supplies the engine status at the internal executor seam; it does not claim a real child emitted that status.
The staged closure fixture reaches the actual response reader's close error through inconsistent response length, while the native closure fixture closes the actual selected file before returning a synthetic refusal.
Both retain independently witnessed copied bytes and complete collateral checks.
The existing [service interruptions](../../internal/systemtest/local_interruption_test.go), [local completion](../../internal/systemtest/local_completion_test.go), [source paths](../../internal/systemtest/upload_source_paths_test.go) and [201 cleanup issues](../../internal/systemtest/upload_recovery_test.go) now assert the corresponding item identities/facets and version-zero download control.

The [drawer tests](../../frontend/src/features/jobs/itemRecords.test.ts) navigate 401 upload records across five bounded pages, preserving literal Unicode/control-character paths, missing observations and overlapping facets without interpreting path text as markup.
Uploads and downloads still require a fresh picker/preflight rather than a history rerun.
These are DOM/service checks, not six-platform native-dialog or visual certification.

## Validation and limits

The final Go 1.27.1 race/coverage producer passed 2,291 named tests (701 top-level tests and 2,098 leaf cases) across all 22 packages, with no failures or skipped tests and 80.6% statement coverage.
The system-test package took 890.494 seconds locally; all 311 Go/SQL source hashes match the tested tree.
This full `./...` result includes the desktop root and mock-Swift command packages and uses Go's default package-local coverage instrumentation.
Earlier internal-only producers used `-coverpkg=./internal/...` to count execution from other packages' tests too, so their percentages are not directly comparable; hosted integration retains its configured cross-package coverage check.
Independent local approval and protected PR/master delivery are recorded below.
The complete producer reconciles the existing scenario report at 40/116 cells with gaps in 38/52 actions; no new coverage credit is inferred from passing tests.
All 880 frontend tests, Svelte/TypeScript checks, frontend production build, vet, native application build, CGO-disabled internal build, upstream Swift core tests and source provenance checks passed.
All 22 binding files were unchanged after regeneration.
Frontend statement coverage remains 81.42%.

Twenty-eight distinct broken Go overlays failed their maintained behavioural assertions without compilation errors, races, timeout substitutions or skipped tests.
They remove the pathname guards, complete recorder configuration, admission and observation acknowledgements, phase ordering, recovery retry/commit stops, affected-stage retention, closure/cleanup checks, undispatched/started distinctions, caller opt-in, item-derived completion, exact failed/issue counts, complete pagination, pure adapter validation and typed diagnostics.
Additional controls reject invented verification and completion without any returned outcome, and retain copied bytes alongside post-commit issues.

The owned 201-name upload adapter fixture acknowledged 603 individual SQLite writes in 4,370.234 milliseconds under the race detector, excluding Swift traffic.
That measurement is local WAL/FULL fixture cost, not production throughput; acknowledgements remain mandatory before dependent effects.
The final full race/coverage producer measured the same 603 acknowledged writes at 5,882.455 milliseconds, separately from that focused run.
The actual-service measurements below begin immediately before `StartUpload` and end after terminal pin release, including service preflight but excluding fixture seeding, independent collateral checks and reopening.
They are local fixture measurements under race/coverage instrumentation, not production throughput or hosted CI headroom.
Each row includes one successful engine upload alongside the selected failed names.

| Provider | Selected failed route | Failed names | Service milliseconds |
| --- | --- | ---: | ---: |
| Mock Swift | Engine staging | 0 | 188.152 |
| Mock Swift | Engine staging | 1 | 208.158 |
| Mock Swift | Engine staging | 201 | 5,681.604 |
| Mock Swift | Engine staging | 401 | 11,007.712 |
| Mock Swift | Native | 0 | 175.110 |
| Mock Swift | Native | 1 | 200.324 |
| Mock Swift | Native | 201 | 4,913.677 |
| Mock Swift | Native | 401 | 9,637.571 |
| SAIO | Engine staging | 0 | 267.179 |
| SAIO | Engine staging | 1 | 324.692 |
| SAIO | Engine staging | 201 | 11,224.613 |
| SAIO | Engine staging | 401 | 21,903.594 |
| SAIO | Native | 0 | 261.001 |
| SAIO | Native | 1 | 307.994 |
| SAIO | Native | 201 | 6,331.352 |
| SAIO | Native | 401 | 11,414.867 |

Two earlier full runs remain failed evidence rather than being relabelled as successful.
The first passed 2,291 named tests but failed root-package setup because frontend build assets were absent when package loading began.
After the assets were built, the second compiled every package but failed seven short-upload leaves and their two parents on a new timing assertion: persisted job timestamps use whole-second precision, so a short job can have equal start and finish values.
The exact monotonic service-duration correction passed all eight short-upload cases across both providers before it was applied and the final full suite ran.
Production Go/SQL remained identical across all three full runs; only the timing test changed.
The protected PR and resulting master integration package/job durations must still be measured against their unchanged 1,200/1,500-second budgets; investigate growth before changing those limits.

## Hosted completion-wait correction

The initial [PR65 integration run](https://github.com/csmart/aeronautes/actions/runs/34297051410/job/102295834232) failed `TestUploadItemRecordsRealSwift/native_false_failed_401` at the credential-release wait.
It passed 2,287 named tests, failed that leaf and its parent, and skipped only the two existing optional environment tests.
The system-test package finished in 974.784 seconds and the integration job in 1,176 seconds, within the unchanged package/job limits.
This was an individual fixture deadline, not exhaustion of either CI budget.

The identity fixture called the thirty-second credential-release helper immediately after starting the upload.
Hundreds of individual acknowledgements and remote operations can legitimately outlast that wait.
The fixture now first uses the harness's existing ninety-second terminal-job wait, then checks credential release with the original thirty-second bound.
Both conditions remain required: a visible terminal row alone cannot acknowledge deferred body cleanup.
The behaviour oracle remains the complete terminal job/item/audit state plus actual credential publication after cleanup and the independently checked data effects.

Controlled overlays on source `039e77584631307448c1d0f5d37691fe3b9d222b` add `time.Sleep(100 * time.Millisecond)` inside the proxy's selected live-PUT refusal branch.
They run the 1- and 401-failure engine cases on both providers with `go test -race -count=1 -coverpkg=./internal/...` and the pinned real rclone.
For observation only, the overlaid cleanup helper records a test error at the original thirty-second deadline and allows one additional bounded ninety-second wait instead of aborting immediately.
That preserves the failure while allowing the actual job to finish and all original identity, ledger, audit, staging-byte, mutation, collateral, rerun and orderly-reopen assertions to execute.
The matching corrected overlay adds only the terminal-job wait before that same helper.
All 311 underlying Go/SQL sources remain identical across these two runs; no delay, grace period or observation hook enters the maintained tests or production code.

| Provider | Failed names | Original wait | Corrected wait | Original/corrected service seconds |
| --- | ---: | --- | --- | ---: |
| Mock Swift | 1 | Pass | Pass | 0.332 / 0.376 |
| Mock Swift | 401 | Fails at 30 s, then completes | Pass | 53.273 / 51.723 |
| SAIO | 1 | Pass | Pass | 0.422 / 0.494 |
| SAIO | 401 | Fails at 30 s, then completes | Pass | 63.780 / 63.014 |

The original delayed run fails only the two expected deadline observations and their parents; the corrected run passes all four leaves and both parents without skips.
Every delayed 401-name job still records exactly 401 failed identities, one copied and verified object with twelve bytes, and all expected retained facts and effects.
The unmodified 401-name controls also pass on both providers under cross-package coverage, with service times of 11.137 and 21.872 seconds.
These controlled delays reproduce premature waiting, not the hosted runner's exact latency or production throughput.
The final maintained identity replay passes all sixteen leaves and both parents with race detection, cross-package coverage and no skips.
Of the 311 Go/SQL inputs from the earlier full validation, only this test file changes; the maintained correction matches the passing controlled overlay after removing its artificial delay.
Independent local review and successful replacement PR/master checks approved this correction as recorded below.
Application code, the shared cleanup helper, pipeline limits, assertions and merge protections remain unchanged.

No scenario cells, complete audit packages or release gates are closed by this caller migration.
Download recording, source replacement/root confinement, backend-held approval and credential authority, current remote identity, stronger content/metadata verification, concurrent writers, per-effect/lost-history recovery, ordinary staging deletion authority, native child quiescence and all six platforms remain separate work.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused.
Valuable-data operation remains on hold.

## Protected delivery

[PR65](https://github.com/csmart/aeronautes/pull/65) delivered the checked upload caller and its separate completion-wait correction after independent local review approved both changes.
The preceding documentation commit records the protected prerequisite deliveries through PR63 and PR64.
The reviewer independently ran the full 2,291-test Go and 880-test frontend suites for the caller, then reproduced the premature fixture wait and verified all sixteen maintained upload identity cases for the correction.
The first failed hosted run and controlled before/after evidence remain preserved; the correction changed two test lines and their evidence record, without changing application code or CI budgets.

Protected rebase merging preserved `af46a276` as `ff8b56f5`, `039e7758` as `75627158` and reviewed head `99a2a4795b9de3760b1df5a37592275615aa2843` as master `2b68d28e000e676537d131b00610cffe0b60205a`.
All three patches match and both heads have tree `86b3fe1e2d93475355081859cb1b6c04d42ed390`.
The current base was delivered PR64 master `6b754f43e3e1f708abc50a7af9955a8ec0b4c074`.

All five actual checks succeeded on the latest reviewed PR head with current master incorporated and again on the resulting master: Go, frontend, owned real-Swift integration, headless smoke and the aggregate gate.
Strict merge protection remained enabled, and only the verified source head was merged.

| Run | System package | Whole integration job |
| --- | ---: | ---: |
| [PR65](https://github.com/csmart/aeronautes/actions/runs/34305861294) | 1,071.119 s | 1,303 s |
| [Resulting master](https://github.com/csmart/aeronautes/actions/runs/34307415807) | 1,074.871 s | 1,299 s |

Each complete integration log contains 2,289 named passing tests across twenty internal packages, zero failures and only the existing optional engine-matrix and keychain round-trip skips.
The required owned Swift integration job ran successfully on both heads.
The finite 1,200-second package and 1,500-second job budgets remained unchanged; the master package has about 125 seconds of headroom.
These whole-suite measurements include runner variance and do not isolate upload recording cost or establish production throughput.
Future slices must measure runtime growth and investigate scheduling before considering any budget change.
New downloads and historical jobs retain record version zero; the complete audit packages and valuable-data release gates above remain open.
