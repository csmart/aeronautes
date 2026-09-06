# ADR 0016 — native request credential selection

**Status:** Proposed for independent local review; no production replacement is implemented.
**Scope:** Audit P10 and W-05; DESIGN §§6.5, 9, 12 and 55; ADR 0015.

## Context

The pinned `ncw/swift/v2` v2.0.5 selects a storage URL and token in separate critical sections.
Its `storage` helper captures `StorageUrl`, releases `authLock`, and then calls `Call`, which reads the token through `getUrlAndAuthToken`.
That method only invokes `OnReAuth` when the current call itself authenticates; another call can already have replaced both credentials in the gap.
The resulting request can send the new token to the previous host or account path.
The same gap affects Aeronautes' direct metadata POST and SLO calls, despite their coherent initial credential snapshot.
These paths are visible in the [pinned library source](https://github.com/ncw/swift/blob/v2.0.5/swift.go#L596-L621).

A deterministic reproduction captures the URL through the library's getter, completes a second authentication, and then invokes its lower-level `Call` with the same callback as its storage helper.
This explicitly schedules the permitted gap between the two reads; it is not a claim that a high-level concurrent operation was paused inside the dependency.
Changing either the host or account path sends a mismatched token and fails the test; the unchanged-endpoint control passes.
A disposable module copy moving the callback out of the conditional passes that reproduction and three race-enabled runs of the existing native Swift package.
The application tree and dependency cache were not modified for that experiment.

## Decision

Carry a narrowly patched, repository-local copy of the pinned v2.0.5 module under `third_party/ncw-swift`, selected by a relative Go module replacement.
Keep the upstream version requirement, source checksum, copyright notice and licence, and record the exact upstream base and local delta.
Preserve upstream source and tests, apart from the reviewed credential-selection change and its documentation.
Do not add a runtime endpoint translation, another credential cache or per-operation connections.

After any necessary authentication succeeds, `getUrlAndAuthToken` must evaluate a supplied `OnReAuth` callback while holding `authLock`, then capture `AuthToken` before releasing that lock.
Evaluate it on every credential selection, including the first attempt and a transport retry, even when another call already authenticated.
A callback error refuses the request before storage I/O; a nil callback retains the explicitly supplied target URL.
The callback may now run without a fresh authentication and must only select the current target, without external I/O or calls that acquire the same connection lock.
All application storage calls must use the library's storage helper or a callback tied to that same session's accepted authentication snapshot.
Audit every callback before adopting the replacement.

URL and token must come from one accepted authentication response; subsequent authentication may replace that response while an already prepared request proceeds.
The change preserves the existing 401 retry and body-rewind rules, shared transport, lazy paging, stream reopen, TLS policy and bookmark-generation admission.
Do not hold a session lock across storage I/O or claim that a later revocation rolls back an admitted operation.

## Alternatives and maintenance cost

The lower-level `Call` has the same split selection and offers no hook that corrects it on an already authenticated connection.
A session-wide lock around operations would serialise storage I/O and streaming.
Per-operation connections would introduce new authentication, retry and stream-lifetime ownership, while an adapter using private endpoint aliases would introduce URL translation and token replacement for every native request.
Refusing requests based only on the latest snapshot could also reject coherent requests prepared before a refresh.
The reproduced credential disclosure and small correction at its source provide the concrete reason for the limited local patch allowed by DESIGN §6.5.

This creates an explicit dependency-maintenance obligation.
Keep the patch small and mechanically compare it with its recorded upstream base during review and upgrades.
An upstream contribution is a possible follow-up; no unreviewed release or moving branch replaces the pin.
Remove the local replacement only when an accepted upstream version passes the same pairing, retry, encoding and full application gates.

## Required proof before delivery

Retain the failing-before host/account reproduction and unchanged-endpoint control as permanent tests.
Cover callback refusal, concurrent progress, already prepared requests, authenticated retries, request-body replay, lazy pages and stream reopen, including endpoint changes and the storage override.
Removing the pairing safeguard must fail the regression without a compile failure.
Run the upstream module's tests explicitly because root `go test ./...` does not include a nested replacement module.
Run the full application race suite with two-account Swift and the pinned real child, build and vet, frontend verification and binding generation, followed by independent local review and all five required PR and master checks.
The replacement does not complete native key cleanup, historical recovery identity, throughput measurement, the six-platform release gates or the broader audit.
The valuable-data release hold remains.
