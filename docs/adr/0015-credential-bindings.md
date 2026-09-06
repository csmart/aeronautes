# ADR 0015 — credential bindings and bookmark lifetime

**Status:** Design accepted after independent local review under the authorised P10 remediation plan.
The durable SQLite prerequisite, blank-key binding guard and conservative credential publication are implemented.
Verified cleanup, recovery and full generation lifetime remain open.
**Scope:** Audit P10, SEC-01/04 and W-05/W-15; DESIGN §§11, 12, 51 and 55; K-SEC-002/003/005/008.

## Context

At the start of P10, a blank key let a bridge caller retain an existing credential while changing its authentication URL, account or trust.
The archived SEC-01 probe demonstrates that the backend then sends the saved key to the replacement receiver.
Requiring a new key for that change is necessary, but the current write ordering introduces another risk: Save replaces the external key before publishing its matching SQLite configuration.
A failed SQL write or concurrent connection can therefore pair a new key with an old endpoint.
Delete has the reverse partial failure: its row disappears before key removal, and a keychain failure leaves the cached session active.

SQLite and the OS keychain do not share a transaction.
The solution must preserve the old credential on a failed replacement, record recoverable external work, and revoke logical authority independently of successful physical key removal.
The frontend cannot grant migration authority with a boolean, a bookmark ID or an echoed backend object.

## Decision

### Credential migration

A stored key belongs to one authentication URL, username, storage URL override and TLS trust identity.
Compare the complete authentication URL and override, including scheme, host, port, path and query, rather than only the host.
Empty TLS mode normalises to system trust.
Custom trust identity uses the actual stored CA bytes, so importing identical bytes under a new CA row ID retains the same trust identity.
A change in custom CA bytes or a transition between system, custom and insecure TLS changes the binding.

Changing that binding while retaining a stored credential requires key reentry.
Refuse the whole edit when the caller supplies no replacement key; do not first change metadata, clear the old key or disconnect a valid unchanged bookmark.
Refused edits do not invalidate the existing connection.
The caller's `HasCredential` field never determines credential presence or authorises retention.
Display name and performance or upload preferences do not change the credential binding.
The editor must explain when reentry is required and retain a useful error if the backend refuses an edit.
There is no reuse-existing-key migration approval capability in this slice; any later native approval flow needs its own reviewed contract under P11.

### Immutable references and coherent reads

Give each replacement credential a fresh random reference in the existing SecretStore namespace.
Never overwrite the value at a reference that a published bookmark or in-flight snapshot can still name.
The bookmark's configuration and its selected reference are published in one SQLite transaction.
Read configuration, reference and generation together; load the key and custom CA using those captured references.
Snapshot acquisition holds a reference lease from that read until the external key read finishes or is abandoned, coordinated with publication and cleanup.
After releasing the lease, a snapshot must not read that reference again without reacquiring it through the current generation.
A retired generation may cause acquisition to fail and retry, but must never resolve to a replacement key under the old configuration.
The existing `bookmark/<id>` references remain readable until a replacement or deletion retires them.
Do not require users to reenter every unchanged legacy credential merely to upgrade the schema.
No reference, key, token or credential-bearing configuration is returned through a frontend DTO.

### External-store work and recovery

