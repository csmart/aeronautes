# Selected-preview scenario baseline admission

[ADR 0093](../adr/0093-selected-set-deletion.md) decision 8 adds `DeleteService.PreviewSelectedDelete` as a read action requiring `happy`.
It observes the captured selection's folder counts and archive owners without creating a job, audit entry or destructive request.
The existing mock and real-Swift archive-preview tests credit this read.
The decision was reviewed separately before preparing this replacement under the [ADR 0069 drift route](../adr/0069-preserve-covered-scenario-cells.md#review-every-baseline-change).
The single-prefix preview remains required and uncovered; it is not a protected pair.

## Candidate and retained failure

The candidate is [PR 404](https://github.com/csmart/aeronautes/pull/404) at `ab7240bb53bdb5481382653c7f2a6c15ab284c2f`, based on master `a04b25ac6298cfa3618fb528aae44dce8d482899`.
[Run 44594774396, attempt 1](https://github.com/csmart/aeronautes/actions/runs/44594774396/attempts/1) records the synthetic merge `1aec384fe64c0d4fc5ad89b66a0dbe1f4c9cb48a` and Go `go1.27.4` in both producer identities.
Go, frontend, smoke and both Swift producer jobs passed.
Integration and the aggregate failed because the old baseline lacked exactly the new read/`happy` identity.
That expected refusal has no validation errors and no missing protected pairs, and remains retained as a failed gate.

The main and heavy producer ZIPs match GitHub's recorded SHA-256 digests:

| Artifact | SHA-256 |
| --- | --- |
| main, 14568480193 | `9b3d52791bc9b91aa66f886f195a2a885037d3fddea051e43c2776a4fd43559a` |
| heavy, 14566421876 | `7393e5edfdc41bb70d626e57218382ad8975328c556fc5f7c5cb20e8e5adfcc9` |

An offline replay imports the candidate's unchanged `scripts/saio-partition.py` and calls its real `join` with the recorded producer identity.
It validates every receipt, successful test and event-writer exit, package/test inventory, lane selection, report and coverage-profile combination before reaching the expected preservation refusal.
This is validation of retained hosted evidence, not a claim that the local Go toolchain produced it.

## Replacement and controls

The complete required snapshot grows from 71 actions and 144 pairs to 72 actions and 145 pairs, with contract hash `ce9a48de1f419432`.
All 87 previously protected pairs remain credited and protected.
The replacement adds the selected-preview `happy` pair, protecting all 88 pairs credited by the main producer.
Its witnesses are `TestDeletePreviewNamesArchiveOwnersMock` and `TestDeletePreviewNamesArchiveOwnersSwift` in `internal/systemtest`, each with a read-completed receipt.
The heavy producer credits no pair.
There are still 34 actions with gaps; this is preservation, not complete scenario coverage.

The same real join passes against the replacement with no drift or missing protected pair.
A separate control removes the existing `AuditService.ExportAudit` `happy` credit from an otherwise valid report; preservation refuses exactly that missing pair.
No source report, receipt, required scenario or gate is weakened for that control.

| Input | SHA-256 |
| --- | --- |
| previous baseline | `6fa91753efbc59c1bb054e9290abf2297dfee667eb06e28fdb4b3c6b3dd24cdb` |
| replacement baseline | `6e9f3845ac6654c45c2b0873770359009ac804470cc841d4bb488549d55b5800` |
| heavyReceiptSHA256 | `282e4dbe817e1c1296296971ccf1e978cf9d583b0a803df68940bdd17ec1c364` |
| heavyReportSHA256 | `eb105bf2b69593e8d7b3c9fa71b722344214fbf123a1982557402e718e3840a5` |
| mainReceiptSHA256 | `212bdf43832e4b6dc04a1addddda5ad7e836872265f367c695459efb102d1264` |
| mainReportSHA256 | `e679c75aa91979de1f1c33ea2c1db04122cfa86e6153425f99320f7c33bf2224` |

The retained evidence packet includes both producer ZIPs and extracted artifacts, old and replacement baselines, the builder, both join replays, the missing-credit control and a SHA256SUMS manifest.
This replacement requires independent review with the contract decision and that packet.
A fresh exact-head full CI run must pass all required checks before a normal protected merge, followed by reconciliation of resulting-master producer evidence.
No release, native acceptance or Mirror change follows from this admission.
