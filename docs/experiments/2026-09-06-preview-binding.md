# Preview intent binding

Date: 2026-09-06.
Scope: the first P06 implementation, Copy/Mirror comparison and container/prefix Delete dialogs.
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

## Verification and limits

The full frontend suite passes 164 tests across 12 files, with no failures or skips and 37.09% whole-source statement coverage.
Svelte/TypeScript check reports zero errors and warnings; the frontend production build and `go build ./...` pass.
No Go source or bridge model changed, so the existing Go scenario ledger is not enlarged and the Go runtime suite is left to the required PR checks.
The local runtime artifacts are the `p06-copy-delete-verified.*`, `p06-preview-controls/`, `p06-adapted-before.*` and `p06-adapted-after.*` records under the task's temporary evidence directory.

The local picker/preflight half of P06 remains the next implementation commit.
This frontend protection does not establish a backend one-shot plan, replay/expiry refusal or unchanged remote data; P11 and P14 remain open.
Bookmark lifecycle and inspector/manifest deletion are P07; paging/Find are P08; destination-name normalisation is P23.
Local filesystem and native-picker obligations remain P18–P21/P30, and these mounted tests establish no native accessibility or platform picker behaviour.
The valuable-data release hold remains.
