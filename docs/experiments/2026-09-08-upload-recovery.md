# Local upload recovery faults — 8 September 2026

This investigation resumes P12/P21 after the P23 delivery record.
The application source is unchanged from master `f012521a1976ab8e7d9c0c0bf995231c69b5671c`; the worktree head `00332020d9a4b9be` adds only the approved metadata-delivery documentation.
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

[ADR 0023](../adr/0023-upload-post-commit-errors.md) proposes a bounded first fix with separate post-commit issues, preserved transfer counts, retained facts and visible partial outcomes.
Broader P12/P13/P21 and valuable-data release gates remain open.
