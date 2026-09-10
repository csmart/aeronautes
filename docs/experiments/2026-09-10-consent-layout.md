# Manifest consent layout at supported small windows

## Scope and oracle

This is the bounded safety part of UI-13 in [the product audit](../audits/2026-09-05/ui-product.md) and its [remediation row](../audits/2026-09-05/remediation-plan.md), which requires full names, confirmations, cancellation and errors to stay accessible at supported small windows.
The applicable contract is the [window and layout model](../DESIGN.md) in DESIGN §4.3 together with the destruction and force disclosure in §§3.4/34/35, the Phase 7 accessibility gate in §60 and the tracked programme in §65.10.
The supported native minimum is a 960×600 window.

The bounded question is only this: the ownership-consent label of `frontend/src/features/inspector/ManifestDeleteDialog.svelte` must keep its complete backing target inside the dialog, and the Cancel and Delete controls must remain visible while it does.
Everything else in UI-13 stays open, including resizable panes, responsive columns and large-namespace retention.

## Before evidence

The retained producer built a clean binary from the accepted contrast source and drove the real application against a private read-only mock.
It completed 159 commands and 61 raw screenshots with only GET and HEAD requests, and its complete remote snapshot was unchanged.

At 960×600 the long-target dialog measures 450×460 pixels at 255,70..704,529 in its default state and 450×318 at 255,141..704,458 in its force state.
The 560-character backing prefix in the consent label crosses the dialog's right edge and continues past the application edge in light and dark and in both states.
The escaping consent text is 10, 8, 11 and 8 exact foreground pixels in the light default, light force, dark default and dark force cases, reaching x=841 against a dialog right edge of x=704.
Counting every matching-colour pixel outside the dialog instead adds one unrelated application pixel at x115,y63 in the light theme, so the consent text itself is the subject of these counts.
The same defect appears at 1280×800 in the light default state, where the escaping text reaches x=1251.
The identical prefix wraps completely in the survival paragraph directly above the label, so the defect is specific to the label's intrinsic width.

The 841-byte DLO and SLO name cases are passing controls at 960×600 in both themes, including expanded consent.
The SLO default also shows its inline-data destruction warning; that dialog reaches the window edges while its title, full name, warning, checkbox and action controls stay visible.
Right-wheel input leaves the prefix region unchanged and Shift-wheel changes only the underlying inspector scrollbar.
Repeated zoom shortcuts left the sampled dialog pixels and the window size identical, so no zoom or scaling coverage is claimed.
Two screenshots named `dark` are the first system-to-light step and are explicitly light controls.
The first producer is retained as failed and incomplete: its final blind input sequence returned a previously copied name and it ended before its final remote snapshot.

## Candidate experiment

The wrapping-only candidate adds one rule to the same component: the consent label's text span receives `min-width: 0` and `overflow-wrap: anywhere`.
It was built from the same source as the before producer with only that rule applied.
The resulting production stylesheet is the before stylesheet plus exactly that one rule with every other rule byte-identical and in the same order, and the production script differs only by the component scope class on the consent span.
The candidate binary honestly reports a dirty source.

Four replays drove the real application against the same fixture and mock: 169, 26, 18 and 23 commands.
All four recorded only GET and HEAD requests and every complete remote snapshot equalled its baseline.
Dialog geometry was measured from the rendered pixels of the dialog's own border, not from a renderer or a stylesheet.
As a control on that measurement, the retained clean before binary was driven through the same harness and reproduced the recorded before box exactly at 255,70..704,529.

The wrapping question is answered yes.
No consent-label text lies beside the dialog in any observed candidate state.
The only matching-colour pixels outside the dialog in the 1280×800 capture are unrelated application content behind the scrim at x1059..1244 and y0..90.
Twelve consent dialogs whose labels are short measured exactly the same box as the before producer, and every one of their computed control points equalled the recorded before coordinate.

The obstruction question is also answered yes, in one state.
The long-target default dialog requires 650 pixels of height, measured with a complete border at 1280×800 in both themes.
At the supported 960×600 minimum the same dialog spans the full 600-pixel viewport: its top border is complete at y=0 and its title is visible, while its bottom border and both action buttons lie outside the viewport.
Wheel input does not recover those controls; it moves the consent checkbox out of view instead.
The force state measures 450×508 in both themes at both window sizes and keeps a complete border, both action buttons inside the dialog and working pointer cancellation.
Those buttons occupy y508..536 at 960×600 and y608..636 at 1280×800, a 29-row fill whose last row sits 17 pixels above the dialog's bottom border at y553 and y653.

One pre-existing condition bounds this result.
The long-name SLO default dialog already needs 602 pixels, measured with a complete border in a 960×800 window; at 960×600 the same dialog shows a clipped 591-pixel extent with no bottom border.
The absent viewport bound is therefore an existing defect that wrapping exposes in a second case rather than one that wrapping creates.

## Observed matrix and remaining acceptance work

