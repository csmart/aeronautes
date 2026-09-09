# Preview focus after asynchronous planning

This bounded UI-11/P29 follow-up restores the initiating Preview control after asynchronous planning discards its focus.
The oracle is the operator-approved focus contract, [UI-11](../audits/2026-09-05/ui-product.md#ui-11--p2-keyboard-and-dialog-semantics-are-incomplete-and-interfere), [DESIGN §4.4](../DESIGN.md#44-keyboard-model), and the existing captured-request, stale-preview and close-lifetime contracts.
Only the same Preview button that owned focus before this request disabled it may regain focus.
Deliberate keyboard or pointer activity, another focus owner, leaving the window, invalidation, closure or unmount permanently retires that claim.
Restoration must neither focus nor activate Start, affect another request or dialog, nor reverse deliberate navigation even if focus later becomes empty.

## Change and observed ordering

[TransferDialog.svelte](../../frontend/src/features/transfer/TransferDialog.svelte) captures the focused Preview, active modal and frozen request intent before planning starts.
It records navigation for that request and removes those listeners when the claim is cancelled or completed.
Immediately before clearing the busy state, it checks that the original button is still disabled and focus is on the document body.
After Svelte renders re-enabling, it rechecks cancellation, current request and modal ownership, connection, enabled state and the empty focus target before focusing that original button.
The existing invalidation and close paths cancel the claim synchronously.
Copy and comparison-only Mirror use the same path; their request, Start and stale-result semantics remain intact.
LocalTransferDialog, the shared focus action and the native smoke driver are unchanged.

The first candidate sampled focus at the first Svelte tick.
That candidate passed 47 component cases and all 960 frontend tests, but failed the actual native restoration control.
A temporary noninteractive trace showed WebKit retaining focused Preview through the first tick and first animation frame, then emitting focusout while Preview was still disabled.
At request completion focus was on the body, but the earlier sample had already recorded no loss.
The final implementation samples before re-enabling instead, without adding a timeout or changing modal ownership.
The diagnostic instrumentation is excluded from the final production build.

## Production-component regressions

[previewFocus.test.ts](../../frontend/src/features/transfer/previewFocus.test.ts) mounts the actual TransferDialog and BrowserState against a recording fake API with deferred replies, using the production focus action.
The 51 cases cover current success and failure, synchronous and deferred focus loss, retained native focus, an initially unfocused Preview, navigation followed by empty focus, window changes, close and unmount, change-and-return intent histories, old/new reply ordering, another modal, and retirement during the re-enable render.
They also verify that restoration causes no Start call and that a later explicit Start submits the captured Copy request only once.
These are UI-to-bridge regressions, with no Go service-scenario credit.

JSDOM has no layout engine and retains focus when a button is disabled; its blur method also refuses a disabled button.
The tests explicitly provide visible-control layout and a per-button platform adapter that exercises real DOM focus events around disablement.
The deferred-loss case temporarily lifts JSDOM's disabled-blur limitation to model the observed later focus loss.
Neither document.activeElement nor the production focus action is mocked.
These component results remain separate from the native evidence below.

## Parent and broken controls

All 51 maintained cases were replayed with parent production source `759848149aedd4aaefc2bf623f34af8fc7f78a81`.
The parent produced 15 intended assertion failures and 36 passing controls.
Replaying the first candidate against the new delayed-loss cases produced four assertion failures, with two retained-focus controls passing.

Seven deliberately broken variants produced 13 intended assertion failures and 14 passing retained-focus controls.

| Removed or broken guard | Failing cases |
| --- | ---: |
| Initially focused Preview ownership | 2 |
| Permanent navigation cancellation | 2 |
| Focus loss while disabled, before re-enabling | 2 |
| Click navigation history | 2 |
| Window-away cancellation | 2 |
| Invalidation cancellation and final current-intent check, at re-enabling | 1 |
| Original Preview target replaced with Start | 2 |

The lifetime variant removes two redundant guards together; it is not evidence that either check alone is necessary.
The earlier scope-change selection still passed on that variant because the button was already re-enabled when the old request finished.
The re-enable boundary case catches the same broken guards while the disabled-state condition still holds.
Every accepted negative result is an assertion failure, with exact source, test selection, exit status and source hashes preserved.
An earlier unmatched filter selected no target cases and is excluded from the negative-control count.

## Actual native before and after

The parent and final app were exercised with real X11 keyboard input on the existing Linux Wails/WebKit runtime, isolated displays, private application state and owned mock ports.
Each fixture used only three generated objects totalling 53 bytes and pinned rclone 1.74.3.
The parent binary came from the verified keyboard delivery and has the same source tree as merged parent `759848149aedd4aaefc2bf623f34af8fc7f78a81`.
The final noninstrumented app was rebuilt with Go 1.27.1, and all generated frontend assets were verified byte-for-byte inside its executable.

| Control | Parent | Final app |
| --- | --- | --- |
| Pause the owned mock, activate focused Preview, complete planning, then press Return without navigation | One Preview request; focus restoration fails | Two Preview requests; original Preview receives Return |
| Tab during planning, then Return after completion | One Preview request | One Preview request |
| Focus an owned separate window and return during planning, then press Return | One Preview request | One Preview request |
| Escape during planning, reopen the dialog, complete the old request, then press Return | One Preview request | One Preview request |

Each row verified zero destination object PUTs, an empty destination and unchanged source payloads.
The first candidate also failed the unchanged native restoration control and is retained separately from the passing final app.
A further final-app run repeated restored Preview with zero writes, then explicitly clicked Start copy and verified all three exact destination payloads, unchanged source payloads, three staged creations, three final creations and matching staging cleanup.
Its first driver attempt had already verified the payloads but wrongly expected three total PUTs rather than the three staged plus three final writes; that failed producer is preserved and excluded from the passing result.

The parent executable SHA-256 is `e8a6ae9b7411a227a6778979be7047924dfde434afb8ea28bd10b8456266148b`.
The first candidate executable SHA-256 is `8d27b5953a0931791f514775dedbb4b3499f11bec2e73f7d08c87486e6bc0b65`.
The final executable SHA-256 is `f3de1bc789d1c2be8465a4501824850722a2034e704203d199d3470ea4dce69a`.
Private evidence retains each captured driver, source and binary identity, mock trace, screenshots, result and failed producer, including the initial missing-Wails-CLI build failure.

## Validation and limits

On Node 22.23.1 and npm 10.9.8, all 51 focused cases and all 964 frontend tests pass without skips, including the existing keyboard, transfer-lifetime and local-transfer suites.
Svelte check reports zero errors and zero warnings, the production frontend and native builds pass, and diff checks are clean.
Independent local review, current-base verification and all five required PR and resulting master checks remain delivery gates.

The native rows establish this bounded Linux Preview behaviour and owned-fixture Copy control.
They do not certify screen readers, OS select popups, other operating systems, general accessibility or valuable-data use.
UI-11/P29 and the valuable-data release hold remain open.
