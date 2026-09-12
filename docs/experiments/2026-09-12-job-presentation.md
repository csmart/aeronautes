# Job operations and reported deletion counts

This is the bounded UI-09/P28 operation-label and Delete-count presentation contract.
The oracle is DESIGN §§47/49, K-UI-005, the actual Go counter writers and the existing drawer's incomplete-accounting, state and diagnostic behaviour.
It does not close UI-09, P28 or the valuable-data hold.

## Preserved before evidence

The unchanged source is `0066181da4a5dca19494964fdaad633d3d24e0da`.
The original UI-09 statement that active jobs cannot expand diagnostics is now historical: the drawer already permits opening engine, settings and retained-item details while a job runs.
The old zero-numerator claim is also historical: the three real Delete modes already display the reported deletion count.
The remaining first-slice defect is missing visible operation labels and the generic transfer noun and copied-byte suffix on Delete rows.

The [exact 19-case probe](probes/2026-09-12-job-presentation.test.ts.txt) mounted the production JobsDrawer, BrowserState and JobItems against the repository's per-test FakeApi in an immutable source archive.
The [producer](probes/2026-09-12-job-presentation.producer.py.txt) and [before evidence twin](probes/2026-09-12-job-presentation.before.json) retain the command, actual exit, source hashes, complete text outputs and named-case inventory.
The raw packet is `/tmp/aeronautes-job-presentation-before-5t6v9v90`.
All 114 archived inputs, including 103 frontend inputs, remained byte-identical before and after execution.
The test command exited **1** in **2.816096046 seconds** under its unchanged **90-second** process budget: **ten intended assertion failures and nine healthy controls**, with no skipped/todo cases, timeout, setup failure or unhandled-error report.
This is failing-before evidence, not an after result or a green suite.

| Cases | Actual unchanged result |
| --- | --- |
| Running Delete prefix and partial Empty container/Delete container nouns | Three failures: `3/10 obj · 0 B`, with `1 failed` retained on partial rows. |
| Visible operation names for the three Delete modes, Copy, Upload and Download | Six failures: each row lacked its operation label. |
| Running Delete with no recorded settings | One separate failure: expanded details claimed `settings defaults`. |
| The three real Delete numerator/state controls | Three passes: numerator three, running progress 30%, partial failure retained and no terminal progress bar. |
| Running Copy/Upload/Download counters and details | Three passes: `3/10`, `24 B`, 30%, engine/settings/active-item panel available, no mutation invoked. |
| Incomplete deletion accounting | Passed: last-confirmed wording and original reason; no final denominator, progress bar or verified total. |
| Nonpositive planned total and first-poll missing rate | Passed: no fabricated denominator, percentage, rate or ETA. |
| Historical partial Mirror | Passed: copied numerator and seven destination deletions remain separate. |

A separate Svelte check exited 0 in 3.016402958 seconds with zero errors and zero warnings.
Node was `v22.23.1`, Svelte `5.57.0`, Vite `8.2.2`, Vitest `4.1.11`, and jsdom `30.0.1`.
The unchanged lockfile SHA-256 is `c601825823cc7d20b5ac96b4da60b8d3cba509b1000c52ebaa7cd2dbb64e3796`.
The probe SHA-256 is `534df2a5b29cf42fd6b8a08db705c315198a88e3bf58d3b9cd81932641e0a4ee`; the original RESULT.json SHA-256 is `45d2d38919ffc9ecc3c04adc303e37e44ae32ad1f2a89ef8574bf01f78c2dbe8`.
Constructed JobDTO snapshots match the real counter writers; the synthetic locators are test-owned display labels.
These results establish frontend projection behaviour, not a Go-service effect, native Wails journey, Swift operation or platform-layout acceptance.

## Presentation contract

Show a visible operation derived only from each row's actual `job.type`.
Use distinct `Delete prefix`, `Empty container` and `Delete container` labels, plus `Copy`, `Upload` and `Download`.
Preserve any other nonempty historical or unknown type as escaped text instead of assigning unverified semantics; an empty type is `Unknown operation`.
Keep the current state badge and route, with narrowly associated label styling that fits the existing row layout.
Labels describe the operation; they do not claim an effect, successful completion, cleanup or independent verification.

For `delete_prefix`, `empty_container` and `delete_container` with complete accounting, keep the existing `objectsCopied` numerator and optional positive `objectsPlanned` denominator, changing only their noun to `objects reported deleted` and omitting the copied-byte suffix.
DeleteService deliberately stores reported deletions in `objectsCopied` at progress and final-report boundaries.
`objectsDeleted` belongs to the historical Mirror destination-deletion report and must not replace, augment or maximise these three numerators.
Do not infer missing, failed, unresolved, verified or actual namespace totals from any displayed count.

Keep the incomplete-accounting branch verbatim, including its last-confirmed `processed` and `reported failures` wording and suppression of final denominator, progress and verified totals.
Preserve every other failure, refusal and separate destination-deletion qualifier, even on unusual retained DTO combinations.
Copy/local byte values and generic object counts, historical Mirror counts, zero/nonpositive-plan guards, progress/rate/ETA behaviour, state, cancellation/rerun actions and active diagnostic access remain unchanged.
No new worker telemetry, polling, backend calculation, DTO migration or mutation is introduced.

The demonstrated missing-settings/defaults issue remains a separately allocated follow-up.
Its `settingsRows` helper and configuration rendering stay unchanged in this slice.
Replaying the identical original 19-case probe after only this correction must still report that distinct failure: the expected first-slice outcome is 18 passes, one failure and actual command exit 1.
Do not remove that case, invert its assertion, mark it skipped or report the whole probe green.

## Implementation and validation sequence

1. Independently review this bounded contract and honest unchanged-source proof before production changes.
2. Change only a small display helper, the row's visible label/count region and narrowly associated label CSS in JobsDrawer.
   Add dedicated `jobPresentation.test.ts` mounted tests without changing existing assertions, BrowserState, budget/settings, cancellation/actions, audit/history, JobItems or Go code.
3. Cover all three Delete modes, active and terminal states, reported numerator versus Mirror's separate field, optional denominator, zero counts, failures/refusals and incomplete accounting.
   Preserve Copy/local byte and progress controls, missing telemetry, active details, literal unknown types and safe text rendering.
4. Run maintained tests before and after the correction, replay the exact original probe with its one remaining settings failure and run independent omitted-label, wrong-Delete-classification, wrong-counter and copied-byte controls against unchanged maintained assertions and deadlines.
   Retain actual failing command exits, raw outputs, source hashes and exact inventories; run focused neighbouring frontend tests, Svelte check and build before strict implementation review.
5. Renew current-base verification and independent range review, then require all five latest PR checks, a protected merge and all five resulting-master checks before recording delivery.

Stop for counter or authority changes, a need to alter existing diagnostic/action behaviour, overlapping ownership, ambiguous operation semantics, source/evidence drift or any need to weaken tests or budgets.
Account filtering, missing-settings interpretation, additional diagnostics, native accessibility/release evidence, wider UI-09/UI-10 and valuable-data acceptance remain separate.
This contract commit claims no production correction or after result.
