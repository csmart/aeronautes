# ADR 0077: durable facts for container and marker creation

**Status:** Proposed on 24 September 2026 as the contract for the operator's decision of the same day ("not best effort, creation gets durable facts"), reviewed before its implementation, and implemented the same day: both creation paths record an intent in the mutation ledger before the PUT and the outcome with its audit event in one required transaction after it, an intent that fails issues no PUT, a record that fails after a PUT that may have landed is a named residue error carrying the attempt identity, and a creation whose confirming read fails is refused as unverified rather than reported.
**Scope:** `MutateService.CreateContainer` and `MutateService.CreateMarker`, the two creation paths v1 keeps under decisions 3 and 10 of the finish line. This extends the L04 recording contract of [ADR 0058](0058-object-metadata-durable-facts.md) and [ADR 0062](0062-container-metadata-durable-facts.md) to creation; it does not close L04 or the valuable-data hold.

## Context

Container and marker creation write to the store and then append an audit event whose failure they ignore: the creation is not best-effort, its record is.
A crash or a lost reply between the PUT and the record leaves the app with no durable statement of what it attempted, and a record that fails after a successful PUT still reports the creation as applied with a hole in the history.
Since 23 September the creation paths capture the admitted account once, so the attribution half is already in place.

## Decision

1. After the existing admission checks (the deferral gate, the writer gate, the storage-policy gate), persist a unique intent in the existing `mutation_facts` table before calling the adapter.
   Its operation is `container.create` with the literal container name as the resource, or `marker.create` with the container name, a slash and the prefix; its bookmark is the captured account.
   An intent failure returns an error, issues no PUT and leaves no committed attempt.
   Migration 0011's ledger is reused; no new schema is needed.

2. Persist the adapter outcome and its audit event in one transaction before returning the result, through the same required-record path the metadata writes use.
   A required outcome or audit failure never returns an applied detail with a nil error.
   If a PUT succeeded or its effect is uncertain, return a named residue error carrying the attempt identity and wording that says the container or marker may exist and the outcome could not be recorded; the remote state is not rolled back.
   Reopening that identity yields the original `intent`, not an invented terminal outcome.

3. Outcomes are `intent`, `applied`, `failed` and `unverified`.
   A received store status for the PUT (201 or 202 for a container, 201 for a marker) followed by a successful read of the created resource is `applied`.
   A received refusal (a rejected policy, a name the store refuses) is `failed`, as is a refusal the service itself makes after the intent exists, such as a marker name occupied by a real object or by an existing marker.
   A PUT error without a received store status, or a successful PUT whose confirming read fails, is `unverified` and returns a named error carrying the identity; the existing transport classification decides which.
   `conflict` and `verify_mismatch` do not apply: creation has no fingerprint and no working copy to compare.

4. Preserve the transaction IDs that were actually observed and never fabricate one.
   Record errors on a path with no possible PUT are errors without a residue claim.

5. Hold the writer gate through the required outcome transaction and release it on every return; the [ADR 0053](0053-synchronous-writer-shutdown-ownership.md) drain order is unchanged.
   The bridge signatures stay as they are: `CreateContainer` returns the container detail, `CreateMarker` the prefix detail, and an unresolved attempt reports through the error's identity as the metadata writes do.

## Required proof

Retain system-boundary regressions on the mock fixture for intent refusal before the PUT, outcome and audit refusal after the PUT, a lost PUT reply, a received refusal and the applied case, each with reopen by identity and independent remote snapshots showing unchanged collateral; run the applied, refused and lost-reply cases on the owned Swift as well.
Show that audit retention does not remove creation facts and that a connection switch after the PUT cannot relabel them, extending the creation-account proof.
Independent review must reproduce the evidence and catch issuing the PUT before the intent, swallowing a required record failure, and classifying an uncertain PUT as failed.

## Limits

A residue error means the container or marker may exist; the operator finds it in the listing and the fact in the ledger, and nothing here deletes it.
The captured account is attribution, not trusted approval; the deletions stay on best-effort audit under the deferred capabilities.
