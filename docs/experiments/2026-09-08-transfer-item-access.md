# Complete retained item access

## Contract and scope

[ADR 0030](../adr/0030-copy-item-recording.md#complete-access-and-interrupted-records) requires complete access before Copy opts into the [logical item-record foundation](../adr/0028-transfer-item-records.md).
The previous drawer exposed only fifty recent jobs and no item pages, so a retained identity could be inaccessible even when storage had kept it.
The first implementation slice adds the history store, three read-service methods, generated bindings and bounded history/item views.
Existing Copy, upload and download callers still create version-zero jobs and show records not captured.
The tests below deliberately construct version-one facts and exercise the actual read service; they are not evidence that actual transfers now record outcomes.

## Behaviour and maintained tests

History defaults to fifty jobs and accepts at most one hundred, ordered by creation timestamp and job ID together.
The last displayed job ID is a keyset cursor resolved within the same read transaction and account filter.
New jobs cannot shift an older page; missing, pruned, mismatched or corrupt cursors require a fresh first page instead of pretending history ended.
The [store tests](../../internal/jobs/history_test.go) cross page boundaries within tied timestamps, add jobs between pages, exercise source and destination account filters and refuse invalid requests, corrupt rows, closed storage and a recording latch without partial results.

`JobService.History`, `Items` and `ItemTotals` expose bounded reads with explicit DTO mapping.
Item pages default to one hundred and accept at most two hundred; row IDs and cursors stay decimal strings through JSON and the JavaScript API.
Raw source, destination, optional staging and relative key values are preserved alongside quoted displays that expose invisible characters without executing markup or parsing a display locator.
Execution and verification remain separate observations under one identity; missing observations are not converted into success.
Uncaptured history, a recorded-empty job and an unavailable query remain distinct, and query failures cannot return a partial page or fabricated zero totals.

The [read-service system tests](../../internal/systemtest/job_items_test.go) reach 402 constructed identities in a job older than 65 newer jobs, through default, 37-row and 200-row item pages.
They preserve 401 distinct failed names with both phases and one reported copied/verified control, including row IDs above JavaScript's exact integer range.
The names cover empty keys, literal versus actual newlines, zero and bidi/zero-width characters, markup, Unicode normalisation distinctions and a valid replacement character.
Other cases retain local and staging locations, comparison-only facts, missing execution, unverified copies, refusals, conflicts and overlapping diagnostics.
Closed storage, required-write latch, a missing observation table and an invalid later identity refuse without leaking fixture error payloads or an earlier valid row.
These tests claim the six read/refusal cells only from actual bound-service receipts; they add no transfer-execution coverage claim.

The drawer keeps live recent-job polling separate from explicit historical pages.
Both job and item lists retain one bounded displayed page, with first/next navigation, account filtering, clear loading/error states and selection reset.
An active item view states that records are incomplete; polling its specific job refreshes the first page and totals at completion even after that job leaves the recent-job window.
Historical job states are labelled as observed when the page loaded, and item details show the last observed state.
Switching jobs, pages or accounts invalidates earlier responses, and a status read failure hides the retained rows and counts until a successful refresh.
The [frontend tests](../../frontend/src/features/jobs/itemRecords.test.ts) navigate 153 retained jobs to 401 item identities, keep displayed pages bounded, inspect escaped full locations and exercise stale success/error responses, availability failures and older active-job completion.
They drain the resolved read and queued UI continuations before asserting that an obsolete response had no effect; checking only the first promise continuation was insufficient and was corrected after a broken-code control exposed it.

## Evidence and limitations

The final local producer passed 641 top-level tests, 1,864 leaf cases and 2,020 named tests across twenty Go packages, with zero failures or skips and 85.7% combined statement coverage.
It ran Go 1.27.1 with race detection, the owned mock and two-account SAIO fixtures, the required Secret Service fixture and pinned rclone 1.74.3.
All 279 Go/SQL source hashes matched the validated working tree after the run; the producer recorded the parent commit because the implementation was not committed until verification completed.
The new access fixtures themselves use only local records and need no Swift or credential authority.
The first producer passed its data assertions but the scenario reconciler rejected duplicate read receipts from the three page-size passes; the corrected full producer retained every assertion and emitted one receipt for that cell.
Its reconciled scenario report is valid at 38/116 cells, with gaps in 38/52 actions; the six new read/refusal cells do not close the wider behavioural matrix.

All 874 frontend tests across 23 files passed, including 22 new item-access tests; combined frontend statement coverage is 81.19%.
Svelte/TypeScript checks, frontend production build, vet, native application build, CGO-disabled internal build, upstream Swift core tests and Swift-source provenance checks passed.
Regeneration reported twelve services, 52 methods and 58 models, with all 22 generated binding files unchanged on the repeated run.

Ten deliberately broken Go overlays were rejected by the matching behavioural assertions: reversed history tie order, missing history continuation, ignored history latch, partial history on decoding error, lossy numeric item IDs, missing item continuation, concealed unavailable pages, fabricated totals, missing verification and raw identity display.
Seven isolated frontend variants were likewise rejected: stale item pages, stale account history, raw identity rendering, hidden unavailable results, capped history, missing completion refresh and accumulated item pages.
The unmodified isolated frontend control passed all 22 tests; these were behavioural failures, not compilation failures.
Independent local review and all five protected PR/master checks remain required before delivery.

This slice does not opt in a transfer caller, change interruption projection or rerun eligibility, certify legacy job/audit error text, or establish a per-effect journal, retry authority, stronger verification or physical durability.
The [valuable-data release gates](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remain open, including the rest of P12/P13/P28 and the dependent authority, filesystem, native-process and platform evidence.
