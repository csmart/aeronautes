# Audit query ownership and read status

This is the bounded UI-10 audit-read contract proposed after the independently reviewed cancellation slice.
It does not close UI-10, P28, UI-09, P24 or the valuable-data hold.
The oracle is UI-10's distinction between empty, unavailable and stale data, DESIGN §§47/49/50/55, K-UI-005, K-SEC-002 and K-SEC-006.
The local audit store returns descending unique IDs with an exclusive `beforeId` cursor; these observations are not proof of remote state, complete accounting or successful verification.

## Before evidence

The unchanged integrated implementation is `836ad0d2138f51741c65de948bab5c5edeb2f3f8`.
Its BrowserState lets older Search replies and unfiltered Recent replies replace the selected query, admits overlapping More requests, and has no distinct audit read status.
JobsDrawer uses the shared query array for its recent count and displays “No matching audit events” from an empty array alone.
The existing mutation wrappers await `loadAudit()` after their effects; that read currently fulfils even when Recent fails.
Changing it to throw would misreport successful mutations and bypass their existing post-audit view guards.

The [six-case probe](probes/2026-09-12-audit-query-state.test.ts.txt) ran against an isolated archive of that unchanged implementation.
The [producer](probes/2026-09-12-audit-query-state.producer.py.txt) preserves the exact command, all 100 frontend input hashes, raw result and exit in `/tmp/aeronautes-audit-query-before-jah_04xb`.
Its [evidence twin](probes/2026-09-12-audit-query-state.before.json) preserves both the original JSON-only run in `/tmp/aeronautes-audit-query-before-2txxcc77` and the same probe repeated with the default reporter to retain complete request/ID diagnostics.
Both record actual exit 1, five intended regression failures and one healthy empty-search control, with no skipped test, timeout or setup failure.
The original took 2.466 seconds and the repeat took 1.865 seconds; the repeat's stderr contains the expected assertion failures.
This probe uses real BrowserState and mounted Svelte with held fake API replies; it does not establish native Wails acceptance.

| Probe | Observed unchanged behaviour |
| --- | --- |
| Literal successful-empty control | Passed; exact prefix ` delete.λ `, result `failed`, all-account scope, cursor 0 and limit 50. |
| Legal full first page followed by two More calls | Three API calls instead of two; IDs 50 and 49 each append twice after the first 50 descending IDs 100 through 51. |
| Old first-page reply after a new filter result | IDs `[11]` replace expected `[22]`. |
| Filter changes away and back without dispatching another search | The retired reply publishes ID 11 instead of leaving the current view not loaded. |
| Late unfiltered Recent reply | IDs `[30]` replace the selected query's `[22]`. |
| Initial query pending in the mounted drawer | The drawer already announces “No matching audit events.” |

The earlier eight-case archive in the [cancellation experiment](2026-09-12-cancellation-feedback.md) remains unchanged.
Its manually seeded two-row More diagnostic is historical evidence, not a valid fixture for owned-query admission.
The new legal 50-row fixture must become a maintained regression without weakening its one-call, cursor or exact-ID assertions.
The earlier audit-error diagnostic intentionally observes the undesirable swallowed/propagated split and must not count as an after acceptance test.

## Selected query contract

BrowserState owns the selected audit query throughout its lifetime, independently of drawer collapse or the active tab.
Retain the existing explicit first-page read when Recent changes is selected and when the operation or result control changes.
Capture the exact literal operation prefix and result, fixed `bookmarkId: ""`, request generation and admitted cursor before awaiting Search.
Changing either filter value synchronously retires pending reads and previous-filter rows, even if the value changes away and back without another request.
Keep the filter controls' existing change-event submission; do not add per-keystroke search or normalise their values.
A new first-page request retires every older first-page or More request, including same-filter refreshes.
Success, failure and loading completion must all require the current generation and unchanged captured literal filters.
An obsolete reply cannot publish rows, errors or exhaustion or clear a newer pending read.

| Current state | Presentation and available action |
| --- | --- |
| Not loaded | State that the query is not loaded; permit an explicit first-page read. |
| Initial or changed-filter read pending | Remove previous-filter rows before dispatch; show loading and the current literal scope; no More. |
| Successful empty first page | Show “No matching audit events” only for that successful query; permit refresh. |
| Successful nonempty query | Show its last loaded rows; permit first-page refresh and More when admissible. |
| Same-query refresh pending | Keep earlier rows with a previously-loaded/refreshing qualifier; no More. |
| First-page failure, including after earlier empty success | Show a fixed unavailable alert; keep only prior same-query rows and label them stale; offer explicit first-page retry. |
| More pending or failure | Keep prior rows and cursor; show loading-more or page-unavailable feedback; permit page retry after settlement or first-page refresh. |

