# Replacement scenario-preservation baseline for the copy destination

Choosing and creating a copy's destination adds, under the typed service surface of [ADR 0011](../adr/0011-typed-service-surface.md), two new scenario-contract rows ([ADR 0115](../adr/0115-choosing-and-creating-a-copy-destination.md)):
- `TransferService.DestinationContainers`, a read row requiring `happy`;
- `MutateService.CreateDestinationContainer`, a mutate row requiring `happy` and `refusal`.

[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed inventory only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract change.
This record is that evidence and the baseline built from it.
It replaces the [bookmark import baseline](2026-11-13-bookmark-import-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly those two rows, and no previously protected pair is lost.
The candidate credits all three of their required cells by its own system test, so this admission adds three pairs to the protected set.

## Candidate source and run

The candidate is branch `copy_destination` at `29e21ead28b2e02a6384908f3a6ab7f0d58b49fa`, draft pull request 357 on master `3e959671ca2ddad522cc2f130865ac6779998e28`, carrying the contract rows and their coverage.
Its CI run is [43330827589, attempt 1](https://github.com/csmart/aeronautes/actions/runs/43330827589/attempts/1), a pull-request run, so the reports record the merge commit `e8f9453daf9b622f8b4859887bcae5590a40209c` and Go `go1.27.4`; the baseline identity carries those recorded values.
The Go race, frontend, smoke, Swift main and Swift heavy jobs succeeded; the integration join failed with "scenario preservation refused", and the aggregate failed on it.
Both producers record numeric test and tee exits of zero.

## The drift diagnostic

The CI join refused preservation against the admitted baseline, SHA-256 `0d05c33abe96dfec2f19730ff7cb93b667ce696d5cfb6f202c4e51b1ab845cd7`, naming exactly the two new identities as required-set drift with an empty missing-protected list.
`build-baseline.py` rebuilt the baseline with the join's own reader against the candidate identity: it refused any loss of a previously protected pair, kept all 89, added `MutateService.CreateDestinationContainer` `happy` and `refusal` and `TransferService.DestinationContainers` `happy`, and reported the expected required-set drift of the two rows.
The next exact-head full CI run re-runs the real join against this baseline on a clean checkout before the protected merge.

## Producer reports

Both reports are valid and record contract hash `46a6d3c205f56659`, 71 actions and 154 required pairs, two actions and three pairs more than the baseline this replaces.
The main report credits 92 pairs, the 89 that baseline protects and the three new ones; `coverageComplete` is false, with gaps on 34 actions, and the heavy report credits nothing.
The new pairs are credited by `TestCopyDestinationCreateMock` in `internal/systemtest`. On the second account, which is not the active connection, it lists the destination's containers (the read's happy), previews a copy to a name that account does not hold, is refused a create that names no preview scope (refusal), and creates the missing container from the preview's result (happy).

| Input | SHA-256 |
| --- | --- |
| main report | `e0e4e0e3e257cc25c7260fbfb1696a43dc6476a919348477b317de798b4a31cc` |
| main receipt | `84c55296c0a82bc5a3afce94b27af04e10264bd5769d924fcf059108725bd263` |
| heavy report | `1766ba53cbf9791d7a04e282711dd4b59744f9fd1fcb2e5142e354a647ade729` |
| heavy receipt | `71334bdf94cf2655742148e2df1ff1179d871219dd315ccf5466343fec03db50` |

Both `exits.json` files have digest `8921738b238bbc9778e286e2efb98896a0252de754c7162889a39f0892ca6d4b` and record test and tee exits of zero.

The private retained packet is `/home/csmart/code/.aeronautes-work/20261113-copy-destination/candidate-43330827589`: the downloaded producer artifacts, the replacement baseline as built, the builder `build-baseline.py`, sealed by a `SHA256SUMS` manifest.
