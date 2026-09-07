# P23 literal-input reproductions — 7 September 2026

This record follows the P23 sequence in DESIGN §65.11 after the delivered P22 work.
The initial observations precede implementation; subsequent sections identify each maintained correction and its delivery state.

## Original frontend probes on current master

The source is master `c7b669e422465754e9674f51e3e364986c905277`.
A disposable copy of its tracked frontend uses the same lockfile and installed dependencies as the prior frontend verification.
The preserved original audit probes are copied without assertion changes; only their absolute imports are redirected to this current source.
Four P23 cases are selected and the eight other archived cases are unselected, not successful evidence.

| Original probe | Observed assertion failure |
| --- | --- |
| Preserve destination prefix bytes in Copy preview | `"  object prefix  "` is sent as `"object prefix"` |
| Send the exact server prefix | `"external"` is sent as `"external/"` |
| Retain or explicitly reject `__proto__` metadata | The key is absent and no error is reported |
| Report added keys independently of `Object.prototype` | `constructor` is not reported as added |

All four selected tests fail their intended assertions, with process exit 1 and no setup failures.
The raw record is `/tmp/aeronautes-remediation-20260905/p23-original-before.json`, `.log` and `.exit`.
`p23-original-probes-inputs.json` records the source commit, original/copied probe hashes and the import-only change.
`p23-prepare-probes.py` records the disposable source/dependency setup.

These are frontend component/helper observations, not evidence of a destructive remote Mirror execution.
Mirror currently supports comparison preview and refuses every start.
Transfer requests still require an empty or slash-terminated prefix, so the first probe demonstrates altered input even though its literal unslashed prefix is unsupported for execution.
The correction must surface that existing validation error rather than silently converting a whitespace-only prefix into root scope.

## Independent metadata-header observations

