# Existing download destination hazards

## Scope and oracle

The before probe exercises actual preflight and Start services on immutable source `cafe784aee6aa8f4dd2dfa2c5dca03b8a7f5631a`, the reviewed download-recording implementation integrated with PR71–74.
All 343 committed Go/SQL input hashes remained unchanged during both accepted producers.
The [archived probe](probes/2026-09-09-download-admission_test.go.txt) is byte-identical to the executed overlay, and the [manifest](2026-09-09-download-admission-probes.json) records its source and producer identities.
These are before-fix failures, not passing CI, new scenario credit or valuable-data clearance.

The oracle is complete preflight refusal of an already evidenced invalid destination before any job, item, transfer audit, selected content GET, child invocation or local effect.
Safe absent subtrees and the accepted initial selected-root symlink remain passing controls.
[ADR 0034](../adr/0034-download-destination-admission.md) defines the independently approved bounded correction and conservative descendant-link policy.
This original probe does not exercise contained descendant aliases, mixed selections, permission failures or concurrent replacement.

## Observations

Mock Swift and two-account SAIO each produce nineteen complete case records: thirteen failing safety leaves and six passing controls.
Each producer exits `1`, with its failing parent and no skipped or unrelated failing cases.
The mock package takes 7.897 seconds; SAIO takes 16.734 seconds.
Every case returns an accepted one-item preflight and admits a nonempty job ID on this before source.
Preflight itself changes neither local snapshots nor transfer-record counts.

| Existing fixture | Native file | Real engine file | Directory marker |
| --- | --- | --- | --- |
| Descendant symlink to the owned outside sibling | Writes the complete 55-byte file outside; reports complete and verified | Stages the complete file, then application placement writes it outside; reports complete and verified | Creates the outside directory; reports complete and verified |
| Dangling final symlink | Admits and reads content, then reports partial/conflict | Admits and reads content, then reports partial/conflict with retained staging | Admits, then reports partial/failed |
| Regular file at a needed parent | Admits and reads content, then reports partial/failed | Admits and reads content, then reports partial/failed with retained staging | Admits, then reports partial/failed |
| Selected root removed after picker capture | Recreates the root and places the file | Admits a failed job before child dispatch | Recreates the root |
| Regular file at a marker name | Not applicable | Not applicable | Admits, then reports partial/failed |
| Safe absent subtree, control | Correct full bytes and expected directories | Correct full bytes and expected directories, no stage residue | Expected directories |
| Initially selected root symlink, control | Correct full bytes in the selected directory | Correct full bytes in the selected directory, no stage residue | Existing selected directory remains unchanged |

The real engine observation runs after its synchronous Copy return and records the complete selected payload in the private stage.
The later snapshot records that payload in the owned outside sibling after parent-side placement.
This identifies the application's placement as the observed escape; it does not claim that the child itself followed the descendant link.
All mutations occur inside disposable owned fixtures, including the deliberately distinct outside sibling.

## Evidence and limits

Each case retains the report, Start result, actual public and stored job, every item identity and observation, item totals, terminal audit and transfer-record counts.
The counts cover jobs, recovery rows, item identities, item observations and transfer audit events.
The probe compares public item pages with the retained records and waits for the job body and account pin release before its final observation.
Both complete owned remote namespaces retain their independently observed content and modelled metadata, with no remote PUT, POST or DELETE.
Complete local snapshots retain full file bytes and hashes, types, modes and symlink targets across the selected, outside and unrelated paths.
They do not model inode identity, timestamps, ACLs or extended attributes.

The first mock attempt failed because its sandbox could not open the local test listener and produced no case records; it remains excluded from application evidence.
The accepted producers use the same probe, overlay, runner, pinned rclone 1.74.3 bytes and Go 1.27.1 toolchain.
The SAIO producer started after the separate integrated full suite had finished successfully.
The archive validator checks producer exits, all cases, immutable source and probe hashes, full local byte/digest consistency and the expected provider-equivalent typed outcomes.
Its successful exit establishes evidence consistency only; it does not turn the failed product assertions into passes or supply independent approval.
Raw streams, full case records, excluded setup attempt and validation results are retained outside the repository.

This is Linux amd64 evidence with an injected backend picker.
It does not certify native dialogs, Windows/macOS filesystem behaviour, child process-group quiescence, crash/reopen behaviour or physical durability.
The current retained-record reads use fresh services over the existing database; they do not simulate a restarted application.
Stable root capabilities, raced ancestors and all wider release gates remain open.

## Disposable replay

Create a separate checkout at the exact immutable source above and add the inert probe through a Go overlay mapped to `internal/systemtest/p18_admission_probe_test.go`.
Set `AERONAUTES_P18_EVIDENCE_DIR` to a new empty directory outside the checkout so every case retains its full observations.
With the project's owned two-account Swift fixture, verified rclone pin and normal integration environment available, run the two focused producers sequentially:

