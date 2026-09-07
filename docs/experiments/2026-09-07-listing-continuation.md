# Native listing continuation validation

## Scope and behaviour oracle

This slice implements the continuation contract in ADR 0021, including its independently reviewed mixed-engine evidence refinement.
The parent is `e64d4bafd368d49aceba055a3080e338e5ae383a`; its production code is the green PR44 master `330ba3a2`.
Native container and object streams stop only at an accepted empty array or an intentional consumer stop.
Every raw page is validated before publication: names must advance strictly after the requested marker and within the page, stay inside the literal prefix, and contain no deep-listing pseudo-directory rows.

Memory remains proportional to one decoded page.
The last raw name advances the marker even when the delimiter view suppresses an own-prefix object.
Consumer stopping remains lazy; cancellation is checked before a fetch and while emitting accepted rows.
There is no sorting, deduplication, prefetch, restart, additional retry or per-row HEAD.
The response patch, credential patch and rclone runner are unchanged.

## Maintained regression cases

`TestListingContinuationContract` contains 52 leaf cases over the actual public session and a controlled HTTP server.
They cover both listing kinds, short/full/empty pages, defaults, starting markers, hostile literal scope, UTF-8 byte order, empty/duplicate/backward names, cross-page progress, later malformed responses, whole-page refusal, early consumer stopping and cancellation.
Object cases additionally cover delimiter prefixes, deep pseudo-directory refusal, raw self-only pages and the starting marker before self-row filtering.
Unexpected extra requests fail bounded marker/count assertions; fixture timeouts do not qualify as evidence.

The same final tests over the parent's two iterator files fail 40 unit cases and pass twelve.
Sixteen of the eighteen new system tests also fail against those old iterators; the two uncapped mixed Copy controls pass.
This gives 56 failing and fourteen passing leaf cases in the preserved parent replay, without skips, build errors, race reports or timeouts.
Each later-refusal system function stops at its first repeated-page failure on the parent; the remaining fault modes are after-change checks, not separately claimed parent failures.

The four original audit probes now all pass unchanged: both short-page cases and the two response-error cases.
The two existing pagination fixtures now expect four requests instead of three because a final partial data page is followed by an explicit empty-page read.
Their early-consumer-stop assertions still require just one request.

## Two-account system evidence

Eighteen top-level tests exercise the actual Browse, Find and Transfer services on both mock Swift and SAIO.
Owned proxies cap all selected source and destination JSON listing requests, including rclone requests, to one row while preserving the marker and actual Swift response.
Independent namespace snapshots bypass those proxies.
This is controlled provider compatibility, not a claim that SAIO naturally violates standard Swift pagination.

Browse returns complete bounded pages with the expected next marker; a subsequent empty page has no marker.
Delimiter prefixes and literal percent/space/Unicode names survive.
Find reaches matching terminal status with every expected name and byte count, then completes checked shutdown.
Read/refusal receipts follow the actual successful observation or refusal; failed asynchronous Find receives no successful-start credit.

The actual Copy preview sees the complete three-name, twelve-byte mixed plan.
Eligible native-routed Copy writes and verifies all three names with exact logical GET digests and sizes, preserves both source and collateral namespaces, and reaches the source terminal marker in both planning/execution and fresh verification.
Normal-page mixed Copy runs the real pinned rclone plus native engine and produces the same complete result.
The globally capped mixed Copy still exposes the separately recorded rclone omission: the job reports partial, the missing destination is independently observed, the other two names match their source bytes, and collateral stays unchanged.
That characterisation emits no Copy receipt; it cannot earn successful-Copy credit.
The original failed completeness assertion and paired cap-removal control remain documented in the [mixed-engine experiment](2026-09-07-short-page-copy.md).

Eight refusal functions cover 22 later-page scenarios across both servers: repeated markers and malformed JSON for container/object Browse, Find and preview, plus out-of-prefix object rows for the latter three consumers.
Browse discards the failed call's rows and marker, preview returns no plan, and Find retains only the earlier valid observation alongside its terminal error.
Every scenario checks exactly two advancing requests and unchanged independent snapshots.
The harness adds a wrapper for the existing bound `TransferService.PlanCopy`; the renderer surface and required scenario denominator are unchanged.

## Independent broken guards

Each variant starts from the same final source and runs all 52 maintained native cases independently.
A clean baseline passes all 52.
All fifteen variants fail their intended assertions, with reconciled process/package outcomes and no skipped cases, malformed event streams, build failures, panics, races or timeouts.

| Independently broken guard | Failing cases |
| --- | ---: |
| Container short-page termination | 6 |
| Container cross-page marker baseline | 4 |
| Container within-page ordering | 3 |
| Container whole-page validation | 7 |
| Container cancellation while emitting | 1 |
| Object short-page termination | 9 |
| Object cross-page marker baseline | 5 |
| Object within-page ordering | 3 |
| Object whole-page validation | 9 |
| Object cancellation while emitting | 1 |
| Equal-name rejection | 8 |
| Backward-name rejection | 11 |
| Literal prefix scope | 4 |
| Deep pseudo-directory rejection | 2 |
| Raw self-row marker advancement | 1 |

The pre-fetch context guard also protects admission, but no independent load-bearing claim is made for it beyond the cancellation tests and existing transport cancellation.
The new emission guards are independently proven above.

## Evidence and remaining work

Raw logs, actual exit files, source hashes and mutation diffs live under `/tmp/aeronautes-remediation-20260905`.
The maintained parent replay is `p22-continuation-final-before`; the original four probes are `p22-original-probes-continuation`; all guard results are in `p22-continuation-controls/results.json`.
The focused system run is `p22-continuation-system-v2`.
The first full run passes 533 top-level tests and 1,111 leaf cases across twenty packages; its per-package coverage is 79.4%, which must not be compared with the combined coverage used in CI.
The final run, `p22-continuation-full-v2`, repeats those 533 top-level and 1,111 leaf passes with CI's `-coverpkg=./internal/...` instrumentation and reaches 84.3% combined statement coverage.
Both full runs skip only the separately gated rclone compatibility matrix; their actual test exit is zero.
Final scenario reconciliation is valid at 24/110 cells, with 41/49 actions still having gaps; the new covered cell is the bound Copy preview read.
The capped mixed partial emits no receipt and contributes no successful-Copy credit to that result.

The source gate reconstructs all 38 checked Swift files and passes all fifteen refusal tests; internal vet and the CGO-disabled internal build pass.
An initial source-gate attempt used the system Go/cache and hit sandbox DNS denial; the configured cached Go environment passes, and the setup failure is not regression evidence.
Frontend code and bindings are unchanged; the required frontend and app-smoke PR checks still apply.

P22 and DS-13 remain open for the pinned engine's short-page compatibility work under the reviewed follow-up plan.
This slice does not establish wider response/RSS budgets, current native authority, durable plans/outcomes, external-writer protection, segment ownership, native-platform behaviour or readiness for valuable user data.
Local review, all five current-head/current-master PR checks, protected merge and all five resulting master checks remain required for delivery.
