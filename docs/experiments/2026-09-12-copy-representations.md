# Copy representation prerequisite

The ADR 0037 compatibility gate found an existing gzip staging failure on the unchanged prerequisite baseline and on the uncommitted first consumer.
This record preserves the before evidence and the bounded local implementation of [ADR 0038](../adr/0038-copy-representations.md); protected delivery remains a separate gate.

## Fixture and source identity

The baseline is `c842bdb9d2a017372b80cdee4922af042451af02`.
The consumer variant adds only the staged-DLO guard and private comparison helper to those product sources.
Both diagnostic variants use the identical test source, SHA-256 `601d0be87cc1489db888dc547f55d2958ad7b38bf7e17c09b5ff601ac6ff223f`.
The baseline selected only the three gzip cases, so an absent new staged GET was not counted as a baseline failure.

The owned Linux Swift fixture uses image `docker.io/openstackswift/saio@sha256:18ddf9feb9d1f419751a66eace8d956ecdaf5a085dedcc54ae51c210447ea1fa`, two test accounts and unique owned namespaces.
Tests use Go 1.27.1, `-race -count=1 -mod=readonly -json -parallel=1 -p=2 -timeout=480s`, with a separate 600-second process limit.
The rclone v1.74.3 executable has SHA-256 `9700aa1273ac73d6d0833c43ba63fe830516422cb131960b8c1a24ced789cba0`.
Its build information names Go 1.26.4, Swift library v2.0.5 and rclone revision `37e4117f48bdef0fa1ba27f6467d7a891b0b55ba`, but also reports `vcs.modified=true`; this is not a reproducible-build claim.

Each case contains a 19-byte control and one ordinary source object forced through the two-segment destination route with 1 MiB chunks.
The fixture holds the first already-applied control publication after staging, checks the subject stage and backing bytes independently, then requires complete stored-byte preservation through the new staged read and native publication.
It compares source/destination/collateral snapshots and original public job/history/items/totals, durable audit and recovery after the job body and pins finish.
The snapshot and source recorder disable their own decompression without rewriting the child's encoding request or the source response.

## Actual before results

| Run | Exit | Seconds | Outcome |
| --- | ---: | ---: | --- |
| Five-case consumer compatibility gate | 1 | 106.512 | Unencoded and unknown coding pass; both valid gzip representations and malformed gzip fail before the first live PUT. |
| Consumer diagnostic, three gzip cases | 1 | 98.407 | Same three failures, with exact engine, wire and durable records. |
| Unchanged-baseline diagnostic, three gzip cases | 1 | 99.121 | The same three failures and normalized job/item/audit/recovery observations. |

All three producers report unchanged source, no process timeout, no race reports, no skips and no stderr bytes.
The required-success assertions remain failures; they were not converted into expected refusals or credited as passing compatibility tests.
Full producer output, source deltas and earlier service-test expectation corrections remain in the separate review evidence packet.

The two valid gzip streams independently decode to the same 1,572,864 bytes, but distinct equal-length gzip header names produce different stored bytes and MD5 values.
Both stored streams are 1,573,020 bytes.
The child declares the second upload chunk as 524,444 bytes but supplies 524,288 bytes, ending with engine exit 5 and `ContentLength=524444 with Body length 524288`.
Malformed gzip contains 1,572,881 stored bytes and ends with engine exit 1 and `gzip: invalid header`.
The source recorder observes two rclone/v1.74.3 GET attempts with `Accept-Encoding: gzip`, no range or query and no source mutation in each failing case.

Both product variants finish partial with zero copied/verified names and two failed identities, before any new subject staged-content GET or live PUT.
Their execution records retain engine/unknown/unresolved and later verification/unverified/not_attempted observations.
For valid gzip, destination traces show two failed segment-upload attempts and two account bulk-delete requests, with staging and two captured-prefix recovery rows.
The first control stage remains, but no subject manifest or live name exists at completion.
Malformed gzip emits no segment upload or bulk cleanup and has only staging recovery.
These are earlier engine-abort outcomes; the later consumer's stage-retention/no-new-cleanup guarantee must not be attributed to them.

