# Audit account attribution

The oracle is DESIGN sections 47 and 50, frozen L11, [ADR 0059](../adr/0059-account-scoped-remaining-reads.md) and the existing [audit query lifetime contract](2026-09-12-audit-query-state.md).
[ADR 0064](../adr/0064-audit-account-attribution.md) proposes the remaining Search correction; production is unchanged in this proposal.

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

## Planned correction and proof

Project stored bookmark IDs and preserve intentional All accounts mode and the existing local-store filter.
Add the promised bookmark selector, show retained IDs with honest current-label and unavailable/accountless fallbacks, and bind query, page and retry state to the captured bookmark with operation, result and cursor.
Reject an unexpected row bookmark in a named query before publishing its page.
Maintain local history access while disconnected or connected elsewhere, without making a label into mutation authority or historical-generation proof.

Promote the catching service and mounted probes into maintained regressions alongside combined-filter, legal continuation, exact retry, unavailable bookmark, accountless and deferred-reply cases.
Prove mapper, filter and lifetime omissions with passing controls, and retain raw before/after and control streams with a path and digest in the implementation review.
Run the affected Go packages with the race detector and the existing frontend audit suites, typecheck and build; broader current-base verification and protected delivery remain separate delivery gates.
This local audit slice needs no Swift mutation experiment or schema migration.
L11, P28, native acceptance and the valuable-data hold remain open.
