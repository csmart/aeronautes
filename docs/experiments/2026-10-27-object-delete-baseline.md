# Replacement scenario-preservation baseline for the object deletion

[ADR 0092](../adr/0092-exact-object-deletion.md) admits `MutateService.DeleteObject` to the scenario contract as a destructive action, and [ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed required set only through its drift route: candidate evidence from the candidate's own run, in which the unchanged admitted baseline refuses the candidate's snapshot, a replacement built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract decision.
This record is that evidence and the baseline built from it.
It replaces the [verify action's baseline record](2026-10-27-verify-action-baseline.md) as the current admission; that record stays as the history of the 52 pairs this one keeps.

## Candidate source and run

The candidate is branch `object_delete_impl` at `b3313ff899757ea9c93b2d6a6febaa13623e545e`, the draft pull request 258 on master `ff3f01380c901d56ee51b79be7fbdbd5e00e0ae1`, carrying the contract row and the unchanged admitted baseline.
Its CI run is [40516591112, attempt 1](https://github.com/csmart/aeronautes/actions/runs/40516591112/attempts/1), a pull-request run, so the reports record the merge commit `8a19728b6a6c8d472889d4c636307d481bd46eb1` and Go `go1.27.3`; the baseline identity carries those recorded values.
Jobs: frontend `122231932018`, Swift heavy `122231932218`, smoke `122231931670`, Swift main `122231932442` and Go `122231932068` succeeded; the integration join `122246759808` failed with join exit 1, "scenario preservation refused", and the aggregate `122247095510` failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

`scenario-preservation.json` (SHA-256 `ccbf830e65a233fc584a97f665c0362d459c9bc93537d761bd904d38a1238267`) reports `preserved: false` with no errors.
It compared against the admitted baseline of 27 October, SHA-256 `2844cb572a36a1a5f1625cd42086908f7d790a9d39173d405b5f3d29c86e383f`.
Its required-set drift names exactly one identity, `MutateService.DeleteObject`, absent from the baseline and present as a destructive action requiring `happy`, `refusal` and `conflict`.
Its missing-protected list is empty: every one of the 52 previously protected pairs is credited by the candidate's main report.

## Producer reports

Both reports are valid, record contract hash `9a97c56252343063`, 55 actions and 125 required cells, and the heavy report credits nothing, as before.
The main report credits 55 cells across 22 actions, with 33 actions still carrying gaps; `coverageComplete` is false.
`MutateService.DeleteObject` is credited for `happy`, `refusal` and `conflict` by `TestObjectDeleteMock`, `TestObjectDeleteSwift`, `TestObjectDeleteRefusesManifestsAndLinksMock`, `TestObjectDeleteRefusesManifestsAndLinksSwift`, `TestObjectDeleteConflictIsRecordedMock` and `TestObjectDeleteConflictIsRecordedSwift` in `internal/systemtest`.

| Input | SHA-256 |
| --- | --- |
| Main `scenario-coverage.json` | `e774fadc7772b14f896cd70b1d4a286218743f2f0fd2ee8eef83a5231db17089` |
| Heavy `scenario-coverage.json` | `7cdd225726f4ca7ce1ce6cdf543000ddbcbb13c9b4e9114ed35e09a06004f32f` |
| Main `receipt.json` | `df0a6b44ac93d8bae98ce6a8d1656b93b28ca13ea0a605d39da6217d968865ae` |
| Heavy `receipt.json` | `474e598283721eaaaf923548ec65433351ae821b1de1cf10349076e1d348c0d5` |

Both 28-byte `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b`.

| Official artifact | ID | Hosted expiry (UTC) |
| --- | --- | --- |
| saio-tests-main | 12740932187 | 2027-01-25 13:57:01 |
| saio-tests-heavy | 12739394471 | 2027-01-25 13:57:01 |
| scenario-coverage | 12741552195 | 2027-01-25 13:57:01 |
| saio-diagnostics-main | 12740539122 | 2026-11-03 14:23:46 |
| saio-diagnostics-heavy | 12739474536 | 2026-11-03 14:07:16 |

The private retained packet is `/home/csmart/code/.aeronautes-work/20261027-release-plan/candidate-40516591112`: 49 files sealed by a `SHA256SUMS` manifest (SHA-256 `079b844914cb43a6acd201ed9b6db16e838741e2756b1ea5668e2aa45218c848`), holding every artifact above, the replacement baseline as built, the builder `build-baseline.py` and its output.

## The replacement baseline

The [baseline](../../internal/scenariocontract/preservation-baseline.json) was built from the two reports by `build-baseline.py` in that packet, which validates them with the join's own reader against the candidate identity, refuses any loss of a previously protected pair, and writes the candidate's complete snapshot, its contract hash as recomputed from the rows, every previously protected pair and every pair the main report credits, and the digests of the four producer files.
It carries the run identity above, the four evidence digests above, 55 actions and 55 protected pairs: the 52 of the verify action's admission and three added, `MutateService.DeleteObject` `happy`, `refusal` and `conflict`, all credited by this candidate for the first time.
The join accepts the file against its own rows and protected set.

## Admission limits

The candidate identity is a pull-request merge commit, not a protected-source commit; the route uses it because the unchanged baseline had to refuse the candidate's own snapshot.
The next exact-head full CI run must pass every required check, including preservation against this baseline, before the protected merge, and the resulting master producer evidence is reconciled and retained afterwards under the routine rules.
A valid incomplete report passes preservation; 33 actions still carry gaps and nothing here claims a complete behavioural matrix.
The capability the action belongs to stays deferred until the operator returns it, and no native desktop journey, security, release or valuable-data hold is closed by this record.
