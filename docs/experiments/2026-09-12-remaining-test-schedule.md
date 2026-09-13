# Remaining serial test groups

Status: contract and implementation independently reviewed; the combined local run passed, with hosted comparison and protected delivery still pending.
The behaviour oracle is the existing [three-slot CI schedule](../CI.md), unchanged test assertions and cleanup, and each group's private resources.
The inspected source is `dcdef302b2e61d0c0a8607751d4605287a0cff85`, tree `60dd8e19737a4f2fb5afdcc93560418081aa6f11`.
The six candidate files and ten relevant harness, helper and CI inputs match protected master `c098faffeaf160630f538b03639a87cd7f7549aa` byte for byte.

## Before evidence

| Completed run | System package seconds | Integration job seconds | Outcome |
| --- | --- | --- | --- |
| [PR107](https://github.com/csmart/aeronautes/actions/runs/34694374324) | 1,286.811 | 1,569 | All five jobs passed |
| [Master attempt one](https://github.com/csmart/aeronautes/actions/runs/34696109829/attempts/1) | 1,500.249 | Incomplete suite | Package timed out; retain the failed attempt |
| [Master attempt two](https://github.com/csmart/aeronautes/actions/runs/34696109829/attempts/2) | 1,470.197 | 1,840 | All five jobs passed |

The passing master package has 29.803 seconds left within its unchanged 1,500-second limit.
The nine candidate bodies below took 103.40 seconds serially in that attempt; this is work eligible for overlap, not a predicted saving.
The separately pending shutdown mock and Swift outer bodies took 31.88 seconds in local runs; their package totals sum to 33.908 seconds including separate process overhead.
Neither local sum predicts the extra hosted runtime when those tests join the full suite.
The broad slowdown across both serial work and other packages in the failed master attempt has no established resource cause.

The unchanged service source also passed a complete local run starting at 16:08:42 UTC and ending at 16:26:06 UTC on 12 September 2026.
Its command was `go test -race -count=1 -json -parallel=3 -p=2 -timeout=25m ./...`, with Go 1.27.1, the accepted rclone pin, built frontend assets and both accounts in the confirmed disposable Swift fixture.
The producer and its outer command exited 0 without timeout: 1,044.3895 seconds total and 879.732 seconds for the system package.
Reconciliation found 21,718 events, 4,415 named passes, 4,061 passing leaves, one optional `TestRcloneMatrix` skip, 22 passing packages and one package without tests.
All 60 existing outer PAUSE/CONT pairs and the exact 847 input hashes were retained; no started test or package lacks its terminal event.
The source archive SHA-256 is `3f5adc9b00292828eed93bb56385701241ce2c918867981a2db3784c3490e556`, and the event stream SHA-256 is `d4ce8ca7253364f014fb2d8a481b10a296ba94373417378f80980188aace3675`.
This local run lacks CI's `-coverpkg`, predates the pending shutdown and settings changes, and does not measure the proposed schedule or establish hosted headroom.

## Exact scope and isolation

Add one outer `t.Parallel()` to each of these nine existing groups in six files under `internal/systemtest`.

| File | Group | Placement and ownership |
| --- | --- | --- |
| `item_interruption_test.go` | `TestInterruptedItemRecordsReopen` | After the complete child-input branch and its return, before parent temporary paths; private database and child output |
| `item_storage_test.go` | `TestTransferItemStorageReopen` | After the complete child-input branch and its return, before parent temporary paths; private database, triggers and child output |
| `job_items_test.go` | `TestJobItemAccessBeyondRecentJobsAndLargeCursors` | Before harness construction; private database and pagination fixtures |
| `metadata_inputs_test.go` | `TestMetadataInputRefusalsMock`, `TestMetadataInputRefusalsRealSwift` | First outer statement; preserve each shared harness and receipt map's sequential cases |
| `copy_record_faults_test.go` | `TestCopyRecordRefusalMock`, `TestCopyRecordRefusalRealSwift` | Before the existing sequential four-mode loop; per-case database, proxy, barriers and namespace |
| `upload_source_paths_test.go` | `TestUploadSourcePathsMock`, `TestUploadSourcePathsRealSwift` | First outer statement; per-case local selections, picker, proxy and namespace |

The two self-reexecuting child branches must return before the new parallel call.
Existing child selectors, command environments, parent/child deadlines and pin-release joins remain unchanged.
Every inner case stays sequential; no helper or production code is part of the scheduling change.
Each provider harness owns its database, services, cryptographically random namespace and scoped cleanup on both accounts.
Mock servers and proxy listeners are instance-owned, transports are cloned, and no candidate changes process-wide environment or the default transport.
Namespace independence does not remove contention for the shared Swift service, CPU, disk or network.

Exclude both `DeleteSweepAuthority` groups because they depend on `t.Setenv("XDG_CACHE_HOME", ...)`.
Also exclude the pending shutdown groups, the remaining local-interruption pair and every unlisted group.
An isolation refactor or additional concurrency requires its own proposal and review.

## Commit sequence and acceptance

1. Independently review this contract, the retained before evidence and isolation on the exact selected source before any scheduling edit or experiment.
2. Add only the nine outer calls and their documentation.
   Preserve all inner names, cases, assertions, outcomes, byte counts, barriers, retries, individual deadlines and cleanup.
3. Verify the actual combined, independently reviewed delivery source, including the separately approved shutdown consumer if it will ship in the batch.
   Retain three parallel slots, race detection, the 25-minute package limit and all other existing full-suite options; a filtered nine-group run cannot establish package or job headroom.
   Preserve each attempt's source, tools, fixture, command, actual producer/outer exits, raw events and artifact hashes.
   Reconcile every started and terminal test, named and leaf inventory, optional skip, expected package and scenario receipt against the applicable baseline, explaining only reviewed additions and the two existing generated-name substitutions.
   An otherwise unchanged 60-group baseline gains exactly nine PAUSE/CONT pairs; child paths remain unchanged.
4. Review the implementation and complete measurement, then obtain final-base range review and all five current-head/current-base PR checks before protected merge.
   Check all five resulting-master jobs and record both hosted package and whole-job time, including setup, compilation, other packages and reporting within the existing 35-minute cap.

Compare the serial phase before the first top-level CONT, the remaining phase, candidate body times and total package and job duration.
A different source, fixture, machine or coverage configuration is not a controlled timing comparison.
Go's reporting order is not an exact occupied-slot counter; PAUSE/CONT counts do not prove the absence of contention.
Preserve coverage options and advisory floors, producer/tee exit handling, complete JSON and scenario reconciliation, diagnostics, artifacts and aggregate refusals.
The action inventory and scenario vocabulary remain unchanged, and report validity stays distinct from incomplete action coverage.

Stop for diagnosis and independent review if a race, deadlock, resource conflict, leaked child or pin, unexpected effect, failed assertion or cleanup, timeout, incomplete inventory, unexpected skip or report failure appears.
Keep the failed attempt and its limits; do not drop cases, suppress failures, increase timeouts or repeat a run merely to replace failure with green evidence.
A passing run with little remaining time does not establish durable headroom; further changes need a concrete diagnosis and separate review.
Scheduling does not close audit findings or establish native-platform or valuable-data acceptance.

## Combined measurement — 13 September

The nine calls are committed as `e5be8215eb1d58cd26f62333c2cb6bc2dfbb2d40`, tree `ef169a754f6315f3570b941a9e783ae7b8adf2b5`; all 879 captured inputs match that checkpoint.
The same local command passed with 4,496 named tests, one optional matrix skip and 69 PAUSE/CONT pairs in 1,073.4 seconds; the system package took 891.120 seconds against the earlier 879.732 seconds.
The candidate also includes the approved shutdown work and a resource observer, so this is not a controlled speedup comparison; the three real-Swift candidate bodies grew from 11.20/13.69/13.80 to 16.18/16.72/19.37 seconds.
The 1,074 resource samples retain 407 samples with observation issues and maxima of six live owned processes, five descendants and 648,441,856 bytes of summed RSS; sequential sampling can miss short-lived processes and shared pages are counted more than once, with no baseline resource comparison or proof of quiescence.
The independent reviewer passed the nine groups together twice under race against the real fixture and approved delivery; the complete-stream scenario report is valid but remains incomplete at 42/117 required cells and 38 gapped actions.
The inventory reader's two apparent missing-parent errors came from slash-containing sibling names; source inspection resolved that inference without rerunning the suite or changing its failed reader receipt.
Retained private packet: workspace-relative `.aeronautes-work/evidence/20260913-test-schedule`, `SHA256SUMS` SHA-256 `0605e8aab5a38de76389b9e41833966f410de7f46db1c6d0f8c2a9a9397e6b99`.
No expiring CI artifact is the sole evidence pointer for this measurement.
At delivery, record hosted package and job durations; if the resulting master system package does not improve on the last passing 1,470.197-second attempt, revert these nine calls through review and green CI rather than retain an unproved scheduling change.