```sh
go test -json -race -count=1 -parallel=1 -timeout=10m \
  -overlay /tmp/aeronautes-destination-overlay.json \
  -run '^TestP18ExistingAdmissionMock$' ./internal/systemtest
go test -json -race -count=1 -parallel=1 -timeout=10m \
  -overlay /tmp/aeronautes-destination-overlay.json \
  -run '^TestP18ExistingAdmissionRealSwift$' ./internal/systemtest
```

Record the real producer exit and both streams; the expected before-source result is `1` for each command.
Do not invert or suppress the safety failures, and do not reuse an earlier evidence directory.
The maintained implementation must replace these failures with whole-plan refusal assertions and retain the positive controls under its separately reviewed contract.

## Maintained correction

The shared [destination policy](../../internal/transfer/local/download_destination.go) opens one inspection root, checks that directory, and inspects existing relative components without following the final entry.
Preflight collects the complete selection's problems before Start can admit a job.
The executor checks the same complete plan after logical validation and before choosing a native temporary name, admitting an item or starting an effect, including for direct unrecorded callers and empty plans.
An existing descendant link is refused even when its target remains inside the selected directory.
The initially selected root alias, ordinary existing directories, new subtrees and the root marker remain accepted controls.
The Windows classification uses actual reparse attributes and distinguishes missing children from ambiguous path and network errors.
The temporary inspection root closes on every path, with closure errors retained.
This correction does not change the later placement, recording or cleanup protocols or establish authority through their check/use intervals.

The maintained [service matrix](../../internal/systemtest/download_destination_test.go) runs forty-six cases on each owned provider.
Its mixed selections place a valid item first and two distinct invalid ancestors later, requiring both problems to be reported and the complete Start to be refused.
It covers the original hazards, contained and deeper aliases, root files, existing directories, marker collisions, empty plans and the accepted controls.
The [queued cases](../../internal/systemtest/download_destination_queued_test.go) hold the actual first child's completed stage at a bounded barrier, admit a second valid job, change its destination, then release the first job.
The second job must remain honestly failed with its planned counters and start/terminal audits, but no admitted items, selected content read, child dispatch or local effect.
The holding job retains its complete bytes and exact successful records in a separate owned directory.
Orderly history reopening preserves those facts and a historical rerun remains refused without contacting Swift.
This is controlled queue ordering and orderly reopening, not crash or concurrent-path authority evidence.

The [direct executor matrix](../../internal/transfer/local/download_destination_test.go) exercises three routes, seven hazards and both recorder modes, with a safe item before the invalid member.
It requires no report, content request, engine call, recorder/progress callback or local delta for all forty-two refusals.
Six empty/root-marker cases preserve the distinction between an existing directory and an unavailable root.
Eleven [native permission cases](../../internal/transfer/local/download_destination_unix_test.go) separately witness directory-open, acquired-root inspection and child-inspection denial before testing the application boundary.
All eleven executed on the local Linux host without skips; a host unable to produce the specified error is explicitly reported as unsupported rather than counted as a pass.
Synthetic [Windows metadata/error cases](../../internal/transfer/local/download_destination_windows_test.go) are cross-compiled for both architectures, but have not been executed on Windows.

The existing progress regression now introduces its competing file from the first admission callback, after the new entry check, and retains its exact copied-file, byte, conflict and callback assertions.
The existing interruption regression replaces a marker-name collision already visible at entry with a witnessed native directory-creation denial, retaining the original failed-marker-then-stage-error, partial-job and audit oracle.
Both providers' complete interruption groups pass eighteen named tests without skips, including the two actual permission denials.
These fixture corrections preserve failures after admission; the new destination cases separately prove refusal of hazards visible before it.

## Before/after and broken controls

The byte-identical original inert probe was replayed against the implementation candidate without changing its assertions.
Mock Swift and real Swift each pass all nineteen leaves plus their parent and package, with no failures or skips and nineteen complete saved records.
All twenty-six former refusal failures now have unchanged full local snapshots, both remote namespaces and record counts, with no selected GET or child return.
All twelve original positive controls retain their complete bytes, permitted directories, exact selected identities and public/durable job, item and audit facts.
The mock package takes 8.563 seconds and real Swift takes 22.842 seconds.
Their raw streams have SHA-256 `e730f425279ab4a716e8757536453af9eff7beabd884384616533c2ebc889a9b` and `3521b757b5d311fb6f31794af9ef8af7d500595f87dfb33180479b76a2339756` respectively.
All 657 candidate files and 351 Go/SQL inputs remained unchanged during that replay; the later interruption-fixture and Windows test-constant corrections are separately recorded.

