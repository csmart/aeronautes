# Download destination spelling — 9 September 2026

## Scope and oracle

This implements the captured-destination prerequisite in [ADR 0032](../adr/0032-download-placement-prerequisites.md).
The oracle is the exact backend-captured directory spelling, full selected bytes and complete permitted local and remote deltas.
The [original archive](2026-09-09-download-boundaries.md) remains unchanged: a symlink followed by `..` selects one directory while lexical cleaning sends downloaded bytes to another.
The correction refuses unsupported spelling before planning or execution; downloads retain record version zero.

## Correction

[Preflight and execution](../../internal/transfer/local/local.go) require valid UTF-8, an absolute destination and spelling already equal to the host's `filepath.Clean`.
Preflight checks before a destination stat, path join or source listing, including an empty remote scope.
Execution checks again before any local write or engine invocation, including an internally supplied plan.
Neither check cleans the path, resolves a symlink, changes Unicode or silently changes routes.
The rejecting preflight has one constant destination label and explanation, with no lossy rendering of invalid bytes.
The [service](../../internal/app/localtransferservice.go) calls this a preflight problem rather than assuming every refusal is a collision.

Relative paths, dot or parent components, repeated separators, a trailing separator and invalid UTF-8 are intentionally refused.
Already-clean absolute roots remain eligible.
Canonical paths, supported Unicode including a literal replacement character, and directly selected symlinks preserve their exact spelling.
This is a conservative spelling restriction, not a stable filesystem-object capability.

## Reproduction and maintained tests

The private before producers ran at `5951f58feab839106c4ed270397b1c03be904e2a`, with 311 unchanged Go/SQL input files and test-only overlays.
The identical 111 package-qualified named tests produced 86 failures and 25 passes before the correction, then 111 passes with the private guard applied.
Those counts include parent tests; the 99 leaves comprise 74 expected failures and 25 controls.
The maintained test files and guard were promoted byte for byte from that passing trial.
Raw producer streams, exits, source and overlay hashes and diagnostics remain separate from the unchanged original archives.

The [service matrix](../../internal/systemtest/download_root_guard_test.go) exercises ten spellings, empty and populated remote scopes, both transfer routes and both owned providers: 80 leaves.
It uses actual application services, the pinned rclone child, mock Swift and two-account SAIO, with an injected backend picker.
Before testing, a read through the captured path proves which directory was selected.
Refusal requires no job, recovery, item, observation or transfer-audit rows, no storage requests, an available store and unchanged complete owned local and remote state.
Accepted controls require full selected bytes, exact public/durable counters, a matching completion audit, no remote writes and unchanged collateral.
History cannot restart the download; orderly child reopening preserves state without storage requests or cleanup.
These fixtures use ordinary remote objects and second-account sentinels; they do not establish shared DLO/SLO behaviour.

The [local preflight tests](../../internal/transfer/local/download_root_guard_test.go) add empty selection and safe JSON refusal checks.
The [executor entry tests](../../internal/transfer/local/download_root_entry_test.go) check directory, native and engine plans against a complete owned filesystem snapshot.
The [service admission test](../../internal/app/download_root_admission_test.go) proves refusal before engine construction, beside a canonical empty-scope control that reaches that boundary.

Six deliberately broken production overlays were detected first in the private trial and again by the maintained tests against all 315 full-suite Go/SQL source hashes.
Each failed through the intended behavioural assertion, with no skips or compiler failures.

| Broken behaviour | Expected failing leaves |
| --- | ---: |
| Remove the preflight guard | 4 service leaves across both routes/providers |
| Remove the executor guard | 3 local execution routes |
| Allow changed spelling | 4 service leaves across both routes/providers |
| Allow invalid UTF-8 | 4 service leaves across both routes/providers |
| Allow relative roots | 4 service leaves across both routes/providers |
| Move the executor check after local work | 2 local filesystem-effect assertions; engine control passes |

The last control returns the intended refusal but has already created a directory or placed native bytes.
Its failure proves that checking only the returned error would miss an unsafe effect.

## Local verification

The focused maintained producer passes 112 named tests across the local executor, app and system packages, including the existing upload/download service regression.
The complete `download-root-full-v1` race/coverage producer passes 2,402 named tests across all twenty expected internal packages, with zero failures or skips and 86.7% statement coverage.
The system package takes 663.482 seconds with owned two-account SAIO, the required Secret Service fixture and pinned rclone 1.74.3.
Its recorded HEAD is `82e1a19e86b0e5146b36ebfdd22d125c43f85a66`, while this correction was uncommitted; the captured 315 file hashes identify the executed source without relabelling that HEAD.
The maintained source remains unchanged after that producer.

