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

[Executor tests](../../internal/transfer/copy/recording_test.go), [post-copy tests](../../internal/transfer/copy/post_copy_recording_test.go) and [adapter tests](../../internal/app/copy_items_test.go) additionally exercise all seven partial configurations, sticky repeated finish, cancellation before dispatch and between staged commits, comparison-only names, final callback ordering, simultaneous post-copy problems and typed diagnostics.
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
Independent local review and all five latest protected PR/master checks are still required before delivery.

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
