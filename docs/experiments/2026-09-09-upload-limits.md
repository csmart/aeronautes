# Destination chunk admission for local upload

This is the bounded DS-11 part of P21, governed by K-XFER-004, DESIGN §25 and the destination `/info` contract.
The baseline is master `33503e0e3c35704731dc86d4d5b1a9338846286d`.
The original [audit probe](../audits/2026-09-05/probes/app_audit_test.go.txt) and [DS-11 finding](../audits/2026-09-05/data-safety.md#ds-11--medium-local-upload-bypasses-the-destination-chunk-size-cap) remain unchanged.

## Before the change

The original `TestAuditUploadChecksDestinationChunkLimit` fails on the baseline: `StartUpload` accepts `8Mi` against an advertised 1 MiB maximum, completes the four-byte upload and records `8Mi`.
The maintained app regression also finds that Copy sends `1048576b` to its engine for an unset chunk on this cluster, while the durable job records an empty chunk setting.

The system reproduction uses the real app services, checked SQLite records, the checksum-pinned rclone 1.74.3 child and independent snapshots of both owned accounts and the local source directory.
A loopback proxy advertises `max_file_size:1048576` for the destination while forwarding its storage operations to the mock or the owned SAIO accounts.
It records write methods, paths, body lengths and user agents; it does not impose an artificial storage rejection or change the provider's actual object limit.
For Copy, the source retains its normal capabilities, so the small limit belongs specifically to the destination.

On both providers the baseline admits an explicit `8Mi` upload and sends one 1,048,704-byte stage PUT.
It also admits an explicit `512Ki` upload and writes three segments.
With no requested chunk, upload again sends one oversized stage PUT, while Copy correctly sends two segments but records an empty chunk setting.
The Copy above-maximum and below-minimum refusal controls pass.

## Shared admission and recording

Upload preflight/start and Copy preview/start share the existing explicit-size checks and one default resolver.
Explicit sizes below 1 MiB or above the advertised destination maximum refuse before engine construction and job creation.
The default is recorded explicitly as `5Gi`, capped to an advertised smaller positive maximum.
An absent or unavailable `/info` retains the standard default and explicit minimum-size validation.
The existing Copy behaviour of honouring an advertised maximum below 1 MiB for an unset default is retained; an explicit below-minimum choice remains invalid.

The engine and job record receive the same resolved settings value.
Copy returns the settings used for engine construction to its caller, avoiding a separate uncapped resolution while recording the job.
Copy overwrite refusal, Mirror start refusal, checked upload records, staging/commit policy and retained recovery authority remain in force.

## Verification

`TestDestinationChunkSettings` covers ten cases for each of upload and Copy, including invalid explicit sizes, equality at the maximum, capped and standard defaults, a smaller-than-minimum cluster limit, missing limits and unavailable capability discovery.
It compares every exposed settings field with the injected engine options and checks that refusals leave no engine call, job, recovery row, item or observation.
Copy and upload preview admission are checked alongside start.

`TestDestinationChunkSettingsMock` and `TestDestinationChunkSettingsRealSwift` each cover upload and Copy above-maximum refusal, below-minimum refusal and a capped default.
Refusals require no job, recovery, item, observation, transfer audit or remote write, with unchanged owned-account and local-source snapshots.
The accepted 1,048,704-byte fixture must become a DLO whose two segments contain the exact original bytes, with actual rclone PUT sizes of 1,048,576 and 128 bytes.
Its visible settings must record `1048576b` and match the persisted SQLite value.
Existing destination objects, other-job staging, unrelated segments, the other account and unselected local files are collateral sentinels.

| Producer | Result |
| --- | --- |
| `audit-before-v2` / `audit-after-v1` | Original DS-11 assertion fails on the baseline and passes after the fix |
| `settings-before-v3` | 14 expected failing leaves and six passing controls |
| `system-before-v3` / `saio-before-v1` | Four expected failing leaves and two passing controls per provider |
| `focused-after-v1` | 36 named tests pass across app and mock system packages, including credential-binding and per-job settings regressions |
| `saio-after-v1` | All six scenario leaves pass, nine named tests including parents |
| `app-after-v1` | All 368 named app tests pass under the race detector; no failures or skips |
| `mutant-admission-v1` | Bypassing semantic admission fails all four refusal leaves; both default controls pass |
| `mutant-default-v1` | Removing the default cap fails both default leaves; all four refusal controls pass |
| `mutant-record-v1` | Recording requested settings instead of resolved settings fails both default leaves; all four refusal controls pass |

The initial `settings-before-v1` and `audit-before-v1` runs could not open loopback sockets inside the command sandbox and are excluded from behavioural evidence.
`settings-before-v2` predates a test error-variable correction; `system-before-v1` had a test syntax error and `system-before-v2` queried the wrong SQLite column.
The final baseline producers above retain only intended behavioural failures.
Later baseline SAIO replay uses an overlay of the unchanged baseline production files and their engine-construction test caller; every overlay and producer input hash is retained separately from the original audit archives.
The broken variants likewise run from separate overlays and do not change the maintained source files.

The maintained tests run with Go 1.27.1 and `-race -count=1`.
`go vet ./internal/app ./internal/systemtest`, formatting and `git diff --check` also pass.
To repeat the focused tests, configure the two disposable SAIO accounts and pinned rclone as documented by the system harness, then run:

```sh
go test -race -count=1 ./internal/app
go test -race -count=1 -run '^TestDestinationChunkSettings(Mock|RealSwift)$' ./internal/systemtest
```

This establishes chunk admission and truthful chunk settings for the tested service paths.
It does not complete P21's DLO completeness, stronger verification or cleanup gates, and does not establish valuable-data release safety.

## Review follow-up

The producers above describe implementation `58791b8dc7953248cf6c15d948d0cd8e45edd287`.
Independent review approved that implementation after a full 2,521-test race run with no failures or skips, exact inventory reconciliation and further real-rclone probes and broken variants.
It also identified a missing maintained assertion: removing upload preview's admission still let the existing start and Copy-preview tests pass.

The follow-up calls `PreflightUpload` for all ten upload cases, checks the refusal reason or accepted report, and verifies that preview constructs no engine and creates no job.
Removing only the upload preview guard now fails four intended leaves; all sixteen other leaves pass (`mutant-preview-v1`, six failed and seventeen passed names including parents).
The per-harness real-Swift group now calls `t.Parallel()` to join the existing bounded schedule; its cases still run sequentially and the mock group remains non-parallel because its harness changes process environment.
`preview-after-v1` passes all 41 named focused app, mock and SAIO tests under `-race -count=1 -parallel=3`, including the real group's pause/resume.
Formatting, vet and diff checks pass; this follow-up changes only tests and this evidence record.
