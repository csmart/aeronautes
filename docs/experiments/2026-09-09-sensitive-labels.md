# Sensitive control labels

## Scope and oracle

The label portion of [UI-11](../audits/2026-09-05/ui-product.md) is checked against DESIGN §4.4 and §65.6 on base `a8ece3429c39468c3da8a4343bbfa7cf8226efce`.
Find query, ACL raw/add fields, audit filters, benchmark confirmation and segment-force confirmation lacked connected visible labels at this base.
Transfer destination controls already have accessible names; they remain unchanged and have a positive regression control.

A visible label must identify and activate the intended native control in its own component instance and current mode.
Activating it must preserve literal values, captured request identity, existing submission and public-ACL/typed-confirmation guards, and close lifetimes.
It must not start an operation.
The patch adds native `label`/`for` associations using Svelte's per-instance `$props.id()`, with small layout adjustments for the added Find and audit text.
It does not change request handlers, modal ownership, focus restoration, transfer logic or convenience shortcuts.

## Production component regressions

`sensitiveLabels.test.ts` mounts the production components and checks the native DOM `HTMLLabelElement.control` association and forwarded activation.
JSDOM does not model native label-click focus; these assertions do not claim otherwise.
The 15 cases cover literal Find submission and retired panels, repeated instances, distinct Read/Write ACL edits through Save in both modes, public-read acknowledgement, audit filter requests, exact benchmark/force arming and explicit submission, and the existing destination names and literal Preview request.
Repeated ACL components also switch modes and verify that the old input is disconnected while the current label follows the replacement.

All 15 cases pass on the candidate.
Replacing only the four production components with the unchanged parent versions yields 14 assertion failures and one passing destination control.
All 968 existing frontend test names remain present and passing: the complete run passes 983 tests without failures or skips.
Svelte check reports zero errors and zero warnings, and the native production build succeeds.
The 634 captured source/test paths remain unchanged throughout those producers; this experiment document was added afterwards.

The following deliberately broken associations were run only in private frontend copies, using the unchanged test file.
Each variant compiles and executes, fails named assertions, and retains the selected positive controls shown below.
Unselected cases are excluded from the passing-control counts.

| Broken association | Assertion failures | Passing selected controls |
| --- | ---: | ---: |
| Detached Find label | 3 | 1 |
| Shared Find ID | 1 | 3 |
| Detached ACL label | 5 | 1 |
| Shared ACL ID | 3 | 3 |
| Detached segment-force label | 2 | 1 |
| Shared segment-force ID | 1 | 2 |
| Crossed audit filter labels | 2 | 3 |
| Detached benchmark label | 2 | 3 |
| Shared drawer ID | 2 | 3 |

These variants produce 21 assertion failures with 20 passing selected controls.
They test the new associations and their instance boundaries; the existing dangerous-operation guards are preserved controls, not newly implemented authority.

## Separate native controls

The real Wails app was exercised on an isolated Linux X11 display at 1280 × 800, with a dedicated MockSwift endpoint, private application state and owned fixtures.
The app was not instrumented.
The candidate executable was built from the uncommitted label patch on the stated base and correctly carries a dirty-source identity.
Its SHA-256 is `4f8aa14c20b3941fb804326ad51df9e3d91917a30507cf2f1160513039250b8c`; all three generated frontend assets were verified as embedded in it.
The before executable is the preserved clean Preview build at `58c6a1f2d02aa6999603f334667e75af37df1ffd`, whose full source tree equals the merged base.
Its SHA-256 is `7a6c15ab90e317c189f87b4be17cea8894fa0163e90f1f03da8d4b29d94b3940`; its original source identity is retained.

The identical segment-force input sequence runs on both executables.
It first qualifies the private-display clipboard with a directly clicked input, clears the field, moves focus to the checkbox, clicks the visible confirmation instruction, types `wrong`, and copies the active selection.
On the before build the field remains empty and the copy contains dialog text: the label-focus oracle fails.
On the candidate it contains exactly `wrong`: the oracle passes.
The direct-input controls then accept `wrong` and `external-dlo` on both builds, with the wrong name disabled and the exact name armed in the retained screenshots.
Neither run activates deletion.

A separate deterministic candidate sequence repeats the initial native inspection across all changed forms.
Clicks on Find query, Read and Write ACL in both add/raw modes, benchmark confirmation, and segment-force confirmation direct keyboard input to the intended field; copied input values match exactly.
Find starts only on the later explicit Return and displays the matching `alpha.txt` result.
The audit operation label accepts the literal ` delete. ` input, and the Result label receives native select navigation to `failed`.
Wrong benchmark and force text stays disabled, while the exact account/object name only arms the action.
ACL changes are cancelled; benchmark, deletion and transfer actions are not submitted in these native controls.

Every completed native sequence has zero application Swift PUT/POST/DELETE requests after fixture creation.
Direct reads verify the six source/backing payloads, the DLO manifest reference and the empty destination remain unchanged.
Native exact mutation-request assertions remain component-test evidence; the native controls establish label activation, arming and absence of effects.

## Evidence and limits

The private evidence index is `parallel/sensitive_labels/verification.json`.
It links source hashes, complete test results and logs, parent and nine broken copies, native build inputs/assets, fixed command sequences, actual screenshots, copied values and wire/payload checks.
The initial native inspection is retained separately from the fixed sequences, including the expected before-build label failure.
No failed producer or control was discarded or reclassified as a successful behaviour oracle.

This is a bounded label-association correction.
The native runtime disables the accessibility bridge (`GTK_A11Y=none`); no screen-reader, accessibility-tree, other-platform, display-scaling or complete native select certification follows.
Broader UI-11/P29 semantics and shortcut work, and all existing valuable-data release gates, remain open.
