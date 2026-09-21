# ADR 0069: preserve covered scenario cells

**Status:** Accepted contract; comparison component prepared; baseline admission and aggregate activation pending.

## Context and oracle

[ADR 0013](0013-scenario-coverage-reconciliation.md) deliberately separates a valid producer report from complete scenario coverage and defers preservation of credited cells.
[Remediation P34](../audits/2026-09-05/remediation-plan.md) requires lost cells and mismatched evidence to block release, while allowing critical regression gates to grow earlier.
At `373d2e537e6267e588cdc2416b89a0d1e1c19c36`, `cmd/scenariocov` exits successfully for a valid incomplete report, and `scripts/saio-partition.py` joins checked main/heavy producers without comparing their cells with an earlier report.
Deleting the last claimant of a cell can therefore leave those checks successful.
This is the explicitly deferred regression gate, not a change to the existing meaning of `valid`.

## Decision

### Admit a reviewed baseline

Keep a versioned repository baseline containing the complete action/kind/required-scenario snapshot and a separate set of protected action/scenario pairs.
The first baseline must wait for the final queued protected source and reconciliation of its actual successful CI producer set; this contract selects neither baseline data nor counts.
Initial admission and routine updates require both main and heavy lanes from one identified workflow run and attempt, the successful aggregate join, and every required check successful for that exact protected source.
For the first baseline this is the existing join; once the preservation check is installed, routine updates also require that check to pass.
Retain the source commit, contract hash, Go version, workflow run/attempt, report schema, and digests of the raw reports and producer receipts with a durable evidence reference.
The baseline must protect every credited pair in that reconciled main report, without choosing a smaller convenient subset.
Record the full required set even for actions with no credited cell, and retain all gaps and optional skips as such.
Missing coverage is neither accepted N/A nor a waiver.
An empty, missing or unverified baseline is not a passing default; do not install the gate before its first baseline has been independently reviewed.

### Preserve identities at the aggregate join

Run the comparison at the existing main/heavy join, after the existing producer, receipt, source, toolchain, run/attempt and inventory checks.
Require the current complete action/kind/required-scenario snapshot and contract hash to match the admitted baseline before comparing coverage.
Any added, removed, renamed or reclassified action or required scenario is required-set drift and fails the ordinary gate.
Every protected `(action, scenario)` pair must appear in the current credited set; equal counts or an unrelated newly covered cell cannot compensate for a missing pair.
Names are the literal contract identities, with no case folding, aliasing or display-label matching, and diagnostics are deterministic.
Covered-pair and witness order is immaterial; required-scenario ordering still follows the existing contract fingerprint.
A different passing top-level witness may preserve the same cell, so test names are evidence rather than permanently fixed identities.
Additional genuine credits may pass without becoming automatically protected baseline entries.

The baseline records historical source identity, which normally differs from the candidate being checked.
Require each current report's commit and Go version to agree with the current producer plan and the join's expected identity; preserve the existing run/attempt checks on the plans and receipts.
Do not require the historical baseline commit or workflow run to equal the candidate's.
Validate both reports' denominator against the baseline, keep the heavy lane's zero-credit rule, and use the main report's credited cells without inventing a report union.

### Review every baseline change

A routine baseline update requires a newly reconciled successful protected-source producer set and independent review of the old/new baseline and its retained evidence.
It must retain the same required snapshot and every previously protected pair, and add every newly credited pair from that admitted report.
Baseline changes must not erase a prior failure, omit an inconvenient cell, turn an optional skip into coverage, or substitute assertions that never emitted an accepted receipt.
Do not regenerate or shrink the baseline automatically from a failing candidate.

Required-set drift fails the gate until a separately accepted action/scenario contract decision authorises the changed snapshot and the following evidence route is completed.
That decision must name every changed identity and its behaviour oracle, explicitly account for any previously protected identity it changes, and state the required coverage for the changed action/scenario contract.
Every other previously protected pair must remain credited; unrelated losses are not admissible.
This route grants no automatic deletion, N/A, new-action waiver or temporary bypass.

Use the candidate's own identified commit, workflow run and attempt to produce both main and heavy reports with valid receipts and successful pre-existing join checks.
Retain the preservation comparison's expected required-set-drift failure as a diagnostic; it is not a successful gate or permission to merge.
Other validation failures or loss of a protected pair not explicitly accounted for by the accepted behaviour decision disqualify that evidence.
The replacement baseline must include the candidate's complete required snapshot and every credited pair, and land in the same independently reviewed change as the changed contract and its decision record.
Review the old/new identities and baseline together with those retained producer and drift diagnostics; editing baseline data alone does not authorise drift.
The next exact-head full CI run must pass every required check, including preservation against the reviewed replacement baseline, before the normal protected merge.
After merging, reconcile and retain the resulting protected-source producer evidence; any further baseline update follows the routine reviewed-update rules.
Unlike first and routine admission, this drift route initially uses candidate-source evidence because the unchanged baseline must refuse that candidate's new snapshot.
The trust boundary remains reviewed repository code and branch protection under ADR 0013, not resistance to a malicious committer who rewrites both the check and its baseline.

