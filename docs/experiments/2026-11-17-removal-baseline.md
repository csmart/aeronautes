# Replacement scenario-preservation baseline for the removal of the staged model

[ADR 0122](../adr/0122-the-stores-answer-is-the-result.md) decision 13 removes the `conflict` scenario from four transfer rows of the scenario contract, because none of them can end in a conflict once every transfer writes in place:

- `TransferService.StartUpload`: an upload never records a conflict; each name is replaced, left as the same after a confirmed comparison, failed, unknown or not attempted.
- `TransferService.StartCopy`: a copy records an unconfirmed difference as refused.
- `TransferService.RerunJob`: a re-run only starts a copy.
- `TransferService.StartMirror`: Mirror refuses every start.

Each now requires `happy`, `refusal`, `cancel`, `partial` and `retry`.
`TransferService.StartDownload` keeps `conflict`: a file that appeared while the download was queued is left alone and recorded as one, and the system tests of that case now credit it from top-level tests.

[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed required set only through its drift route: an accepted decision that names every changed identity and accounts for any protected pair it changes, candidate evidence from the candidate's own run, a replacement baseline built from that evidence, and the next exact-head run passing the real join against it.
This record is that evidence and the baseline built from it.
It replaces the [deletion baseline](2026-11-16-deletion-answer-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

This admission gives nothing up.
None of the four `conflict` pairs was protected, because no test had ever credited them.
Every protected pair is still credited, and one pair is added: `TransferService.StartDownload` `conflict`, which the candidate's main report credits for the first time.

## Candidate source and run

The candidate is branch `store_answer_removal_drift` at `6b28d95f7a5827ec90babfe87927dd93a6291676`, draft pull request 383 on master `c5e95790186c373ea094b4332091d14a3bd93ded`, carrying decision 13 and the removal it describes, with the contract rows changed and the old baseline.
Its CI run is [44051411474, attempt 1](https://github.com/csmart/aeronautes/actions/runs/44051411474/attempts/1), a pull-request run, so the reports record the merge commit `8089391c23f2431539b869544682cd418d46c2e4` and Go `go1.27.4`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `b06c4b7f4dc3ba201fe9f0c223ab123ed9bc82f8a641cb658dd890f4c2365a2c`.
It named exactly the four rows above as required-set drift, each losing `conflict` and nothing else, listed no missing protected pair, and reported no other error.
`build-baseline.py`, the builder of the deletion admission, rebuilt the baseline with the join's own reader against the candidate identity, given an empty list of accounted pairs, so any protected pair the candidate did not credit would have stopped the build.
It kept all 86 protected pairs, gave up none, added the one newly credited pair, and reported the expected required-set drift of the four rows.
The join's own preservation check then passed against the replacement on the candidate's reports, with no drift and no missing pair.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `d943816d615bf15e`, 71 actions and 144 required pairs, four pairs fewer than the baseline this replaces.
The main report credits 87 pairs, which are the 87 the replacement protects; `coverageComplete` is false, with gaps on 34 actions, and the heavy report credits nothing.

| Input | SHA-256 |
| --- | --- |
| main report | `e35cfad1d2ba63b2afa28d8833e57007e78c9c4c6f90d5639f36acaaf2bb3a1d` |
| main receipt | `ec3bb054fee658099dd51adcc64c1d4af6b78c7ba6f1e6b311a4d5fe07eacd76` |
| heavy report | `ecd09aed1502b12491e7deb981761df1133e1f41146216dc2a3f12956ae90e68` |
| heavy receipt | `16ceca2bdb2cc4fd4818b7d6d0436ed6d41643a8296d987ef0d8c6b07ca64aac` |
| replacement baseline | `6fa91753efbc59c1bb054e9290abf2297dfee667eb06e28fdb4b3c6b3dd24cdb` |

Both `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b` and record test and tee exits of zero.

The private retained packet is `/home/csmart/code/.aeronautes-work/20261117-store-answer-removal/candidate-44051411474`: the downloaded producer artifacts, the join's preservation diagnostic, the old and the replacement baselines, the builder with its empty list of accounted pairs, and the join's check of the replacement, sealed by a `SHA256SUMS` manifest.
