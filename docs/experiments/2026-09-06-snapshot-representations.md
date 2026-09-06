# Snapshot representation validation

## Scope

[ADR 0018](../adr/0018-snapshot-representations.md) defines the P02 byte, encoding-header and complete-response contract.
The first implementation preserves encoding evidence; the second will refuse incomplete responses.
The wider fault/barrier, recovery, whole-account and native-platform work remains open.

## Encoding preservation

The unchanged [archived audit probe](../audits/2026-09-05/probes/oracle_encoding_probe_test.go.txt) was run again against master `db13261e2953eb2298c8f477813cad39ca2c8b54` through a temporary Go overlay.
It compiled and failed both assertions: the stored gzip header disappeared, and gzip-to-plain replacement with equal decoded content produced an empty delta.
The same probe passes after the correction.

Each oracle now clones the standard HTTP transport, disables automatic decompression and closes its own idle connections after fixture teardown.
Failed authentication also releases that transport's idle connections.
The oracle sends no Accept-Encoding and relies on Swift returning the stored representation; it does not certify bytes behind a transforming intermediary.
Size and Digest retain content coding, while ordered encoding field values are recorded independently for objects and backing segments.
The shared default transport retains its existing decoding behaviour.

Nine permanent transition controls cover unchanged plain/gzip data, gzip-to-plain replacement, different same-length gzip bytes with equal decoded content, removed/added encoding metadata, unknown codings, malformed gzip and a changed second encoding field.
They compare expected byte digests, lengths, headers and exact changed fields, including an unchanged-state control.
DLO and SLO controls cover assembled encoded bytes and a segment header-only change outside the snapshotted container.
The real-Swift class fixture also writes a gzip object and witnesses the exact stored bytes and header without decoding.

Four negative controls deliberately weaken the final implementation:

| Weakened behaviour | Failing leaf cases |
| --- | --- |
| Implicit gzip negotiation/decompression re-enabled | 12 |
| Process-wide transport modified instead of cloned | 1 |
| Only the first encoding field retained | 3 |
| Backing-segment encoding evidence omitted | 2 |

All four controls compiled and failed behavioural assertions.
The first count includes the explicit request-policy assertions; it is not a count of twelve distinct data-corruption findings.
Each control runs in its own test process against disposable loopback or owned SAIO fixtures.

The full internal race suite passed 450 top-level tests in 20 packages with zero failures or skips, both owned SAIO accounts, the required private keychain fixture and official rclone 1.74.3 with its compatibility matrix.
Statement coverage was 83.1%; scenario reconciliation remained valid at 10/104 cells, with 47/48 actions carrying gaps.
Application build, vet and formatting passed.
The frontend tree and bridge signatures were unchanged; the local build reused embedding assets from that identical frontend tree.
No native-platform execution or additional scenario coverage is claimed.

Independent local review and all five protected PR/master checks remain required before delivery.
Partial-response refusal is the next implementation; existing truncated-body checks remain, but the complete ADR contract is not yet implemented.
