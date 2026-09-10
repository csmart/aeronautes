# Normal-text contrast: reproduction and correction

This record starts with the UI-12 plan and before evidence at `9e7bb1e9d154ed63fb1bd611521af6dfe6c3bf8e`.
The corrected plan through `aff3d142652836819b4bb14ff16d9af241258aa8` received independent approval before the production change below.
Implementation and final-base delivery require their own independent reviews.

## Oracle and scope

The oracle is [UI-12](../audits/2026-09-05/ui-product.md#ui-12--p2-text-contrast-is-insufficient-for-the-dense-control-typography), DESIGN §4 and Phase 7, and the separate frontend evidence boundary in §65.10.
The owner chose the WCAG AA normal-text target of at least 4.5:1 for this correction.
[WCAG contrast guidance](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html) includes placeholders and text shown during hover or keyboard focus, excludes genuinely inactive controls, and does not round a ratio below the threshold into a pass.
Calculate ratios from the identified opaque foreground and background colours, not antialiased edge pixels.
The existing 11–14 px text does not use the large-text exception.

The production scope is token values in `frontend/src/design/tokens.css` and one input/textarea placeholder colour/opacity rule in `frontend/src/app.css`.
The maintained regression belongs in `frontend/src/design/textContrast.test.ts`.
The only documentation changes are this experiment and the bounded DESIGN sequence.
Preserve the warm cream/stone surfaces, graphite ink, brass accents and semantic state families.
Automatic and explicit dark declarations must remain value-identical.
Do not change fonts, type sizes, layout, handlers, transfer behaviour, native control appearance, focus treatment, dependencies or CI gates.
Any additional production file or changed product decision requires a separately agreed scope before editing it.

## Current-source reproduction

The initial private `before-ratios.json` records 32 actual production text pairs in each theme, with 17 measured failures and 47 passing controls.
Independent plan review found that this initial inventory omitted the enabled ACL Remove control's inset surface; the expanded inventory below is the implementation oracle.
These are measurements, not executed regression tests.
The sRGB calculation uses the 0.04045 linearisation boundary and the standard luminance weights; black/white 21:1 and the #777777/#767676 boundary against white are independent arithmetic controls.

| Theme | Actual text/background role | Before ratio |
| --- | --- | ---: |
| Light | Faint footer on app background | 2.7369 |
| Light | Faint container detail on pane | 2.9839 |
| Light | Faint hovered container detail on card | 3.1173 |
| Light | Faint selected container detail on selection | 2.7706 |
| Light | Accent planning badge on card | 3.8944 |
| Light | Accent job-item action on pane | 3.7279 |
| Light | Strong accent class badge on accent-soft | 4.3632 |
| Light | Inverted Create label on accent | 3.6360 |
| Light | App-background Start label on accent | 3.4192 |
| Light | Warning partial badge on warning-soft | 4.0183 |
| Light | Warning dialog text on pane | 4.4458 |
| Light | Success complete badge on success-soft | 4.3508 |
| Dark | Faint footer on app background | 3.9097 |
| Dark | Faint container detail on pane | 3.6730 |
| Dark | Faint hovered container detail on card | 3.4265 |
| Dark | Faint selected container detail on selection | 2.9986 |
| Dark | Danger failed badge on danger-soft | 4.3465 |

The sampled consumers are the browser rail, container column, working list, inspector/editors, job badges and actions, and the create/transfer/delete dialogs.
Passing controls include ordinary and secondary text on their actual surfaces, selected row text, destructive action labels and the other semantic text pairs.
The unchanged secondary text on an inset surface is the lowest passing control at 4.5364:1.
Changing only the two examples in the original audit would leave demonstrated failures in selected rows and semantic badges.

## Expanded inset and soft-fill inventory

The enabled ACL Remove control is included in the normal-text target, including its displayed removal glyph; no inactive-control or symbol exception is taken.
`AclEditor` gives `.chip` the inset background and secondary text, while its transparent `.x` child overrides the foreground to faint and its hover rule uses danger.
The expanded `before-ratios-expanded-v1.json` retains the initial measurements and adds these four actual pairs.

| Theme | ACL Remove control on inset | Before ratio | Proposed ratio |
| --- | --- | ---: | ---: |
| Light | Normal faint text | 2.56836 | 4.60477 |
| Light | Hover danger text | 4.61385 | 4.61385 |
| Dark | Normal faint text | 4.08177 | 6.52551 |
| Dark | Hover danger text | 5.60086 | 6.17571 |

The resulting 68 pairs contain 19 measured failures and 49 passing controls before correction.
The initial candidate's #70695b light faint colour still fails the newly included inset pair at 4.41128:1; that rejected candidate remains preserved.
The revised candidate below uses #6d665a, which reaches 4.60477:1 on the inset while keeping the same warm neutral family.
The initial private label “active tab” is also corrected to “inspector configure action”: the unchanged strong-accent/card pair comes from `Inspector .edit-btn`, while the current active tabs use normal text.
This label correction changes no measured pair or ratio.

The current source contains exactly 22 inset/soft background declarations, enumerated in `inset-soft-surfaces-v1.json` with source hashes.
Their complete text and descendant-colour mappings are:

| Surface | Current consumers and foregrounds |
| --- | --- |
| Inset | `ContainerColumn .modes` contains secondary `.mode` text; selected modes replace the background with card and use normal text; `Inspector .raw` uses secondary text; `AclEditor .chip` uses secondary text, its enabled `.x` uses faint, and `.x:hover` uses danger. |
| Accent-soft | `BookmarkRail .badge`, `ContainerColumn .class-chip` and `WorkingList .badge` use strong accent; `BookmarkRail .badge.plus` replaces the fill with a transparent background, so its faint glyph belongs to the underlying rail surface. |
| Warning-soft | `BookmarkEditor .warning`, `WorkingList .backstage-banner`, both edit panels' `.banner.conflict`, `MetadataEditor .chip.changed` and the grouped conflict/mismatch/partial/cancelled job badges use warning. |
| Success-soft | `MetadataEditor .chip.added` and the grouped applied/complete job badges use success. |
| Danger-soft | Bookmark/Create errors, both edit panels' `.banner.mismatch`, `ManifestDeleteDialog .keeps.warn` and `.error`, `MetadataEditor .chip.removed` and the failed job badge use danger. |
| Info-soft | No current component uses this fill. |

Both edit panels give ordinary banner buttons their own opaque pane background.
Their `button.danger` override instead uses danger text with `background: none`, so “Apply mine anyway” inherits the conflict banner's warning-soft fill.
The maintained checks below include both cases; the initial plan's blanket statement that every banner button had an opaque background was incorrect.
No current component places faint text on a semantic soft fill.
That is not permission to reuse faint indiscriminately: some such hypothetical combinations would still fail the chosen target, so the maintained inventory must make the allowed mappings explicit and require a new checked pair for new or changed usage.
The implementation must enumerate these declarations and inherited/hover overrides instead of relying on an arbitrary subset of token combinations.

## Native before evidence

The unmodified Wails application was exercised on an owned Linux Xvfb display with private state and a separate mock Swift endpoint.
Its preserved clean binary is SHA-256 `f424c292806c11d4e4bcd909e6094b37f9ce4d77397342934898ed160fc2a181`, built from `123ba1de254a8761f8c3f3836e04c01257588987` whose complete source tree equals the starting master tree.
This is a source-identical prior build, not a binary stamped with the new master identity.
The 29-command `native-before-v1` sequence captures both themes, normal and hovered rows, keyboard-focused Filter, empty/filled Create controls, and disabled Copy Preview/Start controls.
There were zero application Swift PUT/POST/DELETE requests, and all six owned payloads, the external DLO reference and empty destination remained unchanged.
The screenshot named `04-light-create-disabled` actually shows an empty required field with an enabled Create button; its filename is historical and supplies no disabled-control proof.
The Copy screenshots provide the actual disabled-control observations.

No authored placeholder rule exists in the starting frontend source.
The native application visibly renders enabled Filter and destination placeholders in grey.
A separate installed WebKitGTK 2.52.5 / GTK 4.22.4 fixture uses the production tokens, base typography and WorkingList input styles to compare the same placeholder against an explicit #a9a9a9 control and a passing normal-text control.
The application and fixture resolve to the same hashed GTK and WebKit shared libraries.
After requiring the actual input background to paint, the baseline and explicit grey text regions are pixel-identical in both themes and differ from the passing controls.
The unmodified application's Filter region also contains that grey in each theme.
The identified opaque grey pair is 2.31335:1 on the light #fffdf9 input surface and 6.72260:1 on the dark #272219 surface; the normal-text controls are 16.53138:1 and 12.61431:1 respectively.
The retained verifier exits 1 specifically for the light placeholder's sub-4.5 ratio after those controls pass.
The pixel comparison identifies the renderer's colour; the ratio is calculated from that opaque colour pair, not from antialiasing shades.
This isolated renderer fixture is separate from the actual application screenshots and is not a component or whole-application accessibility test.

Preserved unsuccessful attempts include a computed-style probe that returned the input colour for `::placeholder`, a first renderer capture with an unpainted dark frame, and a verifier that incorrectly required a fully opaque grey pixel in antialiased dark glyphs.
Those attempts are excluded from the effective contrast-failure claim.
The final controls compare the complete text region, reject the distinct passing control, and require the painted background before comparison.
Current upstream WebKit source was a diagnostic lead only; it is not substituted for this installed-runtime evidence.

## Proposed correction and commit sequence

First commit and independently review this plan and before evidence.
Only after approval, implement one logical correction with the maintained regressions and updated experiment evidence.
The following revised private mathematical candidate passed all 68 expanded pairs at the plan stage; the implementation below applies these same values.

| Theme | Token | Before | Candidate |
| --- | --- | --- | --- |
| Light | text-faint | #98907f | #6d665a |
| Light | accent | #a2792c | #856020 |
| Light | accent-strong | #8a6420 | #76541c |
| Light | warning | #9a6a1a | #8d6017 |
| Light | success | #3d7a44 | #36703d |
| Dark, both declarations | text-faint | #7d7464 | #a19785 |
| Dark, both declarations | danger | #d3705e | #db7866 |

Use the corrected faint token with opacity 1 for input/textarea placeholders, leaving native appearance and automatic forced-colour adjustment intact.
Retain the original candidate that still failed two pairs alongside this passing mathematical candidate.
Also retain the later 64-pair candidate that failed the newly included ACL inset pair.
The normal foregrounds, background surfaces, selection fills and focus-ring values remain unchanged.
Actual disabled attributes and existing disabled opacity continue to convey inactive controls; active muted text cannot claim that exception.

## Required implementation proof

1. Add a table-driven check over the actual production tokens and selector declarations, with meaningful foreground/background pairs for ordinary, muted, selected, action and semantic text.
   Enumerate all current inset/soft-fill declarations and their inherited/hover foreground mappings, including ACL Remove; require new or changed mappings to be checked explicitly.
   Check the complete automatic/explicit dark declaration equality and the input/textarea placeholder rule.
   Keep independent arithmetic boundary controls and assert the unrounded ratio.
2. Run the new regression against the unchanged parent styles before correcting them, preserving the intended failures and unchanged passing cases.
   Prove that independently reverting each corrected colour role, dropping the placeholder rule, and changing only one dark declaration are detected without changing the relevant passing controls.
   Compilation errors and producer failures do not count as effective broken controls.
3. Run the existing theme and relevant component regressions, the complete frontend test suite, Svelte checking and the production/native build.
   Retain the previous 986 frontend test identities and attribute generated assets and binaries to the exact inspected source.
4. Repeat the owned native application controls on the final uninstrumented build in light and dark themes, observing normal, hover, focus, error and disabled states, including the enabled ACL Remove control on its inset background.
   Compare enabled placeholder rendering with the corrected authored colour using the separate renderer controls where the pseudo-element computed-style API is insufficient.
   Keep all failed producers and distinguish visited application states from authored-pair coverage of other states.
5. Obtain independent implementation approval before any authorised delivery action.
   Wait for the coordinator's final-base instruction, repeat the required verification and range review, then require all five current-source PR jobs and all five post-merge master jobs.

Forced colours, display scaling, assistive technology, other native platforms, the complete UI-12/P29 accessibility programme and valuable-data acceptance remain open.
These bounded token and renderer checks do not certify the whole application.

## Implemented correction and regressions

The production change applies the seven colour-role corrections above and the opaque input/textarea placeholder rule.
Both complete dark declaration blocks remain equal.
Backgrounds, normal and secondary text, focus-ring values, typography, component styling, native appearance and handlers are unchanged.

The maintained [contrast tests](../../frontend/src/design/textContrast.test.ts) read the actual token and component selector declarations.
They cover 124 actual text pairs in both themes, retaining every foreground/background combination in the earlier 68-pair inventory, and four placeholder/surface pairs.
The 22 inset/soft-fill background rules are enumerated, with explicit transparent, inherited, selected and hover mappings.
Ordinary/secondary text, destructive action labels, unchanged semantic combinations, dark-declaration equality and independent arithmetic boundaries provide passing controls.
This is an authored-style regression inventory; it does not implement a browser's cascade or certify arbitrary future markup.
New descendant colour mappings still require source inspection and their own checked pair.

The expanded source trace also includes the insecure-bookmark warning glyph, which is a sibling of the accent-soft badge and inherits the bookmark's ordinary, hovered or selected surface.
Two additional dark combinations failed before the correction: danger on warning-soft was 4.17122:1 and danger on selection was 4.11455:1.
The already-planned danger correction reaches 4.59934:1 and 4.53685:1 respectively; neither finding required another colour or production file.
The lowest corrected pair in the maintained inventory remains unchanged secondary text on inset at 4.536402:1.

Running the new tests with the six existing theme tests against unchanged parent styles produced 42 contrast failures and six missing-placeholder-rule assertions, with 97 passing checks (`focused-before-v2`, exit 1).
The corrected source passes all 145 checks (`focused-after-v1`, exit 0).
The initial test run also used a mistaken selected-mode selector; its two lookup failures are retained in `focused-before-v1` and excluded from the contrast-failure count.
The corrected selector reads the production `aria-selected="true"` rule.

Ten private broken variants each fail the unchanged tests, while all eight existing theme/arithmetic controls continue to pass.

| Deliberately broken correction | Failed checks | Passing checks |
| --- | ---: | ---: |
| Light faint reverted | 9 | 136 |
| Light accent reverted | 5 | 140 |
| Light strong accent reverted | 3 | 142 |
| Light warning reverted | 7 | 138 |
| Light success reverted | 2 | 143 |
| Dark faint reverted in both blocks | 9 | 136 |
| Dark danger reverted in both blocks | 11 | 134 |
| Automatic dark block alone changed | 1 | 144 |
| Placeholder rule removed | 6 | 139 |
| Placeholder opacity reduced to 0.5 | 2 | 143 |

The original variant runner stopped because it expected a diagnostic prefix that the placeholder ratio assertion does not print; that runner failure is preserved separately from the ten completed `mutations-v2` results.
Every variant retains its exact source, test hash, full test report and process exit, and the corrected styles were restored byte-for-byte afterwards.
No compilation failure counts as an effective variant.

The complete frontend suite passes all 1,125 tests, retaining the previous 986 names and adding these 139 contrast checks (`full-v1`).
`npm run check` reports zero errors and zero warnings, and `make build` succeeds with every generated frontend asset verified inside the native executable.
The observed binary is SHA-256 `920ad71e01ea0d7b9c80eb93cbf5ae54e2fd737fd69058d061fafec553370697`, stamped `dev`, source `aff3d142652836819b4bb14ff16d9af241258aa8`, `sourceDirty: dirty`.
It is the uninstrumented pre-commit implementation build, with source hashes and the working diff retained; it is not a clean or final-base release build.

## Native after observations

The fresh-state `native-after-v2` application replay completes 64 fixed commands and captures 24 screenshots on the owned Linux display and mock endpoint.
Both themes cover ordinary/hovered rows, keyboard-focused Filter, empty and filled Create controls, a safely rejected `/` container name, disabled Copy Preview/Start, and an unsaved ACL entry's enabled Remove control before hover, during hover and after removal.
The application sends zero Swift PUT/POST/DELETE requests, and all six owned payloads, the DLO reference and the empty destination remain unchanged.
The first interactive pass is also retained; its initial error-trigger command accidentally typed extra command words, so `06-light-create-error` is excluded and the corrected `06b-light-create-error` supplies that observation.

The separate WebKitGTK 2.52.5 / GTK 4.22.4 renderer uses the same production styles and the same hashed GTK/WebKit libraries as the application.
In each theme its placeholder text region matches the explicit corrected-colour control and differs from both the old grey and normal-text controls.
The actual application's Filter region contains the corrected opaque colour in each theme.
The identified placeholder pair is now 5.58887:1 in light and 5.47789:1 in dark, using the opaque source colours for the ratio.
The small ACL glyph is antialiased without an opaque core pixel; its actual normal/hover appearance and removal are observed, while its ratio comes from the checked authored pair on the verified inset surface.

Selected focus, error-text, disabled-dialog and ACL regions match between the inspected first pass and the deterministic replay.
An initial whole-error-dialog comparison also included the input's focus ring and failed on that unrelated difference; the preserved diagnostic localises it to the input, and the final comparison checks the actual error-text region.
The existing keyboard-focus observation remains a separate passing control.
These image comparisons do not calculate contrast from antialiasing pixels.

Conflict-banner actions and insecure-bookmark warnings have authored-pair checks but were not visited in this native replay.
Textarea placeholders, native select rendering, forced colours, scaling, assistive technology and other platforms remain outside the native evidence.
The wider UI-12/P29 programme and valuable-data hold remain open; the final-base review and required jobs that followed are recorded under [protected delivery](#protected-delivery).

## Protected delivery

The delivered range is the five logical commits between master 36e731fe149526d5 and the reviewed head 538e1d0af12cb4bd: the sequence plan, the ACL inset pair correction, the colour implementation, the sidecar source record and its audit index line.
Independent review approved the corrected plan, then the implementation on its original base, and then this range on its final base after the rebase onto the merged case-fold delivery.
That rebase kept both accepted DESIGN sequences whole, placing the contrast block after the case-fold sequence, and altered nothing else.

Its check run 34433301670 completed at the first attempt with all five required jobs successful.
The frontend job reported zero errors and zero warnings from svelte-check and passed twenty-eight test files carrying one thousand one hundred and twenty-five tests, the same test-name set as the approved run with no name dropped or added.
The race-detector Go job recorded twenty-four successful package executions across twenty-three distinct tested packages with no failures, unchanged by this range, which moves no Go, SQL, module, workflow or binding byte.
The containerized Swift integration job passed three thousand and twenty test results with no failures and the same two skips, for an unset matrix binary and an unreachable operating-system keychain.
It measured combined Go statement coverage of 87.2 per cent against the seventy-five per cent floor, one tenth of a point below the preceding delivery's measurement of the same unchanged Go tree, and recorded scenario coverage of forty of one hundred and sixteen cells and thirty-eight of fifty-two actions with gaps, reported valid with the owned service configured and coverage incomplete.
The headless smoke job landed three of three objects and passed.
The run retained both coverage badges, the 353,931-byte scenario coverage artifact and the 6,109,383-byte smoke artifact.

The pull request then left draft state and was merged by rebase pinned to the reviewed head object.
Master became 2ed13884408acef4, whose tree aedfe002e0f20f0a is identical to the reviewed head's tree, and each of the five commits kept its patch identity, message, author identity and intermediate tree.
The resulting master check run 34434806305 concluded success with the same five required jobs.
The pipeline exercised Linux runners only, so native acceptance on the other platforms remains unproven, and the sidecar source record carried by the same pull request closes source availability alone, leaving the advisory disposition, P31, SEC-11 and the valuable-data hold open.
