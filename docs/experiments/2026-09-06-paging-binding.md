# Pagination ownership and retry

This records P08's first implementation under §65.11 and audit UI-03.
The oracle is that a page belongs to its connection, namespace, initial-list generation and requested marker, with one page admitted for the current listing intent.
An obsolete success, error or finalisation cannot alter a newer listing or its pending request.

## Implementation boundary

Container and object pages capture the existing UI scope revision, their initial-list generation and exact continuation marker.
Admission refuses a duplicate pending request, a disconnected or connecting view, an empty marker and an initial list that is pending or failed.
Initial reload clears the old continuation marker and page state, including an identical namespace reload that does not change its visible names.
Publication rechecks scope, generation and marker; finalisation clears only the request object that still owns the pending slot.
Pending and error state are visible only in their original current scope.
Initial container loading also belongs to its originating scope and request generation, so an obsolete load cannot leave a later view busy or clear a newer load's status.

Appending keeps existing row identities and filters duplicate incoming identities, preserving the selection and inspector working copy.
Container identity is its exact name; an object listing row includes its kind so a virtual prefix is not conflated with an object sharing that name.
The response marker must advance before any rows or progress are accepted, unless it explicitly marks the end of the listing.
This follows the ascending-name and greater-than-marker contract in the [Swift Object Storage API](https://docs.openstack.org/api-ref/object-store/).
The comparison uses UTF-8 bytes rather than JavaScript's UTF-16 code-unit order, with supplementary Unicode and prefix-length controls.
Requested namespace and marker strings are not trimmed or normalized.

Both panes disable pending Load more controls, expose their busy state and show an explicit paging error with retry.
A failed page keeps the loaded rows, selected object and retry marker; it does not replace the initial-list error or hide usable rows.
Retry clears the previous page error at admission.
The state methods still reject current errors to their caller; the component handlers consume that rejection after the state records the visible error.
Obsolete errors are retired with their request, as required by the preceding P07 reset work.

## UI scenario matrix

The maintained harness is `frontend/src/features/browser/pagingBinding.test.ts`.
It exercises production BrowserState and mounted ContainerColumn/WorkingList components with a recording Api boundary and deferred responses.
These separate UI-to-bridge scenarios earn no Go service-scenario credit.

| Scenario | Maintained test or group |
| --- | --- |
| Duplicate pending requests append once | `admits one pending page and appends it once` |
| Identical initial reload with the same marker retires a page | `retires a page when the same initial list is reloaded` |
| Current error preserves rows and marker, then retry succeeds | `keeps a current page error separate from usable rows` |
| Non-advancing response cannot append or consume progress | `rejects a non-advancing page` |
| Old success/error completes before or after a newer request | `a retired page finishes ... without changing a newer page` |
| Changed requested marker retires success/error | `retires a page whose requested marker is no longer current` |
| Account/container/prefix change and return, reconnect and active save | `retires page state across ...` |
| Initial pending/error refuses load more even with a stale marker | `refuses page admission during an initial ... load` |
| Empty marker, disconnected and connecting admission | `refuses page admission with ...` |
| Overlapping rows preserve unique identities, selection and detail | `deduplicates overlapping rows while preserving the current selection` |
| Forward/backward, equal, prefix-length, Unicode and terminal markers | `checks marker progression ... using UTF-8 order` and non-advancing test |
| Exact request strings survive whitespace and Unicode | `keeps the exact marker and namespace in the request` |
| Mounted controls, synthetic double click, visible failure and retry | `renders pending and retry controls without hiding loaded rows` |
| Same-name virtual prefix and object stay distinct | `does not conflate an object and virtual prefix sharing a name` |
| Old initial container load cannot own a later busy state | `an obsolete initial container load cannot leave a later scope or load busy` |

## Evidence

With Node 22.23.2 and unchanged locked dependencies, all **618 tests in 18 frontend files pass**, with zero failures or skips.
Whole-source statement coverage is **75.56%** (2,987 of 3,953 statements), not an audit completion percentage.
Frontend type checking reports zero errors and warnings, the production build completes with 205 modules, and `go build ./...` passes with the final assets.
No Go implementation, generated bridge model, dependency or workflow changed.
Independent local review and all five current-head/current-master PR checks, followed by the resulting master checks, remain required.

All **64 new cases** ran against the parent production files at `3f6cf0d9989bdfd0`: **48 assertion failures and sixteen passing controls**, with zero skips, setup errors or unhandled rejections.
The identical cases pass after implementation.
The first eight focused cases separately failed their intended assertions before the fix and passed afterwards.
The existing 554 tests were preserved without changing their assertions, including P07's current-page-error and obsolete-page retirement contracts.

Three original archived bodies were also selected with only import locations changed.
Before this slice, cross-container page retirement passes while duplicate Load more and Stop-before-Find-start-return fail: one pass, two assertion failures and nine unselected cases.
After this slice, both page probes pass while the unchanged Find cancellation probe still fails: two passes, one assertion failure and nine unselected cases.
That remaining failure belongs to the separately planned frontend Find slice.

The final scratch runner loads all 64 cases for every independently broken safeguard, requiring assertion failures and rejecting skipped, import, transform, timeout and unhandled-promise failures.
All **33 variants** are caught; paired counts below are separate container and object variants.

| Removed or broken safeguard | Failing cases, container / object |
| --- | ---: |
| Duplicate request admission | 2 / 2 |
| Initial-list generation | 3 / 3 |
| UI scope | 3 / 3 |
| Requested marker | 2 / 2 |
| Result publication ownership | 9 / 9 |
| Error ownership | 3 / 3 |
| Finalisation ownership | 2 / 2 |
| Initial pending admission | 1 / 1 |
| Initial failure admission | 1 / 1 |
| Duplicate row filtering | 1 / 2 |
| Retry clears old error | 1 / 1 |
| Row kind in object identity | 1 |
| Response marker progression | 10 |
| UTF-8 ordering replaced by UTF-16 | 4 |
| Backward byte rejection | 2 |
| Equal or shorter prefix rejection | 4 |
| Initial container loading finalisation | 1 |
| Initial container loading scope | 1 |
| Disabled pending button | 1 / 1 |
| Visible paging error | 1 / 1 |

The initial runner's general name matching sent two initial-loading controls to the object-page guards instead.
Its result is retained but is not the final evidence; the corrected runner executes the intended initial-loading guards and the complete matrix again.
Local artifacts are under `/tmp/aeronautes-remediation-20260905`: `p08-paging-final-*`, the reproducible `p08-paging-controls.py`, complete `p08-paging-controls-v2/results.json` with per-variant reports, and `p08-paging-archived.py` / `p08-paging-archived/` for the selected original probes.

## Remaining work

This is P08 pagination only.
Frontend Find start/stop/close/navigation and exact result selection, plus backend worker/retention/idle-expiry bounds, remain separately reviewed implementations.
P22 still owns server listing completeness and total retained UI rows; this page append filter is not a bounded-memory namespace inventory.
P24 broader resource budgets, P10/P11 authority, P13 outcomes, P14 external writers and P15 ownership remain open.
These tests do not prove remote data safety or native platform behavior.
The valuable-data release hold remains.
