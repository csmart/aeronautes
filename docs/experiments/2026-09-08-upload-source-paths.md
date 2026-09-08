# Upload source-path correction — 8 September 2026

## Scope and oracle

This is the bounded pathname prerequisite in [ADR 0031](../adr/0031-upload-item-recording.md), before its separate checked upload migration.
The oracle is the full content read through the exact backend-captured path, plus refusal of any whole selection containing an unsupported spelling before transfer work.
An equal length at the live object is insufficient when a cleaned parent path selects a different local file.
This change leaves upload and download jobs at record version zero.

## Actual-service before reproduction

The [unchanged executed archive](probes/2026-09-08-upload-source-service_test.go.txt) has SHA-256 `8bb4c46caa8207b4fafb01a7e95911235058eb24b1fe56716067509c7c11014c`.
It ran at documentation-only source `1891f8fd452a7622293564e21a31784bbb200a4b`, with all 295 Go/SQL input hashes identical to the delivered checked Copy implementation.
The test-only overlay added this one file and replaced no application, engine or harness implementation.
The original archive remains byte for byte as executed; the maintained successor is a separate test file.

The four leaves use the actual picker-token, preflight, upload, job and recovery services, official pinned rclone 1.74.3, owned mock Swift and two-account SAIO.
The picker is an injected backend fixture on Linux, not an OS dialog.
The fixture's captured `root/link/../item` follows a symlink before its parent component and reads `alpha`.
Lexical parent cleaning instead selects the separate `root/item` containing `bravo`.
Both are five bytes.

| Provider | Captured spelling | Uploaded full body | Terminal result | Before oracle |
| --- | --- | --- | --- | --- |
| Mock Swift | Canonical path to the selected file | `alpha` | Complete; one copied/verified name, five bytes | Pass |
| Mock Swift | Symlink followed by `..` | `bravo` | Complete; one copied/verified name, five bytes | Expected failure |
| Two-account SAIO | Canonical path to the selected file | `alpha` | Complete; one copied/verified name, five bytes | Pass |
| Two-account SAIO | Symlink followed by `..` | `bravo` | Complete; one copied/verified name, five bytes | Expected failure |

Every case observes one staging PUT, one live PUT and one staging DELETE.
An independent raw GET and namespace SHA-256 witness confirm the complete five-byte live body.
Both complete owned namespaces, derived containers, unselected sentinels, another job's staging object and shared DLO/SLO aliases and segments remain unchanged apart from the permitted live object.
All owned local files and symlink metadata remain unchanged.
Public/durable job results, terminal audit authority/result, recovery rows and cancellation deregistration agree.
An orderly child reopens the job, audit and recovery state without remote traffic or cleanup; that helper does not inspect an item ledger.

