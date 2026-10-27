# Replacement scenario-preservation baseline for the selected-set deletion

[ADR 0093](../adr/0093-selected-set-deletion.md) admits `DeleteService.StartSelectedDelete` to the scenario contract as a job action requiring `happy`, `refusal`, `conflict`, `cancel` and `partial`, and [ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed required set only through its drift route: candidate evidence from the candidate's own run, in which the unchanged admitted baseline refuses the candidate's snapshot, a replacement built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract decision.
This record is that evidence and the baseline built from it.
It replaces the [object deletion's baseline record](2026-10-27-object-delete-baseline.md) as the current admission; that record stays as the history of the 55 pairs this one keeps.

## Candidate source and run

The candidate is branch `selected_delete` at `4e63b6a04428363a823a244c2561bdb162f50088`, the draft pull request 263 on master `f93e668e54ad95bb5797c19869bab57ff4cec745`, carrying the contract row and the unchanged admitted baseline.
Its CI run is [40589971369, attempt 1](https://github.com/csmart/aeronautes/actions/runs/40589971369/attempts/1), a pull-request run, so the reports record the merge commit `a929eb7f21d4419b1417f94194ae602e022ed979` and Go `go1.27.3`; the baseline identity carries those recorded values.
Jobs: frontend `122491361979`, Swift heavy `122491361904`, smoke `122491361854`, Swift main `122491361824` and Go `122491361616` succeeded; the integration join `122500409080` failed with join exit 1, "scenario preservation refused", and the aggregate `122501781949` failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

`scenario-preservation.json` (SHA-256 `ee49f0d066ff3bbd240512d7d80c0d250ca8b582edd55c42bd6237fdde62a6b9`) reports `preserved: false` with no errors.
It compared against the admitted baseline of 27 October, SHA-256 `d67ebe1fba16350c39ce186f79d627e0609b86d9c49cf1a02eff27e3792425e7`.
Its required-set drift names exactly one identity, `DeleteService.StartSelectedDelete`, absent from the baseline and present as a job action requiring `happy`, `refusal`, `conflict`, `cancel` and `partial`.
Its missing-protected list is empty: every one of the 55 previously protected pairs is credited by the candidate's main report.

## Producer reports

Both reports are valid, record contract hash `382d2db3b45cddd7`, 56 actions and 130 required cells, and the heavy report credits nothing, as before.
The main report credits 66 cells across 34 actions, 25 of them completely and nine in part, with 31 actions still carrying gaps, the nine partial and 22 with no credited cell; `coverageComplete` is false.
`DeleteService.StartSelectedDelete` is credited for `happy`, `refusal`, `conflict`, `cancel` and `partial` by `TestSelectedDeleteMock`, `TestSelectedDeleteSwift`, `TestSelectedDeleteCancelMock` and `TestSelectedDeleteCancelSwift` in `internal/systemtest`.
The six marker and manifest pairs, `MutateService.DeleteMarker` and `MutateService.DeleteManifest` for `happy`, `refusal` and `conflict`, were first credited on master by [run 40570919192](https://github.com/csmart/aeronautes/actions/runs/40570919192), reconciled and retained as protected-source evidence, and are credited again by this candidate; they enter the protected set here rather than through a separate routine update, so that master did not move under this range.

| Input | SHA-256 |
| --- | --- |
| Main `scenario-coverage.json` | `2389f75714992fe73024fc83f3f01b0995a680a486681a898ed3a213ecbcba72` |
| Heavy `scenario-coverage.json` | `563d2bed40594dbf3d51dadf8e6a91e5e0e186bcf5fc95b2f18405719a66b0ac` |
| Main `receipt.json` | `193c14bb5e2fc5bd366d357e621c3c8e02ab850acb97e97ebd270fa5884d725a` |
| Heavy `receipt.json` | `ec71e87f905175687f5e62512ab104dfa64c963b73b7c2929f2b44be625e2c3b` |

Both 28-byte `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b`.

| Official artifact | ID | Hosted expiry (UTC) |
| --- | --- | --- |
| saio-tests-main | 12775650077 | 2027-01-25 21:49:39 |
| saio-tests-heavy | 12774366767 | 2027-01-25 21:49:39 |
| scenario-coverage | 12775305766 | 2027-01-25 21:49:39 |
| saio-diagnostics-main | 12775610243 | 2026-11-03 22:09:27 |
| saio-diagnostics-heavy | 12774621241 | 2026-11-03 22:02:56 |

The private retained packet is `/home/csmart/code/.aeronautes-work/20261027-release-plan/candidate-40589971369`: 49 files sealed by a `SHA256SUMS` manifest (SHA-256 `7c924eb7db6dfd375185d7cc82886e76ad1bd4ae4e5043f1b5fd0cb4728e351f`), holding every artifact above, the replacement baseline as built, the builder `build-baseline.py` and its output.

## The replacement baseline

The [baseline](../../internal/scenariocontract/preservation-baseline.json) was built from the two reports by `build-baseline.py` in that packet, which validates them with the join's own reader against the candidate identity, refuses any loss of a previously protected pair, and writes the candidate's complete snapshot, its contract hash as recomputed from the rows, every previously protected pair and every pair the main report credits, and the digests of the four producer files.
It carries the run identity above, the four evidence digests above, 56 actions and 66 protected pairs: the 55 of the object deletion's admission and eleven added, `DeleteService.StartSelectedDelete` `happy`, `refusal`, `conflict`, `cancel` and `partial`, credited by this candidate for the first time, and the six marker and manifest pairs named above.
Its SHA-256 is `715b4ed6c03f7a437aae0e4a7cb4a2fc8f9ee8bd0a4399a75bec429000cdff95`, and the join accepts the file against its own rows and protected set: the local run of the join's own check against the candidate's two reports reports preserved with no drift and no missing pair.

## Admission limits

The candidate identity is a pull-request merge commit, not a protected-source commit; the route uses it because the unchanged baseline had to refuse the candidate's own snapshot.
The next exact-head full CI run must pass every required check, including preservation against this baseline, before the protected merge, and the resulting master producer evidence is reconciled and retained afterwards under the routine rules.
A valid incomplete report passes preservation; 31 actions still carry gaps and nothing here claims a complete behavioural matrix.
The capability the action belongs to stays deferred until the operator returns it, and no native desktop journey, security, release or valuable-data hold is closed by this record.