The two passing controls publish the original opaque bytes and encoding, with one native identity staged GET.
They establish those control routes only.

## Source attribution and proposed correction

The official v1.74.3 tag resolves to rclone commit `37e4117f48bdef0fa1ba27f6467d7a891b0b55ba`.
Its commit-addressed source archive is 16,409,338 bytes, SHA-256 `01312af454d38519262fc331bbe7913e4a11a266803d595b853c45dec8de26c7`; all 2,555 regular source members were indexed.
The source investigation also captured the exact Swift v2.0.5 and Go 1.26.4 dependency source identities.

The pin's [transport construction](https://github.com/rclone/rclone/blob/37e4117f48bdef0fa1ba27f6467d7a891b0b55ba/fs/fshttp/http.go#L270) maps `NoGzip` to Go's `DisableCompression` and supplies that transport to the [Swift connection](https://github.com/rclone/rclone/blob/37e4117f48bdef0fa1ba27f6467d7a891b0b55ba/backend/swift/swift.go#L474).
Go 1.26.4 [adds gzip when eligible](https://github.com/golang/go/blob/a9ce111d580581fb925ae88f125c69b7d93504ea/src/net/http/transport.go#L2835) and then [decodes a gzip response](https://github.com/golang/go/blob/a9ce111d580581fb925ae88f125c69b7d93504ea/src/net/http/transport.go#L2433), removing its content length and encoding headers.
The pin's [manual copy](https://github.com/rclone/rclone/blob/37e4117f48bdef0fa1ba27f6467d7a891b0b55ba/fs/operations/copy.go#L244) passes the opened stream with the original object information to [chunk upload](https://github.com/rclone/rclone/blob/37e4117f48bdef0fa1ba27f6467d7a891b0b55ba/backend/swift/swift.go#L1545), which sizes chunks from the source metadata.
That source path explains the observed stored/decoded length difference and malformed-gzip refusal; it is corroborated causal inference, not proof that the local binary reproduces every official source byte.

The parent gateway already streams opaque responses with decompression disabled, so its correct behavior does not prevent the child's separate decoder from changing the body afterwards.
ADR 0038 proposes the fixed Copy-only `--no-gzip-encoding` option at service construction, retaining false defaults for local transfers.
The flag affects requests through the child's cached Swift HTTP transport and sends no encoding header itself; it is not equivalent to the later native reader's explicit identity request.

## Local implementation and verification

The Copy service now creates one options value after resolving destination limits and enables `NoGzipEncoding` for both its injected factory and its real runner constructor.
The private builder shared by `Copy` and descriptor-based `CopyInto`, and the separate `CopyTo` builder, append the flag only when that option is true.
The option stays false in the shared settings mapping and ordinary local upload/download construction.
The existing destination-limit test separately checks engine policy and persisted pacing settings, without changing any limit or recording this fixed policy as a user setting.

The verified candidate starts from the four unpublished staged-content commits ending at `a21bb0050bab50cb9ca47cd0cf2b2d7ba4f29152`.
Its runner source SHA-256 is `14d38223a514a4dd594064489a9b43ea0a10736515959299ad2304d863c4c468` and Copy service source SHA-256 is `e67e4673c830eb306d8ee5a26b338cb12d3e84aaff60bc8b668773d2839d5b12`.
The maintained [representation test](https://github.com/csmart/aeronautes/blob/9b1c152dbd8c664582ed9ede0c45b1a743719145/internal/systemtest/copy_staged_representation_test.go) has SHA-256 `c09ec9c94589444ae2c1f89d9788bb20baa8c417d17f27eede4560e8c2c61a78`.
Each producer retained all 769 input file hashes, the source archive and patch, exact command and exit, complete Go JSON output and unchanged-source check.
The deliberately changed variants differ in only their named policy file from that candidate.

These local runs used Go 1.27.1 and the same SHA-256-checked rclone v1.74.3 executable recorded above.
The exclusive owned namespace was on the existing Linux fixture `aeronautes-saio-20260908`, using the same pinned SAIO image through `127.0.0.1:18080`, with both accounts required.
No container restart or configuration change was needed.
Every run used `-race -count=1 -json -p=2 -timeout=480s` and an independent 600-second process limit.
The service cases were serial with `-parallel=1`; the affected-package run retained `-parallel=3`.
All producers finished without process timeout, race report or skipped leaf, and their sources stayed unchanged while running.

| Run | Go exit | Passed leaves | Failed leaves | Seconds |
| --- | ---: | ---: | ---: | ---: |
| Constructor and argv tests before wiring | 1 | 26 | 4 intended | 9.895 |
| Constructor, argv and destination-limit tests after wiring | 0 | 30 | 0 | 9.846 |
| Actual-Swift representation cases | 0 | 9 | 0 | 21.751 |
| Existing staged-content cases, mock and actual Swift | 0 | 16 | 0 | 29.612 |
| Copy argument removed independently | 1 | 3 | 1 intended | 3.642 |
| CopyTo argument removed independently | 1 | 3 | 1 intended | 3.637 |
| Shared settings default enabled independently | 1 | 0 | 4 intended | 2.836 |
| Copy service assignment removed independently | 1 | 0 | 2 intended | 37.960 |
| Complete copy, swiftcontrol, rclone, local and app packages | 0 | 2,137 | 0 | 134.289 |

The nine actual-Swift cases retain the original five DLO successes and add four plain-stage controls:

| Case | Stage | Stored bytes | Result |
| --- | --- | ---: | --- |
| Unencoded | DLO | 1,572,864 | Complete, identical stored bytes |
| Valid gzip | DLO | 1,573,020 | Complete, identical stored bytes |
| Same decoded content, different gzip stream | DLO | 1,573,020 | Complete, identical stored bytes |
| Malformed gzip | DLO | 1,572,881 | Complete, identical stored bytes |
| Unknown coding | DLO | 1,572,864 | Complete, identical stored bytes |
| Empty | Plain | 0 | Complete, identical stored bytes |
| Empty labelled gzip | Plain | 0 | Complete, identical stored bytes |
| Small valid gzip | Plain | 51 | Complete, identical stored bytes |
| Small malformed gzip | Plain | 17 | Complete, identical stored bytes |

Both gzip streams independently decode to equal content and have distinct stored MD5 values, `53fea0e99de577fc2815200cc3fc9493` and `9a965c32eee53e13cb5ef6e204b41914`.
Each DLO case observed exactly one native identity staged GET before conditional publication, and independently checked both backing segments, assembled stored bytes, metadata, source, collateral, recovery and public/durable outcomes after body and pin release.
The plain cases exercised the existing native plain-publication GET, which still advertised gzip, and preserved all stored bytes through that separate path.
They do not claim the new DLO reader ran for a plain stage.
The child sent no `Accept-Encoding` on its recorded requests; this does not imply the parent changed its other native read policies.
No source Range request was observed in these successful routes, and no encoded-listing acceptance is inferred.

Removing only the service assignment restored two gzip source GETs and the original second-chunk mismatch, `ContentLength=524444 with Body length 524288`.
The job ended partial with zero copied or verified names, two failed/unresolved names, two engine bulk-cleanup attempts and three recovery rows.
Source and collateral remained unchanged; the only destination residue was the first control's stage, with neither subject stage nor live publication.
Two proxy context-cancellation errors accompanied the child's abort and remain in the failed producer output.
The selected unencoded case also failed its explicit absent-encoding-header assertion after its other outcome checks; it is not counted as a passing control.
Neither required-success assertion was weakened, and neither failed producer was relabelled as successful compatibility evidence.

The independent builder controls fail only their own opted-in argv assertion, with each other's opted-in command and both defaults passing.
Enabling the shared settings default fails both local factory checks and both actual local constructor argv checks, including descriptor-based download on this Linux host.
The existing corruption, read/close, cancellation, required-record, completion, gateway, TLS, admission and child-lifecycle regressions remain in the passing inherited and affected-package suites.
The timings describe these bounded fixture runs, not a throughput gain or an operating-ceiling measurement.

The consumer, native prerequisite and representation fix still ship together after complete current-base verification, independent range review and five green PR/resulting-master jobs.
Native fallback verification, general metadata fidelity, source-version/final-publication binding, DS-04 and the valuable-data hold remain open.
