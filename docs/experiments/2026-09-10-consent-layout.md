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

The regression is archived verbatim and is intentionally outside the normal passing test suite.
It needs nothing from the author's machine: the pixel oracle sits beside it, the screen capture is inlined, no reference image is used, and every path is an argument.

| Archived source | SHA-256 |
| --- | --- |
| [Regression driver](probes/2026-09-10-consent-layout-driver.py.txt) | `1b86e798c6164a7bbd356e6f456b741c559db7fcfbedb7416b9b466a6f3f0d41` |
| [Pixel oracle](probes/2026-09-10-consent-layout-oracle.py.txt) | `652816234dbb47ad86a5756d5b6a0876374744001fed15febcf80455d3a08c9f` |
| [Gate sequence](probes/2026-09-10-consent-layout-regression.json.txt) | `442a21b65697aefc6dcc844b18e7a107cd45cb6bd356fcc34c24fe240ddaa697` |
| [Journey sequence](probes/2026-09-10-consent-layout-journey.json.txt) | `fc8c3677f0eceeb421009a9cf9087f7668fd024dc44baa21bf8388d6cc981f2a` |
| [Fixture entrypoint](probes/2026-09-10-consent-layout-fixture.go.txt) | `d9a572d709538812a5140da077d2c3c104d140aa9e5153b8c2a3fab69b06acd6` |
| [Fixture data](probes/2026-09-10-consent-layout-fixture.json.txt) | `dbabe8590779de01ecdfa3731c57e549d87a79388a4becd96d7cc94954d4aa7d` |

Copy the driver and the oracle into one directory, naming the oracle `geometry.py`.
Build the application under test with `make build`.
Build the fixture by copying the entrypoint into a package directory of this module and running `go build -o mockswift ./<that directory>`.
Supply a runtime root that provides `usr/bin/Xvfb`, `usr/bin/xdotool` and the `usr/lib64` the application links.

```
python3 consent_layout_regression.py --self-check

python3 consent_layout_regression.py \
  --label native-gate --app bin/aeronautes \
  --fixture-binary mockswift --fixture 2026-09-10-consent-layout-fixture.json.txt \
  --sequence 2026-09-10-consent-layout-regression.json.txt \
  --runtime <runtime root> --out <output directory>

python3 consent_layout_regression.py \
  --label native-journey --app bin/aeronautes \
  --fixture-binary mockswift --fixture 2026-09-10-consent-layout-fixture.json.txt \
  --sequence 2026-09-10-consent-layout-journey.json.txt \
  --runtime <runtime root> --out <output directory> \
  --refusal "refused by the provider with reference $(python3 -c "print('z'*360)") END-OF-REFUSAL"
```

That exact phrase is what the recorded evidence used: 414 characters, of which 360 are one unbroken token.

The fixture answers every request that is not a GET or a HEAD with 405 before reaching its data handler, so a refused attempt cannot change anything.
When a refusal phrase is given it writes that phrase on the status line, because the client builds its error text from the reason phrase; the response body is discarded by the existing normalisation, so a long body would not reach the dialog.
That is a fixture behaviour and needs no change to the error contract.

Each checkpoint asserts five properties measured from the rendered dialog border: the border is complete, the dialog lies inside the viewport, both action controls are visible, the body does not scroll sideways, and no consent text lies beside the dialog.
The driver identifies the actual Cancel and Delete controls in their armed and disabled renderings, so the confirmation input above them cannot be mistaken for the action row.
Behaviour is asserted rather than inferred: keyboard focus reaches a named button only when that button carries the focus ring, the consent control is proved by activation changing its state while neither button holds the ring, keyboard scrolling counts only when the body moves further than a blinking caret, the confirmation field is proved by the danger action becoming armed, the error endpoint requires the paragraph to show the end of its own text, measured from grouped text lines with the right gutter excluded because the body's scrollbar overlaps the paragraph there, so a complete message ends with a short last line and its own bottom padding inside a background that stops above the action row, with declared minimum height and line count, and dismissal requires the modal to be gone, the application to be undimmed and the underlying delete control to be present again.

The process exits non-zero on any failed assertion, on an incomplete sequence, on a failed final data check and when that final check cannot run at all.
`--self-check` proves those contracts without a display or a Swift call: seven cases for the data verdict and four for the exit orchestration, including a completed sequence whose final verification never ran.

## Observed implementation results

The application under test is a clean build at the correction commit, which reports that revision and a clean source.
The four broken controls are the same source with one declaration removed or changed, and each honestly reports a dirty source.

