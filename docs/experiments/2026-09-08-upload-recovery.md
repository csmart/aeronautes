# Local upload recovery faults — 8 September 2026

This investigation resumes P12/P21 after the P23 delivery record.
The initial reproduction used application source unchanged from master `f012521a1976ab8e7d9c0c0bf995231c69b5671c`; its worktree head `00332020d9a4b9be` added only the approved metadata-delivery documentation.
ADR 0020 is the containment oracle: failed persistence reduces authority, uncertainty retains data and records, and a missing fact cannot authorise cleanup.

## Bound-service observations before remediation

The temporary Go overlay adds access to the harness's existing database and a test file; it does not alter application code, provider fixtures, independent comparisons or receipt rules.
The test invokes the actual `TransferService.StartUpload`, job service and recovery status against two owned accounts on both mock Swift and the CI-pinned local SAIO image.
Both providers use the official checksum-verified rclone 1.74.3.
Plain uploads contain 18 known bytes; DLO uploads contain 1,120,000 known bytes with a 1 MiB chunk setting.

SQLite triggers reject only the selected `job_recovery` insertion kind.
A separate HTTP proxy rejects the application's staging DELETE with HTTP 403 before forwarding it; setup and independent snapshots use the direct provider endpoint.
The fault sites are independent, with successful plain/DLO uploads and rejected committing records as controls.

| Case on each provider | Observed live/staging result | Visible job result | Recovery facts retained |
| --- | --- | --- | --- |
| Successful plain upload | Exact live bytes; staging removed once | `complete`, one copied/verified, no error | One staging record |
| Successful DLO upload | Exact assembled live bytes; staging manifest removed once | `complete`, one copied/verified, no error | Staging, captured, committing and committed |
| Rejected committing record | No live object; exact staging bytes retained; no DELETE | `partial`, one transfer failure and visible database error | Staging and captured |
| Rejected committed record | Exact live bytes; staging manifest still deleted once | **Incorrectly `complete`, no visible error** | Staging, captured and committing |
| Rejected plain staging DELETE | Exact live and staging bytes; one rejected DELETE | **Incorrectly `complete`, no visible error** | One staging record |
| Rejected DLO staging DELETE | Exact live and staging bytes; one rejected DELETE | **Incorrectly `complete`, no visible error** | Staging, captured, committing and committed |

All twelve leaf cases execute without skips.
Six fault cases fail as intended, while six controls pass; the two containing top-level tests and package consequently fail with actual process exit 1.
Each case compares local files, the other account and untouched names in the destination account, checks live/staging content through the independent oracle and confirms retained recovery status.
The recovery rows are unchanged after opening and closing a second database handle in the same process.
That is an in-process reopen observation, not a process restart, simulated lost history or physical power-loss result.
No scenario credit is emitted by these exploratory probes.

The old DS-10 claim that successful upload clears these records no longer reproduces: P04 already retains them, including successful-upload facts with no known remote residue.
The remaining reproduced defects are ignored post-commit errors and cleanup after the rejected committed record.
The current tests do not prove transfer-time deletion ownership, audit atomicity or the complete durable identity ledger.

## Evidence and next step

The workspace evidence directory is `/home/csmart/code/.aeronautes-work/20260908`, outside the temporary container filesystem.
`upload-probe-before-v1.json`, `.stderr`, `.exit`, `.inputs.json` and `.summary.json` retain the actual events, exit, selected command, environment and overlay/test hashes.
`upload_fault_probe_test.go`, `harness_probe.go`, `upload-probe-overlay.json` and `run_go.py` retain the executable reproduction.
The command selects `^TestUploadFaultProbe` in `./internal/systemtest` with `-race -count=1 -timeout=10m`, Go 1.27.1, the two-account SAIO endpoint and the pinned engine.
These records describe the current reproduction only; no remediation, independent implementation approval or new pipeline is claimed.

[ADR 0023](../adr/0023-upload-post-commit-errors.md) defines a bounded first fix with separate post-commit issues, preserved transfer counts, retained facts and visible partial outcomes.
Broader P12/P13/P21 and valuable-data release gates remain open.

## Maintained implementation and evidence

The local report now counts post-commit issues independently of its 200-example limit and per-object transfer failures.
A rejected committed record prevents that object's staging DELETE while allowing the existing read-only live verification.
Staging DELETE errors carry the exact attempted name; an authoritative 404 is accepted as absence, and other errors remain unresolved without retry.
The service records a partial job with the issue count, first example and any transfer failure or later terminal reason, preserving copied, verified and byte counts.
The existing jobs drawer displays this message and continues to distinguish recovery-record counts from remote residue and failed transfers.

