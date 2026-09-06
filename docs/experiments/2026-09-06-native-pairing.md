# Native Swift request pairing — 2026-09-06

This records the bounded P10 correction in [ADR 0016](../adr/0016-native-request-credentials.md).
The oracle is one accepted authentication response per request: the selected storage URL and token must belong together, while already prepared requests and concurrent storage I/O may proceed.
Bookmark-generation admission remains the separate ADR 0015 boundary.

## Reproduced failure and correction

Pinned `ncw/swift/v2` v2.0.5 captures the storage URL before `Call` reads its token under another critical section.
Its target callback previously ran only when that call itself authenticated, allowing another call's successful authentication to leave the first request with an old URL and new token.
The imported module changes only `swift.go`: evaluate the callback under the authentication lock on every attempt, refuse an error or empty result, then capture the token.
The two Aeronautes direct-call callbacks select their accepted snapshot and refuse a missing URL.
No storage I/O runs under a new session-wide lock.

`TestLibraryStorageURLAndTokenStayPaired` explicitly schedules the permitted gap with the public library API.
The changed-host and changed-account cases fail on pristine v2.0.5; the unchanged-endpoint control passes.
The test uses only the standard library and `ncw/swift/v2`; changing its package declaration to `swift_test` makes it a standalone upstream reproduction.
It does not claim to pause a high-level operation inside the dependency.
`TestDirectLibraryTargetKeepsPair` applies the same schedule to the application's accepted-snapshot callback: both changed-endpoint cases fail before, while the storage-override control passes.
`TestLibraryTransportRetryReselectsPair` authenticates another call after the first request is prepared and before its transport error is returned; the original retry sends a mismatched pair and fails.
All five failures pass with the correction, and both unchanged-endpoint controls remain green.

## Lifetime and failure controls

- Callback errors and empty URLs refuse before dispatch on both authenticated and lazy-authentication paths; a nil callback preserves the explicit target.
- Missing application snapshots return an error, while an accepted snapshot remains selectable.
- Eight workers issue container HEADs, metadata POSTs and SLO reads while another goroutine performs 150 authentications.
  Each of three endpoint modes (host change, account change and fixed override) completes 1,200 storage calls with no mismatched pair.
  This supplies concurrent progress and stress evidence, not a deterministic scheduling proof or production failure-rate estimate.
- A transport barrier holds an already prepared request while authentication and another storage call complete; releasing it sends its original coherent pair successfully.
- PUT receives one 401 after its complete body is read, then reauthenticates and replays the same payload once, including changed-host, changed-account and override cases.
- Lazy container and object listings fetch the next page only after the consumer advances following reauthentication.
- An already opened object remains readable after reauthentication, and its later range reopen selects the new pair.

Restoring the pristine library through a temporary Go overlay fails the five pairing cases without a compile failure.
Removing only the empty-target guard fails both empty-callback cases, which otherwise cycle through transport retries.
Removing only the application's missing-snapshot guard fails its explicit refusal test.
The existing request-admission and endpoint/redirect regressions also pass in the application suite.

## Dependency provenance

`third_party/ncw-swift/UPSTREAM.json` records v2.0.5, revision `3ff07f8f0968c964c685bc53f57cb5c57a3cae61`, both Go sums and the patch filename.
The complete 37-file source archive, copyright and `COPYING` licence are retained under `source`.
The only upstream delta is `credential-pairing.patch`; metadata stays outside the compared source tree.

`make verify-swift-source` parses the application's module configuration through Go, refuses version/replacement/exclusion drift, downloads the recorded module outside the application replacement, and independently computes the Go `h1:` sums from archive contents.
It applies the patch without fuzz and compares every source filename and byte against the checked-in copy.
It also runs 11 focused checker tests covering source alteration/removal/addition, version and replacement drift, extra versioned replacements, exclusions, wrong sums, conflicting revision metadata, source symlinks, missing/unapplicable patches and a corrupted archive despite unchanged download metadata.
Neither `go mod verify` nor a successful build establishes this directory replacement's provenance.
The complete import retains upstream whitespace in `COPYING`, `bin/update-authors.py` and `integration_test.sh`; the recorded patch also contains required diff-context spaces.
Those produce whole-import `git diff --check` warnings, while the authored application, documentation and checker files pass the scoped check, `gofmt -l .` is empty and patch application uses `--whitespace=error-all`.

The patch and standalone reproduction are prepared for an upstream contribution.
Submission remains an owner action; no upstream report has been posted.
An upstream upgrade must remove or refresh the replacement and pass the same provenance, pairing and application gates.

## Verification and limits

The full application run passed **441 top-level core tests across 19 packages**, with no skips or failures, under `go test -race -count=1`.
It used both disposable SAIO accounts on loopback port 18080 and official pinned rclone v1.74.3, with the rclone matrix enabled.
Combined application statement coverage was **82.9%**.
The reconciled system report remains valid at **10/104 cells**, with **47/48 actions still having gaps**; these unit/HTTP regressions do not close the wider fault/barrier harness.
The native package passed three repeated race-enabled runs; the standalone form was checked separately after removing its dependency on Aeronautes' session constructor.
Go build, vet, the CGO-disabled internal build, frontend typecheck/production build and all **58 frontend tests** passed; generated bindings have no drift.

`make test-swift-core` explicitly passed the imported root package under the race detector with the three remote-account variables cleared, forcing the upstream local mock.
The recorded pristine and prototype baseline has 128 passing tests and two symlink skips because that mock lacks symlink support.
The separate `rs` suite requires live Rackspace CDN credentials and is outside this gate; neither those omissions nor mock results are real-service or platform proof.
The required Go CI job runs both make targets because `go test ./...` excludes nested modules.

An older in-flight 401 may still trigger extra authentication after a newer response was accepted; that existing retry pressure remains relevant to throughput testing.
Native key cleanup, historical recovery identity, end-to-end throughput, six-platform validation and the broader audit remain open.
The valuable-data release hold remains.
