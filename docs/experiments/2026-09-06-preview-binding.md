# Preview intent binding

Date: 2026-09-06.
Scope: P06's two implementations, Copy/Mirror comparison and container/prefix Delete dialogs, followed by local picking and preflight.
Oracle: [DESIGN §65.11](../DESIGN.md#6511-audit-remediation), approved as the P06 sequence, and [UI-01](../audits/2026-09-05/ui-product.md#ui-01--p1-a-late-preview-can-authorize-a-different-destructive-operation).
The preceding implementation is the frontend tree at `350e1dccca723dbd`, identical to merged P05 master `f8ca5acee84257d1150cd0750276f1df5401a27d`.

## Contract and implementation

Only the current successful preview may enable Start.
Each attempt captures its request, dialog generation, browser instance and UI scope revision.
The request and Copy settings are frozen independently of the controls.
Every relevant dialog input invalidates the attempt immediately, including per-job typing before blur and mode changes that are later reversed.
Browser scope setters advance a revision synchronously, so leaving and returning to the same bookmark, container or prefix before a reactive effect runs still invalidates the preview.
Reconnect attempts advance that revision even when they fail and leave all visible request fields unchanged.
This UI revision is separate from the backend credential generation.

Results, errors and preview finalisation apply only to their owning attempt.
Scope changes clear the old display and pending state, allowing a fresh preview whose state cannot be replaced by an older reply.
Close button, scrim close and unmount invalidate the dialog lifetime.
Start rechecks the current intent inside the handler and consumes the preview before awaiting the bridge.
Delete uses the captured scope with freshly entered matching container confirmation; input changes and submission clear the confirmation.
There is at most one pending Start in a dialog, including after an intent edit, and an obsolete Start reply cannot describe the new intent.
Mirror remains unavailable, and returning to Copy requires a new preview.

## UI scenario matrix

These scenarios mount production dialogs with the production BrowserState and record the existing Api boundary.
They belong to the separate UI-to-bridge harness in DESIGN §65.10 rule 4 and earn no Go service-scenario credit.
The parameterised cases live in [previewBinding.test.ts](../../frontend/src/features/transfer/previewBinding.test.ts); the existing [TransferDialog tests](../../frontend/src/features/transfer/TransferDialog.test.ts) retain the P05 refusal disclosures and Mirror controls.

| Scenario | Maintained test name or parameterised family | Required observation |
| --- | --- | --- |
| Source identity, pending and completed | `disarms on $field change, revert=$revert, phase=$phase` | Bookmark, container and prefix changes disarm even after a same-turn return; no stale preview or Start call |
| Same request, new connection attempt | `disarms on same-bookmark reconnect with unchanged request, phase=%s` | A failed reconnect preserves the visible fields but invalidates the preview |
| Connection in progress | `refuses Preview and Start throughout a reconnect attempt` | Synthetic clicks cannot submit during reconnect; a fresh preview is required afterwards |
| Destination and settings input | `Copy disarms on $name input before blur, revert=$revert` | Account, container, prefix, profile, chunk size, transfers and bandwidth invalidate without blur |
| Mode changes | `Copy mode change and return requires a new preview`; `Delete ignores a pending preview after changing to %s and back` | Returning to the original mode cannot revive an earlier attempt |
| Destructive scope widening | `Delete ignores a prefix preview after changing to delete the container` | Prefix counts never arm container deletion |
| Typed confirmation | `Delete requires new confirmation after changing to %s and back`; `Delete refuses a synthetic Start with confirmation %j` | Confirmation is cleared on change and must exactly match the captured container |
| Submission before reactive invalidation | `checks the current scope in Start before the invalidation effect runs` | The handler refuses immediately, even while the old button is still in the DOM |
| Duplicate or missing preview | `refuses synthetic Start before any preview`; `does not launch a second preview or Start while its preview is pending` | Disabled controls cannot bypass handler checks |
| Exact single submission | `starts the exact previewed request once and consumes the preview on error`; `Copy submits the complete previewed settings and both scopes` | Exact captured arguments, one call, and no automatic reuse after uncertain failure |
| Current and obsolete Start replies | `shows the current Start result`; `keeps a late Start %s out of a changed intent` | The current job appears; an older success or error cannot describe the changed intent |
| Old preview replies | `ignores late %s and finalisation while a newer preview waits`; `keeps a newer completed preview after a late %s` | Both completion orders preserve the new result, error and busy ownership |
| Preview retry failure | `clears an earlier preview before retry and leaves a failed preview disarmed` | A failed replacement cannot leave the old preview armed |
| Immediate close and remount | `ends its lifetime immediately on $route close with late $outcome`; `cannot publish after unmount or arm a reopened dialog` | Button/scrim close rejects late success/error before parent unmount; a reopened dialog owns a fresh attempt |

## Before and after

All 89 maintained cases pass after the change.
Running those exact tests against the preceding three production files produces 86 assertion failures and three passing controls, with no skipped tests or setup failures.
The passing controls are the two unmount/remount cases and the unchanged-request Copy settings control; they were not new defects.
The earlier 67-case run is an intermediate subset, not an additional set of failures.

The original two archived UI-01 probes were also reproduced before implementation with their Start-disabled assertions failing.
That first run adapted only Copy's obsolete `to transfer` summary label; ten unrelated archived cases were not selected.
Their original readiness waits require rendering the obsolete preview or its confirmation input, which correct rejection now removes.
For the final paired run, both copies instead drain the resolved reply's microtasks; Delete types its confirmation only when that old input exists.
The Start-disabled assertions remain unchanged, and both probes additionally require the stale preview to be absent.
The same adaptation fails both intended disabled-button assertions before and passes both afterwards, with the other ten cases still unselected.
No timeout waiting for deliberately removed markup is counted as a safety failure.

## Removed-safeguard controls

Each variant runs in a separate scratch frontend against all 89 maintained cases.
The submitted tree is unchanged, every variant loads, and each exits with assertion failures without a skipped test or setup error.

| Deliberately broken behaviour | Failing cases |
| --- | ---: |
| Stop advancing the browser scope revision | 26 |
| Stop advancing dialog generations | 10 |
| Accept an obsolete successful preview | 2 |
| Accept an obsolete preview error | 8 |
| Let obsolete finalisation clear the newer busy state | 4 |
| Delay invalidation until parent unmount after close | 8 |
| Remove Start's current-intent revalidation | 2 |
| Retain a submitted preview and remove the pending-Start guard | 3 |
| Remove the Delete handler's confirmation check | 3 |
| Delay per-job typing invalidation until blur | 3 |

The first two controls independently exercise scope history with an identical request and dialog history with an unchanged browser scope.
The synchronous Start test separately catches relying only on the later reactive effect.

## Copy/Delete verification and limits

The full frontend suite passes 164 tests across 12 files, with no failures or skips and 37.09% whole-source statement coverage.
Svelte/TypeScript check reports zero errors and warnings; the frontend production build and `go build ./...` pass.
No Go source or bridge model changed, so the existing Go scenario ledger is not enlarged and the Go runtime suite is left to the required PR checks.
The local runtime artifacts are the `p06-copy-delete-verified.*`, `p06-preview-controls/`, `p06-adapted-before.*` and `p06-adapted-after.*` records under the task's temporary evidence directory.

At this checkpoint, local picker/preflight binding remained the next implementation commit, recorded below.
This frontend protection does not establish a backend one-shot plan, replay/expiry refusal or unchanged remote data; P11 and P14 remain open.
Bookmark lifecycle and inspector/manifest deletion are P07; paging/Find are P08; destination-name normalisation is P23.
Local filesystem and native-picker obligations remain P18–P21/P30, and these mounted tests establish no native accessibility or platform picker behaviour.
The valuable-data release hold remains.

## Local picker and preflight binding

The second implementation uses `c5960d05d611bf46`, the approved Copy/Delete commit, as its baseline.
It captures the remote account, container, prefix and settings before opening the native picker.
The returned opaque token is admitted only while that intent is still current, then frozen with the complete typed request passed to both preflight and Start.
BrowserState's four local-transfer wrappers now forward that request without reconstructing its scope.
The existing Go request models and bridge API are unchanged.

A separate local-dialog revision advances when the existing upload/download/open state changes.
It catches a mode change and return, or close and reopen, before the parent rerenders without invalidating unrelated Copy/Delete dialogs.
Current scope, mode, revisions, browser identity and dialog lifetime are checked before preflight, before publishing its report and inside Start.
Old picker replies cannot initiate preflight, and old report/error/finalisation replies cannot replace a newer attempt's state.
Only a successful current report without rejection may start; the snapshot is consumed before submission.
Cancelled, empty or failed selections and preflights cannot reuse an earlier report.
One pending Start blocks a new picker until it finishes, and a late Start result cannot describe a changed intent.

The [local UI tests](../../frontend/src/features/transfer/LocalTransferDialog.test.ts) add 108 mounted production-component cases and two BrowserState request-forwarding contract cases.
The props read the same `browser.localTransfer` state used by BrowserScreen, including changes made before a reactive effect can run.

| Scenario | Maintained test family | Required observation |
| --- | --- | --- |
| Remote scope history | `invalidates $field change, revert=$revert, stage=$stage` | Account, container and prefix changes at picker, preflight and ready stages disarm, including same-turn return |
| Connection identity | `invalidates the same-bookmark reconnect at %s`; `refuses picking and Start throughout reconnect` | Equal visible fields cannot preserve an old report or admit a new picker during reconnect |
| Mode and open-state history | `invalidates mode changes, revert=$revert, stage=$stage`; `rechecks %s in Start before reactive invalidation` | Upload/download changes, mode return and close/reopen invalidate before rerender |
| Captured submission | `consumes and starts the exact scope, settings and opaque token once` | One call with the preflight's exact request, and no preview reuse after uncertain Start failure |
| Request forwarding | `forwards the complete captured %s request without rebuilding its scope` | All settings, token and resource bytes survive independently of current BrowserState fields |
| Obsolete replies | `only the newest attempt owns $stage $outcome and busy state`; `ignores an old successful preflight after the replacement report rejects` | Old picker/preflight success, failure and finalisation cannot override the current attempt |
| Closure | `ends the picker lifetime on %s close`; `ignores late preflight %s after closing`; `does not preflight an unmounted picker or arm a reopened dialog` | Close button, scrim, parent close/reopen and unmount retire pending work |
| Failed or empty selection | `cannot reuse the old report after picker %s`; `refuses an empty upload selection`; `refuses an empty directory token` | Cancellation, error, null/empty file lists and empty tokens never reuse a report or launch invalid preflight |
| Current report outcomes | `shows a current rejection without enabling Start`; `allows a current warning and displays the accepted job`; `keeps a failed preflight disarmed and allows a fresh attempt` | Rejection, warning, success and error/retry retain their intended behaviour |
| Duplicate and late work | `refuses a second picker while the current picker is pending`; `ignores a late Start %s after scope changes` | Synthetic duplicate clicks cannot open a second picker or retarget a pending submission |

All 110 cases pass after the change.
Against the preceding two production files, the same tests produce 100 assertion failures and ten passing controls, with no skips or setup failures.
Two of those failures exercise the new complete-request wrapper contract; the remaining 98 are mounted UI assertions.
The 86-case first run is an intermediate subset, not additional failures.

Thirteen independent scratch variants also load and fail their assertions without skips, setup errors or unhandled rejections:

| Deliberately broken behaviour | Failing cases |
| --- | ---: |
| Hide changes to the remote scope revision | 46 |
| Hide changes to the local dialog revision | 4 |
| Admit obsolete picker replies into preflight | 28 |
| Publish an obsolete successful report | 2 |
| Publish an obsolete error | 6 |
| Let obsolete finalisation clear the current busy state | 8 |
| Delay close invalidation until parent unmount | 8 |
| Remove the unmount guard | 2 |
| Remove Start's current-intent check | 10 |
| Retain a submitted report and remove the pending-Start guard | 4 |
| Admit empty picker tokens | 2 |
| Rebuild wrapper scope from current browser fields | 2 |
| Remove the handler's duplicate-picker guard | 8 |

The separate local revision is independently necessary: its four failures are the same-turn mode-return and close/reopen Start cases for both directions.
The final full frontend suite passes 274 tests across 13 files, with no failures or skips and 41.98% whole-source statement coverage.
The Svelte/TypeScript check has zero errors and warnings; the frontend production build and `go build ./...` pass.
The `p06-local-final.*` and `p06-local-controls/` artifacts record the final suite, paired baseline and each mutation under the task's temporary evidence directory.

This completes the defined frontend P06 request/lifetime work, including local picker admission.
It does not cancel an outstanding native picker, revoke its Go-side token, prove native picker serialisation or stop an already submitted job; those capabilities need their own authority and platform evidence.
The Go service scenario ledger is unchanged, and P07/P08/P11/P14/P18–P21/P23/P30 remain open as described above.
UI-01's backend authority requirement and the valuable-data release hold remain.
