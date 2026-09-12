# Public native HTTP error messages

The public `BrowseService.Connect` returned a saved synthetic key when an owned auth server repeated that key in an unmapped HTTP/1.1 error reason.
This record preserves the before evidence and defines a narrow correction under K-SEC-002, K-SEC-006 and DESIGN sections 49, 50 and 55.
The first commit changes documentation only; implementation and its maintained proof require a separate local review.
P10, the broader secret/error audit and the valuable-data hold remain open.

## Behaviour contract

For a native `*swift.Error`, `swiftcontrol.Normalize` must derive `SwiftError.Message` from the local Go `http.StatusText` for its numeric status.
If Go has no text for that status, use the fixed message `Swift request failed`.
Neither the native Text, a provider's HTTP reason, nor a wrapped response excerpt may supply this Message.
This is a local display projection, without secret-specific matching or a new redaction framework.

Preserve the exact numeric HTTP status, existing `classifyStatus` kind and read retryability, original error cause identity through `errors.Is` and `errors.As`, and transaction-ID attachment policy.
Keep nil, already-normalized errors and any error matching `context.Canceled` on their existing paths.
The original cause can still contain provider text and must not be formatted as public output.
This change does not sanitize arbitrary network/deadline/unknown errors, joined cancellation text, existing `SwiftError` values or transaction headers.
It does not change auth retries, request ownership, keychain behaviour, audit storage, cleanup or authority publication.

The display wording change is intentional.
The existing Normalize tests constrain classification, retryability and cause identity rather than the old wording, and inspection of production callers in `internal`, `frontend/src` and `scripts` found no branches on the old native messages.
The new tests must explicitly preserve these machine-readable contracts and fix the new wording as the display oracle.
The standard strings below are from the pinned Go 1.27.1 `net/http/status.go`.

| Native message/status | Local display message |
| --- | --- |
| `Authorization Failed` / 401 | `Unauthorized` |
| `Operation forbidden` / 403 | `Forbidden` |
| `Object Not Found` or `Container Not Found` / 404 | `Not Found` |
| `Timeout when reading or writing data` / 408 | `Request Timeout` |
| `Container Not Empty` / 409 | `Conflict` |
| `Too Large Object` / 413 | `Request Entity Too Large` |
| `Object Corrupted` / 422 | `Unprocessable Entity` |
| `TooManyRequests` / 429 | `Too Many Requests` |
| `Rate Limit` / 498 | `Swift request failed`; rate-limited/retryable classification remains |
| Native 500 with any reason | `Internal Server Error` |
| Zero or other status without Go status text | `Swift request failed`; status/classification remain |

Standard mapped wording such as 304 `Not Modified` and 400 `Bad Request` stays the same.
The change also removes the redundant native `HTTP Error` prefix from unknown-to-Swift but known-to-Go statuses.
Object/container distinction remains available to trusted Go callers through the preserved native cause, rather than the public 404 display text.

## Before evidence

The executed source was immutable master `2f576f77da2e5d05ec2c172ec494aaaf0ad03247`.
Its 782 tracked Git blobs were copied unchanged, with exactly one private test added at `internal/app/public_connect_error_before_probe_test.go`.
The assessor compared every archive member against Git and verified all 783 executed inputs before and after every run.
The [archived executed probe](probes/2026-09-12-public-http-errors/public_connect_error_before_probe_test.go.txt) is byte-identical to that test.
The [evidence twin](probes/2026-09-12-public-http-errors/before-evidence.json) preserves the complete parsed inventory, observations and actual exits, including the failed setup attempt.

The current authoring parent is `836ad0d2138f51741c65de948bab5c5edeb2f3f8`.
Its errors, Session, v1 auth, Connections, BrowseService and BookmarkService files match the before source byte-for-byte.
The only intervening `swift.go` changes add the separate `OwnedRead` option and dispatch; the legacy authentication and `parseHeaders` functions exercised here are unchanged.
This source comparison does not pretend that the earlier producer ran on the newer whole tree.

