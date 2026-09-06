# Credential publication validation — 2026-09-06

This validates the publication slice of [ADR 0015](../adr/0015-credential-bindings.md), against the binding guard at `599358572a51054a390a7532875a083ef85c9ef4`.
The backend-provenance staging refinement passed independent local design review before implementation.
The behaviour oracle is the accepted ADR: unpublished replacement keys cannot alter the old binding, publication compares its captured version, and uncertain external work remains tracked.

## Implemented boundary

A Save records a fresh reference and the originating backend/adapter lifetime in WAL/FULL SQLite before calling the external store.
The keychain call holds neither the SQLite writer nor the bookmark coordinator.
Configuration, selected reference, CA and binding generation publish together, retiring the actual previous reference in that same transaction.
A separate row revision also detects a cosmetic edit during a blocked replacement without changing the credential generation for that cosmetic edit.
Losing edits refuse rather than silently retrying against a changed row or restoring a deleted bookmark.
An edit cannot insert a row; creation generates its own ID.

Connection acquisition reads the configuration, reference, generation and CA in one SQL statement.
It leases the selected reference while the external Get is in progress, checks the generation again and returns no configuration/key on failure.
The application connection builder uses that combined read.
Previously acquired, authenticating and job-held sessions still need the later generation-lifetime work.
The connection builder currently discards the returned generation; cache-entry generation tagging and checks remain open.

A commit error is checked using an independent bounded context before reporting its outcome.
A failed transaction begin or rejected callback cannot commit and returns its original error without that extra read.
A matching published row means the write succeeded; an unchanged row leaves the failure intact.
An unavailable read-back pauses new acquisition through the store/cache entry points and invalidates the service cache.
It keeps both possible keys and the durable evidence.
This does not yet revoke a pointer already held outside the connection registry.

Logical deletion records its selected reference independently of the bookmark row.
Prepared and retired work records carry their own creation time, rather than inheriting the previous bookmark update time.
No physical key deletion or automatic collector runs in this slice.
That includes removal from the current session store, even when a legacy reference has a matching memory-only value.
The editor and README disclose that old keys can remain, and the valuable-data release hold remains in place.

## Regressions and fault evidence

| Boundary | Evidence |
|---|---|
| Put fails after changing external state; SQL rejects publication | The parent overwrites the original key in both cases; the fix retains the original row/key and a prepared replacement record |
| Failed service Save, followed by fresh authentication | The parent gets HTTP 401 from the original receiver; the fix reconnects with the original key and sends no requests to the proposed receiver |
| Connection pauses during key read while Save changes endpoint/key | The parent sends two requests to the old receiver using mixed state; the fix refuses acquisition before either receiver gets a request |
| Preparation precedes external IO | A held Put has a committed journal row visible through a separately opened database handle; an unrelated WriteTx completes while Put remains held |
| Preparation fails, or commits but loses its response | No external Put starts; the committed-but-unacknowledged preparation remains tracked |
| Failed creation after Put takes effect | No bookmark is published, but the unused key has a journal owner/reference independent of any bookmark row |
| Two replacements capture the same version | Acknowledged Put barriers choose one winner; the loser refuses, retaining its candidate and the original retired key without retiring the winner |
| Cosmetic Save during a held replacement | The cosmetic edit retains the key generation and increments the row revision; the replacement refuses instead of reverting that edit |
| Save loses a race with Delete | The row remains absent and both the retired key and losing prepared key remain tracked |
| Snapshot read blocks during replacement, or is cancelled | The old key is neither changed nor removed, acquisition refuses the obsolete generation, and the lease is released on either outcome |
| Publication commits but reports failure | A real SQLite commit followed by an injected error is revalidated for both replacement and cosmetic saves; the selected key remains usable |
| Commit response and subsequent read-back both fail | A closed read handle models unavailable local state; the result is unresolved, both keys survive and acquisition remains paused until a successful re-read |
| Delete commits but reports failure | The absent row and independent retirement record establish logical deletion; no external cleanup is attempted |
| Backend reports key/reference material in an error | Returned errors contain neither the synthetic value nor reference handles |
| A known refusal coincides with an unavailable read handle | Binding/missing-row refusal, rollback, failed begin and a losing replacement keep their original errors and never pause an unchanged bookmark; real lost-commit replies still require revalidation |
| Legacy reference | An unchanged legacy-shaped row remains readable; rotation retires its unchanged legacy key with unknown provenance |
| A different session lifetime has the same reference name | The new memory store cannot supply that key or replace its recorded cleanup provenance |
| Database and persistent fixture reopen, then session fallback | Published and unused synthetic keys are found through a new adapter lifetime; a same-name key in fallback memory is refused and every pending record survives |
| Service deletion | Active/cache acquisition is invalidated while the independent physical-cleanup record remains |
| Retirement timestamp | Replacement and deletion of a bookmark last updated in 2001 record the current operation's time; the previous implementation backdated both records to 2001 |

The first three red results are recorded separately from the later fault and concurrency tests.
The persistent fixture stores only synthetic values in a private temporary file outside SQLite.
It proves database/adapter reopen and retained evidence, not an actual process crash, native keychain selection, a running recovery worker, physical erasure or power-loss safety.
No valuable remote data is used.

## Backend premise and follow-up

The pin is `github.com/zalando/go-keyring v0.2.8`.
Its Linux adapter calls `GetLoginCollection` for each operation, its macOS adapter invokes `security` without an explicit keychain selector, and its Windows adapter uses the current credential set.
The recorded keychain adapter instance is therefore explicitly unverified persistent provenance.
An adapter upgrade must recheck those selectors and the [platform evidence in ADR 0015](../adr/0015-credential-bindings.md#backend-provenance-during-staged-implementation).
Negative lookup through a changed selector or session fallback is not removal evidence.

Outstanding P10 gates are verified native-backend routing and cleanup, visible pending-work status/retry, complete ambiguous-outcome lifetime handling, revocation of held and authenticating sessions, job admission/pins through cancellation cleanup, and the remaining library request URL/token pairing.
Cleanup must eventually share the coordinator used for active prepared writes, acquisition leases and job pins, and do external IO after releasing it.
The current store coordinator never calls the connection cache; preserve that ordering when adding revocation to avoid a lock cycle.
Backup-copy ownership and the six native platform runtime/erasure proofs remain separate release work.

## Verification

The final core run passes 397 top-level tests across 19 packages with zero skips or failures, using the race detector, two-account SAIO on loopback port 18080, official rclone 1.74.3 and the system matrix lane.
Statement coverage is 82.3%; scenario coverage remains valid at 8/102 cells, with all 47 actions still having gaps.
The controlled failure, concurrency and service cases were also repeated three times under the race detector before the final failed-creation and lost-preparation cases were added.
Those final cases pass in the complete run.
Frontend check reports zero errors/warnings, the build succeeds and all 48 tests pass.
Go build/vet and Wails beta15 binding generation pass; the generated bindings have no changes.
The secret and bookmark packages compile for Linux, Windows and macOS on amd64 and arm64; this is compile evidence, not native runtime or keychain-erasure evidence.
The full run and focused red/green records are retained as local review evidence under the `p10-publication-*` filenames.
These results do not close the broader P02/P03 fault harness or P34 coverage gates.
