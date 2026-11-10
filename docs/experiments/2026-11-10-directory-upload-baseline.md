# Replacement scenario-preservation baseline for directory upload

Directory upload adds, under the typed service surface of [ADR 0011](../adr/0011-typed-service-surface.md), one new scenario-contract row: `TransferService.PickUploadDirectory`, a session row requiring `happy`, beside the file pick ([ADR 0103](../adr/0103-directory-upload.md) decision 5).
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [TempURL key slot baseline](2026-11-09-tempurl-key-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly that one new row, and no previously protected pair is lost.
The candidate credits nothing for the new row, so this admission adds a row to the required snapshot and no pair to the protected set: the folder pick's `happy` cell is a recorded gap beside the file pick's.

## Candidate source and run

The candidate is branch `directory_upload_app` at `af1252fe88ef09909cbbf6d1bcfb44a1e793ad7a`, draft pull request 323 on master `6aa0a8f8cf6c81b09f89e966f6552f8755e31a7d`, carrying the contract row and the folder pick's tests.
Its CI run is [42712452878, attempt 2](https://github.com/csmart/aeronautes/actions/runs/42712452878/attempts/2), a pull-request run, so the reports record the merge commit `ca0bc9a7e177a7454d67b7602550bab8411ee2f7` and Go `go1.27.3`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

Attempt 1 of the same run is not evidence here.
Its Swift main lane failed when the race detector reported a data race inside `TestPrefixSweepLaterPassRequestsOnlyNewNames`, an existing sweep test this series does not touch: the test's own one-shot guard is a plain boolean that concurrent mock request handlers read and write.
With one fixture failed, the integration job stopped before the join, so that attempt produced no main receipt and no preservation result.
The whole workflow was rerun, every lane again, as attempt 2.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `543be7854cccdc562c31d24de2830a566124a79bf6e82ffc50fd2f959068a1c4`, naming exactly the one new identity `TransferService.PickUploadDirectory` as required-set drift with an empty missing-protected list.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity: it refused any loss of a previously protected pair, kept all 86, added none, and reported the expected required-set drift of `TransferService.PickUploadDirectory`.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `fd91196825416b86`, 64 actions and 145 required pairs, one more of each than the baseline this replaces.
The main report credits 86 pairs, exactly the 86 that baseline protects, and none for the new row; `coverageComplete` is false, with gaps on 31 actions, and the heavy report credits nothing.

The folder pick has no credited cell because the system harness emits a claim only through its receipt wrappers, and neither pick has one: the file pick's `happy` cell has been a recorded gap since the [September action coverage table](../audits/2026-09-05/action-coverage.md).
The new row therefore raises the required set by one pair and the gap count by one action, and lowers neither.
The folder pick's proof is in the series itself.
`TestDirectoryUploadRealSwift` and `TestDirectoryUploadMock` in `internal/systemtest` pick a folder of nested files, start the upload the pick bound, and read back every object at its folder-relative name by the known body's digest; both passed in this candidate's Swift main lane.
The service tests in `internal/app` cover the summary of a folder with no file, the scope binding and cancellation.

| Input | SHA-256 |
| --- | --- |
| main report | `ec6e816dfe5ea38b8b24b0b49df1fa9906142dcf338b61280029ee2c0bffffa7` |
| main receipt | `1c94498b596831c00a7dcb6a32dabe738802d49d7acca9214716a875a68be7f3` |
| heavy report | `057b1e24c2819960ce4f185ad48bdb5038991bafda7655f070973f1f42bf7e01` |
| heavy receipt | `018a0b72241128437060afd3f8f08372210eda3b8294423bf55d5d825ce3f1a6` |
