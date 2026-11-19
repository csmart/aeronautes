# Checked Copy item recording

## Contract and reproduced failures

[ADR 0030](../adr/0030-copy-item-recording.md) requires the actual Copy caller to retain exact logical identities and separate execution and verification observations, acknowledge records before dependent work, and derive distinct failed names within terminal job/audit completion.
This migration follows the delivered storage, complete-access and interruption/rerun prerequisites.
Local upload and download remain version zero and require separate migrations.

The [original eight-case reproduction](2026-09-08-copy-failure-identities.md) demonstrated both mock and two-account SAIO reporting two, 400 and 400 failures for one, 201 and 401 failed names.
The maintained [actual Copy record tests](../../internal/systemtest/copy_item_records_test.go) now exercise zero, one, 201 and 401 failed names on both providers, each with one successful engine copy.
Every failed key retains both observations under one exact identity, all public item pages are reachable once, and the saved job and audit count distinct failed names once.
The zero-failure case distinguishes recorded-empty failure totals from uncaptured history.
The tests retain the original independent namespace, digest, collateral, recovery and orderly-reopen assertions.

The four maintained mock cases were also run against unchanged PR61 source `82a7ccd3561c43a4a8e2ad5b4dae23ed0dff64f8`, overlaying only their test and snapshot helpers.
All four failed specifically because the actual caller did not capture records; the nonzero cases also retained the old incorrect counts.
That maintained replay is mock-only and is separate from the archived eight-case evidence on both providers.

## Implementation and effect boundaries

The [Copy recorder](../../internal/transfer/copy/recording.go) requires admission, execution and verification callbacks together and keeps advisory progress separate.
The [application adapter](../../internal/app/copy_items.go) builds exact source, destination and actual staging locations from the captured request, then reuses the storage foundation's pure identity and intent validators before attempting a write.
Empty relative keys with valid full names, Unicode, newline, zero and valid replacement characters retain their original values.
Invalid UTF-8, empty full names, negative advisory bytes and invalid staging locations are refused before database access without deliberately latching storage.

Each selected source name is acknowledged before buffering or native execution.
Changed names record refusals without staging; unchanged names record a separate comparison and execution fact without becoming planned transfers.
Required item-write failures latch the shared store against public status/record reads and admission of every new job family, and stop this executor's subsequent dependent work.
Buffered names whose engine invocation never started can record `not_attempted` while storage remains writable; engine-started names with missing final observations remain unresolved.
Abandonment starts with an engine/incomplete diagnostic; typed cancellation, timeout, recognised Swift errors and generic network errors retain their classified code.

The shared [streaming planner](../../internal/transfer/plan/plan.go) now emits and acknowledges the current item before reading its next input row.
Previously, a later listing failure could discard the current comparison, and a refused recording callback was reached only after reading ahead.
The caller also stops the executor on a planning error before `Finish`, preventing a previously buffered item from being dispatched after that error.
Maintained before-fix controls reproduce both faults, and the existing interruption fixtures retain their exact permitted destination effects.
Planning and finish errors both survive the terminal result.

Native, staged and symlink operations return their final typed observation after required stream closure and staging cleanup.
A reported copy followed by a recovery-write, close or cleanup problem keeps its copied result and bytes, with a typed problem and unresolved effects.
Simultaneous problems remain in the overall result even though an item has only one diagnostic.
An invoked destination PUT, including a conditional 412 refusal, is unresolved; only an existing native-symlink conflict witnessed before PUT can use `not_attempted`.
The ordinary native path adds no destination HEAD.
A captured-prefix recording error stops before another authentication attempt, and a committed-record refusal after a live DLO PUT prevents affected staging cleanup and subsequent live commits.
Earlier recovery records remain evidence of attempted work, not cleanup permission.

Verification persists each fresh comparison before advancing the stream and returns its partial report with any error.
Same and HEAD-proven equal Unknown pairs verify; missing, changed and unproven pairs fail verification.
Newly observed source names use comparison-only identities without invented admission or execution, and refused overwrites remain excluded from verification credit.
The [terminal transaction](../../internal/jobs/completion.go) derives distinct failed names and the audit's failure, conflict, refusal, unresolved, unverified and problem facets from the same item totals.
It preserves source authority for the audit, destination authority for recovery and the existing refusal of unjustified complete states.

## Maintained fault and regression matrix

