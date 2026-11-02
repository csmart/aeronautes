# Replacement scenario-preservation baseline for the audit-log export

DESIGN §§50–51 add an operator-facing audit-log export, and [ADR 0011](../adr/0011-typed-service-surface.md) records its bridge surface: one new scenario-contract row, `AuditService.ExportAudit`, a read requiring `happy` and `refusal`.
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [interrupted-job notice baseline](2026-11-01-interrupted-disclosure-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly that one new row, and no previously protected pair is lost.
The candidate credits the new action for `happy` and `refusal` — the two pairs added here — by its own system test.

## Candidate source and run

The candidate is branch `audit_export` at `b811c76d6e02e653851eecf47863d3506c669e90`, draft pull request 287 on master `2ceb8122dac8db37991e80ddfe52b27e48a74f6c`, carrying the contract row and its coverage.
Its CI run is [41388974121, attempt 1](https://github.com/csmart/aeronautes/actions/runs/41388974121/attempts/1), a pull-request run, so the reports record the merge commit `caffdb56f361bc4d7eb1eab0b1b7cdcfe72dd368` and Go `go1.27.3`; the baseline identity carries those recorded values.
The frontend, smoke, Go race, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `8b2ec93df5ca5ae4d30abefe5ea8d95a821ecf2e982b09d0a8ee4ea753bbca31`, naming exactly the one new identity `AuditService.ExportAudit` as required-set drift with an empty missing-protected list.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity: it refused any loss of a previously protected pair, kept all 74, and added the two new pairs, reporting the expected required-set drift of `AuditService.ExportAudit`.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `6ee45266e3d1d10f`, 59 actions.
The main report credits 76 cells; `coverageComplete` is false, with gaps remaining on other actions, and the heavy report credits nothing extra.
`AuditService.ExportAudit` is credited for `happy` and `refusal` by `TestExportAuditArchivesTheTrail` in `internal/systemtest`, which drives the real export against a local-database store (happy) and a headless service with no dialog (refusal).

| Input | SHA-256 |
| --- | --- |
| Main `scenario-coverage.json` | `1e34f052089269c0d45f114bc3b1b3fb7ef23a944676d0991b093bcf34f42389` |
| Heavy `scenario-coverage.json` | `9a5fbdfa6fd83920f3f7c5add82f97790311e0fb3c1bcea6c890d14f63e21421` |
| Main `receipt.json` | `4217cd2807ca1cb19e1734a97c0189e2860bfca31931ca94c612739771696b18` |
| Heavy `receipt.json` | `cdadba7f68cc137763be59bb261c631d0aea4c6e38a538e429a0b83dc9a868b7` |

Both `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b` and record test and tee exits of zero.

| Official artifact | ID | Hosted expiry (UTC) |
| --- | --- | --- |
| saio-tests-main | 13117741816 | 2027-01-31 04:31:46 |
| saio-tests-heavy | 13117191379 | 2027-01-31 04:31:46 |
| scenario-coverage | 13118041228 | 2027-01-31 04:31:46 |

The private retained packet is `/home/csmart/code/.aeronautes-work/20261102-audit-export/candidate-41388974121`: the downloaded producer artifacts, the replacement baseline as built, the builder `build-baseline.py`, sealed by a `SHA256SUMS` manifest.

## The replacement baseline

The [baseline](../../internal/scenariocontract/preservation-baseline.json) carries the run identity above, the four evidence digests above, 59 actions and 76 protected pairs: the 74 of the interrupted-job notice admission and two added — `AuditService.ExportAudit` for `happy` and `refusal`, credited here for the first time.
Its contract hash is `6ee45266e3d1d10f` and its SHA-256 is `720f56aff6a23f61aacc5feace168cd29390c285ad0b7d2006052eeed336588c`.

## Admission limits

The candidate identity is a pull-request merge commit, not a protected-source commit; the route uses it because the unchanged baseline had to refuse the candidate's own snapshot.
The next exact-head full CI run must pass every required check, including preservation against this baseline, before the protected merge, and the resulting master producer evidence is reconciled and retained afterwards under the routine rules.
A valid incomplete report passes preservation; actions still carry gaps and nothing here claims a complete behavioural matrix.
