# Replacement scenario-preservation baseline for the interrupted-job notice

[ADR 0088](../adr/0088-interrupted-job-disclosure.md)'s second slice adds two scenario-contract rows: `JobService.InterruptedDisclosures`, a read requiring `happy` and `refusal`, and `JobService.DismissInterrupted`, a mutate requiring `happy` and `refusal`.
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [bulk and segment deletion baseline](2026-10-28-bulk-delete-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly those two new rows, and no previously protected pair is lost.
The candidate credits each new action for `happy` and `refusal` — the four pairs added here — by its own system test.

## Candidate source and run

The candidate is branch `adr0088_impl` at `004a5709e45914e31802019b5454db40b6114262`, draft pull request 280 on master `1215ecc3ff79a163ffc757a0fa7952c54ecadf38`, carrying the two contract rows and their coverage.
Its CI run is [41281369835, attempt 1](https://github.com/csmart/aeronautes/actions/runs/41281369835/attempts/1), a pull-request run, so the reports record the merge commit `bdf581dccb5eabe438b9fc8eb9d320c374e9d360` and Go `go1.27.3`; the baseline identity carries those recorded values.
The frontend, smoke, Go race, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `d69a084ce0939c15a9fd3884dc5407c31611bdb7a277c785846ea79d07cde298`, naming exactly the two new identities as required-set drift with an empty missing-protected list.
Re-run locally with the join's own reader against the candidate's two reports and the replacement baseline, the check reports preserved with no drift and no missing pair.
That output, `scenario-preservation.json`, has SHA-256 `21f40fe5ae57f60a33bca6ae50eec9740d1b84760abaa6ffbe6a99a56804781a`.

## Producer reports

Both reports are valid, record contract hash `55276603077ba4f5`, 58 actions and 132 required cells, and the heavy report credits nothing.
The main report credits 74 cells; `coverageComplete` is false, with gaps remaining on other actions.
`JobService.InterruptedDisclosures` is credited for `happy` by `TestInterruptedDisclosuresNoticeAndDismissal` and for `refusal` by `TestInterruptedDisclosuresRefuseWhenUnavailable` in `internal/systemtest`.
`JobService.DismissInterrupted` is credited for `happy` by `TestInterruptedDisclosuresNoticeAndDismissal` and for `refusal` by `TestInterruptedDisclosuresRefuseWhenUnavailable` in the same package.

| Input | SHA-256 |
| --- | --- |
| Main `scenario-coverage.json` | `6a02a4b4e905a36d668a5ef0e71a4d4aba5d13b36820dc83d2ba8e06a1f93e73` |
| Heavy `scenario-coverage.json` | `c3e4da0fdc8e3908c1493aded30c7548ac2d377c7f96fd83d47564cba4dc8554` |
| Main `receipt.json` | `564fb957c743b7df361da96edc14ae228f45a5aa5e043e777c7f01ed71eaec38` |
| Heavy `receipt.json` | `850325a9396a79426223153d698060a851bcf4a7acf53a2806e137eee7134773` |

Both 28-byte `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b`.

| Official artifact | ID | Hosted expiry (UTC) |
| --- | --- | --- |
| saio-tests-main | 13074681002 | 2027-01-30 06:34:23 |
| saio-tests-heavy | 13074420280 | 2027-01-30 06:34:23 |
| scenario-coverage | 13073628138 | 2027-01-30 06:34:23 |
| saio-diagnostics-main | 13074457196 | 2026-11-08 07:08:46 |
| saio-diagnostics-heavy | 13074126124 | 2026-11-08 06:49:45 |

The private retained packet is `/home/csmart/code/.aeronautes-work/20261101-adr0088-slice2/candidate-41281369835`: the downloaded producer artifacts, the replacement baseline as built, the builder `build-baseline.py` and the local preservation check output, sealed by a `SHA256SUMS` manifest.

## The replacement baseline

The [baseline](../../internal/scenariocontract/preservation-baseline.json) was built from the two reports by `build-baseline.py`, which validates them with the join's own reader against the candidate identity, refuses any loss of a previously protected pair, and writes the candidate's complete snapshot, its contract hash as recomputed from the rows, every previously protected pair and every pair the main report credits, and the digests of the four producer files.
It carries the run identity above, the four evidence digests above, 58 actions and 74 protected pairs: the 70 of the bulk and segment deletion admission and four added — `JobService.InterruptedDisclosures` and `JobService.DismissInterrupted`, each for `happy` and `refusal`, credited here for the first time.
Its SHA-256 is `8b2ec93df5ca5ae4d30abefe5ea8d95a821ecf2e982b09d0a8ee4ea753bbca31`, and the join accepts the file against its own rows and protected set.

## Admission limits

The candidate identity is a pull-request merge commit, not a protected-source commit; the route uses it because the unchanged baseline had to refuse the candidate's own snapshot.
The next exact-head full CI run must pass every required check, including preservation against this baseline, before the protected merge, and the resulting master producer evidence is reconciled and retained afterwards under the routine rules.
A valid incomplete report passes preservation; actions still carry gaps and nothing here claims a complete behavioural matrix.
[ADR 0088](../adr/0088-interrupted-job-disclosure.md) lifts no capability and relaxes no refusal; no native desktop journey, security, release or valuable-data hold is closed by this record.
