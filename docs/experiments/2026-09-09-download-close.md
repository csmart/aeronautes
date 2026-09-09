# Native download closure — 9 September 2026

## Scope and oracle

This implements the native placement prerequisite in [ADR 0032](../adr/0032-download-placement-prerequisites.md).
The [original probe](probes/2026-09-09-download-close_test.go.txt) remains unchanged.
Its replay against reviewed staging source `e6ec3404f2b1e5e8d58803381a3555c10b7a615b` reproduces two failing leaves and two passing controls on owned mock Swift and two-account SAIO.
Each fault reads all 21 selected bytes successfully, while an independent reader reports Swift `ObjectCorrupted` only on close.
The old service nevertheless places those bytes and reports a complete job with one copied and verified object, 21 copied bytes and an applied audit.
The captured 321 Go/SQL source hashes and original archived probe identify that before-fix producer.

The oracle is successful required source/file closure before the final no-replace link, independently read complete bytes, exact permitted local/remote effects, and matching public/durable outcomes.
A failure before the link must preserve an existing target or leave it absent.
A successful link remains a copied object even if subsequent cleanup fails.

## Correction

[Native completion](../../internal/transfer/local/download_close.go) checks the copied length, required file sync, file close and source close before attempting the final link.
Both handles receive one checked close attempt even when an earlier read, length or sync check fails.
Source closure is also checked when destination-directory or temporary-file admission fails.
The source reader can reject content on close after a successful read, so `io.Copy` success alone cannot authorise placement.

The temporary pathname receives a checked removal attempt on every path after its creation.
Primary and cleanup errors are joined, preserving both causes.
The helper returns the actual placement fact separately from the error.
[Download execution](../../internal/transfer/local/local.go) retains copied objects and bytes after a successful link and records a later temporary cleanup refusal as a post-placement issue.
The existing local-job completion rules keep that outcome partial without adding a failed object or erasing copied-byte facts.
No schema, frontend DTO, application fault hook or download record-version change is added.

## Maintained effect tests

The [service tests](../../internal/systemtest/download_close_test.go) cover five leaves per provider: ordinary completion, source-close corruption, a target created after preflight, source-close corruption with cleanup refusal, and final-link refusal with cleanup refusal.
Their proxy streams the actual upstream GET without a known response length and, for corruption cases, returns a disagreeing HEAD length.
An independent Swift reader must receive the complete original body and the expected close result.
The fault job must report partial, one failed native object and zero copied/verified objects or bytes; the ordinary control must report complete with all 21 selected bytes.

For the concurrent-target case, the proxy creates the final file during the post-GET length HEAD, after service preflight.
The actual no-replace link must refuse and preserve every original target byte.
For cleanup faults, a bounded fixture barrier holds the streamed EOF after the real upstream bytes have arrived.
The test waits until the owned temporary file contains all 21 bytes and proves the final name is absent before removing write permission from the destination directory.
After EOF release, source closure or the final link fails, temporary unlink also fails, and both reasons must remain in the saved job.
The exact named temporary file is retained with complete bytes; no arbitrary residue is permitted.
Fixture permissions are restored before the complete filesystem comparison and teardown.

Every service leaf compares all public/durable job counters, state and reason, the matching terminal audit and its durable bookmark binding, complete owned local and two-account remote namespaces, and absent item/recovery authority.
Reruns remain refused.
Orderly fresh-process reopening preserves the saved state and exact permitted filesystem delta without contacting Swift or reclaiming residue.
The terminal audit retains the existing coarse `verify_mismatch` result for partial jobs; detailed native errors remain in the job reason, without introducing a new audit vocabulary.
These remote fixtures contain ordinary objects and account sentinels, not shared DLO/SLO backing data.

