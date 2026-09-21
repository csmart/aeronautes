# Audit account attribution

The oracle is DESIGN sections 47 and 50, frozen L11, [ADR 0059](../adr/0059-account-scoped-remaining-reads.md) and the existing [audit query lifetime contract](2026-09-12-audit-query-state.md).
[ADR 0064](../adr/0064-audit-account-attribution.md) defines the implemented Search correction.

## Before

On `e4b7730721adc2beea38c0e22257325a352e9132`, the audit store retains `BookmarkID`, but the shared service mapper does not project it.
Named and all-account Search lose the field; a named query still filters to exactly the correct row.
`RecentFor` separately restores the field and is not the defect reproduced here.
The actual browser state and drawer also hide row attribution when supplied with correctly attributed DTOs.
Two events with identical resources, operations and summaries remain visually indistinguishable by account.

The Go probe compiled under the race detector, passed its raw-store control and failed both named and all-account bookmark assertions.
The mounted frontend probe compiled, rendered both rows with identical resources and explicit All accounts scope, then failed the visible bookmark assertion.
These are projection failures; they do not establish a store-filter or recovery-count defect.

The seven-file before packet is `/home/csmart/code/.aeronautes-work/20260921-l11-audit`.
Its `SHA256SUMS` digest is `224ff5261d0a2c075928300e337856a1e9f095bfc1d69278c6c7e877312789f9`.
The packet records source, command, probe hashes, load, concurrency caveats and raw failures; it remains unchanged.

## Correction and proof

Search projects stored bookmark IDs and preserves intentional All accounts mode and the existing local-store filter.
The audit tab has its own bookmark selector and shows retained IDs with current-label and unavailable/accountless fallbacks.
Query, page and retry state capture the bookmark with operation, result and cursor.
An unexpected row bookmark in a named query rejects the whole page through the existing failed-read state before publishing any payload or advancing its cursor.
Historical reads remain available while disconnected or connected elsewhere; labels carry no mutation authority or historical-generation proof.

The maintained tests were first run with unchanged production at `a1809b1f`, whose only change from the reproduced head is the contract documentation.
The Go service test compiled and produced eleven failing subcases with four passing controls; the frontend produced nineteen named assertion failures with one passing All accounts control.
After the correction, all twenty new frontend cases, the existing audit suites and all Go audit cases pass: 96 frontend cases and 21 Go cases including parent cases, with no skips.
The frontend typecheck reports no errors or warnings.
Seven independent omission controls catch the mapper, store filter, outgoing query filter, bookmark lifetime, reply scope, shared fake filter and visible row label through named assertion failures with passing controls.
Deliberate Go test syntax and frontend syntax errors produce build failures with no assertion credit.
The implementation packet is `/home/csmart/code/.aeronautes-work/evidence/20260921-audit-account-attribution`; its sealed manifest digest and clean-head broader verification are recorded in the implementation review.
Raw before failures, unchanged test snapshots, controls, commands, source hashes, load and concurrency are retained there.
Current-base revalidation and protected delivery remain separate gates.
This local audit slice needs no Swift mutation experiment or schema migration.
L11, P28, native acceptance and the valuable-data hold remain open.
