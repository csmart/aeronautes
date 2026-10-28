# Replacement scenario-preservation baseline for the bulk and segment deletions

[ADR 0095](../adr/0095-prefix-deletion-by-the-per-name-protocol.md) decision 9 changes two scenario-contract rows: `DeleteService.StartDelete` becomes an explicit job action requiring `happy`, `refusal`, `conflict`, `cancel` and `partial`, with `retry` dropped because a deletion job never retries a destructive request, and `DeleteService.DeleteManifestWithSegments` moves from a destructive action to a job action requiring the same five, with `retry` dropped for the same reason.
[ADR 0069](../adr/0069-preserve-covered-scenario-cells.md) admits a changed required set only through its drift route: candidate evidence from the candidate's own run, a replacement baseline built from that run's reports that keeps every previously protected pair, and a review of the replacement together with the contract decision.
This record is that evidence and the baseline built from it; it replaces the [selected-set deletion's baseline record](2026-10-27-selected-delete-baseline.md) as the current admission, which stays as the history of the pairs this one keeps.

The expected drift is exactly those two rows: `retry` leaves both required sets, and `DeleteService.DeleteManifestWithSegments` changes kind from destructive to job, with no previously protected pair missing. The previously protected pairs for these two actions, `DeleteService.StartDelete` for `happy`, `partial` and `refusal` and `DeleteService.DeleteManifestWithSegments` for `refusal`, stay credited by this candidate, and the candidate also credits `DeleteService.StartDelete` for `conflict` and `cancel` and `DeleteService.DeleteManifestWithSegments` for `happy` and `conflict`.

<!-- Candidate run, drift diagnostic, producer reports and artifact digests
     are filled from the candidate run below once it completes. -->
