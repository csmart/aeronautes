# Replacement scenario-preservation baseline for bookmark import

The bookmark import adds, under the typed service surface of [ADR 0011](../adr/0011-typed-service-surface.md), three new scenario-contract rows ([ADR 0114](../adr/0114-importing-bookmarks.md)):
- `BookmarkImportService.PreviewImport`, a session row requiring `happy`;
- `BookmarkImportService.SaveImport`, a mutate row requiring `happy` and `refusal`;
- `BookmarkImportService.ImportStatus`, a read row requiring `happy`.

[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [pause and resume baseline](2026-11-10-pause-resume-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly those three rows, and no previously protected pair is lost.
The candidate credits the save's `happy` and `refusal` and the status's `happy` by its own system test, so this admission adds three pairs to the protected set.
The preview is a session row like the upload picks, which the harness credits only through receipt wrappers and does not wrap, so its `happy` cell is a recorded gap.

## Candidate source and run

The candidate is branch `import_bookmarks` at `d66886f86d45574c63927e2093d4c6a2f018bcef`, draft pull request 354 on master `385e6282fc0c31974a7b87dd9e9fd7101bb5feee`, carrying the contract rows and their coverage.
Its CI run is [43309249436, attempt 1](https://github.com/csmart/aeronautes/actions/runs/43309249436/attempts/1), a pull-request run, so the reports record the merge commit `7f18b29b5c1e9d78877194a7c403bccb0362d0ff` and Go `go1.27.4`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `52c617bc6efaba7ce33fc0b087d5f1154a3022f29c0615e1ea6d02e65bb6aa0e`, naming exactly the three new identities as required-set drift with an empty missing-protected list.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity: it refused any loss of a previously protected pair, kept all 86, added `BookmarkImportService.ImportStatus` `happy` and `BookmarkImportService.SaveImport` `happy` and `refusal`, and reported the expected required-set drift of the three rows.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `0e32c4059d121671`, 69 actions and 151 required pairs, three actions and four pairs more than the baseline this replaces.
The main report credits 89 pairs, the 86 that baseline protects and the three new ones; `coverageComplete` is false, with gaps on 34 actions, and the heavy report credits nothing.
The new pairs are credited by `TestImportBookmarksSavesAPreview` in `internal/systemtest`. It previews an rclone config through the real service against a local store, saves the previewed remote (happy), is refused a second save with the spent token (refusal), and reads the outcome (the status's happy).

| Input | SHA-256 |
| --- | --- |
| main report | `f0cc8d03f9910bb80a07c6f3d27b24915a6dfa542276fbee2cb3c8dcf57d8d8d` |
| main receipt | `dc1850dde4568779edce773cae2ed86a4ed3399b5a5e6c085d66d9663cca014a` |
| heavy report | `3a5f28a6f2e5ae65b77bcefaa39ea41e8b1bc41ae11a287727afe42a93152902` |
| heavy receipt | `0d899da6c25c2e68cbd8ccb8ce6978905afa68dc517fa3276be9be2d02b48f0f` |

Both `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b` and record test and tee exits of zero.

The private retained packet is `/home/csmart/code/.aeronautes-work/20261113-bookmark-import/candidate-43309249436`: the downloaded producer artifacts, the replacement baseline as built, the builder `build-baseline.py`, sealed by a `SHA256SUMS` manifest.
