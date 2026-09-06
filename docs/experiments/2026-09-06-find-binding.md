# Find intent, cancellation and exact selection

This records P08's second implementation under §65.11, audit UI-06, §14.2 and §16.
Each Find intent owns an immutable bookmark, container, prefix and UI scope revision, with a separate intent generation.
Stop, replacement, either close control, destruction and navigation retire it immediately, including before startup has returned an id.
The existing bridge remains unchanged.

## Implementation boundary

Find becomes busy when the intent is admitted, before awaiting cancellation of an earlier known id.
A second intent cannot borrow the first one's eventual startup, status, error or finalisation.
Cancellation detaches the old request before awaiting its bridge response; every late id from a retired startup is cancelled without being polled.
Scope setters and initial reload both retire old work, including change-and-return histories and an identical namespace reload.
Blank queries and disconnected, connecting or containerless views refuse startup before the bridge.
Component admission also refuses synthetic duplicate submissions while startup is pending.

Stopped results remain available only in their original scope.
The panel distinguishes cancellation requested, cancellation unconfirmed and a start cancelled before any bridge admission.
It stops presenting old throughput as live and never changes an unfinished backend snapshot into a claimed completed scan just because the user pressed Stop.
A polling error preserves the last count, matches and buffer-full flag.
The panel captures its mounted BrowserState and panel revision, so a stale close, prop change or unmount cannot cancel a later panel's scan.
The main toggle, explicit close and destruction share the same cleanup.

Clicking a match captures its exact object identity before cancellation and checks ownership again before navigation and after the parent listing returns.
Stop, a new selection, a newer scan, close and intervening reload or navigation can retire that follow-up.
Only the first delimiter page of the parent is fetched.
If the exact object is absent from that page or folded into a virtual prefix, the state appends the match as an advisory object row and selects it through the existing one-object HEAD path.
That row does not establish current existence, content or mutation authority.
A missing or mismatched HEAD leaves detail unavailable and editing refused; SLO and DLO selection retains the existing manifest or segment-preview path.
No preceding pages are scanned and no per-row HEAD loop is added.
Literal object-name bytes are preserved, filtering anchors selection by kind and name, and external selection scrolls the virtual list to the result while allowing later manual scrolling.

## UI scenario matrix

The maintained harness is `frontend/src/features/browser/findBinding.test.ts`.
It exercises production BrowserState, FindPanel, WorkingList, VirtualList and Inspector with a recording Api boundary, deferred responses and deterministic polling timers.
These are UI-to-bridge scenarios, with no Go service-scenario credit.

| Scenario | Maintained group or case |
| --- | --- |
| Stop, both closes, unmount and scope changes before startup returns | `Find intent lifetime` |
| Replacement startup and old startup/poll success or failure in either completion order | `Find response ownership` |
| Known and late-id cancellation completing after a newer scan | `cancellation ... stays detached` and `late old cancellation` |
| Pending cancellation followed by Stop, close, navigation or another start | `retires a start waiting for the old cancellation` |
| Scope return, identical reload, reconnect and active/inactive bookmark save | `a pending poll observes ... scope ownership` |
| Current cancellation success, failure and uncertain startup | `Find controls and truthful status` |
| Current startup/poll errors, expired id, retained counts and buffer-full status | `shows a current ... failure` |
| Invalid startup, literal query/namespace and duplicate form events | `Find controls and truthful status` |
| Old panel unmount, BrowserState prop replacement and all close paths | `Find panel ownership` |
| Literal names, existing row, off-page row and same-name virtual prefix | `Find exact result selection` |
| Full first page, bounded retrieval, visible selection and manual scrolling | `makes a selected result beyond a full page visible` |
| Missing object, mismatched HEAD and SLO/DLO detail | `Find exact result selection` and `Find result navigation lifetime` |
| Stop/close/navigation/selection during cancellation or parent listing | `Find result navigation lifetime` |
| Late selected HEAD success or failure after another view takes ownership | `after result selection` |

## Verification

With Node 22.23.2 and unchanged locked dependencies, all **718 tests in 19 frontend files pass**, with zero failures or skips.
Whole-source statement coverage is **77.59%** (3,144 of 4,052 statements), not an audit completion percentage.
Type checking reports zero errors and warnings, the production build completes with 205 modules, and `go build ./...` passes with the final assets.
The existing 618 test assertions were preserved.
No Go implementation, generated bridge, dependency or workflow changed.
Independent local review and all five current-head/current-master PR checks, followed by the resulting master checks, remain required.

