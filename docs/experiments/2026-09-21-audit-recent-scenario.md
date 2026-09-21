# Legacy Recent scenario floor

## Oracle and result

The oracle is [ADR 0059](../adr/0059-account-scoped-remaining-reads.md)'s always-refused unscoped Recent API and [ADR 0013](../adr/0013-scenario-coverage-reconciliation.md)'s source-owned denominator and receipt/lifecycle rules.
At clean source `8e2564f2e64587c9a88496a8a42662e989b340c8`, 53 action rows require 118 cells with inventory hash `225097e88a9d7375`.
Recent incorrectly requires happy even though its public call always returns `ErrBookmarkRequired`.

A private Go overlay added only ordinary tests to the contract and reconciler packages; it changed no production source or scheduling.
The Go 1.27.1 race-enabled before run compiled and caught four assertions: Recent's required cell is happy, its refusal-only row fails validation, the reporter rejects its refusal claim, and the reporter accepts its impossible happy claim.
The table parent also failed; it is not a fifth independent catch.
All 52 non-Recent happy-baseline controls passed.
The existing public refusal, RecentFor account/empty-bookmark, Search, bridge-classification and production-gate controls passed with 23 pass events and no skips.
These synthetic reporter claims demonstrate the coverage contradiction within the accepted cooperating-author threat model, not a successful public read or an authentication guarantee.

## Evidence and correction

The durable packet is `/home/csmart/code/.aeronautes-work/20260921-audit-recent-scenario`.
Its eleven-file `SHA256SUMS` has SHA-256 `93a36104d42b7e5b5c60e26a6bb4bd782775ea4fad8dc34d5d597f248137f6a7`.
It retains both test files, the test-only overlay, raw JSON streams, stderr, exact command/source metadata and test names.
The runs began at 07:54:23Z on 21 September 2026, took 0.514s and 5.723s, and overlapped an independent systemtest run; load rose from 1.30/1.90/2.21 to 1.60/1.95/2.22.
No timing headroom or timing-failure cause is inferred.

[ADR 0068](../adr/0068-refused-audit-recent-scenario.md) replaces only Recent's happy cell with refusal and permits only that exact Read/refusal exception in the validator.
The maintained public-call receipt asserts the refusal sentinel and no returned events against account-labelled rows, then verifies both named-account reads retain their original events before crediting refusal.
The following correction measurements describe the original implementation based on `8e2564f2e64587c9a88496a8a42662e989b340c8`, before later account-read inventory additions.
Its focused correction checks pass under the race detector: 138 contract/reporter, 10 system receipt/inventory/hygiene and 23 public-control pass events, with no skips or failures; `go vet` also passes for the affected packages.
The unchanged reporter accepts the actual refusal receipt, reporting one covered cell from this focused stream, 118 required cells and `coverageComplete=false`.
Comparing all rows with the base inventory leaves exactly 117 cells unchanged and changes the hash to `edbd1f136c0296d3`.
The correction packet is `/home/csmart/code/.aeronautes-work/20260921-audit-recent-scenario-after`; its 37-file `SHA256SUMS` has SHA-256 `6eb2c970f0b11cf4823707837d633c15cd536bc1db45c74f24c8aaba631676b8`.
It retains the staged source identity, patch and file digests, raw runs, actual report, inventory comparison, evidence-reader corrections and concurrent-load context.
All other required cells, including later independently approved additions, RecentFor/Search, production gates and claim/parser lifecycle meaning remain required.
This record establishes neither release completeness nor native-platform or valuable-data acceptance.
