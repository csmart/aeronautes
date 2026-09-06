# Connection cache generation validation

The behaviour oracle is the cache containment step in accepted [ADR 0015](../adr/0015-credential-bindings.md): an authentication begun before a binding replacement or deletion cannot restore its obsolete cached authority after that change.
The same captured generation must be checked when reading a cache entry, and the final check and installation must remain coordinated with bookmark publication.
Cosmetic edits keep the generation and must not cause a false refusal.

## Delivered behaviour

Active and per-bookmark cache entries carry the generation captured by the coherent configuration and credential read.
Authentication revalidates it before capability discovery; final installation revalidates it again after that discovery.
The store holds its publication coordinator across the generation read and the cache action, and the cache mutex nests inside that coordinator.
The SQL read precedes taking the cache mutex, with publication excluded continuously through installation.
A changed binding, deleted bookmark or unreadable generation refuses installation.

Cache readers snapshot an entry, release the cache mutex, acquire bookmark coordination and validate the generation before checking that the same entry is still selected under the cache mutex.
They refuse stale or unavailable generations, including reads of active capabilities, bookmark identity and account display name.
Conditional eviction prevents a failed older lookup from removing a newer cached entry.
Authentication, capability discovery and custom-CA file materialisation do not hold either coordinator; CA materialisation retains its own serialisation lock.

## Permanent checks and negative controls

The application regressions use the real bookmark service, SQLite, session secret store and HTTP fixture.
An acknowledged handler barrier holds authentication or capability discovery while the service replaces or deletes the bookmark, makes a cosmetic edit, or the database becomes unavailable.
Replacement uses the same valid key to publish a new immutable reference and generation, so an authentication rejection by the server cannot stand in for the generation check.
A fresh connection completes before the old result is released, proving that obsolete completion does not replace it.
Deletion must leave no restored active or cached session.
Unavailable state must leave no installed entry, and cosmetic changes must still allow completion.

Separate cache lookups after direct store replacement or deletion prove that correctness does not depend only on service invalidation.
Closed-database reads refuse sessions and active connection details.
A held database-pool connection and its acknowledged wait counter prove that each cache reader has released the cache mutex before waiting for generation validation.
The store callback test verifies that publication coordination stays held throughout cache use, refuses changed, missing, cancelled and unavailable reads, and preserves unresolved state until a successful read restores availability.

Before the production change, all six delayed replacement/deletion cases and four stale lookup cases failed; the two cosmetic lookup controls passed.
The disposable reproduction keeps the original test file against the design-only parent.
After implementation, a separate disposable worktree reverses the cache lock order and releases publication coordination before the cache action.
All five cache-reader lock tests reject the inversion, and both successful-generation callback cases reject the uncoordinated action.
These are targeted negative controls, not evidence of arbitrary fault or process-crash coverage.

## Verification results

The full core run used Go 1.27.1, the race detector, one uncached execution, both task-owned SAIO accounts and pinned rclone 1.74.3 including the engine matrix.
All 412 top-level tests passed across 19 packages, with zero failures or skips and 82.4% combined statement coverage.
The scenario report remains valid at 10 of 104 required cells, with 47 of 48 actions still having gaps; these regressions add no synthetic scenario credit.
Go build and vet pass, the production frontend build passes, and the pinned Wails v3.0.0-beta.15 binding generator produces no tracked changes.
The frontend itself is unchanged by this slice.

## Limits

An already returned session can still admit requests after a bookmark change; this slice only contains cache publication and lookup.
Full request revocation, atomic job admission, queued generation pins, cancellation cleanup and the remaining library URL/token pairing gap are still open.
Physical cleanup remains disabled, including removal from the current session store, until native backend authority and the entire live-reference set are established.
No native keychain, process-crash, power-loss or six-platform lifetime proof is claimed here.
The valuable-data release hold remains in place.