The [SQL refusal fixtures](../../internal/systemtest/copy_record_faults_test.go) use the actual application services on mock Swift and two-account SAIO.
Where a post-PUT ordering witness is required, the existing acknowledged HTTP response barrier holds the final response after an independent destination read; no sleep chooses the fault boundary.
A fixture-only front adapter completes tiny `Expect: 100-continue` bodies before that final-response barrier without changing the barrier's own rejection contract.

| Case | Exact permitted effects and required retained facts |
| --- | --- |
| Admission refusal after an engine name was buffered | No source or destination mutation; the earlier admitted name records not-attempted execution, and no child starts |
| Execution-record refusal after native PUT | Exactly the first native copy is independently visible; no later native or engine work; its missing final observation remains unresolved with reported copied progress retained |
| Execution-record refusal between staged commits | All three names are admitted and staged, then only the first live copy and its staging cleanup occur; later stages remain and no later live commit starts |
| Verification-record refusal after a prior acknowledgement | All three copies finish; only the first verification persists, and the other two reported copies remain unverified without advancing to the third comparison |
| DLO committed-record refusal | Two two-segment stages exist and only the first live DLO is copied; both stages and all four exact segments remain, with the first committing intent and no committed record |
| Zero, one, 201 and 401 failed names | Exact distinct counts, both observations per failed identity, every public page and the successful engine object remain consistent |
| 201 unchanged names | All exact non-admitted identities retain same execution and successful verification, with no Swift mutation |

Every actual SQL-refusal case checks the public unavailable state, all eight new-job families sharing the store, refused rerun before connection traffic, exact previously acknowledged records and orderly child reopening without HTTP or automatic cleanup.
Complete owned snapshots preserve source and unselected sentinels, another job's staging data, external shared DLO/SLO aliases and their segment bytes.
Dropping the SQL trigger does not clear the process-local item-write latch.

The [actual DLO fixture](../../internal/systemtest/copy_dlo_records_test.go) uses two 1,120,000-byte source objects and the pinned engine with a one-MiB chunk size.
The first live object's full bytes are independently verified after the exact committed-record SQL refusal, while the second destination is absent.
It checks both staged manifests and all four segment identities, lengths and SHA-256 digests, exact captured/committing records, source audit authority, no destination DELETE or POST, unchanged owned collateral and child reopening without traffic.
This is a legacy recovery-record refusal, not an item-write refusal, and does not claim the shared item-store latch for that case.

