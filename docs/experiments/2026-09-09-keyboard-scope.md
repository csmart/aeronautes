# Keyboard and modal ownership

This is the bounded UI-11/P29 keyboard and focus correction.
The oracle is [UI-11](../audits/2026-09-05/ui-product.md#ui-11--p2-keyboard-and-dialog-semantics-are-incomplete-and-interfere), [DESIGN §4.4](../DESIGN.md#44-keyboard-model), and the existing captured-target and close-lifetime contracts in §65.9 item 12 and P07.
Keyboard activity in a native control or active dialog must not select or navigate a background resource.
A consumed event belongs to its existing handler, and Escape ends only the active dialog's lifetime.

## Change

The browser's global handler now respects `defaultPrevented`, native selects, and the modal registered by the existing `trapFocus` action.
Inputs, textareas and contenteditable targets keep their existing editing behaviour.
The working list also respects consumed events and lets the existing Alt+Up parent shortcut reach the browser.

Every dialog supplies its own close callback to the shared action, including inspector-owned manifest deletion.
Mount order determines the active dialog.
Only that dialog handles Escape or Tab, and Escape calls the component's existing lifetime-invalidating close function.
An unconsumed Escape targeted at the document body takes the same close path, including while all dialog controls are disabled.
Background key events are refused before list handlers can change selection; focus returns inside the active dialog.
The action makes siblings along the active dialog's ancestry inert, including newly added background nodes, and restores their prior inert values on closure.
An older dialog's destruction cannot restore focus over its replacement.
A missing or unavailable opener falls back to the remaining dialog.

Tab boundaries exclude disabled controls, disabled-fieldset descendants and controls inside closed disclosures, while including the disclosure summary.
An all-disabled dialog remains a focusable fallback.
The bookmark and create forms have labelled dialog wrappers, and all dialogs declare `aria-modal`.
The existing busy-operation semantics remain: Escape may close the UI while an already submitted operation finishes, and its retired callbacks cannot alter a replacement view.

## Production-component matrix

[keyboardScope.test.ts](../../frontend/src/features/browser/keyboardScope.test.ts) mounts the actual BrowserScreen, BrowserState, Inspector, VirtualList and dialog components against the existing recording fake API.
The suite uses deferred bridge replies for deletion, bookmark save and container creation.
These are the separate UI-to-bridge tests required by §65.10; they earn no Go service-scenario credit.

| Cases | Maintained test or family | Required result |
| --- | --- | --- |
| 3 | `leaves arrows with the native … select` | Jobs budget/filter and container versioning keep focus and selection ownership. |
| 3 | `leaves native editing keys with …` | Inputs, textareas and contenteditable descendants retain editing keys. |
| 4 | `respects a control that consumed …` | Consumed arrows, slash and Backspace cause no global action. |
| 3 | `keeps normal list selection…`, `lets Alt+Up…`, `does not let the working list reuse…` | Normal navigation still works; local and global handlers honour event ownership. |
| 7 | `isolates and dismisses the … dialog…` | Bookmark, creation, Copy, Delete, upload, download and credential dialogs own focus and close through their callback. |
| 2 | `keeps manifest intent…`, `lets a dialog control consume Escape…` | Repeated keys and background focus cannot change the manifest target; a child may consume Escape. |
| 2 | `routes body Escape…`, `does not reuse a consumed body Escape…` | Body-targeted Escape reaches the busy dialog; a consumed event does not dismiss it. |
| 2 | `Escape while deletion is busy…` | All-disabled focus fallback and late success/failure preserve a reopened dialog. |
| 3 | `only closes the top…`, `does not restore a removed lower opener…`, `falls back to the remaining dialog…` | Nested dismissal, out-of-order unmount and removed opener preserve the current owner. |
| 1 | `isolates later background nodes…` | Dynamic background nodes become inert; existing inert attributes are restored exactly. |
| 1 | `does not cycle into fields disabled…` | A pending bookmark save cannot make disabled-fieldset children tab targets. |
| 1 | `admits one form submission…` | Duplicate form submission starts once; Escape retires subsequent navigation. |
| 1 | `cycles Tab over visible enabled controls…` | Closed details and disabled actions cannot become a boundary; opening details exposes its controls. |

## Reproduction and broken controls

The final maintained tests were replayed against the production source at parent `33503e0e3c35704731dc86d4d5b1a9338846286d` in a scratch copy.
The selected 19 cases produced 14 assertion failures and five passing controls.
Those failures cover the three native selects, four consumed global keys, consumed list arrows, manifest focus/intent escape, consumed Escape, two all-disabled busy-dialog focus cases, nested isolation and closed-details Tab boundaries.
The input, textarea, contenteditable and normal navigation controls, including Alt+Up, passed on the parent.
This selected replay does not claim that every new case was run against the parent.

A compatibility check also caught Alt+Up being swallowed after adding the global consumed-event guard but before updating VirtualList.
Its assertion failed on the unchanged prefix, and passed after the list deferred that existing shortcut to the browser.

A further probe caught background capture swallowing a body-targeted Escape in the unpublished draft.
The final maintained body-Escape tests reproduced one assertion failure against that preserved draft, with the consumed-Escape and normal-navigation controls passing.
The correction routes an unconsumed Escape to the active close callback before refusing other background keys.

Sixteen independently broken source variants were exercised against the final tests.
Each failed its intended assertion and retained a passing normal-navigation control: 21 failing assertions and 16 passing controls in total.

| Removed or broken guard | Failing cases |
| --- | ---: |
| Body-Escape routing to the active dialog | 1 |
| Working-list consumed-event check | 1 |
| Working-list Alt+Up delegation | 1 |
| Native-select exclusion | 3 |
| Global consumed-event check | 4 |
| Global modal guard | 1 |
| Background key capture | 1 |
| Background focus containment | 1 |
| Inert background assignment | 1 |
| Top-dialog Escape ownership | 1 |
| Consumed Escape check | 1 |
| Closed-details exclusion | 1 |
| Disabled-fieldset exclusion | 1 |
| Opener availability check | 1 |
| Inactive-dialog return-focus guard | 1 |
| Modal unregister on destruction | 1 |

The final replay archived each exact source replacement, selected test names, report and exit code, plus SHA-256 hashes of all 64 frontend source files.
All negative-control failures were assertions, with no setup failures accepted as evidence.
The historical audit files and probes remain unchanged.

## Validation and limits

On Node 22.23.1 and npm 10.9.8, all 33 focused cases and all 913 frontend tests pass, with no skips.
`npm run check` reports zero errors and zero warnings, `npm run build` passes, and `git diff --check` is clean.
The existing 880 tests remain in the full run.
Independent review and all five required PR and subsequent master checks remain delivery gates.

JSDOM has no layout engine or native keyboard default actions.
The tests provide an explicit `offsetParent` layout stand-in and the missing native contenteditable accessor, then exercise the production focus code.
They check untouched select events, real DOM focus calls, inert attributes, Tab boundary handling and form submission callbacks; they do not certify OS select popups, native Tab traversal or Enter activation.
The production-component matrix exercises no native Wails, NVDA, VoiceOver, Orca, platform picker, screen-reader background isolation or valuable-data transfer.
UI-11/P29 remains open for native accessibility, remaining sensitive labels and additional accepted shortcuts; contrast and broader layout work also remain open.
The valuable-data release hold is unchanged.

## Native smoke driver correction

[PR 72 run 34346488957](https://github.com/csmart/aeronautes/actions/runs/34346488957) failed its smoke job after two completed Copy previews without an engine start.
Go, frontend and real-Swift integration passed in that run.
The failed log and artifact 10102004148 were preserved before investigation; its screenshots show the completed preview followed by focus on the Copy tab.
Artifact SHA-256 is `83aa0dd291235e070a02e4e18e9cd024e63a99a215bc4eca309c70c1a392fe3d`.

The smoke driver assumed Preview retained focus while planning temporarily disabled its button.
Native WebKit can drop that focus, so the next Tab returns to the first dialog control and Enter activates Copy instead of Start copy.
The modal ownership guard correctly keeps those keys inside the dialog.
[scripts/smoke.sh](../../scripts/smoke.sh) now clicks Start copy in its existing fixed 1280×800 viewport.
The retry count, screenshots, engine-PUT proof and server-side three-object completion assertion remain unchanged.
No application code changes in this driver correction.

The actual production app at `ef756a0ab3851b234539b66f3d6409c425279985`, built with Go 1.27.1 and its unchanged frontend, was exercised on the existing Linux host with pinned rclone 1.74.3, an isolated X display, private state and an owned mock port.
The original sequence passed without an induced delay, confirming that one passing run did not exclude the failure.
Pausing only the owned mock for 0.5 seconds during Preview captured the disabled Planning state and reproduced both failed start attempts with the original Tab/Enter sequence.
A direct click passed with the same delay: all three destination payloads matched their fixtures, all three source payloads remained unchanged, and the total was 53 bytes.
The actual edited smoke script then passed end to end with the same production binary and pinned engine, including its engine-PUT and three-object completion checks.
Shell syntax and diff checks passed, and the complete frontend tree remained unchanged from the approved keyboard commit.
An attempted Copy-tab click followed by Shift+Tab also failed and was rejected.
Runtime setup failures were preserved separately and count as no application evidence.

This is narrow native smoke evidence for the driver repair.
It does not certify native accessibility, select popups, screen readers or valuable-data use.
