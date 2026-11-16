# Replacement scenario-preservation baseline for deletions that take the store's answer

[ADR 0122](../adr/0122-the-stores-answer-is-the-result.md) decision 6 makes a deletion send each name's DELETE and take the store's answer, so no deletion compares a name with anything and none can end in a conflict.
Decision 11 of that record therefore removes the `conflict` scenario from the six deletion rows of the scenario contract:

- `MutateService.DeleteObject`, `MutateService.DeleteMarker` and `MutateService.DeleteManifest`, which now require `happy` and `refusal`;
- `DeleteService.StartDelete`, `DeleteService.StartSelectedDelete` and `DeleteService.DeleteManifestWithSegments`, which now require `happy`, `refusal`, `cancel` and `partial`.

[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed required set only through its drift route: an accepted decision that names every changed identity and accounts for any protected pair it changes, candidate evidence from the candidate's own run, a replacement baseline built from that run's reports, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [copy destination baseline](2026-11-13-copy-destination-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

Unlike the admissions before it, this one gives pairs up.
All six `conflict` pairs were protected, and ADR 0122 decision 11 names each of them and gives them up on purpose: the tests that credited them drove a refusal that no longer exists.
Every other protected pair is still credited, and no pair is added.

## Candidate source and run

The candidate is branch `store_answer_deletions_candidate` at `38c8ce027a0a8226a3af4368106c51f4883f3b38`, draft pull request 376 on master `2bb5ef44d4e6e40be2470e8f4a30a8b43325ed79`, carrying the decision record, the deletion change and the contract rows.
Its CI run is [43829040045, attempt 1](https://github.com/csmart/aeronautes/actions/runs/43829040045/attempts/1), a pull-request run, so the reports record the merge commit `fc24836897588352a1e1ab64ba2286f0ff9a0fa3` and Go `go1.27.4`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `987cb59396cfe8b2e7839f191ca2213e27e7f84c5954dec358d69a4c7f0aef63`.
It named exactly the six rows above as required-set drift, each losing `conflict` and nothing else, and listed exactly their six `conflict` pairs as missing protected pairs, with no other error.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity.
For this route it takes the list of pairs the decision accounts for, and refuses unless the protected pairs the candidate does not credit are exactly that list and each has left the required set; any other loss stops the build.
It kept the other 86 pairs, gave up the six, added none, and reported the expected required-set drift of the six rows.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `24c93c2fb2a70337`, 71 actions and 148 required pairs, six pairs fewer than the baseline this replaces.
The main report credits 86 pairs, which are the 86 the replacement protects; `coverageComplete` is false, with gaps on 34 actions, and the heavy report credits nothing.

The deletion rows keep their other cells by tests that drive the store's own answers on a real Swift.
A refusal is one a caller can cause, such as an empty name or a wrong typed confirmation.
A partial job is one whose member the store refuses: a proxy in front of the owned Swift answers one object's DELETE with 403 (`TestBulkDeletePartialSwift`, `TestSelectedDeletePartialSwift`).
A cancellation lands at the listing for a bulk deletion and between two members for a selection (`TestBulkDeleteCancelSwift`, `TestSelectedDeleteCancelSwift`).
The `cancel` and `partial` cells of `DeleteService.DeleteManifestWithSegments` were gaps before this change and remain gaps.

| Input | SHA-256 |
| --- | --- |
| main report | `dcecf7ef0d04e447a7bbf43a1e3c9f38fc9d7fefd4e859d576ea95754219552b` |
| main receipt | `ad7186ddce902765c3a6aed96433328a85684fa4f5dcd97cc1d8d496e70e91be` |
| heavy report | `682204355594d271d2fa0e43ea8bfc1b08cd35d68f816b822a24711f03b544dd` |
| heavy receipt | `82b14cbc12c4b94deab929f9f4f760b06defe62cc54b657ec9a482a3274a0c48` |

Both `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b` and record test and tee exits of zero.

The private retained packet is `/home/csmart/code/.aeronautes-work/20261116-store-answer/candidate-43829040045`: the downloaded producer artifacts, the join's preservation diagnostic, the replacement baseline as built, the builder `build-baseline.py` with the list of accounted pairs, sealed by a `SHA256SUMS` manifest.
