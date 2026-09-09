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
