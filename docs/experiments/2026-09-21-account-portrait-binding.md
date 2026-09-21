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

## Evidence and proposed correction

The durable private packet is `/home/csmart/code/.aeronautes-work/20260921-l11-portrait/`.
Its `SHA256SUMS` digest is `46917c39ddad581ff6d48e8bc230d127f1e43e78ce4ab5a31cfead45075044d1`.
It contains the original probe sources, raw logs, structured command/source/concurrency metadata, the master comparison and caller assessment.

[ADR 0063](../adr/0063-account-portrait-binding.md) proposes caller-named Account and Capabilities reads, a single validated tuple per reply, and browser checks for one bookmark and generation before publication.
Implementation follows independent contract approval and includes regenerated bindings, fake parity, maintained catching Go and browser regressions and relevant real Swift account controls.
A correctly labelled read may complete after a later connection switch; no later mutation authority follows from its reply.
The broader live-read race, audit search, mutation writers, L11/P28 completion and the valuable-data hold remain outside this correction.