The [focused filesystem tests](../../internal/transfer/local/download_close_test.go) additionally exercise read, length, sync, file-close and source-close failures, simultaneous primary and close errors, and actual link/unlink permission effects.
They assert that closure occurs before the final name appears and that both handles receive exactly one close attempt.
For the post-link cleanup refusal, this helper fixture uses separate temporary and final directories on the same owned filesystem, allowing a real link while the temporary parent refuses unlink.
It proves actual `SameFile` identity, retained complete temporary bytes and successful-copy accounting beside the cleanup issue.
That is helper/executor evidence; the production native path creates its temporary file beside the target, and this fixture is not a claim of an actual-service post-link permission fault.
The [app completion test](../../internal/app/download_close_test.go) separately checks the reported native post-placement outcome through existing job/audit completion.

## Deliberate broken controls

Twelve production overlays are rejected through the intended maintained behavioural assertions, with seventeen failing leaves and seventeen passing controls.
No compiler failure or skip substitutes for an observed safety failure.

| Broken behaviour | Failing leaves |
| --- | ---: |
| Ignore source-close corruption | 2 service leaves |
| Close the source only after placement | 2 service leaves |
| Ignore file-close failure | 1 filesystem leaf |
| Ignore sync failure | 1 filesystem leaf |
| Omit required sync | 1 filesystem leaf |
| Discard the read error | 1 filesystem leaf |
| Omit the length check | 1 filesystem leaf |
| Replace the final target with rename | 2 service leaves |
| Discard temporary cleanup refusal | 2 service leaves |
| Replace the primary reason with cleanup failure | 2 service leaves |
| Hide a successful copy after cleanup refusal | 1 executor leaf |
| Omit the post-placement issue | 1 executor leaf |

The overlays, raw streams, exits and exact source hashes remain separate from the archived before-fix evidence.

## Verification and limits