The test saves a synthetic credential through the actual public BookmarkService using an owned SQLite store and session-only SecretStore, then calls public List and Connect.
The owned loopback server records the key it actually received in `X-Auth-Key` and reflects it only in a custom reason phrase.
A different synthetic marker is placed in the response body.
Checked hijacked HTTP/1.1 writes preserve the provider-selected reason, whereas `WriteHeader` would replace it with standard wording.
All observed requests used HTTP/1.1.

The healthy control made exactly one auth GET, one `/info` GET and one account HEAD.
Each refused Connect made only its auth GET, returned a zero account DTO, installed no active session and preserved the public bookmark list without a key in its DTOs.
Every completed case explicitly closed the server, joined its owned handlers, checked reply/connection I/O and closed SQLite before its final security assertion.
The mapped 403 custom-reason control remained safe because the native library substituted its fixed `Operation forbidden` text.
The normal-reason 500 control also excluded both canaries.
The custom-reason 500 exposed the synthetic key through native Text, normalized Message and the public `Error()` string, and failed only `SEC-PUBLIC-REASON`.

The source path at the executed commit is `third_party/ncw-swift/source/swift.go:395`, interpolating `resp.Status`, then `internal/swiftcontrol/errors.go:84`, assigning native Text to Message.
Session authentication returns Normalize at `internal/swiftcontrol/session.go:123`, and the failure propagates through `internal/app/connections.go:66,78` to `internal/app/browseservice.go:29`.
`SwiftError.Error` at `internal/swiftcontrol/errors.go:42` puts Message into the returned string.
The response-body marker was absent even from the private unwrapped causes in these runs; there is no independent runtime credit for the appended-body branch.

## Commands, outcomes and provenance

`GO` below denotes the pinned Go 1.27.1 executable, and `SOURCE` denotes the private immutable source copy; these are documented path placeholders.
Both commands ran with cwd `SOURCE`; the executable and private-path command records are hashed separately below.

```text
GO test -race -count=1 -mod=readonly -json -parallel=1 -p=2 -timeout=45s ./internal/app -run ^TestPublicConnectErrorBoundaryProbe$/^(healthy|mapped_403|unmapped_standard_500)$
GO test -race -count=1 -mod=readonly -json -parallel=1 -p=2 -timeout=45s ./internal/app -run ^TestPublicConnectErrorBoundaryProbe$/^unmapped_custom_500$
```

The producer used argument arrays and existing module/build caches with `GOTOOLCHAIN=local`, `GOENV=off`, `GOWORK=off`, `GOPROXY=off`, `GOSUMDB=off`, `CGO_ENABLED=1` and `GOFLAGS=-mod=readonly`.
Each case had a 10-second context and each owned reply a 3-second write deadline; the Go timeout was 45 seconds and the external process deadline was 120 seconds.
These are private containment limits, not new production deadlines.
No deadline fired.

| Run | Actual Go / producer exit | JSON events | Named / leaf tests | Package / process seconds | Credit |
| --- | --- | --- | --- | --- | --- |
| `controls-1` | 1 / 1 | 32 | 2 / 1 | 0.269 / 3.935386 | Listener permission failure before healthy setup; none |
| `controls-2` | 0 / 0 | 23 | 4 / 3 | 1.738 / 2.282929 | Three passing controls |
| `before-1` | 1 / 1 | 14 | 2 / 1 | 0.221 / 0.731072 | Intended public reason/key failure |

All three runs have one package and one top-level test, with complete unique terminal events and no skips, malformed records, race reports or test/producer stderr bytes.
All 69 JSON events were reconciled against the actual producer results.
The first attempt started only the healthy leaf before the sandbox refused the listener; the other two selected leaves never started and have no credit.
After approval of that owned-listener operation, the same frozen source produced the passing controls and intended failure.
Preparation, assessment and sealing exited 0; the actual expected-red Go and producer exits remain 1.
The private packet seals 826 indexed files, excluding runtime cache/config/temp files and the seal's self-references.

