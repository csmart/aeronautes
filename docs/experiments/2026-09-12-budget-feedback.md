# Active-job budget feedback

This records the mounted UI-10 before evidence and the bounded correction contract.
The contract requires independent local approval before production changes.
It does not close UI-10/P28, establish native UI acceptance or clear the valuable-data hold.

## Behaviour oracle

[UI-10](../audits/2026-09-05/ui-product.md#ui-10--p2-operational-errors-are-swallowed-or-presented-as-empty-state) requires unavailable data to differ from empty data and an attempted action to be identified as accepted, failed or unresolved.
[DESIGN sections 38 and 40](../DESIGN.md#38-scheduler-and-resource-budgets) define the application-wide budget and existing profile policy.
`SettingsService.Get` reads the complete four-field document; `Set` validates, persists and applies it before returning its acknowledgement.
The existing frontend API awaits these calls and rejects an absent reply.
A rejected frontend Set reply cannot establish whether that write took effect.

The correction uses one synchronous settings-operation owner in BrowserState.
Get and Set cannot overlap or admit duplicates; a pending operation keeps ownership until its awaited result settles.
New budget writes require a current successful observation and are refused while Get or Set is pending, or the observation is unavailable, stale or unconfirmed.
No older read can overwrite a newer acknowledged write because the two are never admitted concurrently.

A failed Get retains any prior document with an explicit stale qualifier; a cold failure remains visibly unavailable.
The budget area offers an explicit read-only refresh once no operation is pending.
A rejected Set retains its attempted budget and unconfirmed outcome, catches the component's asynchronous rejection, and leaves the prior document as a last observation rather than a claimed rollback.
There is no automatic Set retry or guessed success.
After an uncertain Set, a successful explicit Get is required before another user choice.
That Get establishes the currently observed document and reconciles the select even when the returned number equals the old cache; it does not retrospectively acknowledge the earlier Set.
A failed recovery Get keeps the unconfirmed notice and the last observation.
Only a successful Set response produces saved feedback and replaces the cache with its acknowledged document.

Keep the latest settings attempt and observation in BrowserState so independent job polling, drawer collapse, tab changes and remount cannot erase pending or unconfirmed feedback or release admission early.
This is one bounded latest-attempt notice, not a new historical settings log.
A subsequent explicitly admitted choice may replace that notice after successful observation recovery.
Render safe fixed wording, the attempted budget and the qualified observation; do not interpolate raw provider errors.
Preserve the whole settings document and the existing choices 1, 2, 4 and 8.
Do not change the default-profile fallback, retention values, API, backend validation, scheduler, job actions, audit/bookmark state or unrelated awaits.

## Original source and commands

The immutable source is `4f9e356da7b8ae118bfa1bdbfc2c04623c434af4`.
All 827 archived files matched Git blob identities; each execution used those unchanged files plus one private probe.
The production JobsDrawer, BrowserState, Vite configuration and unhandled-error policy were unchanged.
Only the existing injected Api seam and finite owned deferred replies were faked; no real settings, database, scheduler, Wails bridge or Swift fixture was used.

The copied dependency tree had 4,192 entries and used Node 22.23.1, Svelte 5.57.0, Vitest 4.1.11, Vite 8.2.2, jsdom 30.0.1 and the Svelte Vite plugin 7.3.0.
No install or network request ran.
`<evidence>` below replaces only the original private evidence-root path; the command flags and environment overrides are the actual ones.
Each command ran from `<evidence>/source/frontend`:

```text
/usr/bin/node node_modules/vitest/vitest.mjs run src/features/jobs/budget_before_probe.test.ts --maxWorkers=1 --fileParallelism=false --testTimeout=5000 --hookTimeout=5000 --reporter=verbose --reporter=json --outputFile.json=<evidence>/<run>/vitest.json
```

`controls-1` appended `--testNamePattern=^control:`.
`refused-readback-2` appended the single literal argument `--testNamePattern=^before: rejected Set discloses an unconfirmed outcome \(applied false\)$`.
The only environment additions were `CI=true`, `NO_COLOR=1` and `npm_config_offline=true`.
Each subprocess had a 90-second containment deadline with a five-second terminate/join allowance and a final five-second kill/join allowance; none timed out.
The producer returned the actual Vitest exit, including intended failures.

| Run | Test / producer exit | Pass / fail / excluded | Unhandled rejections | Process seconds | Observation / cleanup records |
| --- | --- | --- | --- | --- | --- |
| controls-1, v1 | 0 / 0 | 2 / 0 / 5 | 0 | 1.875694 | 4 / 2 |
| before-1, v1 | 1 / 1 | 2 / 5 / 0 | 2 | 2.275728 | 12 / 7 |
| refused-readback-2, v2 | 1 / 1 | 0 / 1 / 6 | 1 | 1.776084 | 2 / 1 |

Each report has one file and seven uniquely named flat cases; Vitest calls the explicitly excluded cases skipped, and they receive no passing credit.
Counts across repeated runs are not additional unique coverage.
Every selected case released its owned pending replies and unmounted the component before emitting its cleanup record.
All execution-source fingerprints matched their own before/after snapshots.

## Actual observations and limits

The controls preserve all four settings fields, hold the cache at 2 while a Set is pending, disable the select, then apply acknowledged 4 while retaining choices 1/2/4/8.
The second control calls the existing state Get directly after an initial failed Get and observes 8 with two Gets and zero Sets.
It does not establish a recovery button: none existed.

The five original failed cases show a disappeared cold budget control with no read retry, unqualified editable cached 2 after a later Get failure, both rejected Set outcomes, and duplicate admission.
The refused-before-effect and applied-then-lost-reply fixtures both leave the attempted select at 4 and the cache at 2, with no visible unconfirmed notice.
Vitest retains the actual unhandled rejections `owned Set refused before effect` and `owned applied Set reply lost` from the void event callback.
The simulated provider values describe only test fixture state.

In v1 the refused-before-effect case failed on a further observation defect: explicit Get returned 2 and the cache contained 2, but the select still displayed 4.
Its later feedback assertion and post-read observation were not reached and receive no v1 credit.
V2 moves that observation before the assertion and makes only the DOM equality assertion soft; its separate execution preserves both `UI10-BUDGET-OBSERVED` and `UI10-BUDGET-SET` failures and the unhandled rejection.
The applied case's explicit read observes 4 without a repeated Set.

The duplicate probe sends two synthetic DOM change events synchronously before rendering: 4 then 8.
Both Sets enter; acknowledgement of the first re-enables the control while the second is pending, then the second acknowledgement wins.
Both replies were released before the count assertion failed at two admissions instead of one.
This proves a dispatch-order defect, not physical keyboard/mouse timing or native bridge behaviour.

Preparation and every recorded Git archive, private dependency copy and tool-version command exited 0.
The original assessor exited 1 after miscounting the decorative dash prefix in Vitest's unhandled-rejection heading; the corrected parser exited 0 without rerunning a test.
Its two sources and actual receipts remain separately retained.
Outer preparation/parser receipts are explicitly author-transcribed tool results; native test stdout/stderr/JSON are actual subprocess files.
Early read-only inspection guessed two nonexistent source/config paths and one initial file search exited 2; those failures have no regression credit.
Only three existing private Vitest cache metadata files changed; the original dependency tree stayed unchanged.

There is no before proof here for later Get/Set ownership, remount behaviour, native visual/accessibility acceptance, real settings persistence, scheduler effects, coverage, frontend check/build or broad-suite success.
Those required maintained UI behaviours must be demonstrated on the correction; native and backend acceptance remain separate.
The recorded input hashes do not make the complete operating system a hermetic environment.

## Archive and verification sequence

[The original v1 probe](probes/2026-09-12-budget-feedback/budget-before-v1.test.ts.txt) and [the clarified v2 probe](probes/2026-09-12-budget-feedback/budget-before-v2.test.ts.txt) are byte-identical inert archives.
[The before evidence twin](probes/2026-09-12-budget-feedback/before-evidence.json) records exact case dispositions, normalized command paths, original raw artifact hashes and selected observation values; it is a derived public summary, not rewritten raw output.
The complete private raw packet remains available for independent local review.

| Artifact | SHA256 |
| --- | --- |
| Source archive | `5ea2d822ba88f6a1135c7f5e56b0d563611f7cd0e235cb44a90638574496a66c` |
| Original probe v1 | `5e1bea464cfe5804c5004921478a5a12850cebac8a3bb54d9441f1f08011d1da` |
| Clarified probe v2 | `8f038221701e91af554d76518fae2cba00a794829281c5723ca6ffc5a3ed62de` |
| Original producer | `26c22005ea77b5f418070ba037bf85320321e06b124fd56708072fec16c93861` |
| Clarified producer | `2afead2e0c2e6dfe979b4485f5e480f9f281ca007b963c797d91c34d650ab2d0` |
| Original parser | `a8ea9392afdaac890ce4f64f7c5b376e32feba8c1fc461441a3051804c455c86` |
| Corrected parser | `d0a34d748db53cbf4a2e5a0c7c9aac2c5a559f740c14fb30400763eb1df9f491` |
| Original packet index | `eee42c4fe636c77f086cac80e4456a805bf8330b754a3078b0a61285efeecc93` |

1. Review this contract and the actual before evidence before production changes.
2. Add dedicated BrowserState and mounted JobsDrawer `budgetStatus.test.ts` regressions, preserving meaningful unchanged-source failures and healthy controls.
   Implement the single settings owner, separate observation/attempt outcomes and budget-only feedback/recovery together.
3. Verify cold/cached Get failures, both uncertain Set fixtures, same-value readback, pending Get/Set admission, synchronous duplicate requests, full-document preservation and pending/unconfirmed feedback through collapse, tabs and remount.
   Independently omit the owner, outcome projection and select reconciliation in private copies; retain actual failed producers and assertion evidence.
4. Run the focused state/component and adjacent tests, frontend check/build, and independent local implementation review.
   Final integration owns current-base verification, renewed range review, all five PR checks and all five resulting-master checks.

Only settings cache/status/Get/Set in BrowserState and budget state/action/markup in JobsDrawer are production scope.
Any need to change another author's fields, settings document, API, profile policy, scheduler or mutation semantics is a stop condition.

## Maintained correction evidence

The contract commit is `d3adfb8cd9697ebfc52c430ecfc3fd24b7b58a09` on the immutable source above.
The correction uses separate observation, pending-operation and latest-attempt state, with one synchronous Get/Set owner.
The drawer renders fixed feedback and an explicit read-only refresh; it catches rejected save callbacks while BrowserState retains the unconfirmed attempt.
The select represents the admitted attempt while pending or unconfirmed, then the returned observation after a successful read, including the same-value case.
A refused duplicate DOM event restores the admitted display immediately.
A failed recovery Get adds a stale-read qualifier without discarding the unconfirmed write notice.

The dedicated BrowserState file contains 11 flat cases and the mounted JobsDrawer file contains 10.
They cover complete-document acknowledgement, cold/cached failure and recovery, pending Get/Set ownership, duplicate dispatch, both uncertain Set fixtures and same-value readback.
They also preserve pending state through tabs, collapse and remount, and unconfirmed state through failed recovery reads and remount.
Every owned deferred reply has an afterEach release, and mounted fixtures unmount and clear their document after each case.
These are maintained state and mounted-component tests, not native browser or backend tests.

The [maintained evidence twin](probes/2026-09-12-budget-feedback/maintained-evidence.json) records all 24 actual test/check/build runs, exact case dispositions, both private omission rounds, source/output hashes and parser failures.
It replaces private paths with documented placeholders; original command records and raw outputs remain separate and hashed.
The test producer uses the same pinned Node binary and copied dependency inputs as the initial probe, without installing or fetching anything.
Its 833-file source snapshots include the approved plan and two maintained test files; every before/after execution snapshot and archive member matched.
The source ID is the committed plan parent, so the archive and four final source hashes below identify the tested dirty implementation precisely.

| Run | Test / producer exit | Pass / fail / excluded | Unhandled rejections | Process seconds |
| --- | --- | --- | --- | --- |
| before: maintained tests, unchanged production | 1 / 1 | 3 / 18 / 0 | 3 | 4.066954 |
| focused: initial correction | 0 / 0 | 21 / 0 / 0 | 0 | 3.366595 |
| final-focused: simplified correction | 0 / 0 | 21 / 0 / 0 | 0 | 3.368100 |
| adjacent: initial compatibility run | 0 / 0 | 194 / 0 / 0 | 0 | 21.332550 |
| recovery-read-before: added failed-refresh assertion | 1 / 1 | 0 / 1 / 9 | 0 | 1.815091 |
| final: final-source compatibility run with coverage | 0 / 0 | 194 / 0 / 0 | 0 | 21.280720 |

The first 18 failures include assertions on newly named state fields as well as actual API admission and DOM outcomes; they are not 18 independently reproduced historical defects.
The recovery-read assertion was added after the earlier passes and failed on missing stale-read feedback before its correction.
Its earlier passing runs receive no credit for that added assertion.
The original probe failures and its unreachable-assertion limits remain unchanged.

The final 194 cases span nine files: browser audit status 61, browser state 23, browser budget status 11, browser recovery status 6, JobsDrawer 11, jobs audit status 13, jobs budget status 10, cancellation status 20 and item records 39.
No case was skipped and no unhandled rejection occurred in that final run.
The focused command targets `src/features/jobs` plus the browser `browserState`, `auditStatus`, `recoveryStatus` and `budgetStatus` test files.
It retains one worker, no file parallelism, five-second test/hook limits and verbose plus JSON reporting; coverage adds only JSON and JSON-summary reporters.
Containment limits were 120 seconds per ordinary test/check/build process and 180 seconds for coverage, with five-second terminate and kill/join allowances; none timed out.
Final `svelte-check --tsconfig ./tsconfig.json` exited 0 with zero errors and warnings in 3.266973 seconds.
Final `vite build --mode production` exited 0 in 0.965767 seconds, transforming 209 modules; its native build duration was 560 milliseconds.
The earlier pre-recovery check also exited 0; it is retained separately.

Final coverage hit all 25 statements and all 11 instrumented branch arms within the three changed BrowserState methods/getter.
Whole-file coverage in this focused run was BrowserState 74.55% lines / 56.59% branches and JobsDrawer 92.52% lines / 79.77% branches.
Those figures do not establish whole-frontend coverage or a native acceptance result.

| Final private omission | Failed cases / unhandled rejections | Catching outcome |
| --- | --- | --- |
| Hide failed recovery-read feedback | 1 / 0 | Missing stale-read notice |
| Remove write-owner guard | 2 / 0 | Duplicate state and mounted Set admission |
| Remove Get-owner guard | 2 / 0 | Duplicate cold/cached Get admission |
| Hide the unconfirmed notice | 2 / 0 | Missing attempted-write outcome before/after recovery |
| Keep attempted choice after successful read | 1 / 0 | DOM 4 when current observation is 2 |
| Rethrow from the component callback | 0 / 2 | Both rejected Set callbacks become unhandled |
| Treat Get as historical Set acknowledgement | 2 / 0 | Read incorrectly rewrites the unconfirmed attempt |
| Restore automatic remount Get | 1 / 0 | Remount dispatches an unrequested recovery read |

All eight final controls returned actual test and producer exit 1 with the intended assertion or unhandled-error evidence.
The callback control has two passing cases but two unhandled rejections and process exit 1; Vitest's JSON `success: true` describes its case results and does not make that run successful.
The other selected cases and all explicit exclusions are recorded in the twin.
The earlier seven controls remain a separate round on the simplified source; repeated behaviours are not additional unique coverage.
Each private control changed exactly one declared production file, leaving its maintained tests unchanged.

Reconciliation first exited 1 because excluded Vitest cases may omit `duration`, then exited 1 because the parser incorrectly equated JSON case success with successful process completion in the unhandled-error control.
Both parser sources and actual available receipts remain retained, and the corrected parser exited 0 in 5.067002 seconds without rerunning application tests.
The first parser receipt is explicitly transcribed from the tool result, with no reconstructed raw output or exact-argv claim.
The dependency manifest was captured after the maintained before run; later comparisons admitted only recorded private Vite cache changes.
Reconciliation rehashed all 5,085 original packet entries and all 4,192 dependency entries in each of 16 copies.
For a private control, `tracked.patch` records the author worktree delta; its separate `mutation.patch`, input manifest and full source archive establish the actual mutated source.

| Final source or artifact | SHA256 |
| --- | --- |
| BrowserState | `e21f4f99742373bdcd39d84f4e823f179db27ada94fcfdfe1ff48d3f4eb6ad8b` |
| JobsDrawer | `4661d68e1fb9b31cae667d6b8a2e32ad5c8108004fec3a27830779ae2be21147` |
| BrowserState budget tests | `b2ff2ca6dc637193aecac17a343932ca30a19b138927a90833201e40767fde99` |
| Mounted budget tests | `51f186d2d6ccfe75f7afdabf84921535a482aad52522bf753d6e75838096ac61` |
| Final execution source archive | `274031cff08cd597e324ff7d8d801e0f4f4c637757eb1164780ccacc0df2016a` |
| Maintained producer | `1e7634e6feaf036f6b67e7f7cfb1f0a2149ee4e5ba4f85b422d4a1ed3810ac57` |
| Corrected reconciliation source | `32364ead5d08ee7f2f7278a936e8b48d6930b3982856a48191c9af8d62aed9d5` |

Independent implementation approval, current-base combined verification and protected delivery remain required.
Native visual/accessibility acceptance, actual settings persistence and scheduler behaviour were not exercised here.
UI-10/P28 and the valuable-data hold remain open.