The focused maintained candidate passes 159 named tests, including the three existing progress names, with no failures or skips.
Nine private Go overlays then deliberately change only the relevant production guard, preserving the candidate files and every raw failing producer.
Every producer exits `1` for exactly the expected leaves and their parents; ordinary controls still pass.

| Deliberate change | Failed leaves | Passing control leaves |
| --- | ---: | ---: |
| Restore the prior preflight and executor bodies; new helpers remain compiled but unused | 125 | 28 |
| Bypass preflight destination inspection | 24 | 12 |
| Bypass executor-entry inspection | 57 | 4 |
| Follow descendant final entries instead of inspecting the link | 6 | 18 |
| Treat a child permission error as absence | 3 | 14 |
| Omit inspection of the acquired root | 1 | 16 |
| Omit marker inspection | 8 | 6 |
| Stop collecting problems after the first refusal | 6 | 6 |
| Refuse the accepted initial root alias | 6 | 6 |

The previous-body overlay is a controlled regression comparison, not a claim that the entire checkout equals the historical source.
The permission controls distinguish root acquisition from acquired-root inspection, including the empty-plan case that would otherwise bypass all child inspection.
The contained alias uses a relative `.` target so following it would actually remain inside the selected directory; the test does not rely on an unrelated escape refusal.
These results provide no new scenario-catalog credit or broader filesystem clearance.

## Complete validation and retained failures

The first complete producer retains its real exit `1`: 2,846 passing names and four failing names, comprising the two interruption leaves described above and their parents, across nineteen passing packages and one failed system-test package.
Its scenario reader receives that actual failed exit and retains an invalid report with zero credited cells, withholding system-test claims from the failed package.
The first focused local producer separately preserves the two old progress-fixture failures and their parent.
Neither failed result is reclassified as successful evidence.
The first Windows test compilation also remains failed evidence because the test used two constants not exported by `syscall`; the corrected test obtains those constants from the already required `golang.org/x/sys/windows` package.

The corrected cross-build producer passes all six internal build configurations and both affected test packages for Windows and macOS on amd64 and arm64, fourteen checks in total.
No foreign-platform binary was executed.
The supplementary producer passes frontend checking/build/coverage with 971 tests, Go vet, a native production build, root/build-identity/command tests, the no-CGO internal build, Swift source verification and binding generation.
All 657 source inputs, including 95 frontend files and 22 generated bindings, remained unchanged during that producer.
Its local binary has SHA-256 `f65eabddba0ff86001a0e8526003b290711e1e9eadd0827189fc850fc5320256` and was observed reporting version `dev`, source revision `fea942e0c1443788b16f0dbb005399060ed48c65` and dirty source state.
That identity attributes this precommit local build only and is not a released or reproducible artifact claim.

The corrected complete race/coverage producer exits `0` with 2,850 passing names, 748 top-level names, twenty passing packages and no failures or skips.
Its exact inventory contains all 2,694 prior names plus 156 maintained additions; only the unchanged source's generated one-hour expiry name is mapped using each producer's parent and child event timestamps.
Both optional local engine-matrix and keychain tests actually pass.
All 351 Go/SQL input hashes remain equal to the tested candidate, and the only changes after the failed complete producer are the two identified test-file corrections.
The system package takes 650.542 seconds and the application package takes 146.668 seconds, with 7,667 of 8,786 statements covered, reported as 87.3%.
The complete raw stream has 13,753 events and SHA-256 `94331709ec6a72f04a5ea6c03c0dc391b0349d31481836b28788a11147992a5e`.
All thirty-six scheduled groups and 123 serial system groups remain accounted for under `-parallel=3`; the four open JSON records reflect the source-proved slot-release/report ordering, not four simultaneous workers.
The actual successful scenario reader retains schema three, a complete valid stream, contract hash `6c1e4d947471b8b5`, forty of 116 required cells and thirty-eight actions with gaps.
Its incomplete coverage remains unchanged and grants no new scenario credit.
Independent implementation review and protected delivery remain required.
Native platform acceptance, stable selected-root lifetime, concurrent path replacement, exact-file publication, cleanup ownership and every wider valuable-data gate remain open.

## Protected delivery

The candidate record above retains its original pending-review status and producer identities; the subsequent review and delivery are recorded here.
Independent local review approved the contract and implementation, the direct Windows test dependency declaration and the checked-download delivery documentation as separate logical changes.
The final integrated range was reviewed again after incorporating the delivered sensitive-label changes on master `9e7bb1e9d154ed63fb1bd611521af6dfe6c3bf8e`.
That review passed a fresh 2,876-name race suite in twenty-two packages plus the test-less mock command, with no failures or skips, along with 986 frontend tests and the required build, binding, module and cross-compilation checks.
Its Go test inventory equals the earlier independent implementation review.

