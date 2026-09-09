# Normal-text contrast: reproduction and bounded plan

This is the UI-12 plan and before evidence, starting from `9e7bb1e9d154ed63fb1bd611521af6dfe6c3bf8e`.
Production styles have not changed at this step.
Implementation and delivery require their own independent reviews.

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

The banner buttons have their own opaque backgrounds and are checked with those backgrounds, rather than inheriting the banner fill.
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
The following revised private mathematical candidate passes all 68 expanded pairs; these values have not yet been applied or accepted as final native rendering evidence.

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
