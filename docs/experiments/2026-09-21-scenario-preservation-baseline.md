# First scenario-preservation baseline

[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) requires independent admission of the complete protected-source snapshot before aggregate activation.
The [baseline](../../internal/scenariocontract/preservation-baseline.json) retains all 53 actions, 120 required cells and every one of the 48 literal pairs credited by the main producer.
Heavy contributes zero credits; 72 gaps across 35 actions remain, and `coverageComplete` is false.
This is a bounded preservation floor, not full behavioural or release acceptance.
At first baseline admission, the comparison was still uncalled by join, CLI and CI.

## Source and producer

Protected source is `65d8c5f53dfc57472efcde067b958f34deeaa94a`, tree `318cb1ccd050dbe107e7fb4beaf06095ec4a6250`, [run 35625767556, attempt 1](https://github.com/csmart/aeronautes/actions/runs/35625767556/attempts/1).
Both reports use schema 3, Go `go1.27.1` and contract hash `9f0e99b3cf8bd327`.
All seven exact-source jobs succeeded: Go `106419634759`, frontend `106419634805`, Swift main `106419634785`, Swift heavy `106419634639`, smoke `106419634731`, integration join `106426770329` and required-job aggregate `106426984406`.
The aggregate finished at `2026-09-21T16:48:59Z`; collection reread that source/run/attempt and the protected branch afterwards.
Both lanes record numeric test and tee exits of zero.
Reporter, partition and join success is inferred from the successful named steps, checked workflow commands and retained logs; no numeric exit artifact exists for those commands.

## Retained evidence

Following [DESIGN §65.12](../DESIGN.md#6512-proportionate-remediation-and-a-fixed-finish-line) and [ADR 0040](../adr/0040-proportionate-remediation.md), raw reports, receipts, exits, logs, source assertions and structured evidence remain outside the product repository.
The durable private packet is `/home/csmart/code/.aeronautes-work/20260921-p34-final-queue-65d8c5f5-handoff`, with 403 files and SHA256SUMS digest `a972eaf3cc382496bbdf4804c59181d980645cbbb8a6545200c30a3a25a517e2`.
Its `REPORT.md`, `producer/RECONCILIATION.json`, `SOURCE-MAP-COMPARISON.json` and complete `assertion-map/ASSERTION-MAP.md` record the reconciliation and assertion limits.
The baseline is runtime input; it does not replace that underlying retained evidence.

Core inputs remain byte-for-byte in `producer/artifacts/saio-tests-{main,heavy}/` within that packet:

| Input | SHA256 |
| --- | --- |
| Main `scenario-coverage.json` | `0a6564782b4ca232fb06764479b6b9de7634a7001f723ac00932600b838c2b83` |
| Heavy `scenario-coverage.json` | `1aff19ee3e9f5980c96e06c517da334f3e635daf64455a793d029ee432bae629` |
| Main `receipt.json` | `19a6bdf78e8452f07f35179bc681435515fcbfbce3c149736d084008b5201dc5` |
| Heavy `receipt.json` | `9ac5dee76e99612c49c72ac55d5fef07d92f8273f07d6b6ed826fd793f351cf0` |

Both authentic 28-byte `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b`.
The API digest and byte size matched each downloaded archive; extracted members and producer receipts were also reconciled.

| Official artifact | ID | Archive SHA256 | Hosted expiry (UTC) |
| --- | --- | --- | --- |
| saio-tests-main | 10652484167 | `857fbf26d900098f30c10acaf439413280512eb578c46d5fbe02e4af495759fd` | 2026-12-20 16:28:00 |
| saio-tests-heavy | 10651798833 | `63c1c750d5d7cfa10b3377eaca92aa69d354f62cb4c66ae64c31a403005676e4` | 2026-12-20 16:28:00 |
| scenario-coverage | 10652759258 | `e828d26734f10c7c4205d64252c5b8287983dac51809170bcc71a0334af4b2d1` | 2026-12-20 16:28:00 |
| saio-diagnostics-main | 10653009032 | `a71124238073feeb75a8c817218eedea12b94e3bb502fedc75b012bf8745c062` | 2026-09-28 16:48:10 |
| saio-diagnostics-heavy | 10651818899 | `df3bf77af44de31f0d1d76f2347e93189390f657888631717bcb0be6ea1a4549` | 2026-09-28 16:41:52 |

Hosted artifacts expire; the private retained bytes, not these hashes alone, support later review.
No permanent public hosting or native/deployment acceptance is implied.
Raw job-log hashes are local integrity checks because those endpoints supply no official digest.

## Admission limits

All 46 historical credited pairs and their 96 claimant identities remain; Recent/refusal and container-conflict add two pairs and three claimants.
All 99 distinct cited top-level claimants passed, and their complete source files match the inspected final tree.
Receipt emission alone is insufficient: the reporter requires running-phase attribution, terminal test pass and expected-package pass, including after early claims and checked cleanup.
The complete source-assertion map, not producer validity alone, is part of independent admission.

The four Account/Capabilities cells assert primary happy reads with the requested positive scope and later stale-primary refusals with empty DTOs; secondary reads are controls.
Different bookmarks need not have unequal numeric generations.
These cells do not establish every capability field, same-bookmark invalidation, captured-tuple races or browser publication.
The retained map also preserves narrower older namespace snapshots, the old object-conflict witness's lack of wire/durable/reopen assertions and the download sample-cardinality limit.
The new container-conflict witness's zero-POST assertion covers its instrumented target route, not every wire request.
Deferred-operation credits remain development evidence and grant no production capability.

The rclone matrix and OS-keychain round trip were optional skips, never credited cells or native acceptance.
No required cell is removed, declared N/A or waived, and no main/heavy union is invented.
Aggregate activation and its failure/diagnostic-retention proof were a separate follow-up to admission, recorded below.
Owner, native, security, deployment, release and valuable-data holds remain open.

## Aggregate activation proof

The join now compares against the admitted baseline after its existing prerequisites and output writes.
The workflow records the actual Python join exit, requires zero, and uploads the available combined diagnostics even after failure.
Source-bound local controls reproduce the former lost-cell success and verify actual CLI, exact workflow-shell and required-gate refusal without changing valid/incomplete report semantics.
The unchanged reporter's running claim passes preservation; omitted, skipped and paused claims lose the protected cell and fail it.
Thirteen compiled broken guards fail named assertions; deliberate syntax and setup failures receive no catch credit.
The durable private activation packet is `/home/csmart/code/.aeronautes-work/20260922-p34-activation`, with retained proof manifest `PROOF-SHA256SUMS` digest `104b626f0f0fd9e3909a2a6b0d562e84577a24774854dc9f28c7c3b63c3f0605`.
Its raw synthetic inputs, actual process exits and workflow-wiring checks establish composed local failure-path evidence, not an observed hosted failed upload or new admission evidence.
Exact reviewed-head hosted success-artifact readback and resulting protected-master reconciliation remain required delivery evidence.
