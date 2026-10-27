# Replacement scenario-preservation baseline for the verify action

[ADR 0091](../adr/0091-verify-action-contract.md) admits `BookmarkService.Verify` to the scenario contract, and [ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed required set only through its drift route: candidate evidence from the candidate's own run, in which the unchanged baseline refuses the new snapshot, then a replacement baseline reviewed together with the contract row and its decision.
This record is that evidence and the baseline built from it.
It replaces the [first baseline record](2026-09-21-scenario-preservation-baseline.md) as the current admission; the first record stays as the history of the 48 pairs this one keeps.

## Candidate source and run

The candidate is branch `account_setup_v2` at `77c3caaebe1beeda8c50f6a05dce4cc2fa1fb9dc`, the draft pull request 253 on master `9165cd05ca4f79de07a218d7e7260a543dbf5e80`, carrying the contract row and the unchanged admitted baseline.
Its CI run is [40438457699, attempt 1](https://github.com/csmart/aeronautes/actions/runs/40438457699/attempts/1), a pull-request run, so the reports record the merge commit `4e7a35206a76de0d8fa8e97ea836fe0760c4841b` and Go `go1.27.3`; the baseline identity carries those recorded values.
Jobs: frontend `121972762995`, Swift heavy `121972763208`, smoke `121972763220`, Swift main `121972763235` and Go `121972763265` succeeded; the integration join `121981714102` failed with join exit 1, "scenario preservation refused", and the aggregate `121981861785` failed on it.
Both producers record numeric test and tee exits of zero.

Two earlier candidate runs are retained but disqualified by unrelated failures, as the route requires: [40431534663](https://github.com/csmart/aeronautes/actions/runs/40431534663) (`832c5d2c`) failed the Go and Swift main lanes because the transport wrappers advertised `CloseIdleConnections` to the wire library, and [40434044195](https://github.com/csmart/aeronautes/actions/runs/40434044195) (`46a789db`) failed the bindings drift gate on a regenerated comment while its diagnostic already showed the same drift.

## The drift diagnostic

`scenario-preservation.json` (SHA-256 `a99570589043c010a3b8916490e4173b8381d003fbf2ef4f874257187ad1183e`) reports `preserved: false` with no errors.
It compared against the admitted baseline of 21 September, SHA-256 `b443a65cadd346fb8e5efa65d749750d0fc3b1805a39b1c78f719160df01652d`.
Its required-set drift names exactly one identity, `BookmarkService.Verify`, absent from the baseline and present as a read requiring `happy` and `refusal`.
Its missing-protected list is empty: every one of the 48 previously protected pairs is credited by the candidate's main report.

## Producer reports

Both reports are valid, record contract hash `a4f0914c2a290c0d`, 54 actions and 122 required cells, and the heavy report credits nothing, as before.
The main report credits 52 cells across 21 actions, with 33 actions still carrying gaps; `coverageComplete` is false.
`BookmarkService.Verify` is credited for `happy` and `refusal` by `TestBookmarkVerifyMock` and `TestBookmarkVerifyRealSwift` in `internal/systemtest`.

| Input | SHA-256 |
| --- | --- |
| Main `scenario-coverage.json` | `16e8a19afeb3a20cf72fffd94d8312b17b7a89c1045b5f91bdf8aef4cb79cbf0` |
| Heavy `scenario-coverage.json` | `c0194c149c32183d317a413e06f643bcb9dc3449caf81b6ef4192831327280db` |
| Main `receipt.json` | `f993aa9c92ae936e87ee4927c42375e9af1d3f54f1956a951a97b572fd759a7b` |
| Heavy `receipt.json` | `6351c2094f0bf7ef7ccfa95b6c4758a143af471aa9eb171f8bfe4f6492a069ab` |

Both 28-byte `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b`.

| Official artifact | ID | Hosted expiry (UTC) |
| --- | --- | --- |
| saio-tests-main | 12705374414 | 2027-01-25 02:26:47 |
| saio-tests-heavy | 12705956416 | 2027-01-25 02:26:47 |
| scenario-coverage | 12705904618 | 2027-01-25 02:26:47 |
| saio-diagnostics-main | 12705619062 | 2026-11-03 02:55:06 |
| saio-diagnostics-heavy | 12705946498 | 2026-11-03 02:37:03 |

The private retained packet is `/home/csmart/code/.aeronautes-work/20261027-ui-slices/candidate-40438457699`: 49 files sealed by a `SHA256SUMS` manifest (SHA-256 `bd27f4dfc70908420444ba08c05f08ee359b65333f4e4f6f0fc5d44c18f60a9b`), holding every artifact above, the replacement baseline as built, the builder `build-baseline.py` and its output.

## The replacement baseline

The [baseline](../../internal/scenariocontract/preservation-baseline.json) was built from the two reports by `build-baseline.py` in that packet, which validates them with the join's own reader against the candidate identity, refuses any loss of a previously protected pair, and writes the candidate's complete snapshot, its contract hash recomputed from the rows, every previously protected pair and every pair the main report credits.
It carries the run identity above, the four evidence digests above, 54 actions and 52 protected pairs: the 48 of the first admission and four added, `BookmarkService.Verify` `happy` and `refusal`, `InspectorService.Object` `happy` and `InspectorService.Prefix` `happy`, the last two credited by this candidate for the first time.
The join accepts the file against its own rows and protected set.

## Admission limits

The candidate identity is a pull-request merge commit, not a protected-source commit; the route uses it because the unchanged baseline had to refuse the candidate's own snapshot.
The next exact-head full CI run must pass every required check, including preservation against this baseline, before the protected merge, and the resulting master producer evidence is reconciled and retained afterwards under the routine rules.
A valid incomplete report passes preservation; 33 actions still carry gaps and nothing here claims a complete behavioural matrix.
No native desktop journey, security, release or valuable-data hold is closed by this record.
