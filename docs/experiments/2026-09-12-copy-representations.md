# Copy representation prerequisite

The ADR 0037 compatibility gate found an existing gzip staging failure on the unchanged prerequisite baseline and on the uncommitted first consumer.
This record supplies before evidence for [ADR 0038](../adr/0038-copy-representations.md); it is not an implementation or delivery result.

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

No flag-enabled runtime result is recorded yet.
Required after evidence includes all five original positive cases, small/empty Copy cases, both command builders, factory/real construction, local default scope, independent removed-option controls and preserved authority/lifecycle tests.
The consumer, native prerequisite and approved representation fix must ship together after complete current-base verification and five green PR/master jobs.
DS-04, general metadata fidelity, source-version/final-publication binding and the valuable-data hold remain open.
