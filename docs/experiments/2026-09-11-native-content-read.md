# Native content read: before evidence and maintained-proof boundary

Status: proposed evidence document for [ADR 0037](../adr/0037-staged-content-read.md).
These retained experiments establish why staged-content verification needs an explicit native-read prerequisite; they do not implement or approve that prerequisite.
No new execution was performed to prepare this document.
DS-04 and the valuable-data hold remain open.

## Original sources and executions

| Experiment | Original immutable source | Isolation |
| --- | --- | --- |
| Native Call lifetime and representation | `859ec53c53dbb00851bfbcc796b1a89706d07310` | 700 original tracked files unchanged; one added test file; owned loopback HTTP/1.1 fixtures. |
| Raw auth and error bodies | `0992dca42b6bb8158288c3baaa56f4f0c74a4ec8` | 720 original tracked files unchanged; 46 selected native/Session/module dependencies identical to the preceding source; one added test file. |
| Same-size staged Copy corruption | `5823af8879416deb7b4188a9d84f391bf292fd73` | 700 original tracked files unchanged; one added test file; real Copy service, pinned rclone and fresh namespaces on an owned Swift fixture. |

All seven Go invocations used Go 1.27.1 on Linux/amd64, with race detection and read-only modules.
The following commands replace only the private executable path with `go`; run-directory, cache and evidence-output paths are normalised in the [archive manifest](probes/2026-09-11-native-content-read/manifest.json).
Every other test argument and its order is preserved; these are historical commands, not commands executed for this document.
Each source archive requires its matching probe restored at the manifest's `restore_as` path, with only one raw-body source version installed at a time.

```sh
go test -json -v -race -mod=readonly -count=1 -parallel=1 -p=2 -timeout=45s '-run=^TestNativeCall(Lifetime|Representation)Controls$' ./internal/swiftcontrol
go test -json -v -race -mod=readonly -count=1 -parallel=1 -p=2 -timeout=45s '-run=^TestNativeCallLifetimeExpectedRed$' ./internal/swiftcontrol
go test -json -v -race -mod=readonly -count=1 -parallel=1 -p=2 -timeout=45s '-run=^TestNativeCallRepresentationExpectedRed$' ./internal/swiftcontrol
go test -json -v -race -mod=readonly -count=1 -parallel=1 -p=2 -timeout=45s '-run=^TestRawBodyControls$' ./internal/swiftcontrol
go test -json -v -race -mod=readonly -count=1 -parallel=1 -p=2 -timeout=45s '-run=^TestRawBodyExpectedRed$' ./internal/swiftcontrol
go test -json -v -mod=readonly -race -count=1 -parallel=1 -timeout=150s '-run=^TestCopyStagedContentBeforeRealSwift$' ./internal/systemtest
```

The fourth command ran twice: first with the separately archived original raw control source, then with the corrected final source.
The first five distinct commands had 45-second Go and 120-second process bounds, `GOTOOLCHAIN=local`, `GOENV=off`, `GOWORK=off`, `GOPROXY=off`, `GOSUMDB=off`, `GOMAXPROCS=2` and `CGO_ENABLED=1`, an existing populated module cache and private build/temp/evidence directories.
The Copy command had 150-second Go and 180-second process bounds, recorded `GOTOOLCHAIN=local` and `GOPROXY=off`, existing module/build caches, the official rclone 1.74.3 directory, `AERONAUTES_REQUIRE_TWO_ACCOUNTS=1`, its owned evidence-output directory and `AERONAUTES_SAIO_AUTH_URL=http://127.0.0.1:18081/auth/v1.0`.
The manifest distinguishes these actual environment overrides rather than attributing the native probe's additional settings to Copy.

