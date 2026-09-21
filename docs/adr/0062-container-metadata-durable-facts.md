# ADR 0062: durable facts for container metadata writes

**Status:** Proposed for independent review before implementation.
This extends the L04 recording contract to `UpdateContainer`; it does not close L04 or the valuable-data hold.

## Context

v1 keeps container metadata edits, including the existing ACL, quota and legacy versioning working copy.
[ADR 0058](0058-object-metadata-durable-facts.md) made intent and outcome records required for object metadata, leaving container metadata until the L09 preflight correction merged.
That correction is now on master.
At `930fbe22e5757c19`, `MutateService.UpdateContainer` still calls the store before a best-effort audit append and has no durable attempt identity.
The [container facts reproduction](../experiments/2026-09-21-container-metadata-durable-facts.md) shows a successful DTO after a refused audit append, with no fact after reopening the database.
The adapter also loses its result after a failed verify HEAD or a POST transport error, so the service records `failed` even when the write may have landed.

## Decision

1. Capture the active session, bookmark identity and advertised capabilities together for this call.
   Use that captured bookmark for its intent, outcome audit and returned container classification even if another connection becomes active later.
   This attributes an admitted write to its actual account; it does not add a trusted consent token or establish write authority across a later bookmark edit.

2. After the existing public-ACL and capability admission checks, persist a unique intent in the existing `mutation_facts` table before calling the adapter.
   Its operation is `container.update`, its resource is the literal container name, and its bookmark is the captured account.
   An intent failure returns an error, issues no dependent POST and leaves no committed attempt.
   Reuse migration 0011 and its retention-independent ledger; no new schema is needed.

3. Persist the adapter outcome and its audit event in one transaction before returning the result.
   Required outcome or audit failure must never return an applied DTO with a nil error.
   If a POST succeeded or its effect is uncertain, return a named residue error carrying the attempt identity and wording that does not claim the write definitely succeeded.
   The independent remote state is not rolled back.
   Reopening that identity yields the original `intent`, not an invented terminal outcome.

4. Preserve the preflight, write and verification transaction IDs that were actually observed.
   A successful POST followed by a failed verification read is `unverified` and returns a named container-write error carrying the identity.
   A POST error without a received store status is conservatively `unverified`, using the existing object-write transport classification; this can include an error before transmission when the helper cannot establish that boundary.
   A received HTTP refusal remains `failed`.
   Do not fabricate a write transaction ID or imply that a follow-up read can establish exclusivity.

5. Keep the existing outcomes: `intent`, `applied`, `conflict`, `verify_mismatch`, `failed` and `unverified`.
   Preflight refusal, invalid input and other proven pre-POST failures are failed attempts when an intent was already recorded.
   A fingerprint conflict records `conflict` with no POST.
   A successful verify read that disagrees with the working copy records `verify_mismatch`, preserving the returned state and mismatch.
   Record errors on a path with no possible POST are errors without a residue claim.

6. Hold the existing writer gate through the required outcome transaction and release it on every return.
   Keep [ADR 0053](0053-synchronous-writer-shutdown-ownership.md)'s drain order, [ADR 0054](0054-versioning-preflight-authority.md)'s authoritative preflight and modern-versioning refusal, and all metadata and public-ACL admission rules.
   This slice changes neither the bridge signature nor the editable container surface.

## Required proof

Retain system-boundary regressions for intent refusal before POST, outcome and audit refusal after POST, reopen by identity, and independent remote snapshots.
Exercise applied, conflict, mismatch, received refusal, lost POST response and failed verify-read outcomes, including observed transaction IDs and unchanged collateral data.
Show that audit retention does not remove container facts, that a connection switch cannot relabel them, and that existing writer shutdown and L09 refusal proofs still hold.
Use the owned mock fixture for deterministic faults and the owned real Swift fixture for provider behaviour.
Independent review must reproduce the before/after evidence and catch issuing POST before intent, swallowing a required record failure, and misclassifying an uncertain write as failed.

## Limits

The existing container protocol cannot make the interval between HEAD and POST exclusive; the L09 limitation remains.
The captured account is attribution, not trusted approval for public exposure and not a solution to every L11 authority race.
Object metadata's separate attribution path is unchanged.
`CreateContainer`, marker and manifest writes retain their current recording behaviour in this slice.
Crash or kill, power-loss durability, native window shutdown, physical recovery, download recording and representative deployment acceptance remain separate work.
L04, L08, L09, L11, P12, P13 and the valuable-data hold remain open.