The candidate replays observed these cells, and no others.
At 960×600: light default, light force, dark default and dark force; pointer cancellation in the dark force state; keyboard dismissal in the light force state; and the default state after wheel input.
At 1280×800: light default, light force, dark default and dark force; pointer cancellation in the light force state; and keyboard dismissal in the dark state.
The light force state at 960×600 comes from the second replay, because the first replay skipped its checkbox click after wheel input moved the checkbox out of view; that skip is retained rather than rewritten.

Three cells are not yet observed and are required acceptance work for this implementation: the refused-operation error state, keyboard activation of Cancel, and both named supported window sizes for every state above.
The harness deliberately refuses Return, Enter and space so a confirmation cannot be activated, so keyboard activation of Cancel needs a separate arrangement that still cannot reach the destructive action.
Zoom and scaling, forced colours, assistive technology and non-Linux platforms are deferred gates rather than acceptance work for this slice, exactly as the sequence in DESIGN leaves them.

## Proposed bounded correction

Wrapping alone is necessary and not sufficient, so the correction is wrapping together with a bounded dialog height.

1. Review this plan and its measured before and candidate numbers before changing production styles.
2. Apply the consent label wrapping rule and bound the dialog to the viewport.
   Keep the title and the action row outside the scrolled region so they stay visible at 960×600, and scroll the disclosure body, the consent control, the confirmation input and the error paragraph within the bound.
   Preserve the native checkbox, the complete literal target text, the associated input, all script and markup authority, the existing tokens, typography and focus treatment.
3. Record the maintained component and native evidence below, obtain independent review of the implementation, and deliver only the reviewed current-base source through all five protected checks.

## Maintained oracle

The frontend component environment is jsdom, which reports zero-size bounding rectangles and empty client rectangles.
A rectangle comparison or a mocked layout in that environment cannot detect this regression, so no layout assertion may rest on it.
The synthetic component check is therefore limited to content and interaction contracts: the label renders the complete literal target, the checkbox toggles the force state, the confirmation input arms the danger action only on an exact match, Cancel closes the dialog and a refused operation renders its complete message.

The maintained layout regression is the native replay on a clean binary.
It locates the dialog by its rendered border and must identify the actual Cancel and Delete controls, not a nearby element.
Both control renderings are required: the enabled danger action is the exact danger token fill, observed as a 146×29 block at the bottom right of the default dialog, and the disabled action is that token composited at 60% over the pane, observed as rgb(207,138,125) in light and rgb(144,83,70) in dark at y508..536 of the 508-pixel force dialog.
A check that matches only the exact danger token finds the confirmation input's border instead, which in the candidate force state is a 404-pixel-wide rectangle at y471..495 directly above the buttons; treating that as the action row would let a footer regression pass.

Every native case must show the complete dialog border inside the viewport, no consent text beside the dialog, and both action buttons visible without scrolling.
The same case must reach the end of the consent text, the confirmation input and any error by scrolling and by keyboard focus, with the title and the action row still visible while it does, and must show that pointer and keyboard cancellation close the dialog.
Two deliberately broken controls are required and must fail independently: removing the wrapping rule alone must fail the containment check, and removing the height bound alone must fail the control-visibility check.

## Failure state

The bounded height and scrolling change affects the whole dialog, including the error paragraph that appears after a refused operation, so that state belongs in this proof.
The existing mutation-dialog tests cover short failure text and enabled retry in the synthetic environment only; they establish neither visible errors nor visible controls once a long error increases the content height.

Add one bounded rendered case at 960×600 with long literal content and a refused operation.
Drive it with an owned fixture, or a component service double, that refuses before any data effect, and require the complete remote snapshot to be unchanged with the refused attempt as the only non-read request.
The case must reach the complete error text and the confirmation input by scrolling and keyboard focus while the title and the action row stay visible, and must preserve both pointer and keyboard cancellation.
Include a combined long object name and long backing target, because the retained fixtures exercise those two long values separately.
This stays within the one changed dialog and needs none of the wider UI-13 work.

## Required proof

The full frontend suite, the type check and the production build are required before independent review.
Retain the existing mutation-dialog, keyboard-scope and sensitive-label suites unchanged as authority and lifetime controls.
Preserve every failed producer, and keep separately instrumented renderer evidence labelled separately from the actual application.

## Implementation and maintained evidence

The correction adds the consent label wrapping rule, bounds the dialog to the viewport and moves the disclosure body, the consent control, the confirmation input and any error into a scrolled region, so the title and the action row stay outside it.

The frontend checks reconcile against the delivered inventory.
The suite grows from 1,125 to 1,130 tests with no name lost and no failure, and the five added names all belong to the new consent-layout file.
The type check reports 258 files with no error or warning, and the production build succeeds.
Those checks cover content, interaction and arrangement only, because that environment reports no layout geometry.

## Maintained native regression

The layout regression is archived verbatim and is intentionally outside the normal passing test suite.

