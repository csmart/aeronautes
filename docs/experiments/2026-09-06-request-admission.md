# Bookmark request admission validation

The behaviour oracle is accepted [ADR 0015](../adr/0015-credential-bindings.md): after a binding change or logical deletion returns, an obsolete generation cannot admit a new request, including through sessions already held by a caller.
An operation admitted before the change can still complete or have remote effects; revocation cannot promise rollback.
Cosmetic changes retain their generation and remain usable.

## Delivered behaviour

The connection registry binds a generation check to each bookmark session before its first authentication.
The session's native transport runs that check before every dispatch, covering authentication, library requests and retries, raw requests, capability discovery, lazy listing pages and stream reopening.
The check shares bookmark publication coordination, rejects missing, changed or unreadable generations, and releases coordination before any external I/O.
Standalone sessions created without a bookmark can omit the callback; application bookmark sessions always supply it.

Credential snapshot access checks the generation explicitly because a fresh token can avoid the HTTP transport entirely.
An engine remote retains the captured credentials and trust together with a parent-only admission callback.
Gateway creation validates that authority, and every upstream dispatch checks it again after any intervening scope validation or body read.
The separate sidecar-policy HEAD and the later sidecar PUT each require their own successful admission.
Callbacks do not enter child configuration, frontend bindings or persisted settings.
Refused transport requests close their owned request body, and gateway errors keep private generation-read details out of child responses.

The admission check is the ordering point relative to a bookmark change.
A remote operation whose check completed before that change may reach the server or finish afterwards.
Later retries, listing pages and stream reopens require another successful check.
Live job pins preserve the binding throughout queueing and cancellation cleanup, so an operator edit cannot revoke a running job's cleanup authority.

## Permanent checks and negative controls

Application regressions hold a real session from the bookmark service, using SQLite, the session secret store and an HTTP fixture that counts every request, including authentication and capability discovery.
Fourteen operations are tested after key replacement, deletion, an unavailable database and a cosmetic edit.
The operations cover authentication, capabilities, account/container/object reads, object streaming, PUT/POST/DELETE, raw bulk deletion and symlink inspection, lazy container/object iteration and engine credentials.
Replacement reenters the same valid key so server-side authentication refusal cannot stand in for the generation check.
All 42 obsolete/unavailable cases failed on the unchanged parent; all 14 cosmetic controls passed.
The implementation passes all 56 cases with no requests from obsolete or unavailable sessions.

An acknowledged HTTP barrier delays a 401 until after replacement for native account and PUT requests and raw bulk-delete and symlink requests.
None may send another authentication or storage request, while a fresh replacement session must operate successfully.
Deleting the bookmark after the first listing row must refuse the next page, with one row and no extra storage request, for both container and object listings.
A stream opened before deletion can still be read, but a later contextual seek that would reopen it must refuse without another request.
All seven retry/page/reopen cases also fail on the unchanged parent and pass after the change.

Engine tests retain assertions for original and freshly edited trust, refuse both a captured remote source and new engine setup from an obsolete session, and check the callback on previously captured remotes.
Gateway tests cover initial creation refusal and subsequent GET, HEAD, staged PUT, sidecar creation, capability discovery and bulk-delete requests after revocation.
A held upstream policy HEAD permits a concurrent revocation, after which the sidecar PUT must refuse.
A real HTTP 100-continue response acknowledges the gateway's bulk-body read; revocation before sending that body must prevent upstream dispatch after the body arrives.
Transport tests verify body closure on refusal and require a fresh admission for each dispatch while permitting an already admitted operation to finish.

Five disposable Go overlays independently remove native transport admission, credential-snapshot admission, gateway dispatch admission, the callback attached to engine remotes, or request-body closure.
Every corresponding regression fails with the safeguard removed.
These are focused fault and barrier tests, not complete generic fault-harness or platform coverage.

## Verification results

The full core run used Go 1.27.1, the race detector, one uncached execution, both task-owned SAIO accounts and pinned rclone 1.74.3 including the engine matrix.
All 431 top-level tests passed across 19 packages, with zero failures or skips and 82.9% combined statement coverage.
The scenario report remains valid at 10 of 104 required cells, with 47 of 48 actions still having gaps; these regressions add no synthetic scenario credit.
Go build and vet pass, the production frontend build passes, and the pinned Wails v3.0.0-beta.15 binding generator produces no tracked changes.
The frontend is unchanged by this slice.

## Limits

The library's independently selected storage URL and token can still race during authentication within one bookmark binding; generation admission does not prove that pair coherent.
Historical recovery identity, backend-held plan capabilities and native cleanup authority remain separate work.
Physical key cleanup remains disabled until the original backend and the entire live-reference set are proven; this change does not erase captured credentials from memory.
Each native or gateway dispatch now incurs a local generation read; throughput measurement remains a release gate, and the functional suite is not a performance benchmark.
No native keychain, process-crash, power-loss or six-platform lifetime proof is claimed here.
The valuable-data release hold remains in place.
