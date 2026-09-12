# Copy item records: observe completion before release

## Scope and oracle

The Copy item-record scenario can exhaust its binding-release check while Copy is still running and correctly retaining both credential bindings.
The controlled reproduction below establishes that test-ordering defect; it does not identify the cause of the historical `TestCopyItemRecordsMock/failed_401` intermittent.
This first commit records the evidence and proposed correction for independent review before changing a maintained test.

The source is master `3c5f7f7fe61bedc8199c72e8439400181643058a`.
All 782 archived original files stayed byte-identical during the probe; the only source addition was the private test archived [here](probes/2026-09-12-copy-terminal-wait/copybind_phase_probe_test.go.txt).
The [JSON evidence twin](probes/2026-09-12-copy-terminal-wait/evidence.json) records actual producer exits, command flags, complete named-test inventories, timings and hashes of the retained raw files.
It is a derived summary, not a substitute for the original event streams or a claim of a new execution.

[ADR 0015](../adr/0015-credential-bindings.md#generations-sessions-and-jobs) requires admitted work to retain its captured generation through the entire job body and cleanup.
At this source, `copyItemRecordsRun` calls `StartCopy` and immediately calls `copyCompletionAwaitPins` (`internal/systemtest/copy_item_records_test.go:117–121`).
The release helper acknowledges a released binding only when a bookmark save succeeds, with a separate 30-second deadline per account (`copy_completion_test.go:301`).
The existing `Harness.AwaitJob` waits up to 90 seconds for a terminal state (`harness.go:633`); upload item records already wait for completion before checking release (`upload_item_records_test.go:130`).
`failed_401` means 401 failed names, whose source GETs receive HTTP 403; it is not an authentication-401 scenario.

Terminal state and binding release remain separate observations.
`finishCopy` removes cancellation registration before its terminal transaction; that registry operation does not release pins (`internal/app/transferservice.go:406`).
The job body still has deferred executor cleanup, and the runner releases the slot and pins after the body returns (`internal/app/jobrunner.go:90–111`).
A terminal row alone therefore cannot acknowledge release.
Persistence-failure tests intentionally use the successful-save check when a terminal row cannot be written, so their shared helper must not acquire a terminal-state prerequisite.

## Bounded reproduction

The [earlier investigation](2026-09-11-copy-binding-release.md) separated delayed launch from a suppressed release but explicitly did not observe terminal publication.
This follow-up records actual public nonterminal and terminal states without instrumenting production body entry or return.
It preserves every original 401-name identity, record, audit, recovery, collateral and orderly-reopen assertion in a private copy of that scenario.

The owned mock server sends HTTP 200 headers and the first byte of its actual 69,144-byte source listing, then holds the remaining response body for 35 seconds.
The unchanged native idle timeout is 60 seconds (`third_party/ncw-swift/source/swift.go:499`), and this is a body hold rather than a header stall.
The expected fatal release assertion runs in a child test while the parent keeps the fixture alive, releases and joins the owned response, observes completion, acknowledges both saves and runs the original assertions.
That permits observation after the real failed assertion, but does not reproduce the original failing test's premature fixture teardown.

Each process ran one selected mock case, with Go 1.27.1, rclone 1.74.3, offline readonly module inputs and race detection.
The command shape below preserves all original Go flags; `<go-1.27.1>`, the private source directory and cache locations are explicitly normalised placeholders for the separately hashed original command records.
The producer's outer containment was 240 seconds; no process reached it.

```text
<go-1.27.1> test -race -count=1 -mod=readonly -json -parallel=1 -p=2 -timeout=150s ./internal/systemtest -run <selector>
```

| Process | Selector suffix | Go exit | Process / package seconds | Named / leaf terminals | Events |
| --- | --- | ---: | --- | --- | ---: |
| Baseline setup | `TestCopyItemRecordsMock/failed_401` | 1 | 3.819 / 0.014 | 2 failed / 1 failed | 38 |
| Permitted baseline | `TestCopyItemRecordsMock/failed_401` | 0 | 13.624 / 13.114 | 2 passed / 1 passed | 381 |
| Completion first | `TestCopyBindingPhaseProbe/terminal_first` | 0 | 52.005 / 49.796 | 2 passed / 1 passed | 396 |
| Release check first | `TestCopyBindingPhaseProbe/guard_first` | 1 | 50.458 / 49.954 | 3 failed / 1 failed | 405 |
| Retained test pins | `TestCopyBindingPhaseProbe/retained_pin` | 1 | 45.710 / 45.212 | 3 failed / 1 failed | 404 |

The exact anchored selectors are in the evidence twin.
Each test and package has exactly one terminal event; all 1,624 events parse with complete streams and valid lifecycles, and there are no skips.
All five Go stderr files are empty; the event streams retain setup and intended assertion failures.
The four useful runs have no unexpected assertion, panic, race, timeout or cleanup failure.

With the release check first, its unchanged guard expires after 30,002.522ms while public state remains `running`, with no finish timestamp and both saves refused.
The body hold releases after 35,000.936ms; terminal `partial` is observed at 47,665.599ms from the probe origin, and saves acknowledge at 47,777.862ms and 47,796.724ms.
With completion first, the same hold lasts 35,000.547ms; terminal `partial` is observed at 46,749.806ms, followed by acknowledgements at 46,780.067ms and 46,799.879ms.
The latter 30.261ms and 50.072ms intervals include polling, scheduling and save latency, and are not exact unpin durations.

The retained-pin control acquires one extra test-owned `PinGeneration` lease for each account on the actual fixture store.
Copy reaches terminal `partial` at 13,133.049ms, but both saves still refuse and the unchanged release guard fails after 30,002.854ms.
Only releasing those extra pins permits the later saves.
This proves that checking completion first retains sensitivity to missing release; it does not remove or demonstrate a defect in the production deferred unpin.

All four useful runs reach the unchanged original outcome assertions: 401 exact failed names, one refused source GET per name, 402 recorded items, one copied and verified engine control, one exact partial audit and one retained staging record.
The owned mock response and public-state observer are joined, and every extra test pin is released.
Those observations do not establish quiescence for every child, transport or cancellation path.

## Preserved unsuccessful preparation and limits

The first baseline fails before fixture construction because the command sandbox denies its loopback socket (`listen tcp6 [::1]:0: socket: operation not permitted`).
Its 38-event stream and actual exit 1 remain retained, with no regression or negative-control credit; the permitted baseline is a separate execution.
The primary lifecycle, phase and source assessor exits 0.
The supplemental parser first exits 1 because it incorrectly requires `NamesInExistingJobAuditSummary` to be empty; the existing test merely records that field and permits an example failed name in the job error.
Both parser versions and their actual outputs remain retained, and the corrected parser exits 0 without inventing that assertion.

An exploratory single-event parser also exits 1 with an unterminated-string error because Go splits a long observation across multiple `Output` events; concatenating the complete stream recovers the valid original observation.
Initial `git show`, `rg` and `sed` reads used nonexistent guessed paths, and `command -v rclone` found no PATH entry before the explicit pinned binary was selected.
Their failed inspection outcomes are retained as notes from tool results, not presented as separately captured process transcripts or product failures.
Source archiving and formatting succeed, and the sealed private packet's 925 indexed files were rehashed before producing this public twin.

Terminal observations poll at 20ms, while the existing completion helper polls at 100ms.
The origin precedes fixture construction; no marker directly measures production body entry, return or the exact terminal transaction instant.
Private producers are serial within this task, but host idleness and competing host work were not measured.
Cancellation, real Swift, other native platforms, full-suite contention and current-base CI remain outside this evidence.
The vanished historical intermittent remains unclassified, and one passing unheld sample does not close it.

| Retained identity | SHA256 |
| --- | --- |
| Byte-identical archived probe | `f703fe53d2aae1de7e3620835e74a8b321aefb74aa961e22e41ed8e35fa6883b` |
| Derived public evidence twin | `0e3692b6b93f574548589c6ba20b5602cb0816f2477b695d406a132ceb4af4a5` |
| Sealed private report | `1085266a31985111a46da738db95bbb34ad3f4b68aaca5062df43ef6c48e8e99` |
| Sealed private index | `4d159fc213aaa7bd05beb48e8c975fee58a38d6d74937ea5bc0c08ced9d6205f` |

## Reviewed correction sequence

First review this evidence and the bounded sequence in DESIGN §65.11.
Then add only `h.AwaitJob(id)` before the unchanged `copyCompletionAwaitPins(t, h)` in `copyItemRecordsRun`, with a short explanation that work can outlast the separate cleanup wait.
Keep all four original case sizes, all outcome and collateral assertions, and the successful-save release predicate.
Keep both individual budgets unchanged: 90 seconds for terminal state and a fresh 30 seconds per account for release.
The combined possible observation window becomes larger; this is an explicit consequence of separating the phases, not a claim that total waiting time is unchanged.

Run the four affected maintained mock cases with `-race` and retain their exact output, exits and source identity.
Reuse the sealed held-body and retained-pin controls according to their actual scope; do not claim that they ran a corrected maintained file or add all three long private probes to every full suite.
Obtain independent review of the test correction before integration, followed by current-base verification and all five protected PR/master checks under the delivery owner.
Do not change production, the shared completion helper, persistence-failure tests, workflow budgets or an audit disposition in this slice.
No new product contract, historical-issue closure, DS-04 closure or valuable-data clearance is implied.

## Maintained correction and evidence

After independent approval of the preceding contract, `copyItemRecordsRun` now calls the existing `h.AwaitJob(id)` before its release check.
The only Go additions are that call and a why-comment; removing those two lines restores the complete original file byte for byte.
All original case sizes and assertions remain, and the shared completion helper and production code are unchanged.
The corrected file SHA256 is `485a3fd3c4d6526baf485f66f2d27197c6c8bb2098cacce2b64a7cb4fccf5184`.

The [maintained evidence twin](probes/2026-09-12-copy-terminal-wait/maintained-evidence.json) binds the actual corrected-file execution to parent `6b4e5e50a199293f7497246a3cd221faf6a60738`, its captured two-line diff and all 785 unchanged producer inputs.
The command uses the same Go flags and pinned binaries as above, with selector `^TestCopyItemRecordsMock$`, a 150-second Go limit and 240-second outer containment.
It exits 0 in 26.487 seconds; the package passes in 22.658 seconds.
All 587 events form complete valid lifecycles: one package, one top-level test, five named tests and four leaves pass, with no skips and empty stderr.
The four leaf times are 1.63, 1.62, 6.58 and 11.81 seconds for 0, 1, 201 and 401 refused names respectively.
The unchanged per-name source-refusal observations also reconcile, and formatting and diff checks pass.
No setup, producer, parser or test failure occurs in this maintained run; the unsuccessful before-proof attempts remain recorded separately above.

The held-body and retained-pin controls are reused from the exact earlier immutable-source proof, whose production and shared helper are unchanged.
They are not relabelled as fresh corrected-file executions.
No full-suite load, real Swift, cancellation or other-platform result is inferred from these four mock leaves.
The same scenario also serves the real-Swift variant, so its current-base integration remains a delivery gate for the owner.
Independent implementation review and protected PR/master delivery are still required; the historical failure remains unclassified and the valuable-data hold stays in place.
