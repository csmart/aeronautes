# P22 selected-set engine experiment — 7 September 2026

This disposable experiment investigates the [mixed short-page omission](2026-09-07-short-page-copy.md) after the native continuation correction.
It does not change repository production code or establish full engine compatibility by itself.
The resulting design is [ADR 0022](../adr/0022-complete-rclone-selection.md).

## Inputs and oracle

The production input is native-continuation commit `1fbcdfb3153050946e6ba699ed215610981d577a`.
Its runner SHA-256 is `98ea549d46a5df20467e786aa4fea95c5ae494fb0ae0693c02e4749d2ae4c90b`.
The test input restores the original complete-Copy assertions from the earlier cap experiment, with SHA-256 `7770d65682d6385deab4585cef8ef07da99a4781bf79b4170aae75011982f25e`.
These assertions require all three selected names and twelve logical bytes, independently observed source/destination contents and unchanged collateral state.
Two names use the actual pinned rclone and one newline-bearing name uses the native route.
Every selected source/destination JSON listing remains capped to one row, including rclone requests.
Both test functions run through the actual bound Copy service, once against mock Swift and once against two-account SAIO.

Only the runner argument changes in the candidate: `copy --ignore-times ...` becomes `copy --no-traverse --ignore-times ...`.
The raw selected-file list, names, provider cap, engines, credentials, assertions and all other production files are identical across the three runs.
The runner is restored after the experiment.

## Results

| Run | Process exit | Mock Copy | SAIO Copy |
| --- | --- | --- | --- |
| Baseline | 1 | Fails: selected staged object missing | Fails: selected staged object missing |
| Add `--no-traverse` | 0 | Complete; all names and bytes match | Complete; all names and bytes match |
| Remove the option again | 1 | Same missing-stage assertion failure | Same missing-stage assertion failure |

The race-enabled command is `go test -race -count=1 -timeout 2m -json ./internal/systemtest -run TestShortListingCopy` in the disposable module.
Actual process exits, both test terminal events and the package terminal event agree in every run.
There are no build failures, skips, race reports, panics or timeouts in these results.
The raw evidence is `/tmp/aeronautes-remediation-20260905/p22-engine-spike-{before,no-traverse,restored}.json`, the matching `.exit` files, `p22-engine-spike-inputs.json` and `p22-engine-spike-results.json`.

