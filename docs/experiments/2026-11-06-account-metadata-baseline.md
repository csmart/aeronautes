# Replacement scenario-preservation baseline for account metadata editing

Account metadata editing adds, under the typed service surface of [ADR 0011](../adr/0011-typed-service-surface.md), one new scenario-contract row: `MutateService.UpdateAccountMetadata`, a mutate requiring `happy`, `refusal` and `conflict`.
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [settings-restore baseline](2026-11-04-settings-restore-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly that one new row, and no previously protected pair is lost.
The candidate credits the new action for `happy`, `refusal` and `conflict` — the three pairs added here — by its own system test.

## Candidate source and run

The candidate is branch `acctmeta-baseline-candidate` at `604388997b5a1195ede397a0135ce504d0ed57be`, draft pull request 298 on master `073e37bfa61355e472aa562ac09af075b8b6c6e5`, carrying the contract row and its coverage.
Its CI run is [42170704686, attempt 1](https://github.com/csmart/aeronautes/actions/runs/42170704686/attempts/1), a pull-request run, so the reports record the merge commit `01d62c5f72fcead79d69f843c0ccf80e6e143b05` and Go `go1.27.3`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `507e368352f61e5b58eb2ac8502654fb3b7d7aa7f9cb19d91ce95ad061156346`, naming exactly the one new identity `MutateService.UpdateAccountMetadata` as required-set drift with an empty missing-protected list.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity: it refused any loss of a previously protected pair, kept all 80, and added the three new pairs, reporting the expected required-set drift of `MutateService.UpdateAccountMetadata`.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `0f98572feed32ab8`, 62 actions.
The main report credits the three new pairs among its cells; `coverageComplete` is false, with gaps remaining on other actions, and the heavy report credits nothing extra.
`MutateService.UpdateAccountMetadata` is credited for `happy`, `refusal` and `conflict` by `TestAccountMetadataEditSwift` in `internal/systemtest`, which edits account metadata on the authoritative working-copy fingerprint (happy), refuses a TempURL secret-slot key before any request (refusal), and edits on a stale fingerprint after a competing save (conflict).

| Input | SHA-256 |
| --- | --- |
| main report | `0bd9cee9117d4490147a026a443c9b8aa6236822a5fd1150e307a2f95f156673` |
| main receipt | `e86c0a5a440b8ebfcd3f6ff20c5b326d3179689fd8aa35a85dfacd43bb9c51a4` |
| heavy report | `9992351b87651b5817c9de0342d1936a33fa0ff98e604f94e8b3a259507edb49` |
| heavy receipt | `30380b4af562f0eb56d3873d6316302488e8db1e2b1c183f51a2027b62762d3f` |
