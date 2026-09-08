# Upload item-record prerequisites — 8 September 2026

## Scope and oracle

These before-fix reproductions establish the local-upload starting point for [ADR 0028](../adr/0028-transfer-item-records.md) and [ADR 0030](../adr/0030-copy-item-recording.md) step five.
The actual-service failure-identity probe ran on reviewed source `c3a98bfb2f04af0815d646add7d519c688d1f318`.
The supplementary recovery and pathname probes ran on `8fa7b5fad0843205e861a9861b51a5535f55a476`, which changes only the integration budget and documentation relative to that implementation.
All 295 Go/SQL input hashes remain identical across those sources and delivered master `9923de8a010650853909f9132cab89e5c49adf82` from [PR62](https://github.com/csmart/aeronautes/pull/62).
Each original Go overlay adds one test file without replacing application, executor, engine, database or shared harness code.
The three executed sources below are archived byte for byte as inert text, outside the normal passing suite.

| Probe archive | SHA-256 |
| --- | --- |
| [Actual upload identities](probes/2026-09-08-upload-failure-identity_test.go.txt) | `2a30ce879989083b0a8a7f2c3e9c41efab143ce80160eacf856f0c16d4b145ba` |
| [Required recovery boundaries](probes/2026-09-08-upload-recovery-boundary_test.go.txt) | `0b1bd8a119aee18a6c80c8a6e2e5adbd4625dcbde07659a19e8d50c91ca14bad` |
| [Captured source pathname](probes/2026-09-08-upload-source-path_test.go.txt) | `7f59788d5eefd4e28c531181d8f9c15919c80fee8f1885f54dcce62240aa4728` |

For failure identities, the oracle is DS-08's distinct failed-name total and complete access to retained identities, checked against the exact selected PUT refusal set and independently inspected bytes.
For source fidelity, it is the bytes read through the backend-captured pathname, rather than a cleaned spelling or a same-length substitute.
The recovery probes measure a proposed stronger stop boundary: a required record refusal must prevent a later engine invocation or live commit.
They are not evidence that the earlier bounded [ADR 0023](../adr/0023-upload-post-commit-errors.md) fix violated its own contract; that slice explicitly left later objects on their existing admission checks.

## Actual-service failure identities

Eight cases exercise the actual picker-token, preflight and upload services with Go 1.27.1 race detection, official pinned rclone 1.74.3, mock Swift and two-account SAIO.
The picker is an injected backend fixture, not a native OS dialog.
Each selection has zero, one, 201 or 401 newline-named local files whose native destination PUTs a proxy refuses with HTTP 403, plus one engine-safe file that must copy successfully.
Each selected failing PUT is observed exactly once before upstream acceptance.
The proxy's deliberately hostile, arbitrary response body includes the words “source read refusal”; the implemented and observed fault is a destination PUT refusal.

Both providers give the same result:

| Distinct failed names | Reported failed count | Failed names in existing summaries | Copied and verified names |
| ---: | ---: | ---: | ---: |
| 0 | 0 | 0 | 1 |
| 1 | 1 | 1 | 1 |
| 201 | 200 | 1 | 1 |
| 401 | 200 | 1 | 1 |

Every job remains record version zero, with zero item rows and public item page/totals status `not_captured`.
The successful engine file has twelve independently checked bytes and the expected SHA-256 digest.
Complete snapshots of both owned fixture namespaces preserve unselected sentinels, another job's staging object, shared DLO/SLO aliases and their backing segment bytes, including derived containers under the fixture prefix.
Selected and unselected local files remain unchanged.
Public and durable jobs, all other counters, terminal timestamps, audit authority/result, retained recovery identity and cancellation deregistration pass their checks.
An orderly child process reopens the saved job, audit and recovery records unchanged, with no remote traffic or automatic cleanup.
That child helper does not inspect a complete item ledger; these jobs have no such ledger, and the upload migration must extend the reopening witness.

The complete producer has four passing leaves and four expected failed-count leaves, plus two failing parent tests, with no skips or unrelated diagnostics.
It exits 1 because both 201- and 401-name cases reject the truncated total on both providers.
The cause is the 200-entry `Report.Failures` sample in [local execution](../../internal/transfer/local/local.go), whose length [the shared local runner](../../internal/app/localtransferservice.go) uses as the terminal failed count.
Only the first example reaches the existing job summary; the atomic terminal job/audit transaction alone does not retain the missing identities.

## Supplementary recovery boundaries

These five cases call `local.Upload` directly with an instrumented test engine and owned mock Swift.
They do not run the app service, a real rclone child, SQL faults or SAIO.

| Required record refusal | Observed continuation | Retained/result facts |
| --- | --- | --- |
| Captured segment prefix before an authentication retry | Two engine calls after one rejected callback | No reported live copy; staging remains uncertain |
| DLO `committing` | Two refused callbacks instead of stopping after the first | No live objects; both staged manifests remain |
| DLO `committed` | Both live DLOs are created after the first refused callback | Both stages remain; two copied names, ten bytes and two post-commit issues |

The authentication case emits a synthetic captured-prefix event and a 401-shaped engine result; two engine calls do not prove two real child processes.
Its ordinary-retry control passes with two calls and one eleven-byte copy.
The DLO engine seeds two owned one-segment staged manifests; its no-refusal control passes with two copied names, ten bytes and both stages removed.
The three refusal leaves fail their proposed stop assertions, and the two controls pass, without skipped cases or unexpected diagnostics.
These DLO witnesses use object presence and logical HEAD length, not complete body digests or the actual-service collateral proof above.

The captured callback saves `recoveryBroken`, but the shared authentication helper can invoke the engine again before upload checks it.
Staged commit receives that error by value, and new committing/committed record failures do not propagate a sticky stop to later names or directory groups.
The earlier affected-stage retention and exact post-commit issue count remain visible in the reproduction.
The next design must preserve those facts while specifying and proving the stronger continuation boundary.

## Supplementary captured-path mismatch

The two pathname cases use `PreflightUpload`, `local.Upload`, owned Linux files/symlinks, a test filesystem engine and mock Swift.
An absolute captured path `root/link/../item` resolves through `link` to another owned directory's child, so reading that exact path returns `alpha` from the other directory's `item`.
The separate `root/item` contains `bravo`, also five bytes.
Preflight retains the captured path, observes a regular five-byte source and an absent destination, and selects the engine route.
Engine grouping uses `filepath.Dir`, which cleans the parent spelling to `root`, so the grouped source becomes `root/item` and uploads `bravo`.
The existing live HEAD length check passes and the report counts one copied name and five bytes.
The full content oracle rejects that engine result.

The control changes only the preflight item's route to native; opening the original pathname uploads the intended `alpha` bytes and passes.
There is one expected failed leaf and one passing control, with no skips or unrelated diagnostics.
This proves a backend pathname/grouping mismatch, not that the real OS picker emits that spelling or that every native/engine path is safe.
It is not actual-service, official-rclone, SAIO or six-platform evidence.
Recording the original string alone cannot establish that it identifies the file actually consumed by the engine.
Before upload opt-in, a concrete refusal or routing correction needs actual-service regression evidence and independent review.
Dynamic path replacement, symlink authority and selected-root safety remain wider P18/P19/P11 work.

## Reproduction and evidence

The original producers are `local-upload-identities-before-v1`, `upload-recovery-boundary-before-v1` and `upload-source-path-before-v1`.
Their commands, exits, complete Go JSON streams, captured source/overlay hashes and parsed observations are retained outside the repository.
Validation reconstructs split output chunks before parsing the large identity observations, checks each exact expected failure and control, rejects unrelated diagnostics, and confirms unchanged sources and byte-identical overlays.
These are deliberately failing before-fix runs, not passing CI or new scenario-coverage credit.

From this repository, prepare a disposable replay checkout and a combined test-only overlay:

```sh
git worktree add --detach /tmp/aeronautes-upload-before 9923de8a010650853909f9132cab89e5c49adf82
python3 - <<'PY'
import json
from pathlib import Path
root = Path('/tmp/aeronautes-upload-before')
archive = Path('docs/experiments/probes').resolve()
mapping = {
    'internal/systemtest/upload_failure_identity_probe_test.go': '2026-09-08-upload-failure-identity_test.go.txt',
    'internal/transfer/local/upload_recovery_boundary_probe_test.go': '2026-09-08-upload-recovery-boundary_test.go.txt',
    'internal/transfer/local/upload_source_path_probe_test.go': '2026-09-08-upload-source-path_test.go.txt',
}
overlay = {'Replace': {str(root / name): str(archive / source) for name, source in mapping.items()}}
Path('/tmp/aeronautes-upload-before-overlay.json').write_text(json.dumps(overlay))
PY
GOTOOLCHAIN=go1.27.1 go -C /tmp/aeronautes-upload-before test -json -race -count=1 -timeout=15m \
  -overlay /tmp/aeronautes-upload-before-overlay.json ./internal/systemtest ./internal/transfer/local \
  -run '^TestUpload(FailureIdentityProbe(Mock|RealSwift)|RecoveryBoundaryProbe(Auth|DLO)|CapturedPathProbe)$'
```

Configure the owned SAIO endpoint through `AERONAUTES_SAIO_AUTH_URL` and require both accounts with `AERONAUTES_REQUIRE_TWO_ACCOUNTS=1`.
Put the verified engine on PATH and set `AERONAUTES_OFFICIAL_RCLONE_DIR` and `AERONAUTES_MATRIX_RCLONE` to its directory and executable.
Without those fixtures, a provider skip or engine lookup failure is not the complete evidence above.
The local newline and symlink fixtures were executed on Linux; Windows intentionally skips the newline fixture.
The combined replay command is a convenience for the three individually captured producers, not an additional independent case set.

The combined archive replay also ran on documentation commit `400ec5835963664433f6157820c6d8428dcfdd74`, directly above that master with all 295 Go/SQL hashes unchanged.
It reproduced all fifteen leaves: eight expected failures and seven passing controls, plus five failing parents, with no skips or unexpected diagnostics.
All three archive hashes match both the original executed probes and the replay's overlay inputs.
Its producer is `upload-archives-before-v1`; these repeated cases add no independent scenario credit.

## Next contract and limits

Review the concrete source-path prerequisite and upload recording contract before changing execution hooks or enabling version-one upload jobs.
[ADR 0031](../adr/0031-upload-item-recording.md) is the independently accepted contract; the archived probes remain unchanged.
The [source-path correction record](2026-09-08-upload-source-paths.md) adds the actual-service reproduction, its separate unchanged archive, and maintained refusal regressions before upload item opt-in.
It must acknowledge exact backend-captured identities before dependent effects, stop later work after required recording failure, retain final execution and actual HEAD-verification facts honestly, and derive distinct failed-name totals inside terminal completion.
Preserve copied bytes and the separate exact post-commit issue count when later closure, cleanup or bookkeeping fails.
Retained paths are display/history data, not new picker capabilities or rerun authority.
Upload and download migrations remain separate because their source and placement boundaries differ.

The record does not close a full audit package, native authority, stronger verification, complete per-effect history, full-app restart, native process-kill, fsync/power-loss or six-platform acceptance.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused.
The [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.
