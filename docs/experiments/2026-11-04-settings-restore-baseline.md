# Replacement scenario-preservation baseline for the settings restore

[ADR 0098](../adr/0098-settings-backup-and-restore.md) adds the restore half of the settings backup, and [ADR 0011](../adr/0011-typed-service-surface.md) records its bridge surface: one new scenario-contract row, `BackupService.ImportSettings`, a mutate requiring `happy` and `refusal`.
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [backup-export baseline](2026-11-03-backup-export-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly that one new row, and no previously protected pair is lost.
The candidate credits the new action for `happy` and `refusal` — the two pairs added here — by its own system test.

## Candidate source and run

The candidate is branch `settings_restore` at `9831cb06bcd3cce95c5f34fbc963f39e270a5111`, draft pull request 293 on master `7065b466c811c746db4b4f927ef6f01f4ef71b63`, carrying the contract row and its coverage.
Its CI run is [41782003381, attempt 1](https://github.com/csmart/aeronautes/actions/runs/41782003381/attempts/1), a pull-request run, so the reports record the merge commit `ff6dce799a6b440665609122135d3328edede546` and Go `go1.27.3`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `3bb7e2e442c8beffb6e4f161f1a89f048c62baaeec5e9a53eb2df8c5c1da2bb7`, naming exactly the one new identity `BackupService.ImportSettings` as required-set drift with an empty missing-protected list.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity: it refused any loss of a previously protected pair, kept all 78, and added the two new pairs, reporting the expected required-set drift of `BackupService.ImportSettings`.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `2e5700b792f1701b`, 61 actions.
The main report credits the two new pairs among its cells; `coverageComplete` is false, with gaps remaining on other actions, and the heavy report credits nothing extra.
`BackupService.ImportSettings` is credited for `happy` and `refusal` by `TestRestoreSettingsAppliesBackup` in `internal/systemtest`, which exports a configuration and restores it onto a fresh local-database store (happy) and drives a headless service with no dialog (refusal).

| Input | SHA-256 |
| --- | --- |
| Main `scenario-coverage.json` | `68c1b9fd121ba0d016cf652270ac4ba52549abe375e5ed47b218ef85b7eebfb1` |
| Heavy `scenario-coverage.json` | `f34923f7c60d4bb17be7c460f187fe3773ee6f7816d27a735650d1286c454c56` |
| Main `receipt.json` | `b1b9831284c2c01fbd6e85e6f6d4ed4f988737fe31972ccd4535f4ffa507a011` |
| Heavy `receipt.json` | `92fdc449b14fcb3cbf3ff5e24b5c5173e564db66da05a06cbb9eeaa3bf4b6e34` |

Both `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b` and record test and tee exits of zero.

The private retained packet is `/home/csmart/code/.aeronautes-work/20261104-settings-restore/candidate-41782003381`: the downloaded producer artifacts, the replacement baseline as built, the builder `build-baseline.py`, sealed by a `SHA256SUMS` manifest.

## The replacement baseline

The [baseline](../../internal/scenariocontract/preservation-baseline.json) carries the run identity above, the four evidence digests above, 61 actions and 80 protected pairs: the 78 of the backup-export admission and two added — `BackupService.ImportSettings` for `happy` and `refusal`, credited here for the first time.
Its contract hash is `2e5700b792f1701b` and its SHA-256 is `507e368352f61e5b58eb2ac8502654fb3b7d7aa7f9cb19d91ce95ad061156346`.

## Admission limits

The candidate identity is a pull-request merge commit, not a protected-source commit; the route uses it because the unchanged baseline had to refuse the candidate's own snapshot.
The next exact-head full CI run must pass every required check, including preservation against this baseline, before the protected merge, and the resulting master producer evidence is reconciled and retained afterwards under the routine rules.
A valid incomplete report passes preservation; actions still carry gaps and nothing here claims a complete behavioural matrix.
