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
Correct the fixture first by deriving each child-prefix name and excluding it only when that row does not advance beyond the marker.
Direct HTTP tests must distinguish an own marker from child-group continuation, including markers inside a group or before the filter, while the bound mock/SAIO comparison retains exact child names and unchanged owned namespaces.
This prerequisite changes test fixtures only; the exact-match application defect and the remaining browser/metadata work stay open.
