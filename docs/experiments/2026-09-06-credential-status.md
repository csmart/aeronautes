# Credential cleanup status validation

This slice implements the visible pending-work requirement in accepted [ADR 0015](../adr/0015-credential-bindings.md).
The behaviour oracle is the durable credential journal and publication coordinator: report recorded work without deleting evidence, probing an unverified backend or representing a failed read as an empty journal.
The preceding publication slice already retains references after bookmark deletion and unsuccessful external writes.

## Delivered behaviour

`BookmarkService.CredentialStatus` exposes only retired, unfinished and saving counts.
The store takes one aggregate SQLite snapshot while holding the same coordinator as publication, and distinguishes prepared references used by a still-running save in this app.
It performs no external key read or deletion and returns a generic unavailable error when the local read fails.
These are work-record counts, including uncertain writes that might never have created a key; they are not a native key inventory or an erasure claim.

The status bar opens a focused cleanup view, including when no bookmarks remain.
The view distinguishes unavailable, loading and recorded counts, offers refresh, and discloses that physical cleanup is not available in this version.
Save and Delete invalidate older status requests immediately and refresh after either success or failure.
Status failure or delay does not replace or delay the mutation's result.
Opening the view refreshes it again; superseded successes and errors cannot overwrite a newer response.

## Permanent checks

- Store tests replace a key, inject a Put that takes effect before failing, delete the bookmark, close and reopen SQLite, and read through a fresh session fallback.
  Session, synthetic persistent and legacy provenance retain the same journal records and counts without restoring the deleted bookmark.
  A completely unavailable secret backend does not affect the read.
- An acknowledged Put barrier holds a save open while status reports it as saving, without waiting for the external write.
  Publication removes it from the work count; an injected lost write response leaves unfinished work.
- Cancelled and closed-database reads return an error, with no success claim.
- System tests drive the real bookmark service and SQLite to prove deleted-bookmark work stays visible, no reference or backend handle enters the JSON DTO, and unavailable state is refused.
  The new action declares both happy and refusal scenarios in the inventory.
  Its receipt credits the actual read, and subprocess negative controls reject failed, mutation and unknown-action receipts.
- Frontend tests control promise completion to prove refresh after failed mutations, immediate invalidation, rejection of older successes/errors, and independent mutation outcomes when status fails or stays pending.
  Mounted component tests exercise unavailable state, refresh, counts, the retained-key disclosure, close and reopen.

## Verification results

The full core run used Go 1.27.1 with the race detector and one uncached execution, both task-owned SAIO accounts at the configured local port, and official pinned rclone 1.74.3 including the engine matrix.
It passed 407 top-level tests across 19 packages with zero top-level failures or skips and 82.3% combined statement coverage.
The scenario report is valid with 10 of 104 required cells covered; the new read contributes both declared cells, while 47 of 48 actions still have gaps.
Frontend checking reports zero errors and warnings, the production build passes, and all 58 tests in eight files pass.
Go build and vet pass, and bindings were regenerated with the pinned Wails v3.0.0-beta.15 generator for 48 service methods.
The frontend proof is mounted-component and state testing; native visual inspection and the six-platform credential-runtime gates are not claimed by this record.

## Limits

This slice enables no physical cleanup, including removal from the current session store.
It does not establish verified native backend selection, absence in an original keychain, backup ownership or native key erasure.
The persistent backend used in the store regression is a synthetic file fixture, not an OS keychain.
Full held/authenticating-session revocation, generation-tagged caches, job admission and cancellation-cleanup pins remain open.
The valuable-data release hold remains in place.
