# Local outcomes hidden by later interruption

This is the bounded P13/UI-09 follow-up to [ADR 0024](../adr/0024-local-job-completion.md), following PR54 delivery.
The application source is unchanged at reviewed `12606cb9a297342efde2197bf179f9ea825cb827`, whose tree exactly matches master `b365170716d9bbab6fd270efdaf505d697bf4c3a`.
The external Go overlay adds test cases and wires the existing public SettingsService into the behavioral harness; it does not replace product transfer behaviour.

## Actual-service reproductions

Both providers use actual StartUpload, StartDownload and CancelJob entry points, the pinned official rclone 1.74.3 and two separately witnessed owned accounts.
Every case checks complete owned namespaces, local files, terminal job/audit records and retained recovery facts.
The persisted state is reopened in a separate child test process after a checked database close, with no new service storage requests.
This is orderly reopening, not a crash or power-loss experiment.

The upload selects two newline-named files routed natively and a final engine-safe file in explicit order.
The first native PUT either fails before reaching Swift or succeeds and passes the existing native verification.
The second native PUT reaches an exact HTTP response barrier after the first item and its progress callback finish.
Cancellation cases refuse that second PUT before forwarding, hold its response, call CancelJob, and require the selected exchange to end with context cancellation and no retry.
The final engine batch observes the cancelled context and never writes.
Non-cancelled controls release the same barrier and allow the engine-safe file to finish.
The fixture handles the native client's `100-continue` handshake with a bounded read of its tiny selected body before invoking the existing response barrier.
It does not weaken the barrier's deliberate refusal of interim responses.
An initial probe omitted this adaptation and failed upload setup; those failures are excluded from product-defect evidence.

The download sets the actual public maximum-active-jobs setting to one and holds a first download at its source GET response.
A second StartDownload completes preflight and returns its job ID while its job remains queued with no start time.
The test then creates regular-file sentinels at its planned directory marker path and generated staging path, as required by the case.
After the first job is released, the second encounters an earlier marker-creation failure and a later staging-directory error, or successfully creates the marker before the later error.
The marker and staging refusals are controlled filesystem races after preflight, not simulated crashes.
All sentinels and unrelated local/account data must survive unchanged.

## Observed results before the fix

The final `interrupted-probe-baseline-v2` run exercised sixteen leaf cases: the eight intended outcome assertions failed and eight controls passed, with no skipped cases.
Both providers produced the same outcomes below.
Every named test reached the state, data, collateral and orderly-reopen checks; there were no fixture/setup failures in this final run.

| Case, per provider | Persisted result before the fix | Required result |
| --- | --- | --- |
| Upload: earlier failed PUT, then cancellation | `cancelled`, copied 0, failed 2, empty error | `partial`, preserving the first failure and cancellation reason |
| Upload: earlier successful PUT, then cancellation | `cancelled`, copied 1, failed 1, empty error | `partial`, preserving that copied item, failed attempt and cancellation reason |
| Upload failure control without cancellation | `partial`, copied 1, failed 2, first failure visible | Unchanged |
| Successful mixed native/engine upload control | `complete`, copied 3, failed 0 | Unchanged |
| Download: marker failure, then stage error | `failed`, copied 0, failed 1, only the later stage error visible | `partial`, preserving both error details |
| Download: completed marker, then stage error | `failed`, copied 1, failed 0 | `partial`, preserving the completed item and later reason |
| Download stage error without earlier outcomes | `failed`, copied 0, failed 0, stage error visible | Unchanged |
| Successful download control | `complete`, copied 2, failed 0 | Unchanged |

Cancelled uploads attempt exactly the two selected native PUTs; the first successful item is the only permitted remote delta in its case.
The complete upload controls preserve exact body hashes, while both accounts' other-job and unselected objects remain unchanged.
Downloads perform no remote mutation; their separate first-job file, completed marker and selected file are the only permitted local deltas.
Every upload retains exactly its original staging recovery identity, including jobs with no physical residue; downloads invent no recovery rows.
The terminal audit matches the currently incorrect job state, confirming that atomic persistence alone does not correct outcome selection.

## Evidence and next step

Commands, source/overlay hashes, raw Go test events, actual exit codes and twenty-four observed job records are retained under `/home/csmart/code/.aeronautes-work/20260908/interrupted-probe-*`.
`interrupted-probe-observations.json` records the sixteen per-case dispositions and observed job/audit/recovery snapshots.
The initial upload setup failure is retained separately as `interrupted-probe-baseline-v1`; only version 2 supplies the final reproduction counts.
[ADR 0025](../adr/0025-local-interruption-outcomes.md) defines the next reviewed runner and regression change.
No complete identity ledger, new cancellation boundary, stronger verification, safe retry, Copy migration or valuable-data readiness is established here.

## Maintained implementation and evidence

The independently approved ADR 0025 contract is implemented by a seven-line addition to the local runner's terminal switch.
A returned report with completed items or transfer failures now produces `partial` when execution subsequently returns an error, preserving any first-failure summary and the full later reason.
The existing post-commit issue, no-outcome cancellation/error, verification-transition and atomic-persistence branches remain intact.
No execution or cleanup boundary is changed.

`internal/app/localoutcome_test.go` now covers fifteen direct-finaliser cases, including the original one-completed-item controls with explicitly corrected partial expectations, wrapped cancellation and distinct zero-completion controls.
Their advisory progress intentionally differs from the final report so progress cannot substitute for the saved outcome.
`internal/systemtest/local_interruption_test.go` maintains all sixteen actual-service cases described above, including both providers, selected exchange cancellation, exact bytes/collateral, recovery identities, audit authority and orderly separate-process reopening.
Its only additional service wiring is the existing public SettingsService in the system harness; the common response barrier and all production service APIs are unchanged.
The current drawer renders the stored state, numeric counts and error text directly; it does not infer that a write succeeded from `partial` alone.

The focused run passes all 31 leaf cases and three parents across the app and system-test packages.
The exact old local caller fails thirteen leaves: the eight service outcome cases and five direct-finaliser cases.
All original passing service controls still pass with that old caller.
Seven additional broken variants are detected independently: omitted earlier details, omitted later reason, ignored completed subset, ignored failures when selecting the state, corrupted copied counts, invented failures for unattempted items and lost wrapped-cancellation text.
The controls use Go overlays; application sources are not changed between positive and negative runs.

The final full internal run passes 603 top-level tests and 1,530 leaf cases in twenty packages with zero failures or skips and 85.3% statement coverage.
It uses Go 1.27.1, `-race -count=1`, the pinned engine, the required keychain fixture and two-account SAIO.
All tested Go source hashes match the final implementation.
Scenario reconciliation is valid at schema 3 and remains 32/110 cells, with 38/49 actions having gaps; no additional action credit is invented for these regressions.
All 852 frontend tests pass across 22 files at 79.33% statement coverage, and Svelte reports zero errors and warnings.
Frontend build, Go vet, Linux native build, the CGO-disabled internal build and patched Swift source verification pass.
Raw events, exits, source/overlay hashes, negative-control results, build checks and the valid scenario report are retained under `/home/csmart/code/.aeronautes-work/20260908/interrupted-*`.

Independent implementation review and protected PR/master pipeline verification remain required delivery gates.
The complete identity ledger, 200-failure truncation, unknown acceptance, safe cancellation/placement and cleanup, Copy migration, native platform evidence and valuable-data release hold remain open.