The focused producer passes 28 named tests across the executor, app and system packages with race detection and no failures or skips.
The complete `download-native-close-full-v1` race/coverage producer passes 2,480 named tests across twenty packages with zero failures or skips and 86.9% statement coverage.
All 2,453 previous named tests remain alongside 27 new tests; the one existing clock-generated expiry name is checked against each producer's actual timestamps.
The full run captures 325 Go/SQL source hashes at recorded HEAD `856b7ee862a3b3822f57e0ef0798a8c5591c9441`, with the native correction still uncommitted.
Those hashes match the final focused and deliberate-variant inputs exactly.
The system package takes 703.455 seconds with fourteen independent real-Swift groups under the unchanged parallel limit of three and no sequential fixture overlapping them.
Scenario reconciliation accepts the complete event stream, zero producer exit and captured owned SAIO configuration; coverage remains 40/116 cells with 38/52 actions having gaps.
The 1,200-second package and 1,500-second hosted job limits remain unchanged; subsequent PR/master timings are recorded under [protected delivery](#protected-delivery).
Go vet, the native production build, the internal build without CGO, frontend asset build and vendored Swift source/checker tests pass.
All 22 regenerated bridge files are unchanged.
Independent local review and all five PR/master checks subsequently passed as recorded below.

## Maintained review follow-ups

Independent review approved the native correction while identifying two gaps in the maintained tests: a weakened length check accepting extra bytes, and missing source closure after destination admission fails.
The production implementation already refuses the grown object and checks both admission-close paths.
These follow-ups strengthen regression evidence without changing production behaviour.

The [closure test](../../internal/transfer/local/download_close_test.go) now includes an object longer than its planned size and requires refusal, no final file, one close attempt per handle and no temporary residue.
The [admission tests](../../internal/transfer/local/download_admission_test.go) call the actual native download helper through a mock Swift session whose GET response body records close attempts.
They require exactly one close after an existing destination file refuses directory creation or an owned directory's permissions refuse temporary-file creation.
Each path has a successful-close control and a refused-close case; the latter must retain both filesystem and source-close causes.
Existing local file and sentinel bytes remain unchanged, with no admitted temporary or final name.
The counting transport is captured in one sequential test session and the default transport is restored immediately after session creation; no production hook is added.
This is native-helper/transport evidence, not a new actual-service admission or platform certification.

The focused follow-up producer passes sixteen named tests with race detection and no failures or skips.
Five broken overlays accepting growth, omitting either admission close or ignoring either close error are detected in seven failing leaves with eleven passing controls.
The complete `download-admission-full-v1` race/coverage producer passes 2,486 named tests across twenty packages with zero failures or skips and 86.9% statement coverage.
All 2,480 preceding tests remain alongside six new named tests, including checked timestamps for the existing generated expiry name.
Its 326 captured Go/SQL hashes identify the uncommitted test draft at recorded HEAD `0204d81d5b1d1f0ecf26964a506b11e22f4c4aa9`; production code is unchanged.
The system package takes 682.678 seconds with fourteen parallel groups, the unchanged limit of three and no sequential overlap.
Scenario reconciliation remains valid at 40/116 cells with 38/52 actions having gaps, and all release gates remain open.
Independent local review and protected delivery of these follow-up tests remain required.

With the owned two-account SAIO fixture configured and verified pinned rclone available, the focused replay is:

```sh
go test -race -count=1 -parallel=3 -timeout=20m \
  -run '^(TestNativeDownload|TestDownloadClose)' \
  ./internal/transfer/local ./internal/app ./internal/systemtest
```

Length remains the production content-verification mechanism.
Full test-body comparisons do not establish a stronger checksum contract.
Pathname cleanup does not prove stable inode identity or authority against concurrent replacement of a selected temporary path or its ancestors.
Sync and orderly state reopening do not establish physical durability, power-loss recovery or full native-application restart.
Downloads remain record version zero; complete identity history, directory markers, checked record failures, cancellation at every boundary, native approvals and six-platform behaviour remain separate work.
No complete audit package, scenario gap or valuable-data gate is closed.
The [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.

## Protected delivery

[PR 69](https://github.com/csmart/aeronautes/pull/69) delivered the native closure correction and the preceding staging-delivery record after their separate independent local approvals.
All five required jobs passed on reviewed source `0204d81d5b1d1f0ecf26964a506b11e22f4c4aa9` in the [PR run](https://github.com/csmart/aeronautes/actions/runs/34332206256) with current master incorporated.
The normal protected rebase merge completed at 09:36:48 UTC on 9 September 2026 with the exact expected source head and no ruleset bypass.
Initial REST attempts returned server errors and then reported a merge in progress; the documented GraphQL merge succeeded, and a separate authenticated read confirmed the result.
The resulting master `33503e0e3c35704731dc86d4d5b1a9338846286d` passed all five jobs in its [own run](https://github.com/csmart/aeronautes/actions/runs/34335688612).

The source and merged trees both equal `6c0e061d260ba7f9963abb44161709a50d549650`.
Both logical patches are preserved: `e75858cf` became `155879d1`, and the staging-delivery record `0204d81d` became `33503e0e`.
The source branch remained unchanged during delivery.

| Observed run | System-test package | Integration job |
| --- | ---: | ---: |
| PR 69 | 996.537 s | 1,225 s |
| Resulting master | 971.042 s | 1,187 s |

Both complete integration logs contain 2,478 named passes across twenty passing packages, zero failures and the two existing optional skips, `TestRcloneMatrix` and `TestKeychainRoundTrip`.
Their terminal inventories preserve the complete 2,480-test local baseline, with the generated absolute-expiry name checked against each producer's recorded time.
The PR log SHA-256 is `41a5583d08c147f3c21ff317274e5e7d9631158df1e22708efcac3642ea47a51`; the master log SHA-256 is `40c45abeeb0d4d6a0f0792c2c4e747c0f95ce3e323446c40329ab4251a6aa960`.
The unchanged 1,200-second package and 1,500-second job limits leave minimum observed headroom of 203.463 and 275 seconds.
Continue measuring the fixture schedule before adding the separate download recording matrix; these runs do not promise a fixed runtime.

This delivers the bounded native placement/closure correction.
The maintained admission follow-ups and the wider filesystem, verification, recovery, platform and valuable-data release requirements retain their separate evidence and delivery obligations.