[Executor tests](https://github.com/csmart/aeronautes/blob/9923de8a010650853909f9132cab89e5c49adf82/internal/transfer/copy/recording_test.go), [post-copy tests](https://github.com/csmart/aeronautes/blob/9923de8a010650853909f9132cab89e5c49adf82/internal/transfer/copy/post_copy_recording_test.go) and [adapter tests](../../internal/app/copy_items_test.go) additionally exercise all seven partial configurations, sticky repeated finish, cancellation before dispatch and between staged commits, comparison-only names, final callback ordering, simultaneous post-copy problems and typed diagnostics.
Hostile engine and Swift error payloads are checked in persisted records and public DTOs; an arbitrary string containing a status number cannot invent a typed HTTP status.
[Store completion tests](../../internal/jobs/item_completion_test.go) prove audit and retention refusal roll back the whole terminal transaction and that two failed phases count one name despite a contradictory caller counter.
Existing [interruption](../../internal/systemtest/copy_interruption_test.go), [overwrite refusal](../../internal/systemtest/copy_refusal_test.go) and [listing fidelity](../../internal/systemtest/listing_fidelity_test.go) cases now also check recorded identities and exact facets while preserving their namespace assertions.
The actual rerun control uses records from its first Copy, observes a newly added source through fresh planning and leaves the predecessor unchanged.

## Validation, measured cost and limits

The complete local Go 1.27.1 race producer passed 678 top-level tests, 1,958 leaf cases and 2,130 named tests across twenty packages, with zero failures or skips and 86.2% combined statement coverage.
It used coverage instrumentation, the owned mock and two-account SAIO fixtures, the required Secret Service fixture and pinned rclone 1.74.3.
The systemtest package took 642.590 seconds within the unchanged fifteen-minute package budget.
All 295 Go/SQL source hashes matched after the run; its recorded parent head is the preceding delivery-documentation commit, not the tested implementation identity.
Scenario reconciliation remains valid at 40/116 cells, with gaps in 38/52 actions.

All 878 frontend tests across 23 files passed with 81.42% combined statement coverage.
Svelte/TypeScript checks, frontend production build, vet, native application build, CGO-disabled internal build, upstream Swift core tests and Swift-source provenance checks passed.
All 22 binding files were unchanged after regeneration.
The protected delivery below records the independently reviewed source and complete PR/master results.

Twenty-five negative-control runs using 24 distinct broken Go overlays fail their matching behavioural assertions without compilation errors, races, timeout substitutions or test skips.
The affected-cleanup variant is checked both in an executor fixture and through the actual mock DLO service case.
They remove admission, execution or verification acknowledgement, caller opt-in, distinct counting, matching audit facets, refusal exclusion, typed diagnostics, planning-stop and stream ordering, close/cleanup checks, post-symlink ordering, DLO retention and conservative PUT classification.
The same run of negative controls also rechecks whole-transaction interrupted projection, ignored writes, the shared latch, complete rerun pagination and each unsafe rerun refusal.

The first implementation deliberately uses three acknowledged one-item transactions per unchanged name: admission, execution and verification.
In the full race run, the owned 201-name adapter/store fixture acknowledged 603 writes in 6,939.069 milliseconds, and five complete paginated rerun-guard reads took 48.036, 47.955, 46.939, 51.271 and 46.067 milliseconds.
The actual 201-name unchanged Copy took 4,709.153 milliseconds on mock Swift and 4,758.594 milliseconds on SAIO, each retaining all 603 acknowledged item writes without a Swift mutation.
These are separate local test measurements under race and coverage instrumentation, with SQLite WAL/FULL, not comparable production benchmarks or a throughput claim.
The actual Copy timing excludes fixture seeding and final namespace/reopen checks; the adapter measurement excludes Swift requests entirely.
Batching remains a separate measured optimisation and must preserve acknowledgement before effects.

No new scenario credit is inferred solely from this matrix, and no complete audit package or release gate is closed by caller opt-in.
Item records describe logical operations rather than every HTTP effect; typed item diagnostics do not certify legacy job/audit error text.
The process-local latch does not prove other admitted workers or native children have stopped.
Advisory listing bytes and current verification do not establish stronger content or metadata fidelity.
WAL/FULL SQLite and orderly child reopening do not establish full application restart, lost-suffix recovery, fsync or power-loss durability.
Local caller migrations, remote authority and identity, concurrent writers, stronger verification, filesystem safety, export/retention policy, native child lifetime and all six platform targets remain separate work.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused, and the [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.

## Delivery

Independent local review approved the preceding delivery record, the checked Copy implementation and the measured integration-budget correction before they were pushed.
All five [PR62 checks](https://github.com/csmart/aeronautes/actions/runs/34266734740) passed on final source `8fa7b5fad0843205e861a9861b51a5535f55a476` with current master `44623f5dcc1232c2e06c44a6122ef1488bf05864` incorporated.
The [protected PR62 merge](https://github.com/csmart/aeronautes/pull/62) produced master `9923de8a010650853909f9132cab89e5c49adf82`; all five [resulting master checks](https://github.com/csmart/aeronautes/actions/runs/34268481870) passed.
The merged tree `b70b7c1ff7f0c523f6263adf4cf4006f1d4f3b85` exactly matches the reviewed source, and all three logical patches were preserved without changes.
The actual Copy implementation is `c3a98bfb2f04af0815d646add7d519c688d1f318`, whose 295 Go/SQL files match the successful full local producer and remain unchanged by the budget correction.

| Integration run | System-test package | Whole job |
| --- | ---: | ---: |
| Latest PR62 head | 768.840 seconds | 16 minutes 8 seconds |
| Resulting master | 789.680 seconds | 16 minutes 32 seconds |

Both completed integration streams contain 2,128 named passes, no failures and the two existing optional test skips, `TestRcloneMatrix` and `TestKeychainRoundTrip`.
The required Go job separately runs the isolated Secret Service protocol check; the local full producer includes both fixtures with zero skips.
The [first failed PR62 run and reviewed correction](2026-09-08-integration-time-budget.md#pr62-measured-growth) remain explicit evidence: its fifteen-minute package alarm was not an assertion failure, and its invalid producer receives no scenario credit.
The twenty-minute package and twenty-five-minute job limits remain finite; future growth requires measured diagnosis and may need separate test scheduling work.

This delivers ADR 0030 step four and the Copy portion of the retained-identity correction within DS-08.
Local upload/download, complete per-effect history, remote authority and identity, native lifetime, stronger verification, filesystem safety and physical durability remain open.
No complete audit package or valuable-data release gate is closed by this delivery.
