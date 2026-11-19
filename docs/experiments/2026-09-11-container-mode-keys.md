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

## Implemented correction and results

The correction is confined to `ContainerColumn.svelte` and `containerModeTabs.test.ts`.
The component takes its instance id from `$props.id()`, derives one panel id and one id per tab from it, binds the three tab buttons, and handles keydown on each tab: ArrowRight, ArrowLeft, Home and End compute the target index, prevent the event's default, set the mode, and focus the target button; every other key returns before any of that.
Each tab carries its id, `aria-controls` naming the panel and a `tabindex` of zero when selected and minus one otherwise, and the container list carries `role="tabpanel"`, the panel id and `aria-labelledby` naming the selected tab.
The browser screen, its window-level key handler, the browser state, shared focus helpers and global styles are untouched.

Two additions to the component go beyond the plan's statement that its styles stay unchanged, and both come from the native replay below rather than from the component tests.
The selected tab's card shadow was replacing the global focus ring, and because activation follows focus the focused tab is always the selected one, so the ring never showed; the component now draws the card shadow and the focus ring together on a selected tab that has visible focus.
The focus moved by a key was not given the keyboard focus indication by the engine, because script focus does not inherit it there; the move now requests visible focus through the focus option, which engines without that option ignore.

On the corrected component all nine cases pass, including the two controls that passed before.
Three deliberately broken variants were built from the corrected source and run against the same nine cases.
Removing the `preventDefault` fails the ownership case and the two movement cases that assert consumption, while the vertical-arrow control and the other five still pass.
Removing the roving `tabindex` fails only the tab-stop case.
Freezing `aria-labelledby` on the first tab fails only the association case.

The existing keyboard-scope and paging regressions pass alongside the new file, one hundred and six cases across the three files.
The complete frontend suite passes one thousand one hundred and thirty-nine tests: every one of the one thousand one hundred and thirty prior names is retained and exactly the nine new names are added.
The type check reports zero errors and zero warnings across two hundred and sixty files, and the production build succeeds.

## Native keyboard replay

The Linux build of the reviewed head was driven on the host through the retained portable display runtime, the same read-only fixture mock and the same connect sequence as the earlier native replays, at 1280 by 800 in the light theme.
The virtual display was allocated by the display server itself; the operator's own session is a different display.
The binary reports the reviewed head as its source revision and a clean tree, and its digest, the sequence digest and every capture are retained with the run.
Twenty-nine sequence commands ran, fourteen inputs and fourteen captures followed by the finishing check, so with the connect capture fifteen images are retained; no confirmation key was ever sent, the mock recorded no write, and the complete remote snapshot was unchanged at the end.

Captures were read by region: the brightest of the three tab boxes names the selected tab, a count of focus-coloured pixels inside the strip shows whether a focus ring is drawn there, and the container list, working list and footer regions are hashed so that any change in them is caught.

| Step | Selected tab | Ring in strip | Container list | Working list |
| --- | --- | --- | --- | --- |
| pointer click on the selected row, then on the Logical tab | Logical | none | unchanged | selected row unchanged |
| Shift+Tab | Logical | yes | unchanged | unchanged |
| ArrowRight | All | yes | unchanged | unchanged |
| ArrowRight | Backstage | yes | unchanged | unchanged |
| ArrowRight | Logical | yes | unchanged | unchanged |
| ArrowLeft | Backstage | yes | unchanged | unchanged |
| Home | Logical | yes | unchanged | unchanged |
| End | Backstage | yes | unchanged | unchanged |
| Home | Logical | yes | unchanged | unchanged |
| Tab | Logical | none | first row focused | unchanged |
| Shift+Tab | Logical | yes | unchanged | unchanged |
| ArrowDown | Logical | none | unchanged | list focused |
| ArrowDown | Logical | none | unchanged | selection moved to the second row |

The selected tab follows every key exactly as the component tests assert, the seven consumed presses leave the container list and the working list byte-identical, Tab leaves the strip to the first container row and Shift+Tab returns to the selected tab, so the strip is one tab stop, and ArrowDown still hands focus to the working list.
The mock fixture classifies every container as ordinary, so the three modes list the same rows there; the mode change is visible only through the tabs, and the container list's byte-identical region shows that the selected container did not change.

