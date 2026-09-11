# Container mode tabs: keyboard and panel semantics

## Scope and oracle

This is a bounded part of UI-11 in [the product audit](../audits/2026-09-05/ui-product.md) and its [remediation row](../audits/2026-09-05/remediation-plan.md), which records that tabs lack roving focus, arrow navigation and associated panels.
The subject is the Container mode control in `frontend/src/features/browser/ContainerColumn.svelte`: a `tablist` of three `tab` buttons, Logical, All and Backstage, over the container list that DESIGN §52 defines as modes of the §24 classification.
The applicable contract is DESIGN §4.4 for the keyboard model, the accessible tab pattern for a horizontal tablist, and the ownership rule delivered by [PR72](2026-09-09-keyboard-scope.md): a consumed key belongs to its handler and never reaches the window-level browser handler that would move the background working list.
Everything else in UI-11 stays open, including native select journeys, screen readers, further shortcuts and the other tab-like controls.

## Before evidence on unchanged production

The component declares `role="tablist"` with a label and `role="tab"` with `aria-selected` on each button, and nothing more.
Nine maintained cases were written against the real component mounted inside the browser screen with the fake service, and run on unchanged master 1b520ba2a80f281d5945250548137be7498a1f02.
Seven failed and two passed, each for the reason the case names.

| Case | Before | Reason |
| --- | --- | --- |
| exposes one roving tab stop for the whole mode control | fail | all three tabs are natively focusable, so the control has three tab stops |
| associates every tab with the single container panel it controls | fail | no tab carries `aria-controls`, no element carries `tabpanel`, no tab has an id |
| moves selection and focus with the horizontal arrows and wraps at the ends | fail | ArrowRight on the selected tab is not consumed and changes nothing |
| moves to the first and last tab with Home and End | fail | End is not consumed and changes nothing |
| consumes its own keys without moving the working list selection | fail | ArrowRight is not consumed, so the ownership rule cannot be asserted |
| leaves the vertical arrows to the working list as before | pass | ArrowDown on a tab reaches the window handler and focuses the working list per §4.4 |
| preserves the loaded-row filter and the exact selection across a keyboard mode change | fail | no keyboard mode change happens, so the tab never becomes All |
| lists the containers of the mode reached by keyboard | fail | the same |
| still activates a tab by pointer | pass | pointer activation already works |

The two passing cases are the controls this correction must not disturb.
On the same tree the complete frontend suite passed one thousand one hundred and thirty tests across twenty-nine files, and the type check reported zero errors and zero warnings across two hundred and fifty-nine files.

## Activation and focus contract

The three buttons stay native buttons inside the existing tablist, with their labels, classes, styles and pointer behaviour unchanged.

1. The selected tab is the control's only tab stop: it carries `tabindex="0"` and the other tabs carry `tabindex="-1"`.
2. Activation is automatic: ArrowRight and ArrowLeft move focus to the adjacent tab and select it, wrapping at both ends, and Home and End move focus to the first and last tab and select them.
   A mode change is a local regrouping of containers already loaded, with no request and no change to the selected container, so selecting on focus costs nothing and matches the pointer behaviour.
3. The tablist consumes exactly ArrowLeft, ArrowRight, Home and End by calling `preventDefault` on them, so the window-level browser handler, which already returns on a prevented event, never acts on them.
   Every other key is left untouched: ArrowUp and ArrowDown from a tab still reach the working list as §4.4 requires, and Enter and Space keep their native button activation.
4. The container list is the tab panel: it carries `role="tabpanel"`, a per-instance id and `aria-labelledby` naming the selected tab, and each tab carries a per-instance id and `aria-controls` naming that panel.
   Ids come from the component's own instance id, as the working list, Find panel and dialogs already do.
5. A keyboard mode change sets only the mode.
   The selected container, the loaded-row filter, the server-side prefix and the working list's active descendant are untouched, which is the existing state behaviour and is now pinned by a maintained case.
6. Focus follows the arrow movement onto the newly selected tab and nowhere else.
   The panel is not made focusable in this slice, because its rows are already focusable controls.

The change stays inside `ContainerColumn.svelte`; the browser screen, the global key handler, the browser state, shared focus helpers and global styles are not edited.

## Required proof

- The nine maintained cases pass on the corrected component, and the two controls pass unchanged.
- Three deliberately broken variants each fail only their own case: removing the `preventDefault` fails the ownership case while the vertical-arrow control still passes; removing the roving tabindex fails only the tab-stop case; dropping the `aria-labelledby` update fails only the association case.
- The existing keyboard-scope and paging regressions pass unchanged.
- The complete frontend suite retains every one of the one thousand one hundred and thirty prior test names and adds exactly the nine new ones; the type check and the production build pass; the generated bindings are unchanged.
- A native keyboard check on the Linux build is attempted through the existing host replay harness and recorded whether or not the harness is available.

## Limits

This corrects one control and closes nothing beyond it.
Screen-reader announcement of the tab and panel relationship, native macOS and Windows behaviour, zoom and scaling, the other tab-like controls and the §4.4 shortcuts that are still absent remain open UI-11 and P29 work.
The valuable-data hold is unchanged.

## Proposed shared-document text

The following text is proposed for the shared design sequence list and the UI-11 disposition, to be integrated and reviewed by the shared-document owner rather than edited here.

For `docs/DESIGN.md`, after the manifest consent layout sequence:

> The next bounded [container mode keyboard correction](experiments/2026-09-11-container-mode-keys.md) gives the Container mode tablist one roving tab stop, automatic activation on ArrowLeft, ArrowRight, Home and End, and an associated tab panel, consuming exactly those four keys so the §4.4 browser handler never moves the working list on them.
> It changes one component and pins the unchanged selected container, filter, prefix and working-list selection across a keyboard mode change.
> Screen readers, other platforms, zoom and the remaining §4.4 shortcuts stay open.

For the UI-11 disposition in `docs/audits/2026-09-05/ui-product.md`, once delivered:

> The bounded container mode correction supplies roving focus, arrow and Home/End activation and a labelled tab panel for that one tablist with maintained cases and broken controls; other tab patterns, native select and assistive-technology journeys remain open.
