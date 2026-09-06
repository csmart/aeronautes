# Inspector detail binding

This records P07's first implementation under §65.11 and audit UI-02.
The oracle is that a detail response belongs to the exact scope, resource and detail request generation that requested it.
A completed mutation still returns its original outcome and refreshes the existing audit view; it cannot replace a newer inspector's detail.
The existing C-04 contract remains: mutation identity comes from the fetched detail identity, independently of a subsequently changed raw row index.

## Implementation boundary

Object/prefix selection reads, subordinate SLO/DLO reads, object metadata saves and marker creation share a detail request generation.
Container opens, refreshes and saves share a separate container detail generation.
Both require the captured UI scope revision before publishing, so a change followed by a return to the original names still retires old work.
Current error and loading callbacks use the same ownership check.
A mutation supersedes a pending detail read and ends that read's loading state, including a subordinate segment preview that has not returned.
It does not automatically re-fetch that retired segment preview; selecting or refreshing the object obtains it again.

Object and prefix admission check the loaded resource name, current container and detail scope before sending a mutation.
Container working copies remember the UI connection revision and their name, so a different account or reconnect invalidates them while navigation into a prefix of the same container still permits a valid container edit.
This UI connection revision is not the Go credential generation and establishes no backend authority.
Current reads and mutation results naming a different resource cannot populate the inspector.
An unexpected mutation result is still returned unchanged to its caller; this slice does not reinterpret backend outcomes.

## UI scenario matrix

The maintained harness is `frontend/src/features/inspector/detailBinding.test.ts`.
It mounts the production Inspector with production BrowserState, fixture-derived DTOs and a recording Api boundary.
Deferred promises determine response order; bounded microtask settling allows component updates without a timing race.
These are the separate UI-to-bridge scenarios required by §65.10 rule 4 and earn no Go service-scenario credit.

| Scenario | Maintained test or group |
| --- | --- |
| Object/container/marker result after selection, account, container, prefix, return or reconnect | `mutation result ownership` |
| Same resource, two writes in either completion order | `keeps the newest same-resource mutation` |
| Original outcome and audit refresh survive navigation | `ignores a result after ... while preserving completion` |
| Current exact request, successful result and failure | `applies a current result` and `returns a current failure` |
| Conflict and verification mismatch remain distinct | `preserves current ... conflict and mismatch outcomes` |
| Old save cannot seed a later edit of another object | `keeps the selected object's complete working copy for a later save` |
| Read success/error/finalisation after navigation or reconnect | `detail read ownership` |
| Two reads, both completion orders and old success/failure | `overlapping ... reads` |
| New write supersedes old container refresh | `a newer container mutation supersedes a pending read` |
| Repeated same-container opens own their HEAD reply | `a repeated open owns container HEAD` |
| Wrong working copy or previous connection cannot admit mutation, including force | `refuses a mismatched ... working copy` and `refuses a ... working copy from a previous connection` |
| Container edit remains valid after prefix navigation; opening elsewhere captures the reset prefix | `allows the same container working copy` and `captures container HEAD after clearing` |
| Mismatched read/result names cannot replace the current portrait | `rejects a current ... read` and `does not publish a current ... mutation reply naming another resource` |
| Old large-object HEAD cannot start more work; subordinate replies/errors retire | `does not start a ... subordinate read`, `drops a late ... subordinate response` and `a mutation retires a pending ... subordinate error` |

## Evidence

With Node 22.23.2 and the unchanged locked dependencies, all **379 tests in 14 frontend files pass**, with zero failures or skips.
Whole-source statement coverage is **48.44%** (1,714 of 3,538 statements), not a completion percentage for this audit.
Frontend type checking reports zero errors and warnings, the production build completes with 205 modules, and `go build ./...` passes.
No Go implementation, bridge model, dependency or workflow changed, so no new local Go runtime or service-scenario claim is made.
The usual five PR and subsequent master checks remain mandatory.

All **105 new cases** were run against the parent production BrowserState at `8724d05e4e6940af` in a disposable frontend copy: **78 assertion failures and 27 passing controls**, zero skips or setup failures.
The identical tests pass after the implementation.
The original 72-case subset separately failed 60 assertions before and passed afterwards; it is not the final denominator.
Early type checks caught an incorrect field name in the synthetic DLO reply and were corrected before the final check and suite.
The first control runner also rejected Vitest's alternative rejection-assertion formatting; admission tests now assert the recorded absence of a bridge call directly before inspecting the returned error.
Only the complete final control run below is credited.

The archived UI-02 and UI-05 cases were selected together against the merged P06 tree and failed at their intended assertions, with the other ten archived cases unselected.
Against this first P07 slice, UI-02 passes and UI-05 still fails at the unchanged account-reset assertion: one pass, one failure and ten unselected.
The selected probe bodies are unchanged apart from import locations; the unrelated UI-01 readiness adaptations in the scratch file are not executed.
This is deliberate evidence of the remaining third P07 slice, not a passing claim for the whole archived audit.

The final scratch runner changes one safeguard at a time and requires all 105 cases to load, at least one assertion failure, zero skips, and no import, transform or unhandled-promise errors.
The following 21 variants are all caught:

| Removed or broken safeguard | Failing cases |
| --- | ---: |
| Detail scope | 24 |
| Detail request generation | 16 |
| Container scope | 10 |
| Container request generation | 11 |
| Object working-copy identity | 1 |
| Container working-copy identity | 1 |
| Marker working-copy identity | 1 |
| Object/prefix admission scope | 2 |
| Container working-copy connection | 1 |
| Object result ownership | 12 |
| Container result ownership | 11 |
| Marker result ownership | 11 |
| Read error ownership | 22 |
| Read finalisation ownership | 14 |
| SLO subordinate result ownership | 1 |
| DLO subordinate result ownership | 1 |
| Object read name | 1 |
| Prefix read name | 1 |
| Container read name | 1 |
| Mutation result names | 3 |
| Prefix reset before container HEAD capture | 1 |

Local artifacts are under `/tmp/aeronautes-remediation-20260905`: `p07-detail-final.*`, the reproducible `p07-detail-controls.py`, complete `p07-detail-controls-v2/results.json` with per-variant logs, and the separately scoped `p07-ui-before.*` / `p07-ui-after-detail.*` archived probes.
The earlier incomplete control run is retained separately and is not the final evidence.

## Remaining work

This is inspector result binding only.
P07 dialog admission/lifetime, delayed editor callbacks, mutation-triggered navigation and full bookmark/connection reset remain separate implementation slices.
P08 retains ordinary pagination and Find lifecycle work; the planned P07 reset must still retire their reset-crossing replies.
P10 native authority/cleanup, P11 backend capability/replay/expiry, P14 external writers and P15 ownership remain open.
These frontend tests do not prove that a remote mutation is correct, that storage stayed unchanged, or that native platform and release gates are satisfied.
The valuable-data release hold remains.