The producer `upload-source-service-before-v1` has two passing controls and two expected failing leaves, plus two failing parents, with no skips or unrelated diagnostics.
Its command, complete Go JSON stream, exit, exact source and overlay hashes, and parsed observations are retained outside the repository.
This extends the smaller [test-engine pathname probe](2026-09-08-upload-item-boundaries.md#supplementary-captured-path-mismatch); neither archive is rewritten as a passing result.

## Correction and maintained evidence

[Preflight and execution](../../internal/transfer/local/local.go) require valid UTF-8, an absolute path and spelling already equal to the host's `filepath.Clean`.
Preflight checks before stat or destination lookup for that path and refuses the whole selection.
Execution validates every admitted path before processing its first item, including an earlier valid native item in an internally constructed preflight.
No path is cleaned, resolved, recased or silently routed elsewhere.
Invalid path problems use a selection-index name and constant detail, so JSON cannot replace invalid filename bytes in the refusal.
The service reports a preflight problem without creating a job.

The [focused tests](../../internal/transfer/local/upload_source_paths_test.go) cover relative paths, dot and parent components, empty components, invalid UTF-8, exact valid Unicode including a literal replacement character, and unchanged native/engine routing.
They check both rejected preflights and an internal bypass containing an earlier valid native item.
Owned storage and selected local bytes must remain unchanged on refusal.

The [maintained service matrix](../../internal/systemtest/upload_source_paths_test.go) has six leaves per provider:

| Selection | Engine route | Native route |
| --- | --- | --- |
| Canonical ordinary file | Full `alpha` body, one stage PUT/live PUT/stage DELETE | Full `alpha` body, one live PUT, no staging writes |
| Valid file followed by the unsafe captured path | Whole-selection refusal; no job or writes | Whole-selection refusal; no job or writes |
| Canonically spelled selected file that is itself a symlink | Failed engine invocation; no live copy in this fixture | Follows the captured path and copies/verifies `alpha` |

The native route uses a newline in the remote prefix with an ordinary local filename.
Refused selections preserve job, recovery, item and transfer-audit row counts and leave the shared store available.
Success and engine-failure cases check exact public/durable counters, coarse audit result and bookmark authority, recovery intent, cancellation removal and orderly child reopening.
Every case checks complete owned local and remote collateral; successful bodies have independent full-content and SHA-256 witnesses.

The selected-file symlink case corrected an initial prediction that rclone would simply skip the link and later fail the staging HEAD.
The actual pinned invocation attempts one staging PUT and exits 6 when the followed file size differs from the link's own size.
The reverse proxy observes the client cancelling that exact staged PUT; both owned providers retain no stage or live object in this fixture.
The test allows that specific cancellation only when both the request context and proxy error report cancellation, and asserts its exact count.
All other proxy errors remain test failures.
Engine exit does not acknowledge that proxy callback, so the fixture waits for an explicit cancellation signal before reading its count.
A temporary 200 ms callback delay reproduced the earlier fixture race as a missing cancellation count; the same delayed callback passes with the acknowledgement on both providers.
The job is partial with one failed name, zero copied/verified names and bytes, and retained staging recovery intent.
This is an observed fixture outcome, not a promise that every symlink is skipped or every aborted staging PUT leaves nothing behind.
ADR 0031 requires unresolved staging effects for an invoked but failed engine item when the checked caller is implemented.

The maintained producer `upload-source-service-v3` passes all twelve leaves and both parents with no skips.
The earlier two development runs exposed the mistaken symlink prediction and the proxy's unclassified expected cancellation; they are not counted as passing evidence.
The delayed-cancellation before/after runs are separate test-fixture evidence and do not add new upload scenarios.
Six temporary source overlays deliberately remove preflight refusal, remove execution refusal, allow parent cleaning, echo invalid bytes in the problem name or detail, or reject a valid replacement character.
Each is detected by the matching maintained behavioural assertion, without compilation errors, timeouts, races or unexpected proxy errors.
The parent-cleaning overlay also fails the actual service test on both providers by admitting jobs and writes.
Removing the execution guard admits the earlier valid native item in the bypass test, so this is not credit for removing an equivalent redundant check.

## Final local verification

The final `upload-source-full-v3` producer passes 2,153 named tests across all twenty expected internal packages, with no failed or skipped tests and 86.3% statement coverage.
The system package takes 667.764 seconds, including the required pinned engine, owned two-account SAIO and Secret Service fixtures.
All 297 captured Go/SQL input hashes match the final source, and the scenario reconciler accepts the complete stream and actual zero exit.
The maintained twelve-case source matrix previously took 6.13 seconds on mock Swift and 11.91 seconds on SAIO; these fixture timings are not production throughput benchmarks.

Frontend type checking, build and all 878 tests across 23 files pass, with 81.42% statement coverage.
Go vet, native and internal builds, vendored Swift core tests and source verification pass.
All 22 regenerated bridge binding files remain unchanged.
The first full run exposed an old assertion tied to the word “collisions”; its replacement checks both existing-name problems and refusal to create a job, and the complete app replay passes 299 tests.
The second full run passes all 2,153 tests; the final third run includes the subsequently proved cancellation acknowledgement.
These earlier runs remain separate evidence and are not relabelled as the final source.

The evidence validator also checks the original 295-file Git tree and archive hash, four before leaves and their exact expected diagnostics, all six broken controls, and the delayed cancellation before/after streams.
The documentation check preserves all 46 finding dispositions, 35 detailed work-package requirements and seven valuable-data release gates.
Independent local review and protected PR/master delivery are still required before treating this correction as delivered.

## Replay

Use a disposable checkout of the recorded source for the deliberately failing original reproduction:

```sh
git worktree add --detach /tmp/aeronautes-upload-path-before 1891f8fd452a7622293564e21a31784bbb200a4b
python3 - <<'PY'
import json
from pathlib import Path
root = Path('/tmp/aeronautes-upload-path-before')
source = Path('docs/experiments/probes/2026-09-08-upload-source-service_test.go.txt').resolve()
overlay = {'Replace': {str(root / 'internal/systemtest/upload_source_service_probe_test.go'): str(source)}}
Path('/tmp/aeronautes-upload-path-before-overlay.json').write_text(json.dumps(overlay))
PY
GOTOOLCHAIN=go1.27.1 go -C /tmp/aeronautes-upload-path-before test -json -race -count=1 -timeout=15m \
  -overlay /tmp/aeronautes-upload-path-before-overlay.json ./internal/systemtest \
  -run '^TestUploadSourceServiceProbe(Mock|RealSwift)$'
```

For the maintained successor, run `go test -race -count=1 ./internal/transfer/local ./internal/systemtest -run '^TestUpload(RejectsChangedSourcePath|PreservesExactCanonicalSource|SourcePaths(Mock|RealSwift))$'`.
Configure the owned SAIO endpoint using `AERONAUTES_SAIO_AUTH_URL` and require both accounts using `AERONAUTES_REQUIRE_TWO_ACCOUNTS=1`.
Put verified rclone on PATH and set `AERONAUTES_OFFICIAL_RCLONE_DIR` to its directory.
The maintained symlink fixture intentionally requires Linux; a skip on another platform is not execution evidence for that platform.

## Limits

The guard corrects a captured-path spelling mismatch; it does not bind the selected filesystem object across time.
Files, parents or symlink targets can still be replaced after selection or preflight.
It provides no selected-root capability, native-dialog approval, stronger content verification or six-platform certification.
The independent full-body reads above are test oracles; production upload still verifies live HEAD length.
Complete item history and exact failed-name accounting await the checked upload migration.
No scenario-coverage cell or complete audit package is closed by this bounded fix.
The [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.