| Artifact | SHA256 |
| --- | --- |
| Executed probe | `01d7711908b100a6c237e430335165fc1e3eead8c87b39e5c0abd9d3fa667ca9` |
| Evidence twin / original assessment | `ebd6fc69139cefb50ed684feace01d9d31e42e51a21e5d2bff317076b0e67021` |
| Original source tar | `0cca5d1c4f25b46ecddbdd77661cce4a8b30bbea20e0817aba8e25fbb0c8fbf5` |
| Original input manifest | `ad85927523860aab42d6d21dfe830d5b1bb8567792a51d805f0d5fde104a82f6` |
| Go executable | `30969f97169d7f43fe6a085873d75613adc21e30818a8c61d95bd27275df4624` |
| Preparation producer | `f66715ae974615840066a891c139ce23abf77154805ffbe5ad06adff82362d7c` |
| Test producer | `18f17c07c8e6ce010e43f191128f94856ab902b8486189890886cfcd0071ae0b` |
| Assessor | `67e37032e4b2473ee0e740815ed0039556464add05843aaa3f32b2e4b15abf78` |
| Sealer | `cb30ef98983b4379a241286c80680089b0432c9d7e831115bd1ccf9f27429678` |
| `controls-1` original command record | `157e4b94b77faf745b8d5ba20bfdd81dcbf00d94dae1d6db3a09c8e94341b8fd` |
| `controls-2` original command record | `33243eaa4790c5cc7f6ee8f54a1b77b79c3106f51eecef643d23ddad58e8bbf7` |
| `before-1` original command record | `e99757d7e0ce50038b9189ff1b8bd17f8495089df30447dd3761d2f506359f44` |
| `controls-1` raw Go stream | `f1b3f8a83bffd0fc9740fb4636d2eb48cd438273973bd29e3a0def12c0fbfbc8` |
| `controls-2` raw Go stream | `fc807563623feb3f4ce252fb3d39382d69bba70579f09ad69eca1d3b64be98dc` |
| `before-1` raw Go stream | `4feef6bb6cc5d38b680c763beb230f60aa22cde79a029fc0d67edc5a70c8e670` |

## Maintained correction and stop conditions

After this contract is approved, add dedicated table-driven tests in `internal/swiftcontrol/errors_public_test.go` and a public Connect regression in `internal/app/public_http_errors_test.go` before changing the native Message projection in `internal/swiftcontrol/errors.go`.
Cover standard/unknown/zero statuses, deliberately changed native labels, wrapped provider text, cause identity, cancellation/normalization precedence and unchanged transaction-ID attachment.
Keep the existing Normalize tests unchanged.

Add `internal/systemtest/public_http_errors.go` to return a Receipt tied to the actual public Connect call, and `internal/systemtest/public_http_errors_test.go` for `TestPublicConnectHTTPErrorRefusalMock`.
Its only matrix change is `BrowseService.Connect` with explicit `happy` and `refusal` scenarios; all other rows and shared harness helpers remain unchanged.
Credit the refusal only after the returned error excludes key/body canaries, the failed endpoint has not become active authority, and complete owned namespace/bookmark observations are unchanged.
Retain a healthy Connect control, and do not claim the existing happy matrix gap closed without a qualifying actual observation.
Record the current-parent semantic failure and passing controls, then the fixed results and an omission control restoring native Text that fails the same public security assertion without a credited passing refusal.
Run bounded pinned Go race tests for these maintained cases and the existing Normalize/transaction-ID contract; root owns the full suite and delivery gates.

Stop if wider callers depend on old display wording, a test needs a new auth/transport policy, or the correction expands to general error redaction, secret-store/backend behaviour or cleanup authority.
This is Linux amd64 owned-loopback HTTP/1.1 evidence at the Go public service boundary, without rendered Wails, TLS/HTTP2, real Swift, native keychain or full application audit proof.
The fixture models an approved auth endpoint or intermediary reflecting a key it received; it does not establish interception of another endpoint or a deployed-provider incident.
No rclone process, shared Swift fixture, frontend bundle, full suite or remote operation was used for the before evidence.
