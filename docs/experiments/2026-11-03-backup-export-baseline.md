# Replacement scenario-preservation baseline for the settings backup export

DESIGN §51 and P28 add an operator-facing settings backup, and [ADR 0011](../adr/0011-typed-service-surface.md) records its bridge surface: one new scenario-contract row, `BackupService.ExportSettings`, a read requiring `happy` and `refusal`.
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [audit-export baseline](2026-11-02-audit-export-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly that one new row, and no previously protected pair is lost.
The candidate credits the new action for `happy` and `refusal` — the two pairs added here — by its own system test.

## Candidate source and run

The candidate is branch `backup_export` at `5605258f63cedef6deed67f9550dda42b2c521d6`, draft pull request 292 on master `aa7ba7bab8effcb1b1929b73f747f8e00cae7a1a`, carrying the contract row and its coverage.
Its CI run is [41640321681, attempt 1](https://github.com/csmart/aeronautes/actions/runs/41640321681/attempts/1), a pull-request run, so the reports record the merge commit `aabbfa00532d3170a0c5f24d4cb030d6f0c3a6d3` and Go `go1.27.3`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `720f56aff6a23f61aacc5feace168cd29390c285ad0b7d2006052eeed336588c`, naming exactly the one new identity `BackupService.ExportSettings` as required-set drift with an empty missing-protected list.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity: it refused any loss of a previously protected pair, kept all 76, and added the two new pairs, reporting the expected required-set drift of `BackupService.ExportSettings`.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `4b36aeee9fee9331`, 60 actions.
The main report credits the two new pairs among its cells; `coverageComplete` is false, with gaps remaining on other actions, and the heavy report credits nothing extra.
`BackupService.ExportSettings` is credited for `happy` and `refusal` by `TestBackupSettingsWritesConfiguration` in `internal/systemtest`, which drives the real export against a local-database store (happy) and a headless service with no dialog (refusal).

| Input | SHA-256 |
| --- | --- |
| Main `scenario-coverage.json` | `e8d709ed5b65aca09d0de2e2f43ba3c1858ba329b01673dcadc41e448776ccb4` |
| Heavy `scenario-coverage.json` | `e91df965cb49a740f659d07055a0d75891881560cc3778345ad26084136cb11c` |
| Main `receipt.json` | `114cf1150722a7c6fc99e75a25687b4b980466b4edd950c7481a2c38e42d96e1` |
| Heavy `receipt.json` | `a18e68842c25d82b43c64a78a72d15451f4f4c8a8dd71d472dd23c66ee9b52cc` |

Both `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b` and record test and tee exits of zero.

The private retained packet is `/home/csmart/code/.aeronautes-work/20261103-backup-export/candidate-41640321681`: the downloaded producer artifacts, the replacement baseline as built, the builder `build-baseline.py`, sealed by a `SHA256SUMS` manifest.

## The replacement baseline

The [baseline](../../internal/scenariocontract/preservation-baseline.json) carries the run identity above, the four evidence digests above, 60 actions and 78 protected pairs: the 76 of the audit-export admission and two added — `BackupService.ExportSettings` for `happy` and `refusal`, credited here for the first time.
Its contract hash is `4b36aeee9fee9331` and its SHA-256 is `3bb7e2e442c8beffb6e4f161f1a89f048c62baaeec5e9a53eb2df8c5c1da2bb7`.

## Admission limits

The candidate identity is a pull-request merge commit, not a protected-source commit; the route uses it because the unchanged baseline had to refuse the candidate's own snapshot.
The next exact-head full CI run must pass every required check, including preservation against this baseline, before the protected merge, and the resulting master producer evidence is reconciled and retained afterwards under the routine rules.
A valid incomplete report passes preservation; actions still carry gaps and nothing here claims a complete behavioural matrix.