The same **100 new cases** on parent production `686fb9fdef2611a183e5d5e3616e6c9475291b3f` produce **69 assertion failures and 31 passing controls**, with no skips, setup errors, timeouts or unhandled rejections.
All 100 pass after implementation.
The first ten focused cases also failed their intended assertions before implementation and passed afterwards.
Later adversarial cases caught missing Stop/selection ownership after cancellation, prop-change cleanup and preservation of the buffer-full flag; those failures and corrections are retained separately.

The final scratch runner loads all 100 cases for each independently broken safeguard, requiring assertion failures and rejecting setup, transform, timeout and unhandled-promise failures.
All **42 variants** are caught.
Where a row gives multiple counts, they are separate variants in the listed order.

| Broken safeguard | Failing cases |
| --- | ---: |
| Detach cancellation before awaiting it | 20 |
| Preserve buffer-full status on poll error | 1 |
| Reject blank query | 2 |
| Check intent after previous cancellation | 4 |
| Cancel a late retired startup id | 17 |
| Poll, error and finalisation ownership | 12 / 6 / 11 |
| Stop advances intent | 2 |
| Old cancellation success/error ownership | 9 / 2 |
| Report cancellation uncertainty | 2 |
| Distinguish a start cancelled before admission | 1 |
| Bookmark, container and prefix setter retirement | 1 / 1 / 1 |
| Initial reload and close retirement | 2 / 8 |
| Clear retained results on scope retirement | 2 |
| Panel revision and unmount cleanup | 2 / 2 |
| BrowserState prop cleanup and control ownership | 1 / 1 |
| Duplicate submit and match-click guards | 1 / 1 |
| Disable a pending match button | 1 |
| Visible cancellation notice | 6 |
| Stopped progress and live-throughput display | 2 / 2 |
| Complete gate after cancellation | 8 |
| Later Stop and selection checks after cancellation | 1 / 1 |
| Complete gate after parent listing | 10 |
| Later scan and selection checks after listing | 2 / 1 |
| Object row kind and filter selection kind | 1 / 1 |
| Select the exact result row | 24 |
| Report a failed parent listing | 1 |
| Preserve literal name instead of trimming | 2 |
| Make external virtual-list selection visible | 1 |
| Existing selected-HEAD identity check | 1 |

The first runner exposed test waits that hung under broken admission or cancellation, plus weak timing for a duplicate submission and redundant scope paths that did not isolate the setters.
Those runs are retained as incomplete evidence, not counted as successful controls.
The corrected tests assert bridge admission before awaiting completion, release deferred work in finalisation, cover direct scope changes and submit again after startup is admitted.
The runner also terminates its owned process group on timeout and records an invalid result rather than losing the rest of the matrix.
The complete final matrix is `p08-find-controls-v4/results.json`; every variant is valid.

## Original probe and admission timing

The three selected original archived bodies, changed only to relocate imports, still report two passes and one assertion failure both before and after this slice, with nine cases unselected.
The two page probes pass; the original Find probe waits for `findRunning` and assumes that a bridge start has already happened.
This implementation makes the intent busy before bridge admission, so that wait can complete before any start request is sent.
A separate instrumented copy preserves the assertion and records one start and zero cancels before the fix, but zero starts and zero cancels afterwards.
The latter run cancels an unadmitted intent; there is no returned backend id to cancel.
This literal archived assertion is therefore not claimed as passing.

A second copy changes only that wait to require one recorded `api.startFind` call before pressing Stop, retaining the original late-id cancellation assertion.
With the actual bridge boundary synchronised, the selected probes produce two passes and one assertion failure before the fix and three passes afterwards, with nine unselected cases in each run.
The maintained suite separately covers both cancellation before admission and cancellation of every late id after admission.
The original bodies and both kinds of evidence remain available for independent review.

Artifacts are under `/tmp/aeronautes-remediation-20260905`: `p08-find-final-*`, the reproducible `p08-find-controls.py` and `p08-find-controls-v4/`, plus `p08-find-archived.py`, `p08-find-archived-admitted.py` and `p08-find-archived-observed.py` with their corresponding directories.
The observed probe's request counts are in `p08-find-archived-observed/before-default.log` and `after-default.log`.

## Remaining work

This is the frontend Find slice only.
Backend worker admission, retained-record limits, idle expiry and shutdown cleanup remain the third P08 implementation.
Its timing and shutdown seam still needs prior design review if an additional service contract is required.
P22 listing completeness and retained UI rows, P24 wider budgets, P10/P11 authority, P13 outcomes, P14 external writers and P15 ownership remain open.
The UI tests do not prove backend cancellation, native platform behavior or remote data safety.
The valuable-data release hold remains.
