# ADR 0082: an operator can enable an unproven capability out of band, off by default

**Status:** Proposed on 25 September 2026, revised the same day after review. The operator's request is recorded: a capability must be able to be built and checked while staying disabled by default, in their words "can we improve all of those behind a build option or something that toggles uploads support on or off or something? would be good to build all the data safety in, check it, but still have it disabled by default until we are ready". **The design below is the author's proposal, not the operator's words**: they asked for off by default and named a build option as one possibility; an environment-read switch in the shipped binary is this record's answer and is not yet accepted. Reviewed before its implementation.
**Scope:** `internal/app/v1capability.go`, the startup path in `main.go`, the `Info` document, and the frontend's affordance flags. It adds a way to lift the [ADR 0052](0052-v1-capability-gate.md) gate deliberately; it does not change which capabilities are deferred.

## Problem

ADR 0052 refuses every deferred capability at the bridge boundary and hides its affordances, and it says why the refusal rather than the hiding is load-bearing: the webview can reach a bridge method whatever the interface chooses to display. The code, tests and refusals behind each deferred route are deliberately untouched so that they still work when the gate lifts.

That leaves no way to exercise a capability end to end in a built application while it is still being proven. The routes' own tests run, because a test binary lifts the gate, but a test binary is not the product. An operator who wants to try an upload against their own cluster, before the release that offers it, has to either ship it to everyone or build a private binary that differs from the one under test.

## Decision

1. **One classification, one switch.** `v1DeferredActions` stays the single table naming every deferred action, and it is not edited to enable anything. A separate enabled set, read at startup, records which of those capabilities this process offers anyway. `refuseIfDeferred` refuses when the action is deferred and its capability is not in that set.
2. **The switch is out of band and read once.** The enabled set comes from the process environment at startup, from `AERONAUTES_ENABLE`, as a comma-separated list of capability tokens. It is never a bridge action, never a setting in the database, and never re-read, so nothing the webview can reach and nothing a remote page can persuade the application to store can change **which** capabilities this process offers. That preserves ADR 0052 decision 1 only for the capabilities that remain disabled. An enabled capability is deliberately exposed at the bridge, and a page that reaches the bridge reaches it too: enabling is a decision to run an unproven route, not a way to run it privately.
3. **The tokens are capability names, not action names.** `upload`, `copy`, `bulk-delete`, `marker-manifest-delete`, `benchmark`. A capability enables all of its actions or none, so no half-capability can be assembled from the outside.
4. **Mirror has no token and cannot be enabled.** [ADR 0020](0020-recovery-containment.md) refuses Mirror independently of this gate and is checked first, and ADR 0052 decision 5 says that refusal must never become a scoping decision. A test asserts that no value of the variable enables `TransferService.StartMirror`.
5. **An unrecognised token is ignored and reported.** The application starts with that token contributing nothing, logs a line naming it, and carries it in the `Info` document as unrecognised, so an operator who mistypes `upload` is told rather than left wondering why the affordance never appeared. Failing closed is the safe direction; failing silently is not.
6. **The application says what it is running with.** Startup logs the enabled set. The `Info` document, already a bridge action, carries the enabled capabilities and any unrecognised tokens, so no new action is added and the contract inventory does not change. The interface shows a persistent banner naming every enabled capability and saying that it is not proven, and it offers that capability's affordances only while it is enabled.
7. **The default is empty.** No variable, no enabled capability, and a build that is byte-identical to the one that ships. The switch changes what a process offers, never what an artifact contains.

## Why not a build tag

A build tag would make the shipped artifact unswitchable, which is stronger. It would also mean the binary the operator tests is not the binary that ships, which is the property the release evidence rests on, and it would need a second build for every trial. Setting an environment variable already requires the local access that running the application requires. If the operator later wants the shipped artifact to be unswitchable, a build tag can gate the reading of the variable without changing anything else in this decision.

## Required proof before implementation is accepted

Tier 1 under [DESIGN §65.12](../DESIGN.md). This switch admits entry points that currently refuse, on routes that write and delete; §65.12 assigns an authority change to tier 1 whatever the switch itself touches, and an earlier draft of this record claimed tier 2 on the grounds that the switch has no data effects of its own. It does not get to make that claim on behalf of what it admits.

The proof runs at the public entry points, not at the internal admission helper, and it exercises the production path deliberately: an ordinary test binary lifts the gate, so a case that does not force the gate proves nothing about a shipped process.

- With no variable set, every deferred entry point refuses through its own public method, the interface offers none of them, and a spy over the session and the store records no request and no mutation for any of them.
- With `AERONAUTES_ENABLE=upload`, the upload entry points are admitted and reach their routes, every other deferred entry point still refuses through its public method, and the collateral check shows no effect outside the upload fixture's own namespace.
- A capability enables all of its actions and no others; two capabilities at once behave as one each.
- `mirror`, `StartMirror`, an empty token, whitespace and mixed case are each handled as the decision says, and `StartMirror` refuses under ADR 0020 for every one of them, which is checked at its public method rather than at the table.
- An unrecognised token leaves the set unchanged, appears in `Info` and appears in the startup log.
- The configuration is immutable after startup: changing the variable in the running process changes no admission.
- The existing exhaustiveness test still passes, so a new bridge action cannot land unclassified.

Retained mutants, each of which must fail a case above: the enabled set consulted before the ADR 0020 Mirror refusal; the set read from the settings database rather than the environment; the set re-read per admission; an unrecognised token treated as enabling everything; a token enabling one action of its capability rather than all; the banner shown from the interface's own defaults rather than from the reported set.

## What this does not establish

It does not make any capability proven, and it is not a substitute for the contracts that prove them. It does not change the deferred table, the finish line, the release hold or what a release offers. An enabled capability in a running process is an operator's deliberate trial, and the banner says so.
