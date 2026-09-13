# Scoped selection capabilities

Subject record for the backend selection capability under finish-line item L02 and security review row SEC-07.
Later slices of this subject append here rather than starting new records.
Tier one under [DESIGN §65.12](../DESIGN.md#6512-proportionate-remediation-and-a-fixed-finish-line) and [ADR 0040](../adr/0040-proportionate-remediation.md), because the behaviour decides which account's data an operation reaches.
The decision is [ADR 0041](../adr/0041-scoped-selection-capabilities.md).

## Oracle

A picker-issued selection must authorise exactly the operation, account, credential generation, container and prefix it was made for, must not authorise them indefinitely, and must not survive the effect it authorised.
Anything wider is a widened scope, and a widened scope writes to or reads from data the user did not choose.

The probe asserts that property through the real service boundary — `PreflightUpload` and `PreflightDownload` on `TransferService` — and not against the selection store, so it measures what a caller can actually reach.
Cross-account cases use a genuinely separate second account with its own credentials, mock cluster and bookmark, not a renamed first one.
Every request names a container and prefix that exist in the canonical fixtures, so a refusal is a refusal by the code under test and never a missing-container error standing in for one.

Four cases are intended failures: they state the property that does not yet hold, and they must turn into passes when the correction lands.
Three are healthy controls: they state behaviour that must not change, and they pass now.
A control that failed would mean the harness was wrong rather than the code, which is how the first draft of this probe was caught and discarded.

## Result

Against unchanged source at `31c6c3d5`, `go test -count=1 -v -run TestProbe ./internal/app/`:

| Case | Kind | Outcome |
| --- | --- | --- |
| `TestProbeDirTokenCrossesAccounts` | intended failure | a directory token was accepted for a different account |
| `TestProbeUploadTokenCrossesAccounts` | intended failure | an upload selection was accepted for a different account |
| `TestProbeTokenOutlivesGeneration` | intended failure | a token issued under an earlier generation was still accepted |
| `TestProbeTokenReplaysAcrossScopes` | intended failure | one token was replayed against a different prefix in the same container |
| `TestProbeTokenWorksInItsOwnScope` | healthy control | passed |
| `TestProbeForgedTokenRefused` | healthy control | passed |
| `TestProbeUploadTokenWorksInItsOwnScope` | healthy control | passed |

All four intended failures reproduce.
The two cross-account cases are the finding as SEC-07 words it, reached from the bridge-facing method a page can call.
Independent review reproduced all seven cases under the race detector on the same source, with no race report.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/l02/`, with `SHA256SUMS` beside the files.
Raw output, the structured twin and the probe source stay outside this repository under §65.12.

| File | SHA-256 |
| --- | --- |
| `before-run.log` (raw) | `a9552b66f9c5672ed6fd7cb0a9d8d242652fc9135ae6ae383a74cc20a2cd291c` |
| `before-probe.json` (structured twin) | `e67681abe877db870b8ff9fa621b4d057c5a8180cdecd21dcdcf15fa81bfe296` |
| `selection_capability_before_probe_test.go.txt` (the probe) | `e1ac8c5b0836bb88ce462707e44e6d5176211f1370af993196e85e471b797ba2` |

The probe is not carried in the tree: the maintained regressions that replace it ship with the correction.
No pipeline artifact is cited, so no expiry applies.

## Limits

The binding is partial protection against retargeting and does not authenticate what the user approved.
`BrowseService.Connect` is reachable from the bridge, so a page can connect to a second account before opening the picker while still rendering the first.
The selection is then genuinely made under the second account and every check passes.
What the correction defeats is the retained-token case SEC-07 describes, along with replay, expiry, operation confusion, generation drift and an account swapped while the dialog is open.

A trusted acknowledgement of account, container and prefix for selected local resources, presented outside the webview's control, remains an open L02 requirement and is not measured here.
The forged force and public access-control half of L02 is a separate open requirement and is also out of scope for this record.
L02 does not close when this slice lands.

Both mock clusters are in-process fixtures.
The behaviour asserted is the service's own admission of a selection and does not depend on cluster behaviour, but no real deployment was exercised.

## Follow-up: the correction

A selection now records its operation, the connected bookmark and its generation, the container and prefix it was made for, and the time it was issued.
Both preflights validate that whole binding read-only and may repeat.
Both starts consume the selection atomically under the store lock, so a confirmed pick cannot be replayed into further jobs and two concurrent starts cannot both win.
An unstarted selection expires after ten minutes.
The connection registry is read before the native dialog opens and again when it returns, and the pick is refused if the account moved while the user was choosing.

The bridge surface gained `SelectionScopeDTO` and both pick methods take it, so the generated bindings and the two dialog call sites ship in the same commits.

### Result

Fourteen maintained regressions in `internal/app/selection_capability_test.go` pass, and they replace the archived probe.
The four cases that reproduced SEC-07 are now refusals with their own sentinel errors: a different account for download and for upload, a moved generation, and a different prefix.
The controls hold: a selection works in its own scope for both operations, a preflight may repeat before the start, and a forged token is still refused.

Eight deliberately broken controls were built from the current source by one producer, each removing exactly one safeguard.
Every one is caught, and each only by its own cases:

| Removed safeguard | Caught by |
| --- | --- |
| account binding | the two cross-account cases |
| generation binding | the moved-generation case |
| container and prefix binding | the other-prefix case |
| operation binding | the other-operation case |
| expiry | the expiry case |
| consumption at the download start | the consumption and concurrency cases |
| consumption at the upload start | the upload consumption case |
| picker boundary recheck | the swapped-account case |

The upload consumption guard went undetected on its first build.
That is the dual-path blind spot in its usual shape, and it was closed by adding the upload twin of the download consumption case rather than by adjusting the guard.

Four frontend cases assert the other half of the contract: the pick carries the scope the dialog captured, and a navigation during an open pick does not retarget it.

### Evidence

Same durable private packet, `~/code/.aeronautes-work/20260913-claude/l02/`.

| File | SHA-256 |
| --- | --- |
| `after-run.log` | `5b309f635f3cd72b3817e7be932665667029083018ba726540f665c8c950b032` |
| `guards.json` | `b51ab34684d228ffd12da172683e981fd92e7836fb6dde6dc5721d43df1555c8` |
| `build_guards.py` (the producer) | `e11f362b92e363e27ff5b6312310ffd1bc7d93c66ee2be47f5859ab516de153d` |

### Further limits

Consumption is taken with the job's handle, before the transfer engine is built.
A start that fails at engine construction has therefore spent the selection and the user picks again.
That is the price of deciding the winner under a single lock, and it is preferred to a window in which two starts could both derive a handle.

The picker boundary is two observations, before and after the dialog.
It detects an account that differs at those points; it is not a record of every transient change, and an account moved away and back within the dialog is not detected.
Closing that would need a connection epoch, which this slice does not add.

A systemtest that uploaded into one account while connected to another now connects first, matching what the dialog does.
No product path pairs a pick with an account the user is not connected to.

## Follow-up: the account half of the binding

The first implementation read the account from the connection registry alone and refused a pick when there was no active connection.
A full run showed why that is the wrong coupling: no other local transfer method needs an active connection, and republishing a bookmark drops one, so 515 of 519 failing leaves were picks refused after a fixture republished the bookmark.

The pick now takes the bookmark alongside the container and prefix and resolves that bookmark's current generation itself.
On top of that it observes the connection endpoint — the connected bookmark and its generation, or the honest absence of one — before the dialog opens and again when it returns, and requires the same observation both times.
Absence means nothing is connected and nothing else: the endpoint is read directly under the registry's lock rather than through the validating snapshot, which cannot tell "nothing is connected" from "the connection changed while I was reading it".
The named bookmark's generation is re-read at the same point.
The token is still pinned to exactly one account for its whole life, which is what SEC-07's retained-token case needs.

Independent review found that an earlier form of this check could be walked past: connecting elsewhere during the dialog and then republishing that bookmark leaves no active connection, so the endpoint differed while the requested bookmark and generation did not, and the pick still succeeded.
The endpoint is now compared in its own right, and that transition is a maintained regression for both pickers.

### Result

Twenty-one Go regressions and four frontend cases.
The four picker checks are separated: naming an unconnected account, an account swapped mid-dialog, a connection retired mid-dialog, and a generation moved mid-dialog while nothing is connected.

Independent review then found the remaining conflation: the validating snapshot returns the same error for nothing being connected and for a connection that changed under it, and the second can leave another account connected.
Reading the endpoint directly removes the ambiguity, and a regression drives connects concurrently and asserts the endpoint is never read as absent while a connection exists.

Eleven deliberately broken controls, each caught only by its own cases, none undetected.
`guards.json` records the head it was built from and whether the working tree carried uncommitted changes, because the guards describe the tree they were built from rather than the named commit alone.

### Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/l02/`, digests in `SHA256SUMS` beside the files.
`after-run.log` is the focused proof, `guards.json` the broken controls with the head they were built from, `suite-final.json` the full run.

### Further limits

Consumption is taken with the job's handle, before the transfer engine is built, so a start that fails at engine construction has spent the selection.
That is the price of deciding the winner under a single lock.

The picker boundary is two observations.
It detects an endpoint that differs at those points; an account moved away and back within the dialog is not detected, and closing that would need a connection epoch this slice does not add.

The remaining collateral was fixture ordering rather than product behaviour.
Fixtures that republish a bookmark through a proxy now do so before the user would have chosen, and one fixture that reused a single selection for two prefixes takes one per prefix.
Both are what the dialog does.