| Archived source | SHA-256 |
| --- | --- |
| [Driver](probes/2026-09-10-consent-layout-driver.py.txt) | `086f40345e0fd7797988048b564427d0a9b6458297e81143add23df0c9fff45f` |
| [Pixel oracle](probes/2026-09-10-consent-layout-oracle.py.txt) | `a266fc2f1d4fc1c92110c448f51b044d6dad0fc186ca1d0477a03f11eec381ce` |
| [Regression sequence](probes/2026-09-10-consent-layout-regression.json.txt) | `442a21b65697aefc6dcc844b18e7a107cd45cb6bd356fcc34c24fe240ddaa697` |
| [Keyboard and refusal journey](probes/2026-09-10-consent-layout-journey.json.txt) | `ae85c77911c2913dea2eb1e07add10bb3a3090a68ad31abf6ddf005240e537e5` |
| [Fixture entrypoint](probes/2026-09-10-consent-layout-fixture.go.txt) | `cb76ac4d317d39f1bbb6f42b48130c3059ee58e70f2c79fe309f951c16385405` |
| [Fixture data](probes/2026-09-10-consent-layout-fixture.json.txt) | `dbabe8590779de01ecdfa3731c57e549d87a79388a4becd96d7cc94954d4aa7d` |

The driver takes a run label, the application binary, a sequence and an optional fixture, and it exits non-zero when any assertion fails.
It needs a headless X server, xdotool, the GTK and WebKit runtime the application links, and the fixture binary built from the archived entrypoint against this module.
The fixture answers every request that is not a GET or a HEAD with 405 before reaching its data handler, so a refused attempt cannot change anything.

The driver measures the rendered dialog from its own border and asserts five properties at each checkpoint: the border is complete, the dialog lies inside the viewport, both action controls are visible, the body does not scroll sideways and no consent text lies beside the dialog.
It identifies the actual Cancel and Delete controls in both renderings, so the confirmation input above them cannot be mistaken for the action row.
It also asserts behaviour rather than inferring it: keyboard focus reaches a named control only when that control carries the focus ring, keyboard scrolling counts only when the body moves by more than a blinking caret, and the confirmation field is proved by the danger action becoming armed after typing.

## Observed implementation results

The native evidence comes from a clean binary built at the correction commit, which reports that revision and a clean source.
Eight replays ran against the private fixtures with 59, 38, 29, 41 and 8 commands for the implementation and 8 for each broken control.
Every request was a GET or a HEAD except one refused DELETE in each of the two refusal replays, and every complete remote snapshot equalled its baseline.
The four replays on the original fixture compare 21 entries each and the three on the combined fixture compare 23, because that fixture adds one object; each set is internally equal before, after and at shutdown.

At 960×600 the long-target default dialog now measures 450×586 with a complete border in both themes, where the unbounded wrapped dialog needed 650.
Its force state measures 450×508.
At 1280×800 the default measures 450×650 and the force state 450×508, both with complete borders.
The oracle locates the actual Cancel and Delete controls inside the viewport in every one of those states, and no consent text lies beside the dialog.
Pointer cancellation closes the dialog at both sizes.

The keyboard journey runs at 960×600 on an object whose name is 831 characters and whose backing target is 584.
Keyboard focus reaches Cancel, and the recorded ring positions give the order: the Delete action, then the scrolled body, then Cancel.
Page Down on that body stop scrolls to the end of the literal target while the title and the action row do not move by a single pixel.
Two further tab stops later, typing the complete object name arms the danger action, which proves the confirmation field takes keyboard focus and input.
Activating that armed action produced exactly one DELETE, which the fixture refused with 405, and the complete remote snapshot was unchanged.
In the resulting error state the whole body is traversable from the keyboard in both directions, with the title and action row still fixed, and activating Cancel from the keyboard closes the dialog.
All seven layout checkpoints in that journey passed.

## Independently broken controls

Both deliberately broken controls fail the archived regression, independently and for different reasons.
Removing the wrapping rule alone leaves the border and controls intact but makes the body scroll sideways, cutting the literal target instead of wrapping it, and the run fails on the horizontal-overflow assertion.
Removing the viewport bound alone produces no horizontal overflow but fails three assertions at once: the border is incomplete, the dialog is not inside the viewport, and the action controls are not visible.
The same regression passes on the corrected build.
The original before capture fails the border assertion, because the escaping label overwrites the dialog's right border.

## Retained incomplete observations

The first replay's keyboard-cancel attempt did not close the dialog: its capture still shows the complete 450×586 border.
That attempt pressed Return after a fixed number of tabs rather than after confirming the focus ring, and it is retained as incomplete.
The later replay that finds Cancel by its ring does close the dialog, and it is the evidence for that contract.

Because that first attempt left the dialog open, a following click intended for the theme control closed the dialog instead, so only one theme step was applied.
The later filenames of that replay are therefore unreliable: its captures named dark at 960×600 and at 1280×800 are light, and the three named light at 1280×800 are dark.
The measured theme in each record is authoritative, and both themes are covered at both sizes by the replays taken together.

## Remaining limits

No zoom or scaling coverage is claimed, and no forced-colour, assistive-technology or non-Linux platform observation is made.
The wider UI-13 layout work stays open: named breakpoints, resizable and collapsible panes, responsive working-list columns and bounded large-namespace retention.
This bounded correction does not close UI-13, the accepted responsive-layout gate or the valuable-data hold.
