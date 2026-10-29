# Replacement scenario-preservation baseline for the bulk and segment deletions

[ADR 0095](../adr/0095-prefix-deletion-by-the-per-name-protocol.md) decision 9 changes two scenario-contract rows: `DeleteService.StartDelete` becomes an explicit job action requiring `happy`, `refusal`, `conflict`, `cancel` and `partial`, with `retry` dropped because a deletion job never retries a destructive request, and `DeleteService.DeleteManifestWithSegments` moves from a destructive action to a job action requiring the same five, with `retry` dropped for the same reason.
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed required set only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract decision.
This record is that evidence and the baseline built from it; it replaces the [selected-set deletion's baseline record](2026-10-27-selected-delete-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly those two rows: `retry` leaves both required sets, and `DeleteService.DeleteManifestWithSegments` changes kind from destructive to job, with no previously protected pair missing. The previously protected pairs for these two actions, `DeleteService.StartDelete` for `happy`, `partial` and `refusal` and `DeleteService.DeleteManifestWithSegments` for `refusal`, stay credited by this candidate, and the candidate also credits `DeleteService.StartDelete` for `conflict` and `cancel` and `DeleteService.DeleteManifestWithSegments` for `happy` and `conflict`.

## Candidate source and run

The candidate is branch `adr0095_impl` at `0b62f7df5ac16d977e223d6a9d62fe8c5f1eba1b`, the draft pull request 266 on master `feac812abe909357277516a926d60a0e578a97f2`, carrying the contract rows and the unchanged admitted baseline.
Its CI run is [40787014308, attempt 1](https://github.com/csmart/aeronautes/actions/runs/40787014308/attempts/1), a pull-request run, so the reports record the merge commit `c721c64d377d235a17c6a2b40d75ea8e22b802ae` and Go `go1.27.3`; the baseline identity carries those recorded values.
Jobs: frontend `123162623895`, smoke `123162624175`, Go `123162624197`, Swift main `123162624309` and Swift heavy `123162624619` succeeded; the integration join `123175329808` failed with join exit 1, "scenario preservation refused", and the aggregate `123175491679` failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

`scenario-preservation.json` (SHA-256 `7e775a79dacb7f3eb08a6efc0d02222ad23beadd8038ce9a00dd688fff94c780`) reports `preserved: false` with no errors.
It compared against the admitted baseline, SHA-256 `715b4ed6c03f7a437aae0e4a7cb4a2fc8f9ee8bd0a4399a75bec429000cdff95`.
Its required-set drift names exactly the two identities above, `DeleteService.StartDelete` and `DeleteService.DeleteManifestWithSegments`, the two rows decision 9 changes.
Its missing-protected list is empty: every one of the 66 previously protected pairs is credited by the candidate's main report.

## Producer reports

Both reports are valid, record contract hash `06d9e43fce961af9`, 56 actions and 128 required cells, and the heavy report credits nothing, as before.
The main report credits 70 cells; `coverageComplete` is false.
`DeleteService.StartDelete` is credited for `happy` by `TestPrefixDeleteSparesEverythingElse`, `refusal` by `TestDestructiveRefusalsHaveNoEffect`, `conflict` by `TestBulkDeleteConflictMock` and `TestBulkDeleteConflictSwift`, `cancel` by `TestBulkDeleteCancelMock` and `TestBulkDeleteCancelSwift`, and `partial` by `TestBulkDeletePartialMock` and `TestBulkDeletePartialSwift` in `internal/systemtest`.
`DeleteService.DeleteManifestWithSegments` is credited for `happy` by `TestSegmentJobHappyMock` and `TestSegmentJobHappySwift`, `refusal` by `TestDestructiveRefusalsHaveNoEffect`, and `conflict` by `TestSegmentJobConflictMock` and `TestSegmentJobConflictSwift`.

| Input | SHA-256 |
| --- | --- |
| Main `scenario-coverage.json` | `06491785f5b170f1937c0e464506e12361a1698876eee700bff273465f9c8cab` |
| Heavy `scenario-coverage.json` | `6b3f0a6b1427d8bb04259649a812272535df063902b9e548d5e0de14b906d003` |
| Main `receipt.json` | `422dc1485882bbdeaf8ed7f71f5777eaace9a54e81f7cbe8a4eb1d70f1262c88` |
| Heavy `receipt.json` | `45e2e5cbd9123ca6b353df3cd25b16c66ffc0f38299944188c93d9d058b9bf0f` |

Both 28-byte `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b`.

| Official artifact | ID | Hosted expiry (UTC) |
| --- | --- | --- |
| saio-tests-main | 12861566616 | 2027-01-26 23:23:23 |
| saio-tests-heavy | 12859224143 | 2027-01-26 23:23:23 |
| scenario-coverage | 12861516785 | 2027-01-26 23:23:23 |
| saio-diagnostics-main | 12861192765 | 2026-11-04 23:57:08 |
| saio-diagnostics-heavy | 12860227397 | 2026-11-04 23:34:18 |

The private retained packet is `/home/csmart/code/.aeronautes-work/20261027-release-plan/candidate-40787014308`: 27 files sealed by a `SHA256SUMS` manifest (SHA-256 `ba370bcb122ce7e5eb020d9fc7e8a7044d45443ab9b0c91c338a9a4c4e331c4e`), holding every artifact above, the replacement baseline as built, the builder `build-baseline.py` and its output.

## The replacement baseline

The [baseline](../../internal/scenariocontract/preservation-baseline.json) was built from the two reports by `build-baseline.py` in that packet, which validates them with the join's own reader against the candidate identity, refuses any loss of a previously protected pair, and writes the candidate's complete snapshot, its contract hash as recomputed from the rows, every previously protected pair and every pair the main report credits, and the digests of the four producer files.
It carries the run identity above, the four evidence digests above, 56 actions and 70 protected pairs: the 66 of the selected-set deletion's admission and four added, `DeleteService.DeleteManifestWithSegments` `happy` and `conflict` and `DeleteService.StartDelete` `cancel` and `conflict`, credited by this candidate for the first time; the remaining credited cells of these two actions, `DeleteService.StartDelete` `happy`, `partial` and `refusal` and `DeleteService.DeleteManifestWithSegments` `refusal`, were already protected pairs.
Its SHA-256 is `d69a084ce0939c15a9fd3884dc5407c31611bdb7a277c785846ea79d07cde298`, and the join accepts the file against its own rows and protected set: the local run of the join's own check against the candidate's two reports reports preserved with no drift and no missing pair.

## Admission limits

The candidate identity is a pull-request merge commit, not a protected-source commit; the route uses it because the unchanged baseline had to refuse the candidate's own snapshot.
The next exact-head full CI run must pass every required check, including preservation against this baseline, before the protected merge, and the resulting master producer evidence is reconciled and retained afterwards under the routine rules.
A valid incomplete report passes preservation; actions still carry gaps and nothing here claims a complete behavioural matrix.
The bulk deletion and the manifest-with-segments deletion stay behind the `bulk-delete` token until the operator returns the capability, and no native desktop journey, security, release or valuable-data hold is closed by this record.