The pinned [filtering documentation](https://github.com/rclone/rclone/blob/v1.74.3/docs/content/filtering.md#files-from-read-list-of-source-file-names) and [march implementation](https://github.com/rclone/rclone/blob/v1.74.3/fs/march/march.go) explain why this option avoids the source traversal that omitted the planned tail.
Rclone may still omit a missing selected source without a failing exit, so staging checks and independent completion verification remain necessary.
This small experiment does not cover local callers, larger/nested selections, all object classes, resource bounds or platform materialization.
The ADR's maintained execution regressions, independent review and all five PR/master jobs remain required before P22/DS-13 can be reconciled as complete.
Wider resource and platform evidence stays assigned to the other audit work and release gates.

## Maintained implementation and evidence

After independent design review, `Runner.Copy` adds the single `--no-traverse` option alongside the existing raw selected-file list and `--ignore-times`.
Single-object `CopyTo`, the pin, native routing, gateway, retries, staging/placement and verification code are unchanged.
The capped mixed Copy now requires complete three-name/twelve-byte execution and fresh verification; its uncapped control remains.
The earlier partial result remains in the historical experiment above and in the preserved parent/control logs.

The new `listing_selection_test.go` cases use six selected names, including literal percent/space/Japanese text, a nested name, a newline-bearing native name and a genuine large-object source.
They require six copied and verified names, all thirty-three logical bytes, independent matching destination digests, unchanged source/collateral state and terminal source reads from both planning and fresh verification.
DLO cases run on mock Swift and two-account SAIO; the SLO case runs on SAIO because the mock does not implement SLO creation.
The source witness checks the actual large-object class and two assembled segments before Copy.

The new `local_listing_test.go` cases invoke the bound picker, preflight and Start methods using the existing `WireFilePicker` seam and picker-issued tokens.
Uploads select four files from two local directories, including an empty file and literal names, with an unselected sibling and outside sentinel.
Downloads select four objects across short pages, including a nested path and an empty file, into an owned directory with existing and outside sentinels.
Both require four copied and verified files and twelve exact bytes through the actual pinned runner on mock Swift and SAIO.
Independent filesystem snapshots record names, file types/modes, byte lengths, SHA-256 digests and link targets; the download admits only the expected new files and one directory, and upload leaves the local tree unchanged.
Both Swift namespaces are independently observed, including before/after the read-only preflight.
These small owned Linux fixtures do not establish the separate cross-platform materialization, alias, cancellation or durability guarantees.

The final focused tests use this selection across the runner and system-test packages:

```sh
go test -race -count=1 -timeout 3m -json ./internal/transfer/rclone ./internal/systemtest \
  -run '^Test(CopyHygieneAndContract|ShortListing(MixedCopy|DLOCopy|SLOCopy|Upload|Download)|NormalListingMixedCopy)'
```

On unchanged parent production, eight assertions fail and four controls pass, with no skipped cases or setup failures.
Five failures identify missing staged remote names, two identify missing downloaded files, and one identifies the missing runner option.
The passing controls are the two uncapped mixed Copy cases and the two upload cases.
With the one option added, all twelve cases pass.
Independently removing only that option in a disposable copy of the final maintained source reproduces the same eight assertion failures and four passes.
Actual process exits and test/package terminal events agree; build failures, skips, panics, race reports and timeouts are excluded from this negative evidence.

The raw focused evidence is `/tmp/aeronautes-remediation-20260905/p22-engine-maintained-before-v2.json/.exit`, `p22-engine-maintained-after-v1.json/.exit` and `p22-engine-no-traverse-control.json/.exit`.
`p22-engine-control-inputs.json` records final source/test hashes; `p22-engine-controls.py` and `p22-engine-control-results.json` preserve the one-option control and its assertion identities.
The preliminary `before-v1` tests also assumed that DLO planned-byte counters included logical content; inspection showed that the current counter still uses the zero-byte listing value.
The final tests explicitly retain that existing estimate while requiring all logical bytes at execution and in the independent destination witness.
For these six-name fixtures, DLO jobs plan twenty-seven bytes and copy thirty-three; SLO jobs plan and copy thirty-three.
That planned-byte accounting gap remains P13 work and is not fixed by changing the engine's selected-set traversal.

The full internal race suite with two-account SAIO and the optional pinned-engine matrix enabled passes 541 top-level tests and 1,119 leaf cases across twenty packages, with no failures or skips and 84.3% combined statement coverage.
Raw evidence is `p22-engine-full-v1.json/.exit/.cover` under the same temporary evidence directory.
Valid scenario reconciliation reaches 28/110 cells, with 39/49 actions still having gaps; the new invocation receipts cover upload/download preflights and successful jobs without changing the renderer inventory.
The scenario report is `p22-engine-scenario-coverage-v1.json`, reconciled with the producer's SAIO environment.
An initial reconciliation omitted that environment and reported its configuration as false; `p22-engine-scenario-coverage-without-env.json` preserves that metadata error separately from the unchanged passing test stream.
`go vet ./internal/...`, the internal build with CGO disabled, formatting, and the Swift source gate pass; the latter verifies thirty-eight upstream files with the existing patches and all fifteen checker-refusal tests.

The enabled observational matrix still reports eleven of sixteen engine-list names exact, the same five unsafe-name omissions, a three-MiB DLO with one captured prefix, and missing user metadata.
The enforced Copy executor, local transfer and encoding/large-object suites pass with the existing native routing and metadata handling.
The matrix's successful exit is not approval to send the five unsafe identities through rclone or to rely on it for metadata preservation.
P22/DS-13 delivery still requires independent implementation review, all five current-head/current-master PR checks, protected merge and all five resulting master checks.
The wider valuable-data release hold remains.
