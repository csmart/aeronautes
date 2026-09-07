# ADR 0022: execute the complete rclone selected set

**Status:** Proposed for independent review before changing the runner.
**Scope:** P22 / DS-13, the pinned rclone selected-file invocation, DESIGN §§14.3, 26 and 28.4, K-XFER-009/014, K-PERF-001/002, K-CON-001 and K-TEST-001.

## Evidence

Native listing continuation now exposes the full plan, but the preserved mixed Copy case still reports partial when the owned provider proxy caps every selected container listing to one row.
The two rclone-safe names and one native name total twelve bytes; one engine-staged name is missing.
Removing only the cap makes both mock-Swift and SAIO Copy cases complete.

The pinned v1.74.3 [filtering documentation](https://github.com/rclone/rclone/blob/v1.74.3/docs/content/filtering.md#files-from-read-list-of-source-file-names) says a selected-file list normally filters a traversal; paired with `--no-traverse`, names are addressed individually.
The [pinned march implementation](https://github.com/rclone/rclone/blob/v1.74.3/fs/march/march.go) selects that path when both options are present.
This is evidence about the existing pin, not an engine upgrade.

A disposable experiment over the reviewed native-continuation source preserves the original complete-Copy assertions and global cap.
The baseline fails both mock and SAIO cases, adding only `--no-traverse` to the runner's `copy` argument list makes both pass, and removing that one option makes both fail again.
Repository source remains unchanged during the experiment.
The [experiment record](../experiments/2026-09-07-selected-set-copy.md) preserves the inputs, results and proof limits.

## Decision

Use `--no-traverse` together with the existing `--files-from-raw` and `--ignore-times` in every selected-set `Runner.Copy` invocation.
Apply the same selected-set contract to remote Copy, local upload and local download callers.
Keep single-object `CopyTo` unchanged.

The application already owns selection and stages the selected work; rclone must not rediscover membership through a second directory traversal.
Addressing those selected objects during execution does not add HEAD calls to Browse, Find or native listing/planning rows.
Keep the existing batch/concurrency limits; this decision does not establish the wider byte/RSS and local-inventory bounds assigned to P24.
Individual object requests may cost more than traversal for a dense selection; representative throughput and fairness remain P25 work.

Preserve the pinned binary, literal raw list, native fallback classification, invocation-local credentials/gateway, retry policy and staged destination checks.
Rclone still cannot write a live Swift destination or execute destructive commands.
Conditional native placement, pre/post-write checks and fresh verification remain the source of completion evidence.
Missing selected source names may still be omitted by rclone without a nonzero exit; engine success remains insufficient.
This option does not grant overwrite, Mirror, recovery-cleanup or external-writer authority.

## Implementation and acceptance

After independent design review, add the one runner option and the corresponding flag-surface/argument regression.
Turn the maintained capped mixed Copy case from a partial characterisation into complete three-name/twelve-byte verification, preserving the global cap, actual two engines, uncapped control and independent source/collateral snapshots.
Keep the earlier partial experiment as historical evidence, not as a permanent expectation of a broken current path.

Add controlled selected-set upload/download coverage for the shared runner path with literal names and independent filesystem/Swift observations.
Exercise multi-page mixed remote Copy including nested names and plain/large-object sources, and require exact expected bytes plus unchanged source/collateral state.
Retain refusal/missing-source and stage-isolation checks so this membership correction cannot substitute engine exit status for verification or reopen unsafe writes.

Replay the maintained regressions on the parent and independently remove the new option.
Require assertion failures from the actual paths, excluding build failures, skipped fixtures and timeouts as proof.
Run the relevant complete internal race suite, source gates, actual pinned-engine encoding/large-object tests and local transfer tests.
The exploratory rclone compatibility matrix reports observations and does not itself fail on semantic differences; inspect its result rows separately from the enforced tests.

Require independent local implementation review, all five latest-head/current-master PR checks, protected merge and all five resulting master checks.
Only then reconcile P22/DS-13 against its full execution and verification acceptance.
Other authority, durability, outcome, writer, ownership, resource, local-filesystem and valuable-data release gates remain open.
