# ADR 0038 — preserve encoded bytes in Copy

**Status:** Accepted; the Copy-only implementation has passed local review and compatibility tests, and requires one combined current-base delivery with ADR 0037.
**Scope:** The rclone child created by `TransferService` for Copy, as a compatibility prerequisite for ADR 0037.

## Problem and oracle

The required ADR 0037 real-Swift representation gate fails before the new staged-content reader runs.
Two valid gzip streams with the same decoded content fail during segmented upload because the child supplies decoded bytes with a chunk length derived from the larger stored representation.
Malformed gzip fails in the child's decoder.
The same three cases fail on the unchanged native-prerequisite baseline, so this is an existing Copy staging problem rather than a refusal introduced by the new comparison.
Unencoded and unknown-coding controls preserve their bytes through staging and publication.
The [representation experiment](../experiments/2026-09-12-copy-representations.md) records the exact pin, before results and source attribution.

The oracle is the stored-byte requirement in ADR 0037 and DESIGN §65.11, the independent snapshot rule in ADR 0018, the Copy staging/metadata contract in §28.4 and the rclone authority boundary in §26.2, ADR 0014 and K-XFER-014.
The snapshot oracle does not itself authorise changing all product reads.
This decision gives the Copy child an explicit transport policy while preserving local transfers and the native reader's separate policy.
It neither changes what counts as equal content nor permits accepting a length or digest mismatch.

## Decision

### Explicit Copy construction

Add a false-default `NoGzipEncoding` boolean to the internal `rclone.Options` value.
After deriving destination-limited settings, `TransferService.buildEngine` creates one options value, sets this fixed policy to true, and passes that value to either its injected engine factory or `rclone.NewRunner`.
The resulting runner retains its own options; do not mutate shared transport, session or runner state around an invocation.

When the option is true, both `Runner.Copy` and `Runner.CopyTo` append exactly one `--no-gzip-encoding` argument before the positional `--` separator.
Both methods are part of the existing Copy adapter even though ordinary batches use `Copy`.
The option is not a user setting, persisted pacing field, environment inheritance rule or arbitrary flag facility.
Existing constructors keep their false default unless explicitly opted in by the Copy service.
In particular, local upload/download constructors and any download descriptor helper remain unchanged.
Direct lower-level runner callers do not gain the policy automatically; their tests must select it explicitly where they claim this representation contract.

### Child transport scope

In rclone v1.74.3, the flag disables automatic gzip negotiation and decoding on the cached HTTP transport used by the child's Swift connections.
It applies to both remotes and all requests made through that transport, including listings and upload responses; it is not an object-GET-only switch or a guarantee about arbitrary future transports.
The flag itself sends no `Accept-Encoding` header.
It also does not remove an explicitly supplied encoding header; the fixed Copy argument/configuration surface supplies none.
The existing native staged reader separately sends `Accept-Encoding: identity`; these two wire policies are not equivalent negotiation requests.

Use Swift's stored representation without child-side content decoding.
Valid gzip, malformed gzip and unknown coding remain opaque object bytes.
An ordinary successful Copy must preserve those stored bytes and the content encoding that the existing native publication path copies from the source HEAD.
The flag adds no metadata-copy feature and changes no native publication write set.
Structured responses must still be valid supported JSON; unsolicited encoded listings may be refused rather than silently decoded.
An origin or intermediary that transforms content remains outside the recorded fixture contract, and a changed response must not bypass the source length/MD5 comparison.

Preserve the pin, gateway request allowlist, current-generation admission, per-child credentials, captured TLS trust, redirect refusal, staged-only writes and conditional native publication.
The gateway already disables its own decompression; changing its response encoding headers would conceal representation evidence and is not part of this correction.
Preserve chunk sizes, bounded retries, source reopen/range behavior, abort cleanup, cancellation/grace and child reaping.
The flag neither enables server-side copy nor gives engine cleanup any new authority.

### Alternatives considered

`--header-download` with `Accept-Encoding: identity` would be narrower for the current manual Swift source open, but chooses a different negotiation input and is not propagated through every rclone transfer path.
It needs a distinct policy and compatibility proof; do not substitute it silently if the selected flag fails.
A per-call adapter entry point would require more interface and argument plumbing than the existing service construction boundary.
Changing only the gateway's outgoing request cannot undo the child's prior automatic-decoding decision.
Changing a shared options default would also change local transfers and is outside this scope.

