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

## L01 authority reachability follow-up — 2026-09-13

The oracle is unchanged: ADR 0015 holds that an absence reported by session fallback cannot prove that a key in an unavailable keychain was removed, and that a read through the active backend may report unavailable without establishing absence in another store.
This follow-up is the [Linux finish line](../audits/2026-09-05/linux-finish-line.md) L01 slice and closes the gap between that decision and the read boundary.
No further decision record is proposed.

`Store.ConnectionConfig` returns `secrets.ErrNotFound` both when the recorded origin fails `Origin.ReadableBy` and when the backend reports the value absent, so a changed origin and a genuinely erased credential are one result.
A locked read takes a third path and returns the untyped `credential backend could not read the saved key`, so the two situations that are both unreachable authority are classified differently and neither is recognisable to a caller.
`Connections.open` is the only caller that classifies the result, can test only the erasure sentinel, and hedges its message because the type cannot tell it which is true.
`CredentialStatus` has no facet separating recorded work whose storing authority this session can confirm from work it cannot.

Nine cases ran under the race detector against a `git archive` of the tracked tree at `31c6c3d51f2fd29539c72e4935a7b766b918863b`.
Actual exit was 1 after 6.823 seconds: four intended assertion failures and five passing controls, with no skip, timeout or setup failure.
The changed-origin cases start from session provenance rather than a recorded keychain authority, and the retained restart case constructs a new `Store` over the same open database rather than restarting a process or reopening the database.
They are Go store tests over the real SQLite schema; they establish nothing about a native keychain or physical erasure.
The private packet `/home/csmart/code/.aeronautes-work/20260913-claude/l01/before` retains the probe, producer, command, digests and raw output; the probe SHA256 is `81a73d2815b3d56cf1ec68a4f03665e5854b103a80e65d507e4e38715064462b` and the raw output SHA256 is `702dd025e92a351d3a8dd485e5d7cfae36dcd1efaa744e36e851138726512185`.

The contract adds one predicate for the exact authority that recorded a reference: an origin is verified only by a live session store reading its own instance.
`ReadableBy` stays as permission to attempt a read and is not a reachability claim, because it admits any keychain adapter instance and a locked adapter alike.
Absence proves erasure only under the verified predicate; a keychain or legacy absence stays unproven until verified persistent-backend selection exists, and is refused as unreachable authority rather than reported as erasure.
`secrets.ErrAuthorityUnreachable` carries those refusals and every other failed backend read; it neither wraps nor satisfies `secrets.ErrNotFound`, and carries no reference, instance identifier or credential value.
`Connections.open` separates the two: erasure asks for re-entry, and unreachable authority asks the operator to unlock or reconnect the original store or enter the key again, without claiming the old key was erased.

`CredentialStatus` gains an `Unverified` facet using that same predicate, so the count and the read decision cannot disagree.
It counts recorded work this session cannot confirm it can reach, including every keychain and legacy row, and excludes saves in progress in this app so it overlaps only the retired and unfinished counts.
Zero means every recorded row was written by the store this session is using; it never means work was cleaned up or that a native key is reachable.

A refused read rewrites no binding, retires no reference, deletes no value and removes no journal evidence.
Generation admission, publication leases, job pins and the existing refusals are unchanged.
Verified persistent-backend selection, physical cleanup, the wider P10 and SEC-01/04 rows, native acceptance and the valuable-data hold remain open.

### Correction delivered

`Origin.VerifiedBy` reports the exact authority that recorded a reference, which only a live session store reading its own instance satisfies.
`ReadableBy` is unchanged and now documented as permission to attempt a read rather than a reachability claim.
`ConnectionConfig` returns `secrets.ErrNotFound` only for a bookmark with no reference and for an absence its own recording authority reports; an unreadable recorded origin, a failed backend read and an unproven absence all return `secrets.ErrAuthorityUnreachable`, which does not wrap the absence sentinel and carries no reference, instance identifier or backend message.
`Connections.open` separates the two: an absent key asks for re-entry, and an unreachable store asks the operator to unlock or reconnect it or enter the key again, without claiming the key was erased.
`CredentialStatus` and its DTO gain `Unverified`, counted from the journal and the active origin with the same predicate, excluding saves running in this app.
The cleanup view shows that count, names it as a subset of the retired and unfinished rows, and states in both directions what the number does and does not establish.
Journal-only reporting still works with no active backend, where nothing can be confirmed and every counted row is unverified.

Two existing regressions changed their expected error and were strengthened rather than relaxed: the recorded-session lifetime case and the persistent-reference fallback case now require the unreachable sentinel and additionally require that it is not the absence sentinel, while their preserved value, provenance and durable-work assertions are unchanged.
The status fallback case now expects three unverified rows against a store that recorded none of the work.

The four original probe failures became permanent regressions asserting the new classification positively.
Added cases cover a second keychain adapter instance, a legacy reference that stays readable and whose later absence is unproven, a new session store, the active-save exclusion, the service guidance for both classes, and the DTO facet.

Eight independent broken-guard controls each removed exactly one guard in an isolated copy of the candidate and were all caught, with the unmodified candidate passing first.
Collapsing the verified predicate into `ReadableBy` failed four cases; reporting an unreadable origin as erasure failed seven; trusting absence from any authority failed two; leaving a failed read unclassified failed three; counting active saves failed one; counting by permission instead of verified authority failed two; making the unreachable sentinel wrap the absence sentinel failed nine; and merging the registry guidance failed one.
The producer and its manifest are in the private packet `/home/csmart/code/.aeronautes-work/20260913-claude/l01/after`, with manifest SHA256 `c9585d1959a3d09bd5b8837331a42764802a8a5380d8952f4f12b529769718cf` and producer SHA256 `e8e805ee237350c3edd4e3bb158770b4fddedb815c648656a7aea93324e56db8`.
Those controls run without the race detector because they establish which assertion catches which removed guard, not concurrency behaviour.
Each control unpacks the committed tree, overlays the uncommitted files and discards the copy afterwards, so the commit its manifest records names the starting point rather than the exact source that was compiled.
Read those results as evidence about the guards, not as an identity record for a tree.

This closes the L01 read and reporting boundary only.
Verified persistent-backend selection, bounded native operations, physical cleanup and retry, the remaining P10 and SEC-01/04 rows, native acceptance on the six targets and the valuable-data release hold are all unchanged and open.
No count, refusal or zero result grants physical cleanup authority.

Verification on the delivered head covers the whole module with the race detector, the two-account SAIO fixture on loopback port 18080 and the pinned official rclone: 22 packages pass with no failure, 4,512 distinct test names each with a terminal result, of which 4,142 are leaves; 4,511 of the named results passed, 4,141 of them leaves, and the only skip is the expected `TestRcloneMatrix`.
Two existing whole-struct comparisons needed the new field before that run passed, and both still pin the exact struct and the exact JSON: the keychain publication protocol test now expects one unverified row, because a keychain-recorded candidate is exactly what the contract refuses to call confirmed, and the system credential test now expects `"unverified":0`, because the active session store recorded that work.
Neither change relaxed an assertion, and both packages pass alone.
The system package took 1,170.03 seconds against its 25-minute limit while two other suites shared the host, so that figure is an upper bound rather than an isolated measurement.
Go build and vet pass across the module with the frontend bundle built, so the root package is included; the frontend check reports no errors and its 1,355 tests pass; generated bindings are unchanged under the pinned build tool.
This is Linux evidence on one workstation and establishes nothing about a native keychain on any platform.
