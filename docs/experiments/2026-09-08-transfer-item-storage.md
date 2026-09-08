# Durable transfer item storage foundation

This is the first storage implementation of [ADR 0028](../adr/0028-transfer-item-records.md), following its independent design review.
The [actual Copy reproduction](2026-09-08-copy-failure-identities.md) remains the caller-migration oracle: one, 201 and 401 distinct failed names become two, 400 and 400 reported failures, with only the first name accessible in current job/audit summaries.
The foundation does not opt an existing caller into item recording or fix that reproduction by itself.

## Contract and implementation

Migration 0010 adds immutable job recording-version opt-in, item intents and separate execution/verification observations.
The existing job store exposes bounded `AdmitItems`, `RecordExecution`, `RecordComparison`, `ReadItems`, `ReadItemTotals` and `FinishFromItems` operations.
Identities use the exact job/key pair and typed full locations, with no path, case or Unicode normalisation.
An empty relative key stays distinct from a missing value.
Locations capture logical scope and do not establish immutable account identity, a remote version, native approval, exclusive ownership or retry permission.

Admission rejects an existing key and commits a batch of at most 500 intents together.
Recording an identical observation is idempotent local persistence; conflicting observations cannot replace the first fact.
A comparison-only row cannot become admission for an effect.
An execution refusal and successful verification of that name are incompatible in either recording order.
Diagnostics accept only known stage/code values and an optional numeric HTTP status, preserving exact object identity without storing provider messages or arbitrary error strings.

The failed-name total counts execution or verification failure once per item, independently of sample limits.
Unresolved execution and unverified reported copies remain separate overlapping facets.
Completion derives failed names and checks the ADR's completion conditions inside the existing job/audit transaction.
Other counters remain the caller's established inputs; this foundation does not certify their accuracy.
Both legacy `Finish` and direct terminal `Transition` reject opted-in jobs, which must use item-derived completion.
A migration trigger also refuses late progress or error updates to a recorded terminal summary.

Required record failures set a non-secret process diagnostic and block new admissions.
Already admitted work can save remaining observations and an honest terminal result if storage becomes writable, without clearing the diagnostic.
Typed rejection reasons distinguish an incomplete-success request from storage failure, while the existing terminal-save diagnostic remains unchanged.
Reads distinguish historical-unrecorded jobs, recorded-empty jobs and unavailable records.
Pruning skips jobs with item rows, and foreign keys prohibit deleting their owning jobs or referenced items.

## Maintained storage evidence

The new job-store tests use real SQLite transactions and independent expected identities and counters.
They cover zero, one, 201, 401 and 1,203 failed names, with a separate copied/verified item, both observations for each failed identity, admission batches and page sizes of 37, 100 and 200.
Every page is checked for completeness, order, duplicates, exact identities, observations and timestamps.
Names include empty relative keys, newlines, embedded zero characters, Unicode composition distinctions, slash/backslash distinctions, whitespace and case differences, across separate jobs and captured locations.

SQL refusals exercise the second insert of an admission batch, observation inserts, terminal row updates, audit append and audit retention.
Assertions compare the complete prior records and job/audit state after rejection; they also check the process diagnostic, refusal of further admission and saving of already admitted work after removing the owned refusal.
Other cases cover invalid UTF-8 or location shapes, hostile diagnostic strings, invalid phase/effect/byte combinations, copied items with post-commit problems, duplicate observations, comparison-only names, cross-job or missing cursors, missing jobs, read failures and late writes.
The historical-upgrade test seeds an old failure row at every earlier schema and checks that migration preserves its counters and error without inventing item history.

`TestTransferItemStorageReopen` composes the job store, existing public job-summary service and a separate child process opening the same database.
It stores 401 Copy identities plus upload/download records using typed locations, checks the public terminal failed-name total, rejects a required observation through an owned SQL trigger and observes unavailable reads before closing the parent database.
The child checks all identities and both phases, available-empty versus historical-unrecorded state, and then runs `FailInterrupted` and pruning.
Interrupted item facts and missing outcomes remain unchanged; terminal summaries remain unchanged; only old history without item rows is pruned.
The interrupted job's existing summary counters are not silently rewritten from these records: concrete restart projection remains part of each later caller migration.

## Deliberately broken controls

Sixteen test-only overlays were rejected by maintained assertions:

