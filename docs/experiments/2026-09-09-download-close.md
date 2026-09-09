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
The 1,200-second package and 1,500-second hosted job limits remain unchanged; actual PR/master timings still require observation.
Go vet, the native production build, the internal build without CGO, frontend asset build and vendored Swift source/checker tests pass.
All 22 regenerated bridge files are unchanged.
Independent local review, protected PR delivery and all five resulting master checks remain required.

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