| Run | Build | Declared | Executed | Snapshots | Refused deletes | Result |
| --- | --- | --- | --- | --- | --- | --- |
| gate-good | correction | 8 | 8 | before, after, final | 0 | pass |
| gate-wrapping | wrapping rule removed | 8 | 7 | before, final | 0 | fail: horizontal overflow |
| gate-bound | viewport bound removed | 8 | 7 | before, final | 0 | fail: border, viewport and controls |
| gate-clip | body scrolling removed | 8 | 8 | before, after, final | 0 | pass |
| journey-good | correction | 30 | 30 | before, after, final | 2 | pass |
| journey-clip | body scrolling removed | 30 | 13 | before, final | 0 | fail: no keyboard stop scrolls the body |
| journey-truncate | error paragraph truncated | 30 | 20 | before, final | 1 | fail: the error endpoint is too short |
| endpoint counterexample | correction capture, clipped in memory | n/a | n/a | n/a | n/a | fail: the tail is not the paragraph's own end |
| journey-nodismiss | correction, Cancel activation removed | 29 | 22 | before, final | 1 | fail: the modal is still open |

Every run compared 23 snapshot entries, all equal.
The three runs that complete record a normal after snapshot as well; the five expected failures stop before that step and record the before and shutdown snapshots only.
Only the runs that reach a refusal record a write, and every recorded write is a refused DELETE.

The gate opens the long-target dialog at 960×600 and asserts the five layout properties.
The journey drives the combined object, whose name is 831 characters and whose backing target is 584, and refuses with a 414-character reason phrase containing an unbroken 360-character token.
Its first cycle scrolls the bounded body to the end from the keyboard, reaches and activates the consent control from the keyboard, arms the danger action by typing the complete object name, activates it, reaches the resulting error from the keyboard and dismisses the dialog with the keyboard.
Its second cycle reopens the dialog, refuses a manifest-only deletion and dismisses the dialog with the pointer, so both cancellation methods are proved in the error state.
Throughout, the fixed title and action row do not change by a single pixel while the body scrolls.
At every error endpoint the rendered paragraph measures 168 pixels tall and holds eight text lines, the last covering 15 per cent of the paragraph width, with eleven rows of its own padding below it and its background stopping above the action row.

The measured geometry is unchanged from the earlier submission: 450×586 at 960×600 in both themes where the unbounded wrapped dialog needed 650, 450×508 in the force state, and 450×650 and 450×508 at 1280×800.

## Independently broken controls

Each control removes one part of the correction and fails a different assertion.
Removing the wrapping rule leaves the border and controls intact but makes the body scroll sideways, cutting the literal target, which the gate detects as horizontal overflow.
Removing the viewport bound loses the bottom border, puts the dialog outside the viewport and hides both action controls.
Removing the body's scrolling passes the gate, because the static default state still looks correct, and fails the journey: no keyboard stop scrolls the body, so the end of the target and the error are unreachable.
Truncating the error paragraph keeps the layout and the scrolling, reaches the endpoint check and fails it, because the rendered error is far shorter than the message it must show.
A minimum height alone would still accept a paragraph clipped just above that floor, so the endpoint also requires the end of the paragraph's own text.
Two pixel constructions test that, both built in memory from the passing capture with no retained file altered.
Keeping only the first 120 rows of the error paragraph leaves a background that still stops above the action row, and the endpoint rejects it because the last text line then covers 98 per cent of the paragraph width instead of ending short.
Keeping the first 125 rows while preserving the scrollbar column rejects for the same reason, which is the case a scanline measurement would have accepted, because the scrollbar sits in the excluded gutter and no longer stands in for a line of text.
Removing the Cancel activation from the journey leaves every earlier checkpoint passing and fails only the dismissal assertion.
Together they separate a broken layout, unreachable content, cut content and a dialog that will not close.

## Retained incomplete observations

Earlier exploratory runs are retained under the same evidence root and are not part of this maintained evidence, because earlier revisions of the harness produced them.
Two of their observations are preserved deliberately.

The first replay's keyboard-cancel attempt did not close the dialog: its capture still shows the complete 450×586 border.
That attempt pressed Return after a fixed number of tabs rather than after confirming the focus ring, and it is retained as incomplete.
Because it left the dialog open, a following click intended for the theme control closed the dialog instead, so only one theme step was applied.
The later filenames of that replay are therefore unreliable: its captures named dark at 960×600 and at 1280×800 are light, and the three named light at 1280×800 are dark.
The measured theme in each record is authoritative, and both themes are covered at both sizes by the replays taken together.

## Remaining limits

No zoom or scaling coverage is claimed, and no forced-colour, assistive-technology or non-Linux platform observation is made.
The wider UI-13 layout work stays open: named breakpoints, resizable and collapsible panes, responsive working-list columns and bounded large-namespace retention.
This bounded correction does not close UI-13, the accepted responsive-layout gate or the valuable-data hold.
