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
Independent local approval and protected PR/master delivery remain required.
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

No scenario cells, complete audit packages or release gates are closed by this caller migration.
Download recording, source replacement/root confinement, backend-held approval and credential authority, current remote identity, stronger content/metadata verification, concurrent writers, per-effect/lost-history recovery, ordinary staging deletion authority, native child quiescence and all six platforms remain separate work.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused.
Valuable-data operation remains on hold.
