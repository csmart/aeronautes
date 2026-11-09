# Replacement scenario-preservation baseline for TempURL key slot management

TempURL key slot management adds, under the typed service surface of [ADR 0011](../adr/0011-typed-service-surface.md), one new scenario-contract row: `MutateService.UpdateTempURLKey`, a mutate requiring `happy`, `refusal` and `conflict` ([ADR 0101](../adr/0101-tempurl-key-slots-and-cors-editing.md) decision 5).
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [account metadata baseline](2026-11-06-account-metadata-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly that one new row, and no previously protected pair is lost.
The candidate credits the new action for `happy`, `refusal` and `conflict` — the three pairs added here — by its own system tests.

## Candidate source and run

The candidate is branch `tempurl_cors_b` at `2b2649d73d26a779f3e6b8e41c3570d9c6979488`, draft pull request 319 on master `b92ff19f1344cbe13a65e2978bac6c567f5ab2a8`, carrying the contract row and its coverage.
Its CI run is [42563896295, attempt 1](https://github.com/csmart/aeronautes/actions/runs/42563896295/attempts/1), a pull-request run, so the reports record the merge commit `1645a7c66a2d5da8d575b932587409c83530c26b` and Go `go1.27.3`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `00d59a69bfc01e299b7df37ebaccaffe2a885f3d03f01d8769d2735a9e8c320b`, naming exactly the one new identity `MutateService.UpdateTempURLKey` as required-set drift with an empty missing-protected list.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity: it refused any loss of a previously protected pair, kept all 83, and added the three new pairs, reporting the expected required-set drift of `MutateService.UpdateTempURLKey`.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `acfe7de61c274f2e`, 63 actions.
The main report credits the three new pairs among its cells; `coverageComplete` is false, with gaps remaining on other actions, and the heavy report credits nothing extra.
`MutateService.UpdateTempURLKey` is credited for `happy`, `refusal` and `conflict` by `TestTempURLKeyContainerSwift` and `TestTempURLKeyContainerMock` in `internal/systemtest`, which set a slot against the presence the inspector shows and then clear it (happy), refuse a slot Swift does not read before any request (refusal), and refuse a stale presence after a competing write filled the other slot (conflict); `TestTempURLKeyAccountMock` credits the same pairs for the account target on the mock.

| Input | SHA-256 |
| --- | --- |
| main report | `cc989c2238d8db285c933642df4b6c1dd08f9b2a47f714ddf5a650592cb8a709` |
| main receipt | `faa4b1a334c5f13363f92f2479aa60e0839ef8ec991da2618f7ed6bfa30df5ba` |
| heavy report | `29241eb54245a4fa4ede7f6b4791f85e77dccb30bba668ca6a6662a6636fedfe` |
| heavy receipt | `4ca5aaac396407f594585f23bf42403124748ecbeaa83dd82a506b9e821348dc` |