| Invocation | Actual exit | Process seconds, rounded | JSON events | Unique terminal leaves |
| --- | --- | --- | --- | --- |
| Native `controls-1` | 0 | 3.615919 | 188 | 20 passed: 8 lifetime and 12 representation. |
| Native `lifetime-red-1` | 1 | 1.364364 | 28 | 2 intended failures. |
| Native `representation-red-1` | 1 | 0.313765 | 13 | 1 intended failure. |
| Raw `controls-1` | 1 | 1.414593 | 33 | 2 passed, 1 failed control; no regression credit. |
| Raw `controls-2` | 0 | 2.071073 | 32 | 3 passed. |
| Raw `expected-red-1` | 1 | 1.720570 | 38 | 3 intended failures. |
| Copy, single invocation | 1 | 8.071271 | 21 | 1 passing control and 1 intended corruption failure. |

All streams have complete package/test start and unique terminal inventories, with no malformed records, skipped leaves, race report, panic, Go compile/setup failure or test/process timeout; Go stderr is empty.
The retained native and raw assessors exited 0 and reconciled 64 and 30 diagnostic snapshots respectively.
These reconciliations are evidence inspection, not independent approval or additional behavioural test runs.

The failed raw `controls-1` required an optional text-error excerpt that the pinned library discards when Read returns bytes together with EOF.
The body was consumed and closed, but the control assertion was wrong; its actual exit remains 1.
The final source checks 403 classification, exact consumed bytes and read/EOF/close lifecycle, and is identical in `controls-2` and `expected-red-1`.
Preparation first exited 1 because an overbroad equality gate included unrelated `bulkdelete.go` and `credentials_test.go` changes; narrowing to the 46 relevant dependencies then passed, with the second preparation bounded at 60 seconds.
The first preparation's error is retained as an actual tool transcript rather than separately redirected streams; no archive or test had begun.
A native snapshot-display attempt exited 1 on `TypeError: object of type 'NoneType' has no len()` for valid JSON null; this was inspection failure, not a Go test result.
Copy setup retains an exit-1 missing-`ss` check and two exit-1 process-inspection permission failures before successful inspection as the Swift service user.
The missing-`ss` record is retrospective and has no captured subprocess timeout; subsequent container/process inspection commands used 30-second bounds.
None of these preparation, inspection or failed-control attempts receives regression credit.

## What was observed

The native probes use actual Session construction, production admission/redirect wrappers and native Call with the required storage-target callback; only owned inner HTTP transports are cloned/instrumented.
The lifetime probe obtains Credentials first; the raw-body probe instead explicitly admits the request and uses an empty initial target, exercising native cold authentication.
The observer forwards the inner transport's optional cancellation API while confirming that the unchanged outer wrappers do not expose it.

Private header/read-idle values were 100/300 ms for the lifetime probe, with three retries giving four header attempts.
Header timeout returned with four underlying requests/native workers still active after another 150 ms; body timeout returned with one underlying read/native worker still active after the same observation window.
Owned cancellation, or cancellation plus checked body close, eventually drained the observed work, but workers could still be active immediately when cancel/Close returned.
The drain observation sampled every 5 ms within a 2-second containment cap; observed cancel-to-drained intervals were about 5.857 ms for headers, 0.185 ms for body cancel/close and 51.225 ms for close alone, including measurement overhead.
The passing controls also cover healthy EOF/close, external header/body cancellation and 75 ms caller deadlines; those private deadlines propose no whole-object policy.
This evidence does not permit immediate reuse of a buffer still borrowed by a timed-out Read worker.

The stored-response representation fixture ignores negotiation.
Automatic transport behaviour sends gzip, decodes valid gzip, removes Content-Encoding and changes ContentLength to -1; malformed gzip fails to read.
Explicit `Accept-Encoding: identity` preserves valid gzip, malformed gzip and unknown coding as exact bytes; a private DisableCompression/no-header control also preserves them.
Two different 52-byte gzip representations decode to the same 20 bytes, demonstrating why decoded equality is insufficient for stored-representation identity.
Explicit identity is not wire-equivalent to [ADR 0018](../adr/0018-snapshot-representations.md)'s no-header oracle, and no actual Swift/rclone encoded-content compatibility follows from this fixture.

Raw-body timers were privately set to 100 ms headers and 150 ms read idle, with the existing retry count and no overall caller deadline.
Each fixture acknowledges received headers and entry into a held, nonempty response body before observing for 450 ms.

