# ADR 0016 — native request credential selection

**Status:** Proposed for independent local review; no production replacement is implemented.
**Scope:** Audit P10 and W-05; DESIGN §§6.5, 9, 12 and 55; ADR 0015.

## Context

The pinned `ncw/swift/v2` v2.0.5 selects a storage URL and token in separate critical sections.
Its `storage` helper captures `StorageUrl`, releases `authLock`, and then calls `Call`, which reads the token through `getUrlAndAuthToken`.
That method only invokes `OnReAuth` when the current call itself authenticates; another call can already have replaced both credentials in the gap.
The resulting request can send the new token to the previous host or account path.
The same gap affects Aeronautes' direct metadata POST and SLO calls, despite their coherent initial credential snapshot.
These paths are visible in the pinned [credential selection](https://github.com/ncw/swift/blob/v2.0.5/swift.go#L596-L621) and [storage helper](https://github.com/ncw/swift/blob/v2.0.5/swift.go#L885-L893).

A deterministic reproduction captures the URL through the library's getter, completes a second authentication, and then invokes its lower-level `Call` with the same callback as its storage helper.
This explicitly schedules the permitted gap between the two reads; it is not a claim that a high-level concurrent operation was paused inside the dependency.
Changing either the host or account path sends a mismatched token and fails the test; the unchanged-endpoint control passes.
A disposable module copy moving the callback out of the conditional passes that reproduction and three race-enabled runs of the existing native Swift package.
The application tree and dependency cache were not modified for that experiment.
Independent review also ran eight concurrent container-HEAD workers while another goroutine alternated authentication endpoints for 1.5 seconds.
The unpatched library mispaired 1,810 of 4,444 host-change requests and 1,672 of 4,311 account-change requests; the correction produced zero mismatches over approximately 26,000 requests across three race-enabled repetitions.
These are observations from deliberately intensive authentication churn, not a production failure-rate estimate or a deterministic scheduling proof.
The staged test remains the deterministic regression; the concurrent probe supplies additional progress and stress evidence.

## Decision

Carry a narrowly patched, repository-local copy of the pinned v2.0.5 module under `third_party/ncw-swift/source`, selected by that exact relative Go module replacement.
Keep the upstream version requirement, copyright notice and licence, and enforce the base and patch through the provenance gate below.
Preserve upstream source and tests, apart from the reviewed credential-selection change and its documentation.
Do not add a runtime endpoint translation, another credential cache or per-operation connections.

After any necessary authentication succeeds, `getUrlAndAuthToken` must evaluate a supplied `OnReAuth` callback while holding `authLock`, then capture `AuthToken` before releasing that lock.
Evaluate it on every credential selection, including the first attempt and a transport retry, even when another call already authenticated.
A callback error or an empty callback result refuses the request before storage I/O; a nil callback retains the explicitly supplied target URL.
The callback may now run without a fresh authentication and must only select the current target, without external I/O or calls that acquire the same connection lock.
All application storage calls must use the library's storage helper or a callback tied to that same session's accepted authentication snapshot.
Our direct-call callbacks must return an error if that snapshot has no storage URL.
Audit every callback before adopting the replacement.
Update the library's callback documentation and add a source-header note pointing to this ADR and the recorded patch.

URL and token must come from one accepted authentication response; subsequent authentication may replace that response while an already prepared request proceeds.
The change preserves the existing 401 retry and body-rewind rules, shared transport, lazy paging, stream reopen, TLS policy and bookmark-generation admission.
Do not hold a session lock across storage I/O or claim that a later revocation rolls back an admitted operation.

## Provenance and CI gate

A directory replacement is outside `go mod verify`'s source checks, and `go mod tidy` can remove its upstream `go.sum` entries.
A changed `require` version can also keep building the same local copy.
Neither a successful build nor those module commands establish this copy's provenance.

Record the module path `github.com/ncw/swift/v2`, version `v2.0.5`, upstream revision, Go module `h1:` source sum, module-file sum and patch filename in `third_party/ncw-swift/UPSTREAM.json`.
The pinned source sum is `h1:9o5Gsd7bInAFEqsGPcaUdsboMbqf8lnNtxqWKFT9iz8=`.
Keep the complete local delta in `third_party/ncw-swift/credential-pairing.patch` and the imported module in the separate `source` subdirectory, so metadata is outside the compared tree.

Implement `scripts/check-swift-source.py` with these required checks:

1. Read the application's module configuration and refuse a required version or replacement path that differs from the recorded base and `./third_party/ncw-swift/source`.
2. Obtain the exact recorded module version through `go mod download -json` in a temporary module context, outside the application's replacement.
   Verify the archive contents against the recorded Go `h1:` sum; do not rely on a possibly modified expanded cache directory or the application's `go mod verify` result.
3. Extract the verified source into a temporary directory, apply only the recorded patch there, and require an empty recursive file/content difference against the checked-in `source` tree.
   Missing, changed or unexpected files, an unapplicable patch, and failed download or checksum verification all fail the command.

The local `make verify-swift-source` target must run that script, and the required `Go — fmt, vet, build, test (race)` job must run the same target without allowing failure.
Test the checker with source alteration, required-version drift, wrong replacement, checksum mismatch and patch removal; none may silently build or report success.
The existing `gofmt -l .` gate also applies to the imported source, which is clean under Go 1.27 today.

## Alternatives and maintenance cost

The lower-level `Call` has the same split selection and offers no hook that corrects it on an already authenticated connection.
A session-wide lock around operations would serialise storage I/O and streaming.
Per-operation connections would introduce new authentication, retry and stream-lifetime ownership, while an adapter using private endpoint aliases would introduce URL translation and token replacement for every native request.
Refusing requests based only on the latest snapshot could also reject coherent requests prepared before a refresh.
The concurrent probe shows that such refusal could interrupt many otherwise valid operations under authentication churn.
The reproduced credential disclosure and small correction at its source provide the concrete reason for the limited local patch allowed by DESIGN §6.5.

A token-to-URL acceptance map in the native transport is deferred as a separate defence-in-depth option.
A bounded map needs explicit lifetime accounting so eviction cannot reject requests prepared from older accepted responses, and a token can be reused for more than one accepted endpoint.
Adding that second authority cache would require another request-lifetime contract; it is not needed to prove or correct the current split selection.

This creates an explicit dependency-maintenance obligation.
Keep the patch small and mechanically compare it with its recorded upstream base during review and upgrades.
Prepare the minimal patch and standalone reproduction for an upstream contribution as part of delivery.
Record the upstream link when submitted; the reviewed local correction does not depend on an upstream response.
No unreviewed release or moving branch replaces the pin.
Remove the local replacement only when an accepted upstream version passes the same pairing, retry, encoding and full application gates.

## Required proof before delivery

Retain the failing-before host/account reproduction and unchanged-endpoint control as permanent tests.
Cover callback refusal, concurrent progress, already prepared requests, authenticated retries, request-body replay, lazy pages and stream reopen, including endpoint changes and the storage override.
Removing the pairing safeguard must fail the regression without a compile failure.
The local `make test-swift-core` target must run `go test -race -count=1 .` with `third_party/ncw-swift/source` as its working directory and `SWIFT_API_USER`, `SWIFT_API_KEY` and `SWIFT_AUTH_URL` cleared, forcing the upstream root package's local mock.
The required Go CI job must run that target explicitly because the application's `go test ./...` excludes the nested module.
Independent review passed this root-package command on both pristine v2.0.5 and the corrected copy; the author's corrected run passed 128 tests with two upstream mock symlink skips.
Running `./...` inside either copy also selects `rs`, whose tests fail without live Rackspace CDN credentials.
That unused, service-specific suite is outside this gate; its omission and the root mock's two skips must not be presented as complete upstream or real-service coverage.
Run the full application race suite with two-account Swift and the pinned real child, build and vet, frontend verification and binding generation, followed by independent local review and all five required PR and master checks.
An older in-flight request's 401 can still invalidate credentials obtained by a newer authentication and cause extra reauthentication.
That existing retry-pressure behaviour is outside this pairing correction and remains relevant to the throughput gate.
The replacement does not complete native key cleanup, historical recovery identity, throughput measurement, the six-platform release gates or the broader audit.
The valuable-data release hold remains.
