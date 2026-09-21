# Account portrait binding — 21 September 2026

## Oracle and before source

Frozen Linux finish-line L11 requires account-specific results, paging, retries and errors.
[ADR 0049](../adr/0049-account-scoped-reads.md) and [ADR 0059](../adr/0059-account-scoped-remaining-reads.md) provide the named-bookmark and backend-generation binding; DESIGN sections 10 and 53 govern capability unknowns and account portraits.
The before probes ran on `5f71a163f800c238d981fe419bb974d59869b003`, with tracked production files unchanged.
The relevant service methods, DTOs, connection snapshots, generated bindings and browser connection flow are byte-identical to master `74cfc7288428beea4425b519b56c320bc220fb8e`.

## Results

Two independent loopback HTTP accounts exercised production authentication, account HEAD and `/info` handling.
Holding alpha's account HEAD, completing a beta `Connect`, and releasing alpha made `Connect(alpha)` return alpha's count of 11 containers with name `AUTH_beta`.
The no-switch control passed with `AUTH_alpha` and 11 containers.
After alpha connected with max-file-size 111 and policy alpha, beta connected with 222 and policy beta; the original caller's unbound `Capabilities()` received beta's snapshot.
Both catching Go assertions compiled and failed under the race detector; these were not build failures.

The browser probe used the real `BrowserState` and production API adapter with generated-service stand-ins modelling the separately reproduced Go behaviour.
After a second bridge connection moved to beta, the bound `Containers(alpha)` read refused, but the caller converted that error to empty rows and still published alpha's portrait, beta's capability snapshot and connected bookmark alpha.
Its no-switch control passed and its changed-account assertion failed with 222 instead of 111.
This proves wrong display and capability-gating input, not a performed mutation.

The Go run took 4.67 seconds and the frontend command took 1.66 seconds.
At the start, load averages were 0.42/0.59/1.18 and no other Go test process was present.
These are local protocol and browser-contract results, not SAIO or native desktop acceptance.

## Evidence and correction

The durable private packet is `/home/csmart/code/.aeronautes-work/20260921-l11-portrait/`.
Its `SHA256SUMS` digest is `46917c39ddad581ff6d48e8bc230d127f1e43e78ce4ab5a31cfead45075044d1`.
It contains the original probe sources, raw logs, structured command/source/concurrency metadata, the master comparison and caller assessment.

[ADR 0063](../adr/0063-account-portrait-binding.md) now binds Account and Capabilities to the caller's bookmark and one validated tuple per reply, with browser checks for one bookmark and generation before publication.
The initial listing must match the same scope before publishing rows, including when a re-save and reconnect changes the generation of the same bookmark between replies.
That mismatch keeps the bound portrait and capabilities while showing the existing listing error with no rows.
The service, regenerated bindings, browser and fake implement the independently reviewed contract together.
Maintained regressions also failed on the contract-only head `fd288321` before the implementation: the Go held-read assertion returned beta's name with alpha's counts, and 14 browser assertions failed while three controls passed.
Those sources and raw failures are sealed in `/home/csmart/code/.aeronautes-work/20260921-portrait-binding/red/`, whose `SHA256SUMS` digest is `c249428bfee25b5ef471c2ea22e969c649ee00c0b373c5b0fdaf58a87b2a122a`.
The corrected focused service checks passed under the race detector, including both public mock and real Swift account controls; 163 affected browser tests passed and the type check reported no errors or warnings.
Coverage includes stored generation changes without service invalidation, a held HEAD across re-save and reconnect, unknown capabilities from unreadable or disabled `/info`, whole-attempt refusal and hiding a newer initial listing with no continuation marker.
A correctly labelled read may complete after a later connection switch; no later mutation authority follows from its reply.
The broader live-read race, generation checks on post-connect refreshes, audit search, mutation writers, L11/P28 completion and the valuable-data hold remain outside this correction.