| Held response | Observed blocked stack and interval | Outcome after explicit external cancellation |
| --- | --- | --- |
| Nominal successful v1 auth | `drainAndClose`, 451.652039 ms; accepted auth snapshot already present, no storage request sent. | Context cancellation; one auth request, zero storage requests reached the server. Four cancelled storage RoundTrips still entered the transport after the auth drain. |
| Storage 401 | `drainAndClose`, 451.648933 ms, before refresh. | Earlier **nonnil 401 response plus the later authentication cancellation error**; the rejected response still requires caller ownership despite the error. |
| Text 403 | `appendResponseBodyToError`, 450.849828 ms. | The result remains a 403 error and is **not** `errors.Is(context.Canceled)`; the raw read/drain cancellation errors are discarded by the legacy path. |

External cancellation was deliberately issued after observing the blocked Call; deferring cancellation until Call returned could not end that wait.
The three held handlers observed request cancellation and completed before forced barrier release; observed cancel-to-joined intervals were 0.735415, 0.269040 and 1.640011 ms respectively, including measurement overhead.
The probe joined callers, counted request/read work and checked native helper stacks, then released/closed every owned fixture and rechecked before reporting its intended failures.
Those observations do not impose a retroactive legacy-auth lifetime guarantee or establish universal synchronous transport shutdown.

The real Copy probe used an immutable source with a 19-byte control and a 2,097,152-byte subject, staged by real rclone with a 1 MiB chunk size.
It held the first live PUT's completed response, independently witnessed that live control plus both acknowledged stages and the absent second live name, then replaced one subject segment with different bytes of equal length.
Both jobs nevertheless completed with copied/verified counts 2/2, zero failed/unresolved/unverified item totals, four retained recovery rows and one conditional subject-publication PUT.
The control's subject digest stayed `16d4332fd09b6be31ddfdfec0407650920805f02dd2c0ec0dbd4a2f8b6d23f09`; corruption produced `0f16914d947829500fcb6956a5a77c44e21c9ebb9f3d0541d11164d1cb46b878` at the same length.
Public job/item/totals/terminal-audit observations matched the stored facts; complete source snapshots and request traces proved no source mutation, and destination collateral was limited to the two copied names and the subject's owned segments.
The owned fixture image was `sha256:2c39297ee7e8ff768d10ad970a982a89a87a44eaae5b9140ea80738e67c92d02`; container/listener ownership, health and unchanged proxy configuration were checked before and after, and fresh test namespaces were absent after cleanup.
The first-PUT barrier captures only a small completed response; unrelated segment uploads and full-object GETs must remain streaming.

## Archived bytes and retained evidence hashes

The four files below are byte-identical historical probe sources, deliberately stored as `.go.txt` outside active test discovery.
The original raw control version is retained separately; it must not be treated as an intended-failure test or silently replaced by the final version.
The manifest records original source commits, restore paths, exact elapsed values, flags, environment normalisation, leaf identities and actual outcomes.
Raw command/result/event/snapshot records and producer/assessor sources remain retained separately; this proposed public archive contains the four probe sources and manifest, not those private runtime records.

| Archived source | SHA-256 |
| --- | --- |
| [native_call_probe_test.go.txt](probes/2026-09-11-native-content-read/native_call_probe_test.go.txt) | `e424d31df42fe6172dd3602c33d1e06d8d03227679001790e9b3d301f83bd070` |
| [raw_body_probe_test.go.txt](probes/2026-09-11-native-content-read/raw_body_probe_test.go.txt) | `30feeb0c962b98f82ab5bbcdaa95465e3377d0da59786daf17a91b2b1e3b3e37` |
| [raw_body_probe_controls_1_test.go.txt](probes/2026-09-11-native-content-read/raw_body_probe_controls_1_test.go.txt) | `be4075bd0a4e79e1777f7200518814750689400f49bae2d896b49e3deb3fd039` |
| [copy_content_before_probe_test.go.txt](probes/2026-09-11-native-content-read/copy_content_before_probe_test.go.txt) | `a58bae633082940f96c75262def4b7c9dc05cfdf7f02f7c24a1eadad82e10cb3` |

