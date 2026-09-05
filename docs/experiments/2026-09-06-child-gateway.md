# Child storage gateway validation

The implementation follows [ADR 0014](../adr/0014-child-storage-gateway.md), including its independently reviewed storage-policy amendment.
The behaviour oracle is the captured endpoint/TLS policy, the invocation's source/staging scope, byte-preserving streaming and the existing bounded cancellation contract.
Tests ran on Linux amd64 with Go 1.27.1, the official pinned rclone 1.74.3 and two test-owned Swift accounts.
The full internal suite passed 365 top-level tests with the race detector, zero failures and zero skips across nineteen packages.
Full application build and vet passed, and the internal packages also built with CGO disabled.
The pinned Wails generator produced no binding or module-file drift.
This record does not close the valuable-data release hold or imply six-platform execution evidence.

## Credential and scope regressions

`TestRunnerRefusesStorageRedirects` exercises the actual pinned child across 301, 302, 303, 307 and 308, for reads and staged writes to same-origin, cross-port, cross-host and HTTPS-to-HTTP targets.
Each of the forty cases requires the intended upstream redirect to have been reached, a failed transfer and zero requests, credentials or writes at the redirect target.
Against the original direct-child runner, thirty-two cases exposed token forwarding; all forty now pass through the gateway.
The eight other direct-child cases were already refused by the child's redirect/body behaviour and remain covered.

Focused HTTP regressions cover missing/wrong local tokens, absolute proxy requests, foreign hosts/accounts, encoded names, source mutations, live destination names, other jobs' stages, unrelated segments, container deletion, unsupported verbs and write queries.
Container creation requires a fresh gateway-issued HEAD of the exact bound destination and the same single policy value, compared byte for byte, or no header when the HEAD omitted it.
Missing or failed destination HEAD, duplicates, mismatches, unrelated or stale child HEADs, unexpected policy and combined policy/ACL changes refuse creation without upstream mutation.
`Connection` header nominations cannot suppress a validated policy or manifest before forwarding.
DLO headers must refer to generated segment prefixes under the same invocation's staged object.

Bulk cleanup reads and validates the entire bounded body before sending it upstream.
Tests include mixed valid/invalid names, malformed escapes, double encoding, container-only entries, blank lines, size/name limits and byte-preserved HTTP 200 responses containing per-name failures.
Rejected bodies result in zero upstream requests.
No test treats HTTP 200 alone as evidence that cleanup succeeded.

## TLS, streaming and lifetime

Actual pinned-child downloads verify loopback HTTP, captured custom CA and explicit insecure trust, and refusal of an untrusted certificate under system trust.
Native gateway tests separately verify custom/system/insecure policy, malformed CA refusal and unchanged gzip bytes and length headers.
Independent sixteen-MiB upload/download tests block the producer after its first chunk until the consumer receives it, so buffering an entire object would fail rather than pass unnoticed.

The real-child cancellation test waits for an active download before cancelling, then checks the cancelled result, stopped upstream traffic and absence of a final local file.
A signal-aware test child proves that scoped cleanup remains available during the existing SIGINT grace period.
Gateways close on child exit or at the ten-second cancellation deadline, whichever comes first; the deadline is independent of child reaping.
An additional race-enabled deadline regression holds a real upstream request open while withholding invocation cleanup, and verifies connection and listener revocation without a child exit.
Source/destination listeners also close after normal exit, failed exit, failed partial setup and mixed-trust refusal.
Graceful cleanup remains best effort, and incomplete transfers still require native reconciliation.
These tests do not establish process-tree termination on Windows/macOS or recoverability after every platform-specific crash.

## Real Swift request trace

Replaying the [preserved recorder](2026-09-05-child-requests.go.txt) through the gateway produced 74 upstream requests, compared with 73 on the direct-child runner.
The additional request was the gateway's destination-container HEAD before sidecar creation with `X-Storage-Policy: 1replica`.
The three-MiB upload produced a DLO and one captured generated prefix, and the downloaded bytes matched the source.
An injected refusal on the second segment PUT produced one account-root bulk DELETE naming the failed upload's first successfully written segment, entirely inside that invocation's generated prefix.
The trace records token presence and test-owned object names, never token values.

The matrix still measures eleven of sixteen names exactly and the same five unsafe-name omissions, with user metadata absent from the raw child copy.
The application must continue its native name routing and metadata replay.
The recorder's exit status establishes that the measurement ran, not that those known compatibility differences are safe.

## Streaming cost

`BenchmarkGatewayStreaming` transfers 64 MiB over a local HTTP fixture in each iteration, with the client draining bytes as they arrive.
One paired run of three iterations per route on an AMD Ryzen AI 9 HX PRO 470 measured:

| Route | Time per transfer | Throughput | Allocated bytes per transfer | Allocations |
| --- | ---: | ---: | ---: | ---: |
| Direct fixture | 10.020 ms | 6697 MB/s | 80,376 | 125 |
| Through gateway | 11.647 ms | 5762 MB/s | 148,986 | 248 |

The complete standalone benchmark process, containing both routes, peaked at 12,028 KiB RSS as measured by `/usr/bin/time -v`.
These are short synthetic loopback measurements, not WAN throughput, a per-job RSS guarantee or evidence for the unexecuted platform lanes.
The extra hop has a measurable cost; its transfer allocations remain small relative to the 64 MiB payload.
The separate producer/consumer barriers establish streaming correctness.

Reproduce from the repository root with a working Go toolchain:

```sh
go test -c -o /tmp/aeronautes-gateway-bench.test ./internal/transfer/rclone
/usr/bin/time -v /tmp/aeronautes-gateway-bench.test \
  -test.run '^$' -test.bench BenchmarkGatewayStreaming \
  -test.benchtime=3x -test.benchmem
```

The SAIO CI job supplies `AERONAUTES_OFFICIAL_RCLONE_DIR` from its checksum-verified fetch, making the real-child regressions part of that required job.
Developers can set the same variable to their verified pinned binary's directory; absence skips only the explicitly labelled real-child lane.
