# ADR 0061 — preserve early verification and final publication facts

**Status:** Accepted and implemented with the native download recording correction.
**Scope:** Correct the native-download recording conflict in [ADR 0060](0060-native-download-content-binding.md) while retaining [ADR 0033](0033-download-item-recording.md) required-record stop boundaries.
Engine content binding and L07 cancellation expansion remain outside this decision.

## Problem and oracle

At `acda6c3c`, native download postpones its only verification write until all native items have run.
The unchanged service regressions show that a later admission refusal loses the earlier item's verification, and that a verification write refusal is discovered only after later selected GETs and publications have occurred.
ADR 0033 requires execution and verification acknowledgement before the next dependent effect.
ADR 0060 also requires detecting a published name substituted before final confirmation.
One immutable verification row cannot truthfully represent both observations at different times.

The oracle remains exact selected identities, independently witnessed local and remote effects, ADR 0033 required-record acknowledgements, and ADR 0060 source/content/publication checks.
The inode descriptor correction remains required throughout the confirmation window.

## Decision

Keep the existing execution and verification observations immutable and restore their per-item acknowledgement order.
Successful native content, closure and no-replace placement must be recorded before another native GET, directory/temporary creation or engine effect may begin.
A refused execution or verification write stops later dependent work exactly as ADR 0033 requires.
Configured recorders must provide the final-publication callback as well as the existing three callbacks, and incomplete configuration must refuse before any effects.
Direct callers without a recorder keep their unrecorded compatibility mode, including the same final identity check and descriptor lifetime.

Add one explicit final-publication requirement to each newly admitted native file, and one separate immutable final-publication observation after successful early verification.
Do not infer the requirement from a staging-name prefix or redefine the existing verification field.
Directory markers, engine items, upload, Copy and historical items do not acquire this requirement.
Historical rows default to no additional requirement; migration must not fabricate observations or alter earlier terminal summaries.

The final check keeps the current ADR 0060 window: after the native loop, before any queued engine dispatch.
It compares the published name through the held destination parent with Stat on the descriptor pinned before writer closure.
A matching identity produces a verified final-publication observation with no new transfer effect.
A missing, replaced or uninspectable name produces failed/unverified final-publication evidence, leaves the current occupant untouched and identifies the final location as unresolved.
The second descriptor prevents inode-number reuse from turning a substitution into a match; it does not freeze the pathname after the check.

Every exit also attempts final confirmation for already completed native items whose execution and early verification were acknowledged, including a later admission refusal or cancellation.
These are read-only checks and completion records, not transfer, placement or filesystem cleanup authority.
Saving such already witnessed completion facts may continue despite the job-store latch, as ADR 0033 already permits; public reads and new jobs remain unavailable until the existing recovery boundary.
If execution or early verification for the current item was not acknowledged, release its descriptor and leave the corresponding missing facts visible.
Close all remaining pins on every return, including a final-publication record refusal.

A required final-publication write refusal stops effects that have not yet begun, including queued engine work.
It cannot stop or undo native effects that occurred before that final write was attempted.
The completion transaction refuses an unqualified complete result while a copied item lacks a required successful final-publication record.

## Stored and displayed facts

The authoritative Go item contract gains a boolean final-publication requirement and an optional final-publication observation.
The application DTO and generated frontend type carry both additions, and the jobs view displays the final check separately from the original content/placement verification.
An absent final observation means not required for an unflagged historical/other-route item, not reached for an item that never established copied execution, or missing required evidence for a copied flagged item.
The UI must not present an earlier verified row as sufficient when final-publication evidence is missing or failed.

Migration 0013 adds the item flag and a separate one-row-per-item publication table, preserving the two existing observation phases and their immutable rows.
The store accepts publication facts only for an admitted flagged native-download identity with acknowledged copied execution and successful early verification.
Repeated identical facts are idempotent; a different second fact, a mismatched identity or a terminal-job append is refused.

Failed final-publication evidence contributes one distinct failed name, an unverified copy, unresolved final-location state and a problem, without double-counting an identity whose earlier phase also failed.
Missing required final evidence contributes an unverified copied item and blocks complete; it does not invent a witnessed failed comparison.
The same rules feed item pages, exact totals, terminal job state and audit, and orderly reopen preserves them without contacting Swift.

Retain witnessed copied counts and bytes when a later publication check fails: the earlier placement happened.
Reduce the final verified count only when the final check actually witnesses a mismatch or cannot establish identity, and retain the exact count independently of failure display samples.
A refused persistence write does not erase a successfully witnessed check, preserving ADR 0033's distinction between counters and missing durable facts.

## Ordering, cost and evidence

This contract was reviewed before its consumers changed.
The separate account-scoped browser caller correction remains required at integration; the final-publication fields do not change read authority or bookmark arguments.
The Go executor, store, adapter, DTO, generated bindings and UI must form a complete implementation rather than shipping a partial contract.
Migration 0012 belongs to the independent persistent-backend work.

Keep all existing required-record refusal assertions and their exact GET/local-delta expectations.
Add maintained service cases on the mock and two-account Swift for early verification followed by publication substitution, a successful control, cancellation/late admission failure preserving completed facts, refused final-publication persistence before queued engine work, and missing final facts after reopen.
Prove that removing the held descriptor or final comparison makes a named behavioural assertion fail after compilation, and keep the original remove/recreate witness.
Verify item pages and UI distinguish early verification, successful final confirmation, failure, missing evidence and historical absence.
Exercise SQL immutability, identity validation, upgrade/reopen, exact distinct facets and false-complete refusal.

The addition costs one small required-record transaction and one retained descriptor per successful native item until final confirmation.
One descriptor per pending native item still scales with the native selection; no new workload ceiling or larger safety budget is introduced.
This does not close L10, L05, DS-04 or the valuable-data hold.

Rejected alternatives are delaying the only verification write (breaks ADR 0033), rewriting a prior verification (erases history), dropping final substitution detection (breaks ADR 0060), and weakening service assertions to accept later effects after a required-record refusal.
