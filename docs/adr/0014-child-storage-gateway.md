# ADR 0014 — Enforce Swift transport policy before the child

**Status:** Design and storage-policy amendment accepted after independent review; implemented with the validation below (2026-09-06).
**Detail:** DESIGN §12.3, §26.2, §44.1 and §65.11; K-SEC-001/002/006/007/008; audit SEC-02 and P09.

## Evidence and problem

Native Swift calls refuse redirects, but the pinned rclone receives the real storage token and follows a storage redirect with `X-Auth-Token` intact.
The audit's real `Runner.CopyTo` reproduction sends a synthetic token from a loopback storage endpoint to a different hostname and port after HTTP 302.
Repeating that reproduction on 5 September failed on both the official 1.74.3 pin and the official 1.75.1 candidate.
The candidate archive was verified against its published SHA256SUMS: archive `982b5aa772841168f8e380f139e9e787b2a105403e32b94da8676a0e1c0a13ab`, extracted Linux amd64 binary `f66d8c1d552ad90296a11bc8b46d56a7fa5da1a7fa05e7ca522d95df92c4a4c0`.
This is a Linux reproduction of that specific leak, not compatibility or security approval of either release.

The pinned binary's `rclone help flags redirect` exposes only the WebDAV option.
The [1.75.1 release notes](https://rclone.org/changelog/#v1-75-1-2026-09-04) describe other redirect fixes, but the executed Swift reproduction still fails.
The [pinned HTTP transport](https://github.com/rclone/rclone/blob/v1.74.3/fs/fshttp/http.go) and [Swift backend](https://github.com/rclone/rclone/blob/v1.74.3/backend/swift/swift.go) do not provide the needed redirect-refusal setting.
Upgrading alone is therefore insufficient evidence for closing SEC-02.

## Decision

Use one private, ephemeral loopback gateway per remote for each `Runner.Copy` or `Runner.CopyTo` invocation.
Keep rclone as the pinned external byte-moving subprocess and keep the existing selected-name, staging, pacing, retry and cancellation rules.
The gateway enforces transport policy; it does not classify objects, plan mutations or claim ownership of segments.
Without additional method/path restrictions, its local bearer token delegates the real token's upstream account authority for the invocation's lifetime to any local process holding it.
The local token has the same credential-exposure class as today's child environment; withholding the real token alone does not reduce that authority.
Apply the role restrictions below as part of the first gateway implementation, rather than deferring them to a later capability framework.

- Aeronautes captures the real storage URL, token and TLS trust for that invocation in memory.
  The child receives a loopback storage URL with the same account path and a fresh random local token through named-remote environment configuration.
  Neither token enters argv, the job config file, SQLite, the renderer or diagnostics.
  The child is no longer directly configured with the upstream Swift token.
- Each gateway has one fixed upstream origin and account path.
  Caller-controlled hosts, absolute request targets and paths outside that account cannot redirect its authenticated traffic.
  Preserve encoded object paths and query values without path cleaning or decoding/re-encoding object names.
- The source remote allows only `GET` and `HEAD` inside that account.
  The destination allows reads and `PUT`/`DELETE` of the invocation's staging objects and corresponding generated segment paths, never live destination names or another invocation's stage.
  `CopyTo` binds an exact staged object; `Copy` binds its fresh batch prefix; both bind the destination container and matching `<container>_segments` sidecar.
  Allow only a zero-body sidecar-container creation `PUT`, after a fresh authenticated `HEAD` issued by the gateway for the exact destination container at validation time.
  If that response contains one nonempty `X-Storage-Policy`, require the same single value on creation, comparing bytes without case folding or trimming; if it omits the policy, require creation without the header.
  A failed HEAD, ambiguous policy header, omitted expected policy, unexpected policy or different value refuses creation with no upstream mutation.
  Structural refusals require no upstream contact; policy validation performs only the bound read before deciding whether to forward creation.
  Refuse ACL, metadata, versioning and history headers; the bound storage-policy value is the sole policy-header exception.
  Require each staged `X-Object-Manifest` to name a generated segment prefix inside the same invocation's sidecar scope.
  Allow the observed abort-cleanup form, `DELETE` on the account path with `bulk-delete=1`, only after reading a bounded complete `text/plain` body and validating every URL-encoded `/container/object` line against the invocation's staged names or generated segment paths.
  Any out-of-scope name, malformed encoding/body or exceeded body/name limit refuses the whole request before upstream contact; decode each name exactly once using Swift's wire semantics, without path cleaning.
  Container names without an object are forbidden in the bulk body.
  Forward a validated body and its response unchanged, including the middleware's per-name results inside an HTTP 200 response; response status alone does not establish cleanup success.
  Do not allow `POST`, `COPY` or any other method without a separately reviewed, recorded compatibility requirement.
  The observed request classes and allowlist are [recorded in the engine surface](../rclone.md#gateway-request-policy).
  Refuse `Connection` options other than `close` and `keep-alive`, so a child cannot nominate a validated policy or manifest header for removal before forwarding.
- Require the local token on account requests and replace it with the captured upstream token only after scope validation.
  The library's anonymous capability request may reach only the precisely derived `/info` path, without an upstream token.
  Other unauthenticated requests fail locally before upstream contact.
- Build the upstream transport from the same endpoint/TLS policy as native Swift.
  System trust, the captured custom CA and explicit insecure mode remain per endpoint.
  Keep the existing mixed-trust refusal until a separate compatibility change proves that relaxing it is safe.
  The local child uses literal `127.0.0.1` HTTP and needs no TLS overrides or CA file; the upstream request uses the actual upstream authority in both its URL and `Host` header.
- Use a single upstream round trip, never an HTTP client that follows redirects.
  Strip `Location` from every response and map redirects to a non-retryable refusal such as HTTP 403, with a transfer error.
  Strip authentication response headers; do not expose token values in errors or logs.
  The gateway has no arbitrary forwarding, CONNECT, browser CORS or rclone RC interface.
- Stream bodies with bounded buffers and preserve content encoding and length semantics.
  Apply connect/header/idle bounds and close the listeners, active requests and idle upstream connections when the invocation ends or setup fails.
  Invocation cancellation retains the existing SIGINT cleanup grace required by K-XFER-003: the scoped gateways remain usable until the child exits or the ten-second kill deadline, whichever comes first.
  The deadline revokes the gateway independently of whether child reaping has completed.
  Tests must prove cancellation and cleanup; a stopped child must not leave a usable credential gateway.

This ADR does not change the engine pin or claim to resolve the full P10 session-generation contract, child lifetime on all platforms, sidecar advisories, or destructive-write races.

## Alternatives

A maintained rclone patch could add Swift-specific redirect refusal, but would require six custom binaries, a new build/integrity/distribution contract and ongoing security maintenance.
It remains an alternative if the gateway cannot pass the compatibility and resource tests.
A plain HTTP forward proxy cannot inspect redirect responses inside HTTPS CONNECT tunnels without introducing another TLS trust mechanism.
Relying on HEAD preflight, stripping a token only after a redirect, or assuming a newer version fixes the issue does not meet the credential oracle.
Replacing the complete byte engine with native transfers is a larger product change than this transport adapter.

## Storage-policy compatibility amendment (2026-09-06)

The first gateway implementation's full Swift integration run refused three existing transfer tests because the pinned child creates its sidecar with `X-Storage-Policy` from the destination container.
The original request recording had omitted that header, so the bare-creation proposal missed this compatibility requirement.
The preserved recorder now includes the policy value, and repeating its two-account experiment with the original direct-child runner produced 73 requests, including one sidecar-container PUT with `X-Storage-Policy: 1replica`.
No token values were recorded.
The [pinned backend](https://github.com/rclone/rclone/blob/v1.74.3/backend/swift/swift.go) obtains that value in `fetchStoragePolicy` and sends it in `makeContainer`.

Bind the permitted header to a fresh authenticated destination-container HEAD issued by the gateway while validating creation.
Do not retain earlier child HEAD results; failures or ambiguous headers refuse creation.
The child cannot choose another policy, substitute a different container's HEAD, or attach other policy-changing headers.
Do not strip the requested policy and silently use the cluster default, because that could change where segments are stored.

[Swift assigns storage policy at container creation](https://docs.openstack.org/swift/latest/overview_policies.html#containers-and-policies); changing an existing container's policy requires deletion and recreation.
The HEAD binds the allowed value, but is not an atomic creation or exclusivity proof and does not solve concurrent container replacement.
Preserve any upstream conflict or failure and keep the broader generation/concurrency work open.

The amendment passed independent design review before implementation.
Regressions cover matching and omitted policy, missing or failed destination HEAD, duplicate headers, unrelated-container HEAD, mismatched values and combined policy/ACL mutation.
The unchanged real-Swift transfer tests pass through the gateway, including segmented upload and cleanup.

## Implementation sequence and gates

1. Review this decision and its bounded scope before changing the locked direct-child credential/TLS path.
2. Establish a single coherent URL/token/expiry/TLS snapshot for the invocation through a separately reviewed P10 prerequisite, then add the gateway at the common runner boundary with real-child redirect regressions and focused transport/security tests.
3. Run native and real-child TLS checks, two-account Swift, selected-name/encoding, staging/DLO prefix capture, cancellation, partial/error and large streaming-body regressions.
4. Record measured memory/throughput costs and required platform evidence; keep any unexecuted lane explicit.
5. Update the active configuration table and requirements/ADRs to describe the actual shipped path, retaining the original seam findings as historical evidence.

The redirect matrix covers 301/302/303/307/308 for both remote reads and staged remote writes, including cross-host, cross-port, same-origin and HTTPS-downgrade targets.
The independent assertion is zero requests and zero tokens at each redirect target, a failed transfer result, and no unplanned destination delta.
Negative tests cover wrong/missing local tokens, out-of-account and encoded path escapes, unauthenticated `/info`, invalid CA data, setup failure, cancellation and listener teardown.
They also prove that source mutations, destination live-name writes, another invocation's stage, unrelated segment paths, container deletion and unsupported methods fail locally with zero upstream delta.
Bulk-cleanup tests cover valid generated names, mixed valid/invalid names, malformed and double-encoded escapes, container-only entries, body/name limits and per-name failures; rejected bodies must cause zero upstream requests.
Header tests cover forbidden container policy changes and out-of-scope DLO manifest prefixes.
Reject dot or empty path segments at this child boundary because the existing name router already sends such object names through native Swift.
Positive tests preserve accepted overrides, scoped TLS, exact names and large streaming transfers without whole-object buffering.
No recorded experiment becomes a passing release gate merely because rclone exits zero.

The [implementation validation record](../experiments/2026-09-06-child-gateway.md) describes executed Linux tests, the repeated Swift request trace and measured streaming costs.
The SAIO CI job exports its verified sidecar directory so the real-child redirect, TLS and cancellation regressions run as required checks.