The [Swift API reference](https://docs.openstack.org/api-ref/object-store/#create-or-update-object-metadata) defines metadata names as case-insensitive ASCII HTTP token names and documents underscore-to-hyphen conversion.
The [Swift WSGI header adapter](https://github.com/openstack/swift/blob/master/swift/common/swob.py) maps header names through an environment representation that cannot distinguish underscores from hyphens.
This documents an identity limitation; the exact running fixture must still be observed independently.

A raw HTTP probe creates one uniquely named Aeronautes-owned container on the local SAIO account, then five owned plain objects with known bytes and an unrelated `Keep` metadata sentinel.
Each object receives a metadata POST, an independent HEAD and a GET compared to the known source bytes.
Three container metadata POSTs are also followed by independent HEAD observations.

| Resource | Submitted key | Observed result |
| --- | --- | --- |
| Object | `constructor` | `Constructor` retains the submitted value and the sentinel |
| Object | `toString` | `Tostring` retains the submitted value and the sentinel |
| Object | `__proto__` | HTTP 202, but the submitted key is absent; only the sentinel remains |
| Object | `a_b` | HTTP 202, but the submitted key is absent; only the sentinel remains |
| Object | `A-B` | `A-B` retains the submitted value and the sentinel |
| Container | `constructor` | `Constructor` retains the submitted value |
| Container | `__proto__` | HTTP 204, but the submitted key is absent; earlier `Constructor` remains |
| Container | `a_b` | HTTP 204, but the submitted key is absent; earlier `Constructor` remains |

This fixture drops underscore-bearing headers rather than returning renamed keys.
Do not generalise its observed dropping to every deployment or claim that literal existing `__proto__` metadata was erased by the application.
Both the documented normalization and this fixture's dropping violate exact preservation of an entered underscore-bearing key, supporting explicit refusal before mutation.
The probe leaves every object's known bytes unchanged and verifies deletion of its owned objects and container afterwards.
It does not exercise the bound application mutation methods, DLO preservation, concurrency or whole-account collateral safety.

The script and raw observations are `/tmp/aeronautes-remediation-20260905/p23-metadata-wire-probe.py` and `p23-metadata-wire-observations.json`.
The record includes all eight header observations, five known-body digests and successful cleanup verification, without credentials.
The maintained application tests must separately prove complete working-copy serialization, supported-key round trips and pre-mutation refusal with independent namespace snapshots.

## Container identity in the mock fixture

The transfer test preparation exposed a fixture mismatch before any production changes.
The mock routes an escaped container path component directly into its storage map, so spaces, Unicode and literal percent escapes acquire different names in the account listing.
Object path components already receive a separate decode.
Five direct HTTP tests create a container and object, then compare the account listing with the known submitted container name.
All five fail that identity assertion: for example, `"trailing  "` is listed as `"trailing%20%20"` and `"%2F +%20"` becomes `"%252F%20+%2520"`.
The unmodified-source record is `/tmp/aeronautes-remediation-20260905/p23-fixture-before-v1.json` and `.exit`, with exit 1, five assertion failures and no skipped cases.

The corrected bound-service baseline is `p23-transfer-go-before-v2.json` and `.exit` in the same directory.
It has 27 passing leaf tests and 17 assertion failures: eight unit and eight bound-service failures expose the missing slash-container admission guard, while the remaining failure is the mock's escaped-container identity mismatch.
The equivalent trailing-space container copy passes on SAIO with both engines and independently checked destination bytes.
These snapshots cover all containers within each test's owned namespace, including derived sidecars, rather than the whole account.
The earlier `p23-transfer-go-before-v1` run used unsupported subtest receipt credit and preview refusal credit; retain it for diagnosis, but do not count it as valid negative evidence.

Repair the mock route in a separate reviewed prerequisite before the application transfer change.
Keep the independent oracle, receipt rules and actual-provider assertions intact, and require literal percent escapes to be decoded once while an encoded slash in a container component is refused.
This is a fixture contract repair, not evidence that the real Swift provider changes container names or that P23 is complete.

## Maintained fixture repair

The mock now decodes the container component once after splitting an escaped request path, and rejects a decoded slash before dispatch.
Its server-side COPY destination header needs the same decoding: the client escapes that container too, so retaining the escaped header component would leave COPY looking up a different container from the corrected route.
Five direct COPY cases reproduced that missing lookup, and three encoded-slash cases reproduced the missing explicit refusal, before the header correction.
The record is `p23-fixture-copy-before-v2.json` and `.exit` under the same temporary evidence directory.
Object components retain their existing separate decoding.

| Maintained test surface | Cases | Independent observation |
| --- | ---: | --- |
| Direct mock HTTP container/object operations | 5 | Exact account and object listing names, known GET bytes, container count and deletion |
| Direct mock encoded-slash container refusal | 4 | HTTP 400 and unchanged empty account inventory |
| Direct mock COPY to literal containers | 5 | A known literal fixture container receives the exact object name and bytes |
| Direct mock COPY encoded-slash refusal | 3 | HTTP 400, unchanged account inventory and known source bytes |
| Bound PlanCopy and StartCopy, mock and SAIO | 14 | Exact scope, two objects and eleven bytes, expected native routing, complete owned namespaces and unchanged collateral |

The bound cases cover root, leading source/destination spaces, space-only prefix components, repeated separators, percent escapes, Unicode and trailing container spaces.
The trailing-space container cases retain separate containers with the trimmed name and sentinel bytes, so choosing the trimmed container cannot pass the independent observation.
Each mixed-path copy includes an rclone-eligible object and a newline-bearing native object; repeated-separator scopes route both objects natively.
These are direct fixture and bound Go observations, not mounted frontend or native desktop end-to-end evidence.

Replaying the final tests with only the original mock source substituted produces 18 assertion failures and 13 passes, with no skips.
The final corrected run passes all 31 leaf cases across 18 top-level tests and two packages.
The raw before/after records are `p23-fixture-parent-v3.json/.exit` and `p23-fixture-after-v2.json/.exit`.
The parent overlay and `p23-fixture-parent-v3-inputs.json` identify the unchanged test hashes and original mock source.
The earlier focused runs remain diagnostic history rather than the final test totals.

Ten independent broken variants are caught by the maintained tests: missing or repeated container decoding, query decoding that changes a plus, missing encoded-slash refusal, trimming a container, and missing object decoding, including the corresponding COPY-header guards.
`p23-fixture-controls-v2.py` records the commands; `p23-fixture-controls-v2/` retains each overlay, actual exit, assertion output, final source/test hashes and the results table.
The overlays leave the repository implementation and assertions unchanged.

The final full internal race run passes 559 top-level tests, 1,150 leaf cases and all 20 packages, with no failures or skips and 84.7% combined statement coverage.
It uses both local SAIO accounts and the verified pinned rclone, including the optional rclone matrix.
The raw event stream and process exit are `p23-fixture-full-v2.json` and `.exit`; `p23-fixture-cover-v2.out` is the combined profile.
`p23-fixture-scenario-coverage-v2.json` reconciles that actual stream, exit and expected package set with the producer's SAIO configuration: valid process, stream and contract, 28/110 covered cells and 39/49 actions still having gaps.
Frontend build, Go build, Go vet and the 38-file upstream Swift source check also pass.
The earlier full run preceded the COPY-header correction and does not substitute for this final verification.

Independent review approved this prerequisite, which was delivered through [PR47](https://github.com/csmart/aeronautes/pull/47).
All five [PR checks](https://github.com/csmart/aeronautes/actions/runs/34092324766) and all five [master checks](https://github.com/csmart/aeronautes/actions/runs/34093089713) passed.
The protected rebase merge preserves its four logical commits; master `e6e01f650da57478eaae5e88abad099e54413a22` has exactly the reviewed `dc3633cb8c841fdccd919444e5bf5f7a9e19703e` tree.
The prerequisite itself does not fix the transfer dialog or its application admission guard.
The mock's separate DLO and symlink reference-header parsing has not been validated for escaped container names by these plain-object cases; no general header-fidelity claim is made.

## Literal transfer requests and refusal

The transfer dialog now preserves destination container and prefix strings through preview, prepared-request ownership and Copy start.
Quoted source and destination scope text distinguishes `"" (container root)` from `"   "`, with markup escaped as text.
Changing only whitespace invalidates the prepared request, and a late error from an earlier scope cannot replace the current preview.
Non-resource settings retain their existing parsing.

Go admission rejects a slash in either container name before resolving sessions or creating a job.
The existing prefix rule remains unchanged: a nonempty transfer prefix must end with `/`.
An unsupported literal prefix reaches the normal validation error and stays visible in the editor, rather than being trimmed into root or another prefix.
Mirror remains comparison-only with every start refused, and Copy still refuses changed existing destinations.

| Maintained test surface | Cases | Observation |
| --- | ---: | --- |
| Mounted transfer dialog | 30 | Literal Copy/Mirror preview payloads, Copy start matching the captured request values, quoted scope, escaped markup, refusal display and stale-preview/error ownership |
| Go transfer admission | 14 | Eight slash-container refusals and six accepted literal scope shapes |
| Bound PlanCopy and StartCopy, mock and SAIO | 14 | Previously delivered exact-name, byte, counter, routing and owned-namespace checks remain intact |
| Bound invalid source/destination scopes, mock and SAIO | 16 | Preview refusal, no Copy/Mirror job, no recorded jobs and unchanged independent owned namespaces |

The final mounted assertions on the original component fail 23 cases and pass seven, with exit 1 and no skips or setup errors.
The corrected component passes all 30.
Seven separate broken variants are caught: trimming the prefix, trimming the container, refusing space-only container admission, omitting the root label, trimming displayed scope, changing scope values at Start and interpreting scope markup.
Each control fails a concrete assertion with exit 1; the Start comparison uses a captured value snapshot and permits an identical cloned request.
`p23-transfer-ui-controls.py` and `p23-transfer-ui-controls-v1/` preserve the parent, positive and seven control runs, actual exits and source/test hashes.

On the corrected fixture but original application admission, the maintained Go cases produce 16 assertion failures and 28 passes, with no skips.
These are the eight unit and eight bound slash-container failures, independent of the earlier mock decoding defect.
The corrected focused run passes all 44 leaf cases across 32 top-level tests and two packages.
The raw records are `p23-transfer-go-before-v3.json/.exit` and `p23-transfer-go-after-v1.json/.exit`.
Two separate overlays remove the container or prefix guard: the former catches 16 failing leaf assertions and the latter eight bound refusal failures.
`p23-transfer-go-controls.py` and `p23-transfer-go-controls-v1/` preserve both actual exits, assertions and unchanged implementation/test hashes.

The full frontend run passes 754 tests in 20 files, with no failures or skips and 77.73% statement coverage.
Its final record is `p23-transfer-ui-full-v2.json/.log/.exit`, with `p23-transfer-ui-coverage-v2.json` retaining coverage.
Svelte/TypeScript checking and the frontend production build pass.
The full internal Go race run passes 577 top-level tests, 1,180 leaf cases and all 20 packages, with no failures or skips and 84.7% combined statement coverage.
It uses both owned SAIO accounts, the verified rclone pin and the optional rclone matrix.
`p23-transfer-go-full-v1.json/.exit` and `p23-transfer-go-cover-v1.out` retain its actual event stream, exit and profile.
`p23-transfer-scenario-coverage-v1.json` reconciles that stream, actual exit and expected packages with the producer configuration: valid process, stream and contract, 28/110 covered cells and 39/49 actions still having gaps.
Go build, Go vet and the 38-file upstream Swift source check pass.

The four unchanged original frontend probes now produce one pass and three intended assertion failures; eight other archived cases remain unselected.
The exact destination-prefix probe passes, while literal browser filtering and the two metadata dictionary probes still fail.
`p23-original-after-transfer-v1.json/.log/.exit` and `p23-original-after-transfer-inputs.json` retain that mixed result and the import-only adaptation.
The original transfer probe's unslashed prefix demonstrates preserved UI bytes; the separate bound Go refusal tests establish that unsupported input cannot execute.

All records above are under `/tmp/aeronautes-remediation-20260905`.
The implementation started on the reviewed fixture tree and moved onto its identical merged master tree with source/test hashes unchanged; `p23-transfer-base-switch.json` records that comparison.
This is mounted frontend and bound Go evidence, not native desktop end-to-end, visual layout or six-platform validation.
Independent local review approved transfer source `198b3042b93d8e6367d348855fc1743823f93be2` after inspecting the implementation, maintained tests and broken-behaviour controls.
[PR48](https://github.com/csmart/aeronautes/pull/48) passed all five [PR checks](https://github.com/csmart/aeronautes/actions/runs/34095697995) before protected merge.
All five [master checks](https://github.com/csmart/aeronautes/actions/runs/34096695188) passed on `4f0cee2943215511e37d3e0a58329fe8bfd3f645`, whose tree matches the reviewed source exactly.
UI-04 is delivered for the defined literal transfer-input and admission scope.
P23, UI-07, UI-14 and the wider valuable-data release hold remain open.

## Browser baseline and delimiter fixture prerequisite

The next slice starts from green dependency master `e3c32f6967d33762f83f4bc696926a419697aa1f`, with the delivery-only documentation commit `afdaf5631070c41f0c876a80904cf28803f24a3f` on top.
Before any application change, the new mounted frontend/helper tests have 24 assertion failures and 15 passes, with no skips.
They reproduce added slashes, truncated leaf labels, an incidental rendered trailing space, absent partial-prefix breadcrumbs, collapsed breadcrumb whitespace and loss of an exact matching object in the fake listing.
`p23-browser-ui-before-v1.json/.log/.exit` retains that preliminary run under `/tmp/aeronautes-remediation-20260905`.
These are development regressions, not final implementation totals or native visual evidence.

The bound Browse tests exercise eight literal filter shapes on both mock Swift and SAIO, with one-row provider pages, two-row bridge pages and independent namespace snapshots.
Five unslashed exact-name cases fail on each provider because the application drops the matching plain object and fetches an extra page to fill the result.
The slash-terminated own-marker cases pass on SAIO but fail on the mock: after returning the query prefix's own real object, the mock treats that marker as an instruction to omit all its children.
The root control passes on both providers.
The preliminary run has twelve failing and four passing leaf cases, with no skips; `p23-browser-go-before-v1.json/.exit` retains the actual events and process exit.

The shared mock and the separate swiftcontrol test fixture both skip every object beneath any slash-terminated marker before deriving the delimiter row.
That overbroad shortcut confuses the query's own object with an already returned child-prefix group.
The first proposed correction would have excluded every derived group that did not advance beyond the marker; direct provider observations below disprove that assumption.
Direct HTTP tests must distinguish an own marker from child-group continuation, including markers inside a group or before the filter, while the bound mock/SAIO comparison retains exact child names and unchanged owned namespaces.
This prerequisite changes test fixtures only; the exact-match application defect and the remaining browser/metadata work stay open.

The first direct HTTP run checks eleven marker shapes through each mock fixture and both mock/SAIO harnesses, plus three bound own-marker cases on each provider.
On SAIO, markers `scope/a/o` and `scope/a/one/` under `scope/` return the enclosing `scope/a/` group; a root marker `scope/a` similarly returns `scope/`.
Both existing fixtures already match those three provider results, so their initial expectations were wrong.
The `p23-marker-before-v1.json/.exit` run has 27 failing and 23 passing leaf cases, but twelve failures reflect that incorrect expectation repeated across four surfaces; retain it as diagnostic evidence, not the final negative baseline.
The initial test files and hashes are preserved in `p23-marker-initial-tests/` under the same temporary directory.

The revised fixture correction excludes a child group only when its complete derived name equals the requested marker.
This preserves the observed distinction between an own-prefix object, a completed child group and an arbitrary marker inside a group.
Correct the three direct HTTP expectations to the observed provider behaviour, and require bound Browse to retain its existing refusal of non-advancing pages without returning partial rows.
Rerun the corrected tests on the unchanged parent before changing the fixtures; application source, assertions about exact literal names and the independent namespace oracle remain intact.

## Maintained delimiter fixture repair

The shared Swift mock and the separate swiftcontrol test fixture now exclude a delimiter group only when its complete derived name equals the marker.
They retain children after the query's own real object and preserve the actual provider's enclosing-group responses for arbitrary interior markers.
The [upstream container backend](https://github.com/openstack/swift/blob/master/swift/container/backend.py) corroborates this equality rule; the fetched source and checksum are retained in `p23-marker-upstream-backend.py` and `p23-marker-upstream.json`.
The existing Go `validateListingName` guard remains unchanged and refuses those non-advancing provider pages before bound Browse publishes rows.

Eleven direct HTTP shapes run through each mock fixture and the mock/SAIO harnesses, covering own markers, completed groups, interior markers, markers before a partial filter, deep listings, repeated separators, spaces, percent escapes and Unicode.
The two harness runs also assert bound refusal for each of the three non-advancing responses.
Three further bound Browse cases per provider require exact child names and bytes, raw marker continuation, two-row bridge bounds and termination through one-row provider pages.
Independent snapshots cover every container in the owned namespace on both accounts before and after each harness test.
The raw HTTP checks do not claim application-action credit; the bound successful reads emit one receipt claim per top-level action/scenario after the observations pass.

Replaying the final maintained tests with only the two original fixture bodies substituted produces 15 assertion failures and 35 passes, with no skips.
All 50 cases pass with the correction in the final full run.
`p23-marker-parent-v3.json/.exit` and its two-file overlay retain the final parent evidence.
The earlier `p23-marker-before-v2` baseline has the same 15/35 result before the fixture edit; the initial `before-v1` expectations remain diagnostic history as described above.

Eight independent broken variants are caught: restoring either original shortcut, inventing a stricter provider comparison in either fixture, repeating a completed group in either fixture, emitting duplicate groups and removing the existing Go page-order guard.
The page-guard variant produces six bound refusal failures on mock Swift and SAIO, identifying the actual Go guard rather than relying on an unrelated error or a frontend check.
`p23-marker-controls-v2.py` and `p23-marker-controls-v2/` retain the exact commands, overlays, source/test hashes, process exits and results.
The final implementation and test hashes match that control record.

The final internal race run passes 583 top-level tests, 1,230 leaf cases and all 20 packages, with no failures or skips and 84.7% combined statement coverage.
It uses both owned SAIO accounts and the verified pinned rclone, including the optional matrix.
`p23-marker-full-v2.json/.exit`, its summary and `p23-marker-cover-v2.out/.txt` retain the actual events, exit and coverage.
`p23-marker-scenario-coverage-v2.json` reconciles the actual exit, complete expected package set and producer environment: valid process, stream and contract, 28/110 covered cells and 39/49 actions still having gaps.
The initial full run passed its tests but emitted duplicate coverage claims from the new test, so it was rejected by the reconciler and is not the final scenario evidence.
`p23-marker-scenario-rejection-v1.txt` records that bookkeeping error; the final test emits one successful receipt only after all cases and namespace checks pass.
Go vet, the CGO-disabled internal build and whitespace checks also pass.

The application and frontend are unchanged from the delivered dependency master.
A separate replay of the preserved browser tests with only the corrected shared fixture overlaid has ten intended application failures and six passes: the two mock-only own-marker failures are gone, while exact plain matches still fail on both providers.
`p23-browser-after-marker-v1.json/.exit` and its overlay preserve that separation, and the parked browser test hashes remain unchanged.
Frontend tests were not rerun for this Go fixture-only slice; its browser implementation, metadata work and native visual validation remain separate.
Independent local review approved fixture source `10996fe662e5be16272ab7008994d110e237aba1` and its complete four-commit range.
[PR49](https://github.com/csmart/aeronautes/pull/49) passed all five [PR checks](https://github.com/csmart/aeronautes/actions/runs/34112498074) before protected merge.
All five [master checks](https://github.com/csmart/aeronautes/actions/runs/34113783241) passed on `a27a9c1ac3141023a6b287174a64a90721511b9b`.
The merged tree is exactly `46f24a6708ea2020cdc1aa737fce5fa7a82352a6`, matching the reviewed source, and range comparison preserves all four logical commits without patch changes.
This fixture prerequisite is delivered; the browser and metadata application work is separate.
P23, UI-07, UI-14 and the wider valuable-data release hold remain open.

## Literal browser filtering and navigation

The application slice starts from the delivered fixture master `a27a9c1ac3141023a6b287174a64a90721511b9b`.
Its oracle is the literal Swift `prefix=` contract in DESIGN §16, exact name identity in K-TEST-001 and selection-only authoritative reads in §14.2/K-PERF-002.
The Prefix field no longer appends a slash, and labels remove only the complete directory portion of the filter.
The incomplete final component appears as escaped, quoted text rather than a navigable folder; complete folder components retain their whitespace and empty components remain navigable.
Plain object labels no longer gain an incidental trailing space, while the optional SLO badge keeps its text separator.
The Go adapter and frontend fake exclude only a slash-terminated folder's own object, retaining a plain object that exactly matches an unslashed filter.
Existing page validation, lazy continuation, selection ownership and mutation/transfer admission remain in place.

The preserved browser tests were copied into a new worktree without overwriting newer fixture changes.
`p23-browser-restored.json` records the source, base and four verified test-file hashes.
The final test setup enables CSS processing and proves that the existing object-name `white-space: pre` rule is loaded before checking breadcrumb and filter styles.
The preliminary `ui-before-v1` style failure did not load component CSS and is not itself rendering evidence.
The maintained tests also check the SLO separator and establish that the old selection/detail and page requests are pending before testing their retirement.
The Go test emits only one successful read receipt per top-level action/scenario after every case and both namespace comparisons pass.
Its earlier parked multiple-receipt loop was corrected before any successful implementation run.

With application source still unchanged, the final frontend/helper regression run has 25 assertion failures and 16 passes across 41 cases, with no skips.
The separate bound Go run has ten assertion failures and six passes across sixteen cases on mock Swift and real Swift, with no skips.
The own-folder and root cases already pass after the delivered fixture repair; the five exact unslashed object matches fail on each provider.
`p23-browser-ui-before-v2.json/.log/.exit`, `p23-browser-go-before-v2.json/.exit` and `p23-browser-parent-inputs-v2.json` retain the actual final baseline and exact source/test hashes.
All 41 frontend/helper cases and all sixteen bound Go cases pass after the correction, recorded in `p23-browser-ui-after-v1` and `p23-browser-go-after-v1`.

Mounted tests cover eleven typed prefixes, exact object and folder labels, selection identity, the SLO badge, quoted incomplete components, HTML-like text, spaced/empty breadcrumb navigation, activation, Up, root, page continuation and late page/detail success and failure.
The bound Browse tests cover exact plain names, space-only and trailing-space filters, repeated separators, Unicode, literal percent escapes, folder self-objects, leading empty folders and root.
One-row provider pages and two-row bridge pages must return the exact names, kinds and object byte counts, preserve every raw continuation marker, stop lazily and reach an empty terminal provider response.
Application-only request traces reject any per-row object HEAD; independent observations bypass that trace and compare the complete owned namespaces on both accounts before and after each top-level test.
The HEAD-injection control below fails all sixteen cases, proving that those traces detect the forbidden reads.

Fifteen independent frontend variants are caught in an isolated copy with unchanged tests:

| Broken behaviour | Failing cases |
| --- | ---: |
| Append a slash to typed input | 10 |
| Trim typed input | 9 |
| Remove the incomplete component from leaf labels | 6 |
| Add an incidental label space | 6 |
| Hide the partial filter | 4 |
| Render the filter as a folder button | 5 |
| Interpret filter text as HTML | 1 |
| Trim the filter caption | 2 |
| Collapse breadcrumb spaces | 1 |
| Collapse filter-caption spaces | 4 |
| Drop exact unslashed matches in the fake | 3 |
| Include the folder's self-object in the fake | 1 |
| Publish a stale successful page | 1 |
| Publish stale object detail | 1 |
| Retain the old local page filter | 2 |

The isolated parent also reproduces 25 failures/16 passes, and its positive control passes all 41 cases.
`p23-browser-ui-controls.py` and `p23-browser-ui-controls-v2/` preserve commands, hashes, actual exits and assertion messages.
The first control setup stopped before running tests because a requested source replacement matched both success and error guards; version 2 scopes it to the successful-page guard.
That setup error is not a caught product fault and is not included in the fifteen variants.

Five separate Go overlays are caught with unchanged maintained tests on both providers:

| Broken behaviour | Failing cases |
| --- | ---: |
| Include the folder's self-object | 4 |
| Trim the provider query prefix | 6 |
| HEAD every object row | 16 |
| End after a short provider page | 16 |
| Trim the continuation marker | 8 |

The original Go adapter separately reproduces ten failures/six passes.
`p23-browser-go-controls.py` and `p23-browser-go-controls-v1/` preserve the overlays, commands, actual exits, hashes and assertions.
Every broken variant exits with assertion failures, without build failures, skipped cases, timeouts or race reports; the author source and test hashes remain unchanged.

The full frontend suite passes 788 tests in 21 files, with no failures or skips and 78.02% statement coverage (3,185/4,082).
`p23-browser-frontend-full-v1.json/.log/.exit` and `p23-browser-frontend-coverage-v1.json` retain that evidence.
Svelte checking reports zero errors and warnings, and the production frontend build succeeds.
The complete internal Go race run passes 585 top-level tests, 1,246 leaf cases and all twenty packages, with no failures or skips and 84.7% combined statement coverage.
It uses both owned SAIO accounts, the verified rclone pin and the optional rclone matrix.
`p23-browser-full-v1.json/.exit`, its summary and `p23-browser-cover-v1.out/.txt` retain the actual process and coverage evidence.
`p23-browser-scenario-coverage-v1.json` reconciles that actual exit and event stream against the expected package set and producer environment: valid process, stream and contract, 28/110 covered cells and 39/49 actions still having gaps.
Go vet, application build, the CGO-disabled internal build and whitespace checks also pass.

The unchanged original P23 probes now report two passes and two intended metadata assertion failures; eight other archived cases remain unselected.
The transfer-prefix and browser-prefix probes pass, while metadata `__proto__` retention and own-property diff membership still fail.
`p23-original-after-browser-v1.json/.log/.exit` and its input record preserve that mixed result with only absolute imports adapted to the fixed isolated frontend.
All temporary records above are under `/tmp/aeronautes-remediation-20260905`.
This is CSS-enabled mounted-component and bound Go/provider evidence, not native desktop end-to-end or six-platform functional approval.
Independent local review approved source `6b51743f8501df0e1c2e3ab9d8d0b7b79760c148` after independently running the full Go/frontend suites and its own broken-behaviour controls.
All five [PR50 checks](https://github.com/csmart/aeronautes/actions/runs/34116583674) passed before protected rebase merge, followed by all five [master checks](https://github.com/csmart/aeronautes/actions/runs/34117458281).
Merged master `73dc0c5e7421ccdd36e07e6ac16e9ff5b633e398` has the identical reviewed source tree `3327e0ca311d04a6a1e055a39f4a225e0cc852c1`; the one-commit range comparison is unchanged.
The browser slice is delivered and UI-07 is closed for this defined scope.
P23, UI-14, metadata wire-identity validation and the wider valuable-data release hold remain open.

## Raw metadata-value observations

Before the metadata implementation, a disposable raw HTTP probe sent seven ASCII values through container creation, object metadata POST and container metadata POST on the owned local SAIO account.
These 21 observations bypass Aeronautes and establish provider behaviour only; they are not application regression tests or action-coverage claims.
Each object kept its known body bytes and an unrelated `Keep` metadata sentinel.
The seven uniquely named containers and their objects were removed afterwards, with absence verified.

| Submitted value | Object metadata POST | Container creation and metadata POST |
|---|---|---|
| Empty string | Existing empty value retained | Header absent |
| One or two ASCII spaces | Existing empty value retained | Header absent |
| Leading ASCII space | Space stripped | Space stripped |
| Trailing ASCII space | Space stripped | Space stripped |
| Two interior spaces | Preserved | Preserved |
| Ordinary text | Preserved | Preserved |

The [container metadata contract](https://docs.openstack.org/api-ref/object-store/#create-update-or-delete-container-metadata) defines an empty value as removal and advises UTF-8 then URL encoding for values.
Aeronautes currently passes values as raw headers and compares returned header values; this probe observes that existing representation and does not rule out other encodings.
An implicit codec change could reinterpret existing percent-bearing metadata and needs its own compatibility contract.
The Go 1.27.1 transport source separately calls [`textproto.TrimString`](https://go.dev/src/net/http/header.go) when writing headers; its [ASCII-space definition](https://go.dev/src/net/textproto/textproto.go) includes space and tab.
The transport rejects the remaining ASCII control bytes, including CR/LF and DEL, as invalid field values.
These sources support checking raw-header values before mutation without generalising the ASCII-space probe to Unicode whitespace.

The planned admission guard must retain empty object values, refuse empty container values, refuse boundary ASCII spaces/tabs and invalid header control bytes, and preserve supported values exactly.
Removing a container key remains an explicit omission from the complete working copy.
Maintained bound tests on both mock Swift and SAIO must separately prove refused calls emit no mutation, leave both owned namespaces unchanged, and preserve supported object/container values and unrelated DLO metadata.
Mounted editor tests must keep rejected input visible and send complete metadata maps, including JavaScript inherited-property names, without silently discarding invalid names.
Duplicate empty-key rows with nonempty values need an explicit duplicate result because the existing empty-string sentinel cannot represent that error.
The dictionary and Go admission fixes remain separate reviewed commits and will be delivered together; neither this probe nor the plan closes UI-14.

The script and actual observations are `/tmp/aeronautes-remediation-20260905/p23-metadata-value-probe.py` and `p23-metadata-value-observations.json`.

## Metadata working-copy regressions

The maintained helper and mounted object/container editor regressions establish the frontend part of the approved metadata plan.
`metadataIdentity.test.ts` mounts the real edit panels with `BrowserState`, changes input fields and observes complete requests at the API boundary.
The API stub returns a deliberate rejection so the tests can assert that the entered keys, values and error remain visible and that the editor stays open.
Those stubbed errors test frontend handling only; they do not establish Go admission or provider refusal.
The Go key/value guard and its bound mock/SAIO regressions are a separate implementation, recorded below, that must accompany this frontend slice in joint delivery.

The 25 helper cases cover own string-valued entries, inherited-property names, undefined/non-string entries, case-insensitive diff membership, spelling from the relevant side, literal keys and duplicate detection.
The 40 mounted cases cover both edit panels: complete payloads including `__proto__`, `constructor` and `toString`; additions, changes and visible removals; case-only renames; literal whitespace/empty/Unicode/unsupported keys; escaped HTML-like text; empty values; untouched blank rows; and duplicate case variants and empty keys.
Only a completely unused row is ignored.
Duplicate absence now uses `null`, so duplicate empty-key rows with nonempty values cannot accidentally pass either panel's save guard or disappear from the error display.
P06/P07 editor baseline, lifetime and late-result ownership remain unchanged.

On the unchanged parent, the final maintained setup reports 41 assertion failures and 24 passes across 65 cases, without skipped tests.
`p23-metadata-ui-before-v2.json/.log/.exit` preserves that result, and `p23-metadata-ui-parent-inputs.json` records the parent source and final test hashes.
The earlier v1 had the same counts; v2 makes the absent duplicate-message assertion report the missing text explicitly.
After the four production-file changes, `p23-metadata-ui-after-v1.json/.log/.exit` reports all 65 passing.

Seventeen independent broken variants each fail maintained assertions with actual exit status 1:

| Broken behaviour | Failing cases |
|---|---:|
| Lose `__proto__` in an ordinary output object | 5 |
| Trim entered keys | 10 |
| Drop empty keys with nonempty values | 7 |
| Change saved key spelling to lowercase | 39 |
| Trim entered values | 23 |
| Detect duplicates case-sensitively | 5 |
| Include non-string values | 1 |
| Include inherited properties | 7 |
| Compare base keys case-sensitively | 16 |
| Compare working keys case-sensitively | 18 |
| Use inherited-property membership for additions | 14 |
| Use inherited-property membership for removals | 9 |
| Hide the duplicate-empty-key message | 2 |
| Admit duplicate empty keys in the object panel | 1 |
| Admit duplicate empty keys in the container panel | 1 |
| Drop the complete object working copy | 17 |
| Drop the complete container working copy | 17 |

The isolated positive control passes all 65; the parent replay fails the same 41.
The control script checks for actual assertion failures, complete test selection, no skipped cases and unchanged author source/test hashes, then restores its isolated copy to the fixed source.
`p23-metadata-ui-controls.py` and `p23-metadata-ui-controls-v1/` retain all nineteen executions, including the passing positive control separately from the seventeen broken variants and parent.

The full frontend run passes 849 tests in 22 files, without failures or skips, and reports 79.01% statement coverage.
`p23-metadata-frontend-full-v1.json/.log/.exit` and `p23-metadata-frontend-coverage-v1.json` retain the actual results.
The locked offline install, Svelte check with zero errors/warnings, production build and whitespace check also pass.
The original archived P23 probes now report four selected passes and eight unselected cases, with only absolute imports redirected to the isolated fixed frontend.
`p23-original-after-metadata-ui-v1.json/.log/.exit` and its input record retain that selection and source hashes.
All records named here are under `/tmp/aeronautes-remediation-20260905`.
This frontend slice adds no Go action-coverage claim or native desktop functional evidence.
Independent local review approved the frontend commit `98bdac7fd0bf86c771d1501890901e116c06e8bd`, independently running all 849 frontend tests, the check/build and a prototype-loss control that fails five cases.
The Go admission review and all five PR/master checks on joint delivery remain required; UI-14, P23 and the wider valuable-data release hold remain open.

## Metadata admission and provider round trips

The Go slice applies one private validator at object metadata update, container update and container creation before any write.
It rejects empty or non-ASCII HTTP-token keys, underscores and case duplicates without changing the input map.
It also rejects boundary ASCII spaces/tabs and invalid HTTP control bytes, and rejects empty container values because Swift interprets them as removal.
Supported values remain literal, including empty object values, interior spaces/tabs, percent text and Unicode whitespace.
Explicit omission still removes a key from the complete working copy.
Provider length/count limits, special metadata families, fingerprints, capabilities and manifest policies retain their existing behaviour.

The bound tests use the actual mutation services through both mock Swift and SAIO.
An application-only proxy traces every non-GET/non-HEAD request; fixture setup and independent namespace snapshots use the direct provider URL.
For each refusal, the tests require an error, no outgoing write and unchanged complete owned primary and secondary namespaces.
This prevents an applied write followed by a verification error from being counted as safe refusal.
The 124 refusal cases cover 20 object inputs and 21 inputs for each container operation on each provider.
The eight round-trip cases cover plain and DLO metadata updates, container updates and container creation on both providers.
They verify exact supported metadata, explicit removal, empty object values, known body bytes, content headers, DLO manifests/segments, unrelated objects, container metadata families and the secondary account.
Each successful call must send exactly one intended POST or PUT.
Creation additionally checks the returned detail DTO against the independent namespace; it does not fabricate an `applied` outcome absent from that API.

With only the maintained harness tests added to unchanged parent `98bdac7f`, 58 of 132 cases fail intended assertions and 74 pass, without skips.
The 58 failures are 18 object-update, 20 container-update and 20 container-create cases; all eight supported round trips already pass.
The remaining 66 refusal cases were already blocked by the HTTP transport.
After the guard, all 132 bound cases pass without skips, and 23 validator cases separately cover map preservation, supported token punctuation and all disallowed header control bytes.
`p23-metadata-go-before-v1`, `p23-metadata-go-after-v1` and `p23-metadata-unit-v1` retain actual JSON events, process exits and summaries.

Sixteen independent broken variants each fail maintained assertions with actual exit status 1:

| Broken behaviour | Failing cases |
|---|---:|
| Omit object validation | 18 |
| Validate objects only after writing | 18 |
| Omit container-update validation | 20 |
| Validate container updates only after writing | 20 |
| Omit container-create validation | 20 |
| Validate container creation only after writing | 20 |
| Allow underscore keys | 14 |
| Allow case duplicates | 7 |
| Allow an empty key | 7 |
| Omit token validation | 5 |
| Omit control-byte validation | 1 |
| Allow boundary ASCII whitespace | 34 |
| Allow empty container values | 5 |
| Refuse supported empty object values | 4 |
| Treat Unicode whitespace as removable HTTP whitespace | 9 |
| Refuse supported interior tabs | 9 |

The positive control passes all 155 selected bound/validator cases; the parent-entry-point replay fails the same 58 of 132 bound cases.
The control script checks complete selection, actual assertion failures, no skips/build errors/panics/races/timeouts and unchanged author source/test hashes.
`p23-metadata-go-controls.py` and `p23-metadata-go-controls-v1/` retain all eighteen executions and their Go overlay/source hashes without modifying the author files.

The full mock/SAIO internal race run with pinned rclone passes 590 top-level tests and 1,401 leaf cases in 20 packages, without failures or skips, at 84.9% statement coverage.
`p23-metadata-full-v1.json/.exit`, its summary and `p23-metadata-cover-v1.out` retain the evidence.
Reconciliation uses the actual test exit, complete JSON stream, 20 expected packages and the same producer environment, including the configured SAIO endpoint.
`p23-metadata-scenario-coverage-v2.json` is valid and credits 32/110 required cells, with 38/49 actions still having gaps.
The earlier v1 report omitted the SAIO environment during reconciliation and is retained for diagnosis, not cited as the configured-provider report.
The four newly credited cells are object metadata refusal, container-update happy/refusal and container-create refusal.
Successful container creation is tested but its happy cell remains open because the current receipt requires an actual applied outcome and creation returns a detail DTO.
No action inventory, required scenario or receipt-credit rule is weakened.
Go vet, application-package build, CGO-disabled internal build and whitespace checks also pass.
The approved frontend source is unchanged from the 849-test, check/build evidence above.
All records named here are under `/tmp/aeronautes-remediation-20260905`.
The implementation evidence above preceded the independent Go review and combined delivery recorded below.

## Metadata delivery

Independent local review approved the frontend working-copy commit and the Go admission commit separately, including independent full-suite runs and broken-behaviour controls.
Both commits were delivered together through [PR51](https://github.com/csmart/aeronautes/pull/51), following transfer PR48 and browser PR50.
All five [PR checks](https://github.com/csmart/aeronautes/actions/runs/34124455916) passed on source `0fae42120e90552f61f89f18a6b95726e13c4f11` with the current master incorporated before the protected merge on 7 September 2026.
All five [resulting master checks](https://github.com/csmart/aeronautes/actions/runs/34125252087) passed on `f012521a1976ab8e7d9c0c0bf995231c69b5671c`.
The source and merged tree are both `73f9ffce99fe7292f6ee77e04c55fe9a4598c524`; the browser-delivery note, metadata plan, frontend and Go commits retain four equivalent logical patches after the rebase merge.
This closes UI-14 and P23 for the defined literal transfer, browser and metadata-input contract.
It does not close backend plan authority, durability, outcome accounting, external-writer races, metadata-only replication, native-platform validation, accessibility or the valuable-data release hold.
The scenario result remains 32/110 required cells, with 38/49 actions still having gaps and no invented container-creation applied outcome.

The temporary `/tmp/aeronautes-remediation-20260905` files referenced above were no longer present when this delivery status was recorded after the handoff.
Their test counts and control results describe the earlier author and independent review runs, not a fresh rerun or a currently available raw-log archive.
The maintained tests and committed review records remain available; GitHub job status and Git tree/patch identity were rechecked for this delivery record.