Use fixed safe errors such as “Audit events are unavailable. Retry this search.” and “More audit events are unavailable. Previously loaded results are shown.”
Never render raw bridge or SQLite exceptions.
Show the all-accounts scope explicitly, independently of the jobs account filter.
Use escaped Svelte text, existing per-instance labels and status/alert semantics.
The promised bookmark filter, per-row account/transaction diagnostics and broader UI-09 work remain open; this slice does not change DTOs or the existing event vocabulary.

## Paging admission and progress

More requires a successfully loaded current query, nonempty rows, a usable last ID, no pending query request and no confirmed exhaustion.
Admit it synchronously so two same-turn calls issue one Search request.
Keep the existing page size of 50 and explicit append behaviour.
A failed page preserves its cursor and exhaustion state; retry uses that same cursor, while first-page refresh or a filter change retires it.
Require positive safe integer IDs in strictly descending order, at most 50 rows, and every More ID below the captured cursor.
A duplicate, reversed, oversized or non-advancing page is unavailable: append nothing, preserve prior rows and offer retry or refresh.
Do not repair malformed pages by sorting, deduplicating, truncating or partially appending them.
A successful empty More reply may establish exhaustion while preserving prior rows.
This preserves explicitly requested accumulated history; P24's wider retained-row bound remains open.

## Separate Recent summary

Keep `auditEvents` for Search and a separate Recent cache bounded by the existing limit of 50.
Recent has independent not-loaded, loading, available and unavailable state and its own latest-request generation.
Validate its bounded descending IDs before accepting the cache rather than presenting a malformed slice as current.
The summary count must come from the last successful Recent slice and identify that scope, never a filtered query or total-trail count.
A failed refresh keeps any previous cache only with an unavailable/stale qualifier, including a previous zero.
An older Recent success, failure or finally cannot replace a newer cache, error or pending state.
Recent must never mutate selected-query rows, errors, exhaustion or loading, in either completion order.
Expose Recent unavailability in the collapsed summary and provide an explicit read-only Recent retry in the audit view.
Keep every existing connect/mutation `loadAudit()` call and its await ordering; add no polling or automatic effect retry.
`loadAudit()` records a read error and fulfils normally, so successful mutation return values and post-audit navigation checks stay intact.
An actual mutation rejection must still reject.
Search, page and Recent retries call read methods only.

## Implementation and validation sequence

1. Review this contract and the legal unchanged-source reproduction before production changes.
2. Correct only BrowserState audit fields, `searchAudit()` and `loadAudit()`, and the audit summary/view in JobsDrawer as one coherent implementation.
   Add dedicated browser and jobs `auditStatus.test.ts` files.
   Change only the three existing mutation-test assertions that observe Recent through the shared array; keep their exact `object.metadata.update`, `marker.delete` and `manifest.delete` expectations and every sibling assertion.
3. Exercise initial pending/rejection/retry/empty states, retained nonempty and empty results on failed refresh, older success/failure/finally, literal filter edits and change-away-and-back, legal More admission, replacement searches during More, failed-page retry and malformed-page rejection.
   Cover Recent/Search independence in both completion orders, overlapping Recent reads, stale zero and nonempty caches, summary attribution and mounted read-only retry controls.
   For every existing mutation wrapper, reject its following Recent read and prove the exact successful return/effect remains; retain true mutation rejection controls and the unchanged connection and post-audit navigation tests.
4. Preserve the before packet, run the same six probes on the final source, and run independent controls removing query ownership, guarded failure/finally, duplicate More admission, page progress, Recent separation and nonthrowing post-mutation reads.
   Each control must fail its named unchanged behavioural assertion with its actual exit and unchanged timeout/budget.
   Run focused frontend tests, Svelte check and build, then obtain strict independent implementation review before handback.
5. Integrate on the actual current master with renewed verification and range review; require all five current-head/current-base PR checks, protected merge and all five resulting-master checks before recording delivery.

Do not change cancellation, connection or selection authority, mutation methods/timing, API/DTOs, fake API behaviour, backend storage, settings, bookmarks, retention policy or existing test/CI budgets in this slice.
Stop and return any required expansion or conflicting active ownership to the coordinator.
No implementation or after result is claimed by this contract commit.
