# Interrupted item records and guarded reruns

## Contract and reproduced failures

[ADR 0030](../adr/0030-copy-item-recording.md#complete-access-and-interrupted-records) requires startup to project distinct failed names from acknowledged version-one records and retain every execution, verification and recovery fact.
Other counters remain the last saved progress, and a projection failure must refuse stale public status and new job admission for the session.
The backend rerun action must inspect retained facts before creating a job, pinning bookmarks, authenticating or contacting Swift.
Uncaptured or unreadable records, unresolved effects and unverified reported copies refuse that action.

The maintained [orderly-reopen](../../internal/systemtest/item_interruption_test.go) and [service-level rerun](../../internal/systemtest/rerun_items_test.go) tests were also run with the exact pre-fix `jobs.go` and `transferservice.go` from PR60 source `83d45e54b96f762042ee0aaa040930f60c74ae3b` substituted through Go overlays.
The old startup sweep retained the stale failed counter of 400 despite a durable distinct total of one; the mock and real-Swift rerun cases admitted a new Copy from uncaptured history.
Those three maintained system tests failed their behavioural assertions, while the fresh-planning control passed, with no skips or compilation errors.
Earlier focused probes independently reproduced stale counts for one, 201 and 401 failed names, an accepted corrupt-counter projection without a latch, and admitted reruns for uncaptured, unresolved and unverified history.
These are constructed logical-record fixtures; current actual Copy, upload and download callers have not opted into recording.

## Implementation and maintained evidence

`FailInterrupted` reads bounded pages of one hundred pending jobs within its existing single write transaction.
For version-one jobs it derives distinct failed names from item totals before saving the failed terminal state, preserving other counters and labelling them as the last saved progress.
Historical version-zero counters and uncaptured status remain explicit.
Decode, query, write and ignored-update failures abort the entire transaction, return no partial success and latch the shared store against public job/history/item reads and new jobs.
The existing application startup passes this same store to the job runner and services.
The latch neither proves other admitted workers stopped nor establishes native-process quiescence.

The [store tests](../../internal/jobs/interrupted_items_test.go) count one, 201 and 401 identities once despite failures in both phases, preserve the other saved counters, project 206 pending jobs across page boundaries and leave an existing terminal job and recovery fact unchanged.
They refuse an unavailable database and malformed counters, a missing totals table, and updates that abort or silently affect no rows.
The write-refusal and ignored-write faults fire only after an earlier job page has been updated inside the same transaction, proving that the earlier page must roll back too.
Every new-job family sharing the store is refused after the latch, and another startup sweep cannot clear it in that process.

The orderly child-reopen test retains complete pages for one, 201 and 401 failed names, with newline, zero, Unicode and valid replacement characters, both failure observations, a missing execution and a copied item without verification.
It verifies the exact failed-name projection, unchanged other counters, explicit last-saved labels, unresolved and unverified facets, retained committing records, uncaptured legacy history and no invented audit.
The test creates no connections or native processes and performs no automatic cleanup.
An orderly database close and child reopen do not establish full application restart, crash, fsync or power-loss safety.

`RerunJob` checks every bounded retained item page before reading totals; zero uncertainty totals alone cannot certify readable identities or observations.
It returns constant explanations for unreadable, uncaptured, unresolved and unverified history before entering normal Copy validation or connection authority.
The [backend tests](../../internal/app/rerun_items_test.go) include an invalid identity and observation after item 200, missing observations storage, a required-write latch, closed storage and a missing job.
They also exercise refusals without a connection service, and the existing actual-Copy test confirms that completed unrecorded transfers cannot use one-click rerun.
A separately constructed, fully inspected history fixture permits a new job which carries the original scope and effective settings through current planning and finds a newly added source name.
It does not replay a prior admission or rewrite old records.

The mock and two-account real-Swift system cases assert no new job, storage requests, record changes or audit changes on each refusal.
Independent complete namespace snapshots preserve source sentinels, another job's staging object, shared segments and their external DLO/SLO aliases.
Permitted-rerun cases observe the newly planned destination bytes independently, preserve the remaining namespaces and historical facts, and credit only the actual rerun receipt.
Those maintained receipts add the existing rerun happy/refusal cells; they do not certify stronger verification, reviewed immutable plans or safe retry of unknown effects.

The drawer displays the backend refusal as an alert, retains the interrupted reason and available item view, and offers no fallback admission after refusal.
Four [frontend cases](../../frontend/src/features/jobs/itemRecords.test.ts) cover the exact constant reasons and unchanged displayed job/item facts.
One-click rerun is now unavailable for all historical and newly created unrecorded Copies until the checked caller migration; an explicit fresh Copy through the normal dialog remains available subject to its existing restrictions.

## Validation and remaining gates

The complete local Go 1.27.1 race producer passed 653 top-level tests, 1,888 leaf cases and 2,048 named tests across twenty packages with zero failures or skips and 85.8% combined statement coverage.
It used the owned mock and two-account SAIO fixtures, required Secret Service fixture and pinned rclone 1.74.3.
The systemtest package took 511.220 seconds under the existing fifteen-minute package budget.
All 285 Go/SQL source hashes matched after the run; its recorded parent head predates the implementation commit and the separately verified master branch switch preserved every uncommitted source file byte for byte.
Scenario reconciliation is valid at 40/116 cells, with gaps in 38/52 actions.

All 878 frontend tests across 23 files passed, with 81.42% combined statement coverage.
Svelte/TypeScript checks, frontend production build, vet, native application build, CGO-disabled internal build, upstream Swift core tests and Swift-source provenance checks passed.
Wails regeneration updated only the rerun method's generated comment; a repeated generation left all 22 files unchanged, with twelve services, 52 methods and 58 models.

Nine broken Go overlays were rejected by behavioural assertions: stale failed counts, committing an earlier page after a later write refusal, accepting an ignored update, omitting the interruption latch, allowing each of the three unsafe rerun modes, reading only the first page and exposing a raw unavailable-store error.
Two isolated frontend variants hiding the refusal or interrupted-progress reason failed all four matching cases; the unchanged isolated control passed all 26 item-view tests.
The negative controls had no compilation failures or test-timeout substitutions.

Independent local review and all five protected PR/master checks are required before delivery.
Checked Copy admission, execution, verification and item-derived completion are still the next ADR 0030 slice, followed by separate local caller migrations.
Per-effect history, remote identity and authority, stronger content/metadata fidelity, filesystem safety, native child lifetime, physical durability and all six platform targets remain wider work.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused, and the [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.

## Delivery

[PR61](https://github.com/csmart/aeronautes/pull/61) delivered this slice through a protected rebase merge at 16:18:15 UTC on 8 September 2026.
Both logical commits passed independent local review before publication; the reviewer also repeated the complete race suite and frontend checks on the implementation commit and independently exercised the failure controls.
The reviewed source was `82a7ccd3561c43a4a8e2ad5b4dae23ed0dff64f8`, and resulting master was `44623f5dcc1232c2e06c44a6122ef1488bf05864`.
Their trees are identical at `8bc43ce1559f4f84081779e11c19c4432be46878`, and the delivery-documentation and implementation patches were both preserved unchanged by the protected rebase.
All five actual latest jobs passed in [PR run 34248659338](https://github.com/csmart/aeronautes/actions/runs/34248659338) before merging, with the current master incorporated and active strict protection without bypass actors verified.
All five resulting master jobs passed in [run 34250226792](https://github.com/csmart/aeronautes/actions/runs/34250226792).
The integration systemtest package took 588.759 seconds in the PR and 582.863 seconds on master; the complete integration jobs took 12 minutes 56 seconds and 12 minutes 48 seconds respectively, within the unchanged fifteen-minute package and twenty-minute job budgets.
Both CI integration runs had zero failures and the existing optional `TestRcloneMatrix` and `TestKeychainRoundTrip` skips; the separate full local producer above had zero test skips.
This delivers the interruption and rerun prerequisites only; actual Copy and local transfers remain unrecorded until their separately reviewed caller migrations, and the valuable-data release hold remains open.