[PR77](https://github.com/csmart/aeronautes/pull/77) merged at 20:56:03 UTC on 9 September 2026 through the normal protected rebase operation.
The reviewed source was `dffe875e0cb5890d8c485085f787484ef4e0b597`; the resulting master is `4bd4937e97ff08d35881054e2b92388ca2215cde`.
Their tree is exactly `efed326a2eeec7d6c8f04a70d9e844968d78069d`, and all four raw patches, complete commit messages, 659 tracked-file hashes and 351 Go/SQL hashes match.
The four logical commits remain separate.

All five required jobs and all 67 actual steps passed on attempt one in both the [PR run](https://github.com/csmart/aeronautes/actions/runs/34394382263) and the [master run](https://github.com/csmart/aeronautes/actions/runs/34403997565).
There was no workflow retry or reused execution.
Every PR job checked out merge-ref `65735fb842eedcad4deb6175d4871e1f6719cd37`, whose tree equals the reviewed source; every master job checked out the resulting master above.
The final premerge checks verified the current base, exact approved head, empty remote review discussions, all required successful checks and the strict ruleset with no bypass actors.

| Required job | PR duration, seconds | Master duration, seconds |
| --- | ---: | ---: |
| Go formatting, vet, build and race tests | 737 | 878 |
| Frontend checks, build and tests | 87 | 71 |
| Real-Swift integration | 1,366 | 1,343 |
| Built desktop smoke | 294 | 271 |
| Aggregate refusal gate | 6 | 6 |

Each integration stream has 13,740 events and exactly equals its retained scenario artifact event stream.
Both have 2,850 named outcomes: 2,848 passes and only the two source-witnessed optional skips, `TestRcloneMatrix` without its matrix variable and `TestKeychainRoundTrip` without an available desktop keychain.
Their corresponding local full-suite executions remain recorded passes.
The complete PR/master inventories match, with 748 top-level names, twenty passing packages, all thirty-six scheduled groups and 123 serial system groups.
They retain every prior PR75 name and outcome plus exactly 156 passing additions; the only generated-name mapping uses the unchanged one-hour expiry source and each stream's actual parent/child timestamps.
All eleven native inspection-denial cases and both post-admission directory-creation denials executed and passed; the former explicitly logged UID 1001 and the actual permission errors.

The PR/master system packages took 1,057.509/1,037.815 seconds, and their whole integration suite steps took 1,318/1,294 seconds.
The existing twenty-minute package and twenty-five-minute job limits were unchanged.
Reported combined statement coverage is 87.3% on the PR and 87.2% on master; source and terminal inventories match, but the cause of that measured difference is not established here.
Both scenario reports remain valid but incomplete at forty of 116 required cells, with thirty-eight actions carrying gaps and contract hash `6c1e4d947471b8b5`.
The report checkout identities match their respective producers, and no new scenario credit or stronger concurrency claim is inferred from overlapping event records.

Both frontend runs pass all 986 tests with zero Svelte errors or warnings.
Each smoke run connects, browses and copies through the verified pinned engine, observing three destination objects and preserving five screenshots and the request log; its assertions do not compare payload bytes.
Full byte and collateral checks remain the separately attributed maintained local/service evidence above.
The Go and integration jobs report Go 1.27.1, while both smoke builds report Go 1.27.0.
Each aggregate executes all nineteen negative refusal controls.

The final author binary has SHA-256 `26393570719680bbc3b61fe1ff5cfb91fd169405063d778af050977471dc83e0` and was observed reporting `dev`, revision `dffe875e0cb5890d8c485085f787484ef4e0b597` and clean source.
It was a plain Go build with VCS stamping and without `-trimpath`; the independent Wails production build reported the same identity but produced different bytes.
These are source-attribution observations, and the original dirty candidate binary above keeps its own historical identity.

The complete PR/master integration raw logs have SHA-256 `0815732e660d46a6288ca79120e5d25a31ecce037f925573e97da1b787594bd9` and `5c334369b614de5ffb59d893d320f15fcdc7b0371834e477e47653576fd53dc6` respectively.
Retained scenario ZIPs have SHA-256 `6059ed29005afdcdfefff0760432fd7f29c4e4412b8efb436a1df698fac15340` and `90716ebe591df7e82ff3255da98abce68ea725482aae26420811c0344a90c5cf`; smoke ZIPs have SHA-256 `2164e77ead3be15e8457b41aa8d79b300851613ee26b0bf3c6f340c3bff07b07` and `9120532e2f75a40a5b4fcf6e35d924fc2983cbe70cd9854cba7ad6448fcb79e0`.
Full raw inventories, physical job identities, checksum readbacks and source/patch comparisons remain preserved with those artifacts.
This delivers only the ADR 0034 existing-state admission correction; stable root lifetime, concurrent replacement, publication/cleanup authority, complete path identity, native platform acceptance and the valuable-data release hold remain open.