| Retained raw event stream | SHA-256 |
| --- | --- |
| `native/controls-1/events.jsonl` | `5e88d608638837e41a276d55dddd68b2cab1fd0f974e8ff4d0f2fca46810fa6b` |
| `native/lifetime-red-1/events.jsonl` | `8fa6eacd8520a3f8d738dbe8469facaef64b81e12dfff454978e019325693494` |
| `native/representation-red-1/events.jsonl` | `20a0b8f620231b7777dc1c467cb1b6d26bd232d768ee791da521f27ce65e222f` |
| `raw/controls-1/events.jsonl` | `98780cca21501834259af1e70b84aafbe84cd2d8432b719495350fd631acba55` |
| `raw/controls-2/events.jsonl` | `f4dcd2bddef9df4b9e19b1186c6639aa890e12cd4c405f238ccba425148d7cae` |
| `raw/expected-red-1/events.jsonl` | `55e4ef5e81fd0e90add39ad68ea0c3ee118758be747bf3ee815835f3742655b1` |
| `copy/single-invocation/events.jsonl` | `d4aad5d182546c8de4bd1d0e14b1a621482f7be873b82d1a422cde9de1696c34` |

The pinned Go binary's recorded SHA-256 is `30969f97169d7f43fe6a085873d75613adc21e30818a8c61d95bd27275df4624`; rclone 1.74.3's is `9700aa1273ac73d6d0833c43ba63fe830516422cb131960b8c1a24ced789cba0`.

## Proposed maintained-proof boundary

The archived before tests assert old worker/raw-stack or unsafe-publication observations and deliberately fail afterwards; they are not drop-in passing acceptance tests for ADR 0037.
Its proposed opt-in read requires maintained tests for joined local request/read ownership, checked unused/auth/401/error-body closure, response-plus-error ownership, generic body-consuming auth with coherent credential pairing, admission and read cancellation, and complete opaque HTTP 200 responses through checked EOF/close.
The consumer then needs a real-service refusal regression at the first-PUT barrier, a matching healthy/resegmentation control, complete collateral/recovery/public-record assertions, and checked recording/completion failures that cannot permit later dependent effects or invent saved outcomes.
A same-size mismatch should become failed/unresolved staging evidence before its committing record or public PUT, retaining the bad stage and its segments; its item Unverified facet stays zero because that facet counts copied items lacking verified comparison, not diagnostic codes.
Existing skip limitations, plain/native and segmented-source paths retain their accepted scope.
Actual owned-Swift and pinned-rclone valid/malformed-gzip/unknown-coding controls are still required for explicit identity encoding; the loopback stored-response fixture does not establish that gate.

One additional boundary is source inspection only: at `e09c477801911e2d307e8cf9a6c93efd467ee26a`, confirmed unchanged at `044cb70a31c14e1345e9033ed8a513104bf647fa`, a failed observation during the final staged name need not latch the executor stop.
`flushBatch` checks context before each name but not after its final observation (`internal/transfer/copy/executor.go:508–516`), so `Finish` can return nil and `runCopy` can reach `Verify(background)` (`internal/app/transferservice.go:593`).
A focused maintained test must establish the actual final-name cancellation outcome; this unexecuted observation neither proves a reproduced failure nor expands cancellation policy.

Shared auth-lock waits, uncooperative transports/bodies, generic-auth close failures, TLS/HTTP2 and other native platforms are not proved by these Linux HTTP/1.1 runs.
MD5 remains an integrity check within the stated Swift model; no source-version snapshot, binding of dynamic segment membership through final publication, later-skip equality, crash/power-loss durability or destructive follow-up authority has been established.
The prerequisite and narrow staged guard require separate local reviews within one eventual delivered change; no unused standalone read API, DS-04 closure or release of the valuable-data hold follows from this document.
