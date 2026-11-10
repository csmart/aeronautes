# Replacement scenario-preservation baseline for pause and resume

Pause and resume adds, under the typed service surface of [ADR 0011](../adr/0011-typed-service-surface.md), two new scenario-contract rows: `TransferService.PauseJob` and `TransferService.ResumeJob`, session rows requiring `happy`, beside `TransferService.CancelJob` ([ADR 0104](../adr/0104-pause-and-resume.md)).
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [directory upload baseline](2026-11-10-directory-upload-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly those two new rows, and no previously protected pair is lost.
The candidate credits nothing for either row, so this admission adds two rows to the required snapshot and no pair to the protected set: the pause and resume `happy` cells are recorded gaps beside the cancel's.

## Candidate source and run

The candidate is branch `pause_resume_dev` at `49bb2309c29145b0411299a2c9c4226d364ba84b`, draft pull request 328 on master `94390b4dd0388fb21da8c00a749a06a05164c4ca`, carrying the contract rows and the pause and resume tests.
Its CI run is [42907544207, attempt 1](https://github.com/csmart/aeronautes/actions/runs/42907544207), a pull-request run, so the reports record the merge commit `52a093a181a1c9fcee9dcd956e71e1dd4d8ae031` and Go `go1.27.3`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `3ac072b05c4efbc8f760f25619dfb8a9114a5becc76134e7809ac87b490d7f19`, naming exactly the two new identities `TransferService.PauseJob` and `TransferService.ResumeJob` as required-set drift, with an empty missing-protected list and no errors.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity: it refused any loss of a previously protected pair, kept all 86, added none, and reported the expected required-set drift of those two identities.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `1d76918da967dff4`, 66 actions and 147 required pairs, two more of each than the baseline this replaces.
The main report credits 86 pairs, exactly the 86 that baseline protects, and none for either new row; `coverageComplete` is false, with gaps on 33 actions, and the heavy report credits nothing.

Pause and resume have no credited cell because the system harness emits a claim only through its receipt wrappers, and the job controls have none: the cancel's `happy` cell has been a recorded gap since the [September action coverage table](../audits/2026-09-05/action-coverage.md).
The new rows therefore raise the required set by two pairs and the gap count by two actions, and lower neither.
Their proof is in the series itself.
`TestPauseResumeUploadRealSwift` and `TestPauseResumeUploadMock` in `internal/systemtest` start an upload of two folders through the real engine, pause it as it starts, see the destination unchanged while it is parked, and resume it to the end with every file at its own name and bytes and nothing else in the account touched; both passed in this candidate's Swift main lane.
The service tests in `internal/app` cover each route's pause, resume and cancel while paused, a pause asked while queued, a refused pause, and a resume after another job's lost record has stopped the store.

| Input | SHA-256 |
| --- | --- |
| main report | `d27fda406821a475a1bd4e7c5269e96d2cdef156bbc38b450d266cf5ddeacba2` |
| main receipt | `86e54b22c245c57d6db0b0eae374695cca410b19905967f9a91fd0a2ee7def7a` |
| heavy report | `e306a7a9817333f706a7e9c1291f19e2254efda65618da499844cd1adbd47245` |
| heavy receipt | `a7461a2b1966d3a4e69f903c8bdf41072289fae1ca75f876c162a5bd4c3d4811` |
