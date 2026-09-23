# ADR 0065: preserve the account of an object metadata write

**Status:** Accepted and implemented for object metadata attribution.

## Context

[ADR 0058](0058-object-metadata-durable-facts.md) makes the object metadata intent and outcome/audit records required.
Those records must describe the same attempt and the account whose object was changed.
At `e4b7730721adc2be`, the service obtains a session, reads the active bookmark again for intent, and reads it once more for the outcome's audit event.
The [account attribution probe](../experiments/2026-09-21-object-metadata-account-attribution.md) catches a connection change after the first account's POST: the remote change and reopened fact belong to that account, but its audit row names the second account.
This is the existing account-specific result requirement in frozen L11, within the object metadata capability v1 keeps.

## Decision

1. Capture one validated active session and bookmark together when the object metadata write enters its existing writer gate.
   Reuse the connection registry's existing validated snapshot; unavailable, deleted or changed bookmarks retain its current refusal behaviour.
   Do not acquire a second active session or bookmark to label that attempt later.

2. Use the captured session for the adapter and the captured bookmark for both the required intent and the outcome transaction's audit event.
   A subsequent connection switch must not relabel either record.
   Preserve the unique attempt identity, operation and literal object resource across the transaction and reopen.

3. Preserve ADR 0058's ordering and failure semantics.
   A refused intent prevents the dependent POST, outcome and audit remain atomic, uncertainty and residue remain explicit, and the writer gate stays occupied through the required record.
   The adapter's metadata preflight, conflict handling, verification, observed transaction IDs and non-exclusive write limitation are unchanged.

4. Preserve the bridge signature and editable surface.
   The captured bookmark attributes the admitted operation; it grants no new authority to the caller and makes no claim about trusted approval, the account the caller intended, or current authority after a later edit.
   Existing per-request generation admission remains in force.
   Stored bookmark identity does not add a historical generation ledger.

## Required proof

Keep a same-account passing control and a protocol-barrier regression that changes the active connection during the first account's save.
Use identical resource names on two accounts, independent remote snapshots, raw audit reads and reopening of durable facts.
Exercise successful and unsuccessful outcomes so the account follows the attempt even when the adapter or required record fails.
Run the maintained system boundary on the mock and owned real Swift providers, preserving the existing intent, outcome, uncertainty, retention and shutdown tests.
Independent review must reproduce the current failure and catch a compilable mutation that labels the outcome with the later active account.

## Alternatives and limits

Reading the active bookmark again at completion reproduces the defect whenever the operator connects elsewhere while the write finishes.
Blocking all connection changes for a metadata write would add a new lifetime and UI policy that correct attribution does not require.
Carrying the already validated tuple is the smallest change that keeps the existing operation and its records together.

Audit.Search projection and display, account portraits, named-read snapshot races and container mutation attribution remain separate slices.
The deferred `CreateContainer`, `CreateMarker` and `DeleteMarker` paths still use best-effort recording; until 23 September 2026 the two creation paths could relabel their audit event after a connection switch, and `DeleteMarker` still can.
   Container and marker creation, returned to v1, capture the admitted account since 23 September 2026 (the [attribution record](../experiments/2026-09-21-object-metadata-account-attribution.md) follow-up); the deletions still record under the active connection.
No schema, frontend mutation binding, public-ACL approval, crash/power-loss, physical recovery or native acceptance is added.
L11's caller-named mutation binding remains open: this captures the admitted account without proving which account the caller intended.
L04, L11, P12, P13, P28 and the valuable-data hold remain open.
