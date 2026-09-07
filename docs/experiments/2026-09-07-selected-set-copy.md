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