Keep a non-secret journal of prepared and retired credential references, their owning bookmark and their secret-backend identity.
The journal records work that may need compensation even if the external operation reported failure after taking effect.
Its records must survive before the corresponding external operation starts.
The database prerequisite is WAL with `synchronous=FULL` on every pooled connection; check the effective settings instead of assuming that requesting WAL succeeded.
SQLite documents that WAL NORMAL can lose a committed transaction after power loss, while FULL adds a WAL synchronisation for each commit; see [PRAGMA synchronous](https://www.sqlite.org/pragma.html#pragma_synchronous) and [WAL performance considerations](https://www.sqlite.org/wal.html#performance_considerations).
That setting depends on the filesystem and storage device honouring synchronisation and does not by itself prove the broader P12/P13 recovery contract or native power-loss behaviour.

Credential replacement follows this order:

1. Validate the requested binding and record a fresh prepared reference durably.
2. Write the new key at that reference while the old reference remains unchanged.
3. Inside the publication transaction, require the bookmark row to exist and its generation to equal the generation captured during validation.
   Assign the published generation plus one, publish the new configuration and reference, and retire the reference read from that same row in the same durable transaction.
   If the comparison fails, refuse the edit and leave its prepared replacement tracked for cleanup.
4. Revoke the previous generation's logical authority, then attempt external cleanup of references proven unreferenced.
5. Retain failed cleanup records for retry and report the committed result with cleanup pending, rather than presenting a successful publication as an unchanged failed edit.

If external Put fails or publication does not complete, keep the previously published configuration and key usable and retain enough journal state to remove the unused replacement.
An ambiguous SQL commit error requires checking the published reference before compensation; do not delete a key that might have been selected by the commit.
If that check fails, retain the candidate and journal record for recovery.
Report the publication outcome as unresolved and pause use of that bookmark until its published generation can be revalidated; do not claim an unchanged failure or a successful save.
An edit never inserts a bookmark row; creation uses a fresh backend-generated ID.
A Save that loses a race with another credential publication or deletion must not silently retry against the changed binding or recreate the deleted row.
The cleanup worker must coordinate with active Save operations so it cannot mistake a still-running prepared write for abandoned work.
For cleanup, a reference remains in use if it is published, belongs to an active prepared write, has a snapshot-acquisition lease, or is pinned by an admitted job until that job and its cancellation cleanup finish.
Claiming a reference for cleanup must check this set under the same coordination used to acquire leases, publish credentials and admit jobs; no new user can acquire a claimed reference.
Release that coordination before calling the external store, retaining the durable cleanup record until removal is confirmed.
Do not hold the global SQLite writer transaction while waiting for an OS keychain operation or prompt.

Deletion atomically removes the bookmark's logical authority and records its credential for cleanup.
Removing the bookmark row must not cascade-delete the cleanup record.
Revocation occurs before the service reports the logical deletion, even when external key removal fails.
Retries operate on the cleanup record and never recreate the bookmark or reactivate its sessions.
The user can distinguish complete deletion from logical deletion with pending credential cleanup.
An ambiguous deletion commit revokes in-memory authority and pauses acquisition while the outcome is unresolved.
Re-read the bookmark and cleanup record before declaring deletion or attempting physical cleanup; an unsuccessful re-read retains the journal evidence and cannot justify deleting a possibly published key.

The active secret backend needs an explicit identity for journal routing; a session-only store is not the same authority as the persistent OS keychain.
Ephemeral identity must include its lifetime so recovery can distinguish an ended session from an unavailable persistent backend.
An `ErrNotFound` from session fallback cannot prove that a key in an unavailable keychain was removed.
Legacy references have no recorded backend provenance: keep persistent cleanup uncertain until the OS keychain can be checked, while removing any matching value from the current session store.
Do not silently clear persistent cleanup records when falling back to memory.
This does not select or implement the vault in ADR 0006.

### Backend provenance during staged implementation

The current `go-keyring` adapter does not expose a pinned persistent store identity.
Its Linux calls select the login collection or default alias each time, its macOS calls use the current keychain search list/default, and its Windows calls use the current credential set.
These selectors are not immutable: see [Secret Service aliases](https://specifications.freedesktop.org/secret-service/latest-single/#aliases), [Apple's keychain contexts](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains) and [Windows credential-set selection](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credreada).
Finding a marker through a search list therefore does not prove that a later negative lookup checked the store that accepted an earlier write.
Do not invent that proof from a backend kind, machine name, user name or marker alone.

The publication slice records provenance conservatively before external writes:

| Origin | Recorded provenance | What it proves |
|---|---|---|
| New session value | Session backend and a random ID for that store instance | The exact in-memory store lifetime that accepted the write |
| New keychain value | Keychain backend and a random ID for that adapter instance; persistent authority unverified | Which adapter invocation lifetime attempted the write, without asserting a stable native store |
| Existing reference | Legacy unknown provenance | Neither persistence nor the original backend has been established |

Instance IDs and reference names are internal non-secret metadata; neither is a frontend capability or a credential value.
Never equate an adapter instance ID with a persistent authority ID.
An unchanged legacy reference remains readable through the active backend, and a new keychain reference remains readable after restart through the keychain adapter by its unique immutable name.
Those reads may succeed or report unavailable; they do not establish absence in another store.
A recorded session reference is read only from its originating live session store, never from a newly opened session or a keychain that happens to contain the same name.

The first publication slice retains all prepared and retired work after failure or replacement and adds no automatic garbage collector.
It must still keep logical deletion's selected reference tracked, without depending on the removed bookmark row, and must never rewrite a published reference.
This deliberately avoids deleting a reference before the later acquisition/job coordination and native-backend proof exist.
It is not completed recovery: pending keys can consume storage, and the cleanup/status slice must surface and resolve that work before P10 is complete.
No session-fallback result, new adapter lifetime or persistent `ErrNotFound` can silently remove that evidence.
Native selection, verified persistent authority and recovery after a lost deletion response remain required follow-up work, with tests for changed defaults and unavailable original stores.
Do not claim that these metadata records establish backup-copy ownership or prove native keychain erasure.
The additional [write-verification prerequisite](0017-keychain-write-verification.md) addresses false success from dismissed native prompts before publication, while retaining these original-store and cleanup requirements.

### Generations, sessions and jobs

Each published credential binding has a monotonic generation, including replacement of a key without an endpoint change.
Session acquisition and publication must verify that their captured generation is still current after any blocking read or authentication.
Each session cache entry records that generation, so cache lookup and revocation use the same identity as acquisition.
Revocation covers active, cached, held and still-authenticating sessions, including native and child request admission.
After an edit or logical deletion returns, an obsolete generation cannot admit a new request.
An already admitted remote operation can have effects despite cancellation; its outcome still needs the normal transfer and recovery rules.
Do not claim that revocation rolls those effects back.

Coordinate job admission with credential-generation changes and deletion so that checking for active jobs and accepting a new job cannot race.
Admission acquires the generation pin before durable job creation, including queued jobs that have not acquired an execution slot; failed creation releases the pin.
Refuse a binding change, key-only replacement or deletion while an admitted job still uses the bookmark, and require both the job and its cancellation cleanup to finish before retrying that edit.
Cosmetic changes can retain the binding and need not cancel a job.
This preserves the existing child cancellation cleanup grace instead of revoking a running transfer's cleanup authority during an edit.
Queued work must retain its reviewed generation and refuse a changed or deleted one rather than silently acquiring the bookmark's replacement identity.
Backend-held plan capabilities remain P11 work; the generation check is a prerequisite, not a substitute for plan authority.

## Implementation sequence and proof

Implement and review each logical slice separately:

1. Establish and verify the durable SQLite prerequisite, recording its cost and the limits of the proof.
2. First add binding-change refusal with the editor explanation as an independently tested containment commit.
   Then add backend-aware prepared/retired references, immutable replacement and coherent credential snapshots as the next publication commit.
   Record unverified native provenance honestly and retain unused references until the cleanup prerequisites are implemented.
3. Make logical deletion and pending-work status recoverable while retaining all external references.
   Keep session, legacy and unverified persistent provenance distinct across fallback and restart.
4. First tag cached sessions with their captured generation and revalidate after blocking authentication and capability discovery, coordinating cache publication and lookup with bookmark changes.
   This is a bounded containment slice: an authentication started before edit/delete must not install an obsolete cached session after that change.
   It does not revoke already-held sessions or grant cleanup authority.
   Then coordinate job admission and generation revocation through request admission and cancellation cleanup, and close the remaining library request URL/token pairing gap.
5. Establish verified persistent-backend selection before using absence as proof of completed cleanup.
   Enable physical cleanup and retry only after both native authority and the complete live-reference coordination above are proven.
   Cover failed external deletion, a lost deletion response, changed defaults, unavailable original stores and legacy/session fallback without restoring deleted bookmark authority.

The cache containment slice precedes physical cleanup because obsolete authentication can republish a session independently of keychain availability.
Cleanup already depends on acquisition leases and admitted-job pins, so completing that lifetime coordination first removes a prerequisite instead of allowing a collector to run without it.
Native selection may be investigated independently, but this ordering does not select a replacement for ADR 0012, weaken original-store absence proof or choose the vault in ADR 0006.
The cache implementation must use one lock order, bookmark coordination before the connection cache, and release both before authentication, capability discovery or any other external I/O.
Convert every existing path to that nesting, including `Connections.Session`, which currently holds the cache mutex while calling `Store.Available`; remove that reverse nesting before adding any bookmark-coordinator-to-cache path.
The final generation recheck and cache installation are one atomic step under bookmark coordination with the cache mutex nested inside, so an edit either precedes the check and refuses the obsolete session or follows installation and removes it through the service's invalidation.

Each slice must state which guarantees it implements and which remain open; P10 is complete only when the combined gates pass.
The valuable-data release hold remains throughout.
The SQLite prerequisite's [validation and measured write cost](../experiments/2026-09-06-sqlite-durability.md) do not establish credential recovery or physical power-loss safety.
The binding guard compares the current persisted row inside the bookmark update transaction and ignores caller-supplied credential presence.
The service publishes an imported CA in that same transaction, so a refused edit or injected bookmark-write failure leaves no imported CA row.
Permanent service regressions cover the retarget receiver, retained live connection, explicit replacement, CA reimport and failed CA publication; store regressions cover every binding field, cosmetic edits and stale edits.
The subsequent publication slice uses immutable replacements, durably prepared work, a generation/revision comparison and a single configuration/reference/CA read with an acquisition lease.
Logical deletion retains the selected reference independently of the removed bookmark, and no physical cleanup runs yet.
The [publication validation record](../experiments/2026-09-06-credential-publication.md) distinguishes the red service reproductions, injected boundary failures, database/adapter reopen proof and remaining native/lifetime gates.
Retired and unused keys remain in credential storage; the editor and README disclose this interim behaviour alongside the valuable-data release hold.
The status bar now opens a credential cleanup view, with counts for retired references, unfinished prepared writes and saves still in progress in this app.
Its read uses the publication coordinator and local journal without contacting the secret backend or changing recorded work.
Counts survive bookmark deletion and database reopen; failed status reads remain unavailable instead of claiming an empty journal.
The frontend refreshes after successful and failed save/delete attempts and when the view opens, and discards responses superseded by another refresh or mutation.
The [status validation record](../experiments/2026-09-06-credential-status.md) covers these boundaries; the counts describe recorded work and are not proof that native keys exist or were erased.
Verified native cleanup remains open; the subsequent request-pairing correction is recorded below.
Removing matching legacy values from the current session store is also deferred; this slice makes no external Delete call, even for memory-only values.
The cache containment slice now records the captured generation and refuses obsolete results after authentication and capability discovery.
Final validation and cache installation share the publication coordinator, with only the cache update nested under the cache mutex.
Cache reads validate their generation and identity again before returning a session or active connection details; unavailable reads refuse use.
The [cache validation record](../experiments/2026-09-06-cache-generations.md) covers delayed authentication, replacement and deletion, unavailable reads, and lock-order negative controls.
Cache containment alone closes obsolete publication and lookup; sessions already returned to callers require the following request-admission step.
Copy, Mirror, upload, download, deletion and benchmark jobs now pin their captured credential generations before preflight and durable job creation.
Binding changes, key-only replacement and deletion refuse while any admitted job holds that bookmark; cosmetic edits retain the binding.
Publication rechecks job pins after an external key write, so a job admitted during that write keeps its credentials and the unused candidate stays tracked.
Failed starts release every acquired pin, including upload recovery-record failure; queued cancellation and running work release only after the entire job body and its cleanup return.
The [job lifetime validation record](../experiments/2026-09-06-job-generations.md) covers all six entry points, acknowledged cancellation/cleanup barriers and deliberately broken safeguards.
These pins protect live jobs in this process; they do not bind historical recovery records or plan capabilities, revoke held-session requests, or authorise physical key cleanup.
Bookmark sessions now carry a generation check before their first authentication and run it before each native HTTP dispatch, including implicit retries and later listing or stream requests.
Credential snapshot access checks the generation even when a fresh token avoids HTTP, and engine remotes carry the check to the parent gateway.
Gateway creation and every upstream request validate that authority, including the separate policy HEAD and dispatch after a blocked bulk-body read.
The check is coordinated with publication, with coordination released before external I/O; an operation admitted before a change may still complete or have effects afterwards.
The [request-admission validation record](../experiments/2026-09-06-request-admission.md) records obsolete-session failures, retry/page/reopen barriers, gateway checks and safeguard-removal controls.
This closes request admission by obsolete bookmark generations.
The subsequent [ADR 0016](0016-native-request-credentials.md) correction pairs the native library's URL/token selection within one binding on every attempt, with permanent failing-before regressions and a verified upstream source/patch gate.
The [pairing validation record](../experiments/2026-09-06-native-pairing.md) distinguishes that proof from the remaining native cleanup, historical recovery identity and throughput work.

Permanent regressions must cover the service-level retarget attack with zero requests to the new receiver, explicit reentry, URL/port/account/override/TLS changes, and identical-byte CA reimport.
Inject failure before and after each SQL/keychain boundary, including a Put that takes effect and then fails, publication failure, ambiguous commit and failed cleanup.
Prove that old credentials remain intact on refused or unpublished edits and that compensation cannot remove the published key.
Exercise concurrent Save and snapshot acquisition, delayed authentication racing edit/delete, held-session use after deletion, and queued job admission racing a binding change.
Prove that two Saves capturing the same generation cannot both publish, a Save racing deletion cannot recreate its row, and the losing candidate remains tracked without retiring the winner's key.
Exercise key-only replacement during a job and cleanup racing both snapshot leases and cancellation cleanup; no reference still in use may be removed.
Restart tests must find prepared or retired synthetic keys without restoring deleted authority, and session fallback must not erase persistent cleanup evidence.
Verify that keys, tokens and reference handles never reach DTOs, returned errors or audit records.
Label fault injection, persistent test-backend recovery, process crashes, actual native keychains and power-loss evidence separately.

## Tradeoffs

Reentry is less convenient than silently moving a saved credential, but provides a concrete boundary that a compromised webview cannot bypass with an existing bookmark ID.
Failed cleanup can retain unused keys until the correct backend is available; that residue is tracked and visible, rather than traded for a lost active key or restored deleted authority.
FULL synchronisation adds write cost across the database; measure it and keep performance tuning within the durable contract.
Generation and admission coordination must have an explicit lock order and must not call into a connection-cache lock while holding a lock that cache readers acquire in the opposite order.