Two things the replay established that the component tests could not.
A pointer click on a tab does not focus the button on this engine; it sets the point from which the next Tab or Shift+Tab moves, so keyboard focus reaches the strip on the next Shift+Tab, as the second step shows.
On the first corrected build the selected tab never showed a focus ring under keyboard focus, and after the first arrow move no tab showed one on the second build either; the two component additions above were made for those observations, and the replay on the reviewed head shows the ring on the focused tab at every keyboard step.
The same sequence on a build without the shadow addition, retained beside the run, shows the selected tab moving identically and no ring at any step, which is the before evidence for that addition.

## Limits

This corrects one control and closes nothing beyond it.
Screen-reader announcement of the tab and panel relationship, native macOS and Windows behaviour, zoom and scaling, the dark theme in the native replay, the other tab-like controls and the §4.4 shortcuts that are still absent remain open UI-11 and P29 work.
The native replay covers one engine on Linux, one window size and one theme, and its ring detection is a pixel count in a fixed region rather than a reading of the accessibility tree.
The valuable-data hold is unchanged.

## Proposed shared-document text

The following text is proposed for the shared design sequence list and the UI-11 disposition, to be integrated and reviewed by the shared-document owner rather than edited here.

For `docs/DESIGN.md`, after the manifest consent layout sequence:

> The next bounded [container mode keyboard correction](https://github.com/csmart/aeronautes/blob/0a4226c7f735e26e907e91d8c8de84af11078b33/docs/experiments/2026-09-11-container-mode-keys.md) gives the Container mode tablist one roving tab stop, automatic activation on ArrowLeft, ArrowRight, Home and End, and an associated tab panel, consuming exactly those four keys so the §4.4 browser handler never moves the working list on them.
> It changes one component and pins the unchanged selected container, filter, prefix and working-list selection across a keyboard mode change.
> Screen readers, other platforms, zoom and the remaining §4.4 shortcuts stay open.

For the UI-11 disposition in `docs/audits/2026-09-05/ui-product.md`, once delivered:

> The bounded container mode correction supplies roving focus, arrow and Home/End activation and a labelled tab panel for that one tablist with maintained cases and broken controls; other tab patterns, native select and assistive-technology journeys remain open.

## Protected delivery

The delivered range is the nine logical commits between master d561eaac17f1839e and the reviewed head ba7b65c47d224d20, cherry-picked patch for patch from three independently approved branches: this slice's two commits (the plan and the keyboard semantics of the container mode tabs), together with the four commits of the download root authority correction and the three commits of the engine-reserved name routing correction.
Independent review approved each slice on its own base and then the combined range on its final base, with every patch marked identical by range-diff and no shared document in the range.

Its check run 34652123581 completed at the first attempt with all five required jobs successful: the frontend job in one minute and forty-three seconds, the headless smoke job in two minutes and three seconds, the race-detector Go job in eight minutes and fifty-nine seconds, the containerized Swift integration job in thirty minutes and two seconds, and the aggregate job.
The run retained the two coverage badges, the 473,665-byte scenario coverage artifact, the 6,112,982-byte smoke artifact and a 9,964-byte Swift diagnostics artifact.
The retained badges report combined Go statement coverage of 88.1 per cent and frontend coverage of 91.07 per cent.
On the same head the full race suite with the real Swift fixture reconciled to 3,772 leaves with 3,771 passing, none failing and the single expected matrix skip, and the frontend suite passed 1,150 tests in thirty files with svelte-check reporting no errors and no warnings.

The pull request then left draft state after thirty-one premerge guards passed and was merged by rebase pinned to the reviewed head object at 22:32:55 UTC on 11 September 2026.
Master became bf5bd6b3d491bbb4, whose tree cf2548534276a07a is identical to the reviewed head's tree, and each of the nine commits kept its patch identity, message and author identity.
The resulting master check run 34654485439 concluded success at the first attempt with the same five required jobs.
The pipeline exercised Linux runners only, so native acceptance on the other platforms remains unproven, and the shared design, engine-pin and register text for this slice follows in the integrator's own range.