## Required proof

| Boundary | Maintained evidence and deliberately broken control |
| --- | --- |
| Service selection | Observe the true option through the injected Copy factory and the real service/child path. Ordinary local upload and download construction must retain the false default and omit the flag. Removing the service assignment must fail the real representation case; enabling the shared default must fail the local-scope control. |
| Both commands | Capture argv for opted-in and default `Copy` and `CopyTo`; require exactly one flag before `--`, with selection, retry/pacing and secret/config hygiene preserved. Remove each builder's flag independently and require its own assertion to fail. |
| Actual staged DLO | Retain the five original success cases: unencoded, two distinct equal-length gzip streams of equal decoded content, malformed gzip and unknown coding. Compare complete source, staged manifest/backing and live stored-byte evidence plus final encoding, exact public/durable outcomes, recovery and collateral after job body/pins finish. Require the new identity staged GET before conditional publication. |
| Other Copy sizes | Add empty and small plain-object representation controls through the actual service and pin; distinguish those materialised-plain stages from the new DLO consumer. Do not infer these paths from the large-object case. |
| Wire and failure | Observe absent child `Accept-Encoding` and exact source bytes without rewriting the oracle or proxy. Removing the flag must restore the known gzip engine failure. Preserve all failed producers and distinguish engine abort cleanup from a later content-check refusal. |
| Existing safeguards | Retain gateway TLS/encoded-response, mixed-trust refusal, credential refresh, per-dispatch admission, exact staged/segment scope, redirects, cleanup and cancellation/reaping regressions. The complete ADR 0037 corruption, incomplete/read/close, cancellation and record-refusal cases still apply. |

The two valid gzip fixtures must independently decode to equal content while their stored bytes and MD5 differ.
Passing them proves byte preservation for the tested route, not collision-resistant authenticity, source-version pinning or complete replication fidelity.
The current effective-settings JSON records pacing, not this fixed source policy; use source, argv and wire evidence rather than claiming it attests the new option.
Keep API/listing, retries and cleanup observations explicit when reconciling the real pin; a successful object read alone cannot establish all child behavior.

## Implementation and delivery sequence

1. Review this contract, its DESIGN sequence, the enumerated flag and the preserved baseline failure before changing production policy.
2. Obtain independent review of the already prepared ADR 0037 consumer and its passing corruption/public-outcome regressions, recording the unresolved representation gate as a delivery dependency.
   This allows a coherent local consumer commit; it does not permit publication while the compatibility cases fail.
3. Implement the two-file service/runner policy and its dedicated construction, argv and actual-service representation tests as a separate locally reviewed fix.
   Resolve active file ownership before editing the shared runner file.
   Rerun the original five-case gate without weakening it, the small/empty controls and each independent omission/scope control.
4. Reconcile the combined native prerequisite, consumer and representation fix on the actual current master.
   Complete the source gate/rejection tests, explicit nested native gate, appropriate root/application race/build/static checks and actual owned-Swift compatibility evidence.
   Obtain renewed independent review of the complete range and require all five latest-head/current-base PR checks, protected merge and all five resulting-master checks.

No unused reader API, failing representation gate or policy-only prerequisite ships separately.
There is no dependency pin, workflow budget, metadata policy, shared Session, local-transfer or frontend change in this decision.
If those boundaries cannot satisfy the gate, return to scope review rather than widening the implementation implicitly.

## Remaining limits

Native fallback paths retain their existing representation behavior and require separate verification/remediation.
Segmented-source verification, source changes during transfer, changes after the staged read, final-publication binding, later aggregate observations and legacy strength labels remain unresolved DS-04/P16 work.
Existing retries may reopen a source with Range; this option does not pin a source version or add full-stream verification to such reads.
The native authentication-lock and uncooperative-I/O limits in ADR 0037 remain.
The stored-byte fixtures do not certify arbitrary transforming proxies, native platforms, crash/power loss or operating ceilings.
No Mirror start, overwrite, source deletion or automatic segment deletion is enabled, and the valuable-data hold stays on.
