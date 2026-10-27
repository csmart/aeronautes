# ADR 0091 — admit the bookmark verify action to the scenario contract

**Status:** Accepted (2026-10-27) with the replacement baseline it authorises, built from candidate run 40438457699 as the [baseline record](../experiments/2026-10-27-verify-action-baseline.md) describes and reviewed in the same change as the contract row, as [ADR 0069](0069-preserve-covered-scenario-cells.md) requires.
**Scope:** The action/scenario contract decision for one new bridge action, `BookmarkService.Verify`, and the evidence route by which the preservation baseline admits its snapshot.
It changes no other action, no previously protected pair, and no gate.

## The changed identity

`BookmarkService.Verify` is new.
Its kind is `read`: it authenticates the bookmark details as typed, HEADs the account, persists nothing and returns only the account name and the HEAD counts ([ADR 0011](0011-typed-service-surface.md), 2026-10-27 note).
Its required scenarios are `happy` and `refusal`, the same pair `BookmarkService.CredentialStatus` carries: a read whose other required cell is the endpoint's refusal of a wrong key.

## The behaviour oracle

- A right user and key against the fixture account answer with that account's name, the suffix of the storage URL the endpoint returned, and non-negative counts.
- A wrong key is refused by the endpoint and the refusal reaches the caller; nothing is saved either way, so the bookmark list is the same before and after.
- The session is built as a saved bookmark's is: the endpoint policy (DESIGN §65.11 P09) and the TLS trust apply before any request.
- A saved key serves a verify only while the binding it was saved under is unchanged, and every request is admitted under the generation the key was acquired with ([ADR 0015](0015-credential-bindings.md)); a typed key is used for the exchange and discarded.
- No token, storage URL or key appears in the result (K-SEC-002).

## Previously protected identities

None change.
The 48 protected pairs of the admitted baseline stay required and must stay credited; `BookmarkService.CredentialStatus`, `Save`, `Delete` and `List` keep their rows and kinds.

## Required coverage

`TestBookmarkVerifyMock` and `TestBookmarkVerifyRealSwift` in `internal/systemtest` credit `happy` and `refusal` on the mock and the real fixture from one top-level test each, through the harness receipt that binds a credit to the call that earned it.
Unit tests in `internal/app` cover the typed-key, saved-key, changed-binding, saved-CA and endpoint-policy paths, revocation during a held authentication or a held account HEAD, and the closing of the one-shot session.

## The evidence route

1. The candidate branch carries the contract row and the unchanged admitted baseline, so its CI join reports the expected required-set drift; that failure is retained as a diagnostic and is not a passing gate.
2. The candidate's own commit, run and attempt produce the main and heavy reports and receipts with every pre-existing join check successful; they are retained with their digests.
3. The replacement baseline takes the candidate's identity and evidence digests, the complete 54-action snapshot, every previously protected pair, and every pair the candidate's main report credits; the heavy report credits none, as before.
4. The replacement baseline, this record and the retained diagnostics are reviewed together in the same change as the contract row; editing the baseline alone authorises nothing.
5. The next exact-head full CI run must pass every required check, including preservation against the reviewed replacement baseline, before the protected merge; the resulting protected-source producer evidence is reconciled and retained afterwards.

## Limits

This record admits one read action and its two cells.
It grants no waiver, no N/A and no change to any other action's required set, and it does not make the verify action evidence of native desktop acceptance.