`internal/systemtest/upload_recovery_test.go` exercises actual picker selection, StartUpload, pinned rclone, job and audit services on mock Swift and SAIO.
The fourteen cases on each provider include successful plain/DLO uploads, committing/committed record refusals, a live HEAD verification mismatch after committed-record refusal, first/last DELETE refusals, 201 refused plain DELETEs, refused DLO cleanup, already-absent responses, landed deletions whose responses report failure and a mixed failed live write plus cleanup issue.
The independent witness compares known logical bytes, the exact two segment names and their byte slices, staging/live manifests, both owned account namespaces, another job's staging sentinel and all selected/unselected local files.
Recovery rows are compared by exact kind, account, container, prefix and live identity, alongside the job counters and exact terminal audit event.
No whole sidecar namespace is exempted from the collateral comparison.

Each case then closes the database with a checked result and starts the compiled test binary as a separate process.
That child opens the database, requires `FailInterrupted` to change zero terminal jobs and returns the job, recovery rows/status and audit events for exact comparison.
The parent's storage proxy observes no new request during reopening, and both remote snapshots remain unchanged.
This is a process reopen after orderly close, not a process kill, full application startup, power-loss or native desktop recovery test.
The terminal audit wait acknowledges the existing separate job/audit writes; it does not claim atomic persistence.

The final parent overlay, `upload-control-parent`, restores both application files from the reviewed pre-implementation head while retaining the maintained tests.
It exits 1 with eighteen fault-case failures and ten passing controls across the two providers; both containing tests fail, with no skips.
The fixed focused run passes all twenty-eight bound cases and nine service terminal-outcome cases, plus the pre-existing upload-retention regression selected by the same pattern.
The service cases separately exercise later error/cancellation and mixed outcomes with a supplied local report; they are not a real child-cancellation barrier claim.
The local report test checks 203 issues, exactly 200 retained examples and unchanged transfer results.
No new scenario credit is emitted by these tests.

Eleven deliberately broken Go variants each exit 1 on their intended maintained assertion: ignored committed-record reporting, cleanup after that refusal, ignored DELETE reporting, truncated issue counts, hidden terminal messages, incorrect failed-transfer counts, lost verified-copy counts, later errors/cancellation hiding an earlier issue, false errors for already-absent staging, erased verification failures and unbounded examples.
Their runs contain no build failures, races, panics or skips; the parent comparison is separate from those eleven variants.
A Vite transform separately hides errors on partial jobs, and the new mounted drawer test fails on the missing visible message; its other five tests are unselected.
The production source remains unchanged by every negative variant.

The same workspace evidence directory retains `upload-control-*.json`, their actual exits and input/source hashes, `upload-controls/results.json`, the overlay sources, `run_upload_controls.py` and the UI transform/log.
The early `upload-maintained-before-v1/v2` and `upload-focused-after-v1` runs are test-development records: they exposed incorrect fixture assumptions about manifest formatting, fully qualified segment names, Swift's sanitised error text and the bookmark DTO.
They are not counted as regression proof; the final parent overlay and passing maintained runs supersede them.

The final full internal Go race run, `upload-full-after-v2`, passes 594 top-level tests and 1,439 leaf cases in all twenty packages with zero skips and 85.1% combined statement coverage.
It uses Go 1.27.1, official rclone 1.74.3, the explicit engine matrix, two-account SAIO and the required private D-Bus protocol fixture.
The initial full run had thirty-seven keychain-case skips because the rebuilt coding container lacked `dbus-daemon`; installing that dependency and requiring the fixture removed those skips in the complete rerun.
Reconciliation uses the actual producer exit, complete event stream and `go list` package set under the same SAIO environment; its valid result remains 32/110 cells with 38/49 actions still gapped.
All 850 frontend tests pass in twenty-two files with 79.01% statement coverage, and Svelte checking reports no errors or warnings under Node 22.23.1.
Frontend production build, Go vet, Linux application build and the CGO-disabled internal build pass; the native build reports upstream GTK deprecation warnings.
The patched Swift source verifier and its fifteen refusal tests also pass with the restored Go environment.

The bounded implementation still needs independent local approval and protected PR/master delivery.
The wider P12/P13/P15/P16/P21, audit durability, exact outcome ledger, native platforms and valuable-data release hold remain open.
