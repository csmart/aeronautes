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

The correction adds the consent label wrapping rule, bounds the dialog to the viewport and moves the
disclosure body, the consent control, the confirmation input and any error into a scrolled region, so
the title and the action row stay outside it.

The maintained checks reconcile against the delivered inventory.
The frontend suite grows from 1,125 to 1,130 tests with no name lost and no failure, the five added
names all belong to the new consent-layout file, the type check reports 258 files with no error or
warning, and the production build succeeds.
Those checks cover content, interaction and arrangement only: the complete literal target and refusal
message, the force toggle, exact-confirmation arming, cancellation, and which elements the bounded
region owns.

The native evidence comes from a clean binary built at the correction commit, which reports that
revision and a clean source.
Five replays ran against the private read-only fixture: 59, 38 and 29 commands for the implementation
and 8 each for the two broken controls, with 56 screenshots in total.
Every request was a GET or a HEAD except the single refused delete described below, and every complete
21-entry remote snapshot equalled its baseline.

At 960×600 the long-target default dialog now measures 450×586 with a complete border, in both themes,
where the unbounded wrapped dialog needed 650.
Its force state measures 450×508.
At 1280×800 the default measures 450×650 and the force state 450×508, both with complete borders.
In every one of those states the oracle locates the actual Cancel and Delete controls inside the
viewport, in their enabled and disabled rendering, and no consent text lies beside the dialog.
Pointer cancellation closes the dialog at both sizes.

Keyboard focus reaches Cancel and activating it closes the dialog.
The recorded ring positions show the order: the first stop is the Delete action, the second is the
scrolled body itself, and the third is Cancel, so the bounded region is also the mechanism that makes
its content reachable from the keyboard.
In the refused-operation case Cancel is the second stop, because the error changes the content.

The refused-operation case uses a combined fixture whose object carries an 831-character name and a
584-character backing target.
Activating the delete produced exactly one DELETE request, which the fixture answered with 405 before
touching data, and the complete remote snapshot was unchanged.
The dialog kept its complete border at 450×586, rendered the whole refusal message, and kept the title
and both actions visible while the disclosure, the consent label and the error were reached by
scrolling.

Both deliberately broken controls fail, independently and for different reasons.
Removing the wrapping rule alone leaves the border and controls intact but makes the body scroll
sideways, cutting the literal target instead of wrapping it, which the horizontal-overflow check
detects.
Removing the viewport bound alone leaves no horizontal overflow but loses the bottom border and both
action controls at 960×600.
The original before capture fails the same border check, because the escaping label overwrites the
dialog's right border.

## Remaining limits

No zoom or scaling coverage is claimed, and no forced-colour, assistive-technology or non-Linux platform observation is made.
The wider UI-13 layout work stays open: named breakpoints, resizable and collapsible panes, responsive working-list columns and bounded large-namespace retention.
This bounded correction does not close UI-13, the accepted responsive-layout gate or the valuable-data hold.
