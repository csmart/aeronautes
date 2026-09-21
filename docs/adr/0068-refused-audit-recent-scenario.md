# ADR 0068: refusal coverage for legacy Recent

**Status:** Proposed; independent contract review precedes implementation.

## Context

[ADR 0059](0059-account-scoped-remaining-reads.md) requires the unscoped `AuditService.Recent` method to refuse and provides `RecentFor` for the named-bookmark read.
At `8e2564f2e64587c9a88496a8a42662e989b340c8`, the public method delegates to `audit.Store.Recent`, which always returns `ErrBookmarkRequired`.
The scenario inventory still assigns that method the default `Read` happy cell, and its validator rejects every row without happy.
The [catching experiment](../experiments/2026-09-21-audit-recent-scenario.md) confirms that the reporter accepts a happy claim for this impossible result and rejects a refusal claim.
This is a contradiction between the accepted API and coverage floor, not evidence of a successful unscoped read.

## Decision

1. Keep `AuditService.Recent` in the bound-method inventory as `Read` with exactly one required scenario: `refusal`.
   Replace its impossible happy cell; do not remove the action, reinterpret refusal as success or restore an unscoped read.
   Keep `RecentFor` and `Search` and their happy requirements unchanged.

2. Make the validator exception specific to the exact `AuditService.Recent` action.
   That row must remain `Read` and require exactly `refusal`; default happy, mixed happy/refusal, missing refusal, other kinds, unknown scenarios and duplicates must fail validation.
   Every other action must retain the existing happy-baseline rule.
   Do not add a general exemption flag, a new kind or a general required/N/A policy.

3. Preserve the other 117 required cells at the named source revision, including deferred production actions and Mirror's happy gap.
   The replacement keeps that revision's total at 53 actions and 118 required cells while changing its inventory hash.
   Later integration must retain independently approved inventory additions and recompute the resulting revision's denominator.
   Production gates, API signatures, backend refusal and account-scoped audit semantics are unchanged.

4. Use the existing source-owned membership check to reject Recent happy claims and admit Recent refusal claims only under [ADR 0013](0013-scenario-coverage-reconciliation.md)'s existing receipt and test-lifecycle rules.
   A refusal receipt must carry the actual public call's error and be credited only after its refusal assertions pass.
   Failed, skipped, unfinished, paused or malformed claims gain no new credit.
   Keep the wire format, parser, lifecycle state machine, single-use receipt, defensive copies and deterministic hash meaning unchanged.

## Required proof

Retain ordinary table-driven tests over copies of the inventory: only Recent may omit happy, and only its exact Read/refusal contract validates.
Keep the four catching assertions from the experiment as maintained contract/reconciler coverage, with passing non-Recent controls.
Add a top-level system test that calls the actual public Recent API against an owned database containing account-labelled audit data, requires `ErrBookmarkRequired` and no returned events, and emits its refusal receipt only after those assertions.
Run the existing public refusal, RecentFor account/empty-bookmark, Search and production-gate regressions.
Run the contract, receipt, inventory, hygiene and reconciler suites to preserve their existing negative parser and lifecycle coverage.
Compare the before/after required-cell sets so only Recent happy is replaced by Recent refusal in this slice.

## Limits

This is one explicit floor correction under the existing non-gating, cooperating-author reporting contract.
It does not authenticate claims, add a release coverage gate, settle the full required/N/A matrix or claim complete scenario coverage.
The separate release, native-platform, valuable-data and product-decision gates remain open.
