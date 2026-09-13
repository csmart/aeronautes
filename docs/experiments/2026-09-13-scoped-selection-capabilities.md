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