All 2,291 prior named tests remain present, plus the 111 new tests.
One existing unchanged-source test generates its absolute expiry subtest name from the current time; the inventory check verifies each run's actual one-hour expiry before comparing it.
Twelve independent real-Swift groups pause and resume under `-parallel=3`, with no sequential fixture overlapping them.
The scenario reconciler accepts the full stream, actual zero exit and captured SAIO configuration, but coverage remains 40/116 cells with 38/52 actions having gaps.
No new scenario credit is claimed.
The existing 1,200-second package and 1,500-second hosted job limits remain unchanged; measured hosted timings are recorded below.

Frontend asset build, Go vet, production native build, the internal build without CGO, and vendored Swift source verification with its checker tests pass.
All 22 regenerated bridge binding files are unchanged.
The initial binding-check command referenced an unavailable CLI path; the recorded retry used the existing pinned generator and passed without changing bindings.
Frontend source and dependency files are unchanged, so their full test suite is left to the required PR check.
Independent local review and protected PR/master delivery subsequently passed, as recorded below.

## Delivery

Independent review approved source `a3ee071cad5461c3db80bb01bd2dfb2adc85236b` after its own complete race run: 2,402 named passes, twenty packages, no failures or skips, and 656.5 seconds for the system package.
It checked all 315 author input hashes, reproduced the refused spellings before the guard, tested additional path edges and rejected eleven deliberate weakenings at the intended assertions.
The reviewer also passed all 880 frontend tests and confirmed unchanged generated bindings.
The native picker observation below remains supplemental rather than platform certification.

[PR67](https://github.com/csmart/aeronautes/pull/67) passed all five [latest PR checks](https://github.com/csmart/aeronautes/actions/runs/34319824696) against the current master before the protected merge.
All five [resulting master checks](https://github.com/csmart/aeronautes/actions/runs/34321545648) also passed.
The reviewed source and merged master `7ad1fb81707d472b6cb34ab6d10c67fb4747e854` have the identical tree `039717d21975374fb46cdce8458e83330cbb7e8b`.
The two logical commits, prior scheduling delivery and this destination correction, were preserved as equal patches.

The PR integration package took 913.092 seconds and its complete job took 1,129 seconds.
The master integration package took 884.460 seconds and its complete job took 1,090 seconds.
Each full hosted stream contains 2,400 named passes across twenty packages, zero failures and the two existing optional skips, `TestKeychainRoundTrip` and `TestRcloneMatrix`.
The complete local inventory is accounted for, including an independently checked absolute-expiry case whose name changes with the clock.
Twelve independent real-Swift groups use the unchanged parallel limit of three.
The package retained at least 286.908 seconds of headroom and the complete job at least 371 seconds; later additions must be measured against the same finite limits.

Review follow-ups include an eligible-root nil-plan refusal, maintained filesystem-root and Unicode-normalisation edge cases, and dialog wording that still mentions collisions.
These are distinct from stable inode binding, safe staging cleanup, native closure and exact download history.
No complete audit package, scenario gap or valuable-data gate is closed by this delivery.

## Native picker observation

A separate private Linux probe uses the verbatim Wails picker method from source `5951f58f`, the actual `TransferService.PickDownloadDir` and its JSON DTO round trip, without connection or job services.
That picker method remains unchanged in this correction.
It runs on an isolated Xvfb display with the GTK local dialog backend selected explicitly for the fixture.
The final `download-native-picker-v11` run selects three existing owned directories: an ordinary name, `space-é` and a direct symlink.
Each entered location ends in `/`, as captured before acceptance.
GTK returns the corresponding clean absolute path without that trailing separator, including the unchanged direct-symlink spelling; the captured value, DTO and JSON value match exactly.
The runner checks that each returned path identifies the intended directory.

Earlier attempts remain failed or incomplete evidence: display setup and portal constraints prevented selection, keyboard completion changed the intended paths, and the clipboard attempt initially accepted an unwanted highlighted completion.
The final driver pastes the exact location and removes that selected completion before accepting it.
The results and before/entered/after screenshots are preserved separately from those attempts.
This observes one Linux GTK backend; it does not certify the portal backend, a full application transfer, native approvals or the other platform targets.

## Replay and limits

With the owned SAIO endpoint and both accounts configured, verified rclone on PATH and `AERONAUTES_OFFICIAL_RCLONE_DIR` pointing to it, run:

```sh
go test -race -count=1 -parallel=3 -timeout=20m \
  -run '^Test(DownloadRootGuard|DownloadRejectsChangedDestination|DownloadRejectsDestinationBeforeEffects|UploadAndDownloadJobs)' \
  ./internal/transfer/local ./internal/app ./internal/systemtest
```

The tests' independently read full bodies are correctness oracles; production verification still uses its existing length checks.
Clean spelling does not bind an inode, exclude ancestor or symlink replacement, prove exclusive cleanup authority or establish physical durability.
Stage cleanup, native closure, exact download item history and the remaining fault boundaries retain their separate implementation slices.
No complete audit package, scenario gap or release gate is closed.
The [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.