### Refuse inconsistent evidence and retain diagnostics

Reject unsupported schemas, absent required metadata, malformed identities or field types, duplicate keys/actions/pairs, unknown scenarios, and protected or credited pairs outside the required set.
Reconcile each row's required, covered and gap sets, its kind, and the aggregate action/required/covered/gap counts before using any credit.
`coverageComplete` must agree with the actual gaps, and `valid`, `processSucceeded`, `streamComplete` and `contractValid` must all be true without contradictory reported errors.
Covered entries must retain at least one witness and its parallel evidence entry, including evidence string values allowed by the reporter; this does not add a new behavioural oracle or authenticate the test's assertions.
Recompute the contract hash from the complete action/kind/required snapshot using the existing contract hash definition rather than trusting a matching label alone.
Require the reports' source/toolchain/contract metadata to agree with their producer identity and admitted contract, preserving the existing schema's legitimate empty-list representation.
`saioConfigured` remains only an environment-configuration observation and cannot stand in for actual required fixture execution.
An unreadable, truncated or internally inconsistent report must fail, never become an empty successful set.

Keep the raw main/heavy reports, events, actual process exits and producer receipts regardless of the comparison outcome.
Write a separate gate result identifying the candidate and run/attempt, baseline digest, drift, missing protected pairs or validation error; retain it on failure as well as success.
Failure to read or write required evidence also fails the aggregate, with the available error and raw evidence retained.
The comparison's nonzero exit must reach the required CI aggregate without a best-effort or continue-on-error conversion.
Do not rewrite the producer's `valid`, gaps or `coverageComplete` to encode preservation success.

## Required proof before implementation is accepted

Use the existing partition-helper tests and realistic reporter fixtures, plus a focused aggregate invocation that records its real exit and retained diagnostics.
Keep existing producer/parser controls and budgets unchanged.

| Control | Required result |
| --- | --- |
| Valid incomplete report retains every protected pair; baseline origin is an older run | Preservation passes while gaps and `coverageComplete: false` remain truthful. |
| Last credited claimant disappears, omits its receipt, pauses through the claim, or skips | No credit for that claimant; fail if the protected cell has no other passing witness. |
| One protected pair is replaced by a different pair at the same total | Fail by identity; extra coverage cannot offset the loss. |
| A redundant witness disappears but another still credits the same cell | Pass preservation without requiring a fixed test name. |
| Required action, kind or scenario changes, including a new uncovered action | Fail for drift pending the separate contract decision; do not lower the denominator. |
| Valid identified drift-candidate producers, followed by a reviewed replacement baseline | Retain the first run's drift failure; only a fresh exact-head full CI pass permits merge, followed by protected-source reconciliation. |
| Replacement evidence has an unrelated protected-cell loss or another validation failure | Refuse admission; the accepted identity change cannot excuse another failure. |
| Missing/empty baseline, duplicate or unknown entries, inconsistent sets/counts/flags, incompatible schema or contract hash | Fail with the specific cause retained. |
| Current report metadata disagrees with its plan, or a lane, receipt, producer/tee exit or required fixture is invalid | Fail; an older baseline origin remains a passing control, not an identity mismatch. |
| Heavy report has zero credits, then an unexpected credit | Preserve the existing zero-credit success and nonzero-credit refusal. |
| Remove the new set comparison or suppress its nonzero exit | A named behavioural assertion fails; a syntax, compilation or setup failure is not a caught regression. |

Demonstrate the lost-cell case against the unchanged valid-report behaviour before the new comparison, then through the corrected aggregate path.
Retain before/after and broken-guard outcomes separately, including a deliberately invalid control proving the runner cannot mislabel setup failure as a catch.
Workflow verification must show that comparison failures block the required aggregate and still retain diagnostics.

## Scope and remaining decisions

The intended implementation surface is the existing partition helper and its tests, reviewed baseline data, and the aggregate workflow invocation and diagnostic retention.
The comparison/report-validation component and its focused controls are prepared in `scripts/saio-partition.py` and `scripts/saio-partition-test.py`; the existing join, CLI and CI do not call it.
Baseline admission, aggregate wiring and required workflow failure-propagation proof remain pending.
The Go reporter/parser, scenario receipts, suite selection, process deadlines, fixtures and application behaviour remain unchanged.
A valid incomplete report can pass preservation; a full producer set is not a complete behavioural matrix.
This bounded regression gate is not P34 release acceptance and does not make the current release workflow consume exact-source engine, native or deployment evidence.
The explicit per-action/UI required-versus-N/A contract, deferred/refused capability treatment, supported target scope, required native/engine/deployment evidence and their acceptance decisions remain open.
Missing tools or infrastructure and existing deferred-capability tables are not automatic N/A.
No native, security, release or valuable-data hold is closed.