| Removed or broken rule | Required witness |
| --- | --- |
| Unique job/key identity | Duplicate admission is rejected without extra rows |
| Admission before copied outcome | An unadmitted comparison cannot claim a copy |
| Transaction rollback | A failed later insert leaves no partial batch |
| Verification phase/effect rules | Verification cannot claim a mutation |
| Distinct failed-name aggregation | Execution and verification failure count once |
| Failure totals beyond 200 | Larger identity sets retain their exact total |
| Page continuation | Every identity is reachable |
| Pruning protection | Item jobs remain and ordinary history can still be pruned |
| Completion condition checks | Missing, uncertain, unverified and problem outcomes reject complete |
| Item-derived terminal count | A caller's guessed failed total cannot become the saved total |
| Legacy completion refusal | Recorded jobs cannot bypass item-derived completion |
| Direct terminal-transition refusal | The alternate completion route cannot bypass the same gate |
| Diagnostic validation | Provider-like stage/code strings cannot enter records |
| Refusal/verification separation | Refused overwrites cannot consume verification credit |
| Historical recording marker | Unrecorded history cannot masquerade as recorded-empty |
| Terminal summary protection | Late progress cannot replace final counters or reason |

The overlays changed Go or embedded SQL only in external test inputs; source and migration hashes remained unchanged.
These controls prove the maintained assertions detect these particular broken rules, not that every possible mutation or failure is covered.

## Local verification

The complete Go 1.27.1 race run passed 626 top-level tests, 1,691 leaf cases and 1,836 named tests across all 20 internal packages, with no failures or skips.
These are alternative counts of the same run, not additive totals.
The run used the pinned official rclone, owned mock/two-account Swift fixtures and the required isolated Secret Service fixture.
Whole-internal statement coverage was 85.6% with `-coverpkg=./internal/...`; every recorded Go source and SQL migration hash matched afterwards.
Scenario reconciliation remained valid at 32/110 cells, with 38/49 actions still carrying gaps; the storage test claims no new matrix credit.

Frontend verification passed 852 tests across 22 files at 79.33% statement coverage, type checking, the production build, Go vet, the native Linux application build, the CGO-disabled internal build and the patched Swift source check.
The pinned Wails binding generator completed and produced no binding or dependency-file changes.
No native Windows/macOS or six-target runtime evidence is claimed by these local Linux checks.

## Limits and next work

This is storage and existing-summary-service evidence, using supplied logical observations rather than executed transfer effects.
The child test is an orderly database reopen, not a full application restart, process kill, orphan-child, fsync or power-loss test.
No new HTTP fault framework or scenario-coverage credit is claimed.
The original actual-service Copy probe and its complete owned-namespace comparisons remain the migration oracle.

Review the concrete Copy admission/observation adapter, complete item-access UI, restart projection and unresolved-rerun refusal before changing its hooks.
Then reproduce and migrate local upload and download separately, including their exact post-commit issue totals and missing authority/filesystem prerequisites.
The complete per-effect journal, malformed bulk replies, lost-history handling, stronger verification, resource bounds, retention/export and native-platform evidence remain open.
Mirror starts, existing-destination Copy overwrites and automatic recovery reclamation remain refused.
P12/P13/P28 and the valuable-data release hold remain open.

## Delivery

[PR58](https://github.com/csmart/aeronautes/pull/58) delivered the shared storage foundation after independent local review of its four logical commits.
The reviewer independently passed the full race suite with 1,836 named tests across 20 packages and no test skips, passed all 852 frontend tests, and detected sixteen meaningful broken variants, including the combined read-latch and retention protections.
The review requires the later Copy adapter plan to state the full latch-on-write-refusal scope and validate identity representability before admission; these remain explicit caller prerequisites.

All five actual [PR checks](https://github.com/csmart/aeronautes/actions/runs/34211333265) passed on reviewed source `7d61bc7a4109655d0e18c8bc7eefaf431d7a7eed` with current base `87df5495aaa1711ad2bcc506104495087580dca8` incorporated.
The protected rebase merge at 09:55:31 UTC on 8 September 2026 produced master `17cbe69058aa20fe1d0d6330c64f5103f1228e58`.
All five [master checks](https://github.com/csmart/aeronautes/actions/runs/34212679624) passed on that commit.
The merged tree `73549ee8337ce99b78cef81b14864819fec45ee8` exactly matches the reviewed source, and all four patches match individually after the merge.

This delivers the storage foundation only.
Existing Copy, upload and download callers remain unrecorded, and their complete item-access UI, interrupted-result projection and unresolved-rerun refusal remain separate reviewed migrations.
The broader audit packages and valuable-data release hold remain open.
