# Verification record

Date: 2026-09-05 UTC.
Product revision: `cc6465ac69ec00756d9976bbf6cf7531b07da66d`.
All product-source inspection, builds and tests used the isolated `project_audit` worktree at this revision.
The primary checkout and unrelated worktrees were left unchanged.
Dependencies, tools and test state were temporary or ignored build output; no real user account, keychain credential or stored object was used for the probes.

## Environment and inputs

| Input | Observed value |
| --- | --- |
| Host environment | Linux amd64, Fedora 44 Toolbx environment |
| Go | Official Go 1.27.1 archive; temporary installation; `GOTOOLCHAIN=local` |
| Go archive SHA256 | `63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445` |
| Node | Official Node 22.23.2 Linux x64 archive; checked against its SHASUMS256 manifest |
| Frontend dependencies | Repository lockfile; `npm ci --ignore-scripts --no-audit --no-fund` |
| Wails | Repository pin `v3.0.0-beta.15` |
| Native build libraries | GTK4 4.22.4, WebKitGTK 6.0 API / library 2.52.5; GCC and pkg-config available |
| rclone | Official 1.74.3 Linux amd64; fetched by the repository checksum-verifying script |
| rclone executable SHA256 | `9700aa1273ac73d6d0833c43ba63fe830516422cb131960b8c1a24ced789cba0` |
| Go vulnerability scanner | `golang.org/x/vuln` v1.7.0; offline public OSV snapshot, 4,378 entries |
| Advisory snapshot commit | `7106aff44bfd324e2f3ec6b0f0927ee03a835325` |

Tool archives were downloaded to a task-specific temporary directory with approved narrow network access.
The repository script checks the official rclone archive and the exact embedded executable.
Go module/build and npm caches were also placed under the temporary audit directory.
No system toolchain or global ECC configuration was changed.
The `--ignore-scripts` install succeeded for the exercised build/test paths; it is not a verification of arbitrary dependency lifecycle scripts.

## Existing-suite and build results

Commands below omit only task-specific temporary cache/PATH settings and output redirections.
Use the versions above and run location-dependent commands from the indicated directory.

| Working directory | Command/check | Observed result |
| --- | --- | --- |
| `frontend` | `npm run check` | Svelte: 0 errors, 0 warnings |
| `frontend` | `npm run build` | Vite 8.2.2 production build passed; 203 modules; JS 174.61 kB / 55.07 kB gzip; CSS 50.54 kB / 6.89 kB gzip |
| `frontend` | `npm run test:coverage -- --coverage.reportsDirectory=<audit-scratch>/frontend-coverage` | 6 files, 48 tests passed; statements 17.18%, branches 17.73%, functions 19.08%, lines 25.46% |
| Repository root | `go test -json -race -count=1 -coverprofile=<audit-scratch>/go-cover.out -coverpkg=./internal/... ./internal/...` | Exit 0; 19 packages passed; 299 top-level tests passed, 33 skipped; 316 passing test events including subtests; 75.8% internal statements |
| Repository root | `AERONAUTES_OFFICIAL_RCLONE_DIR=<verified-sidecar-dir> go test -race -count=1 ./internal/transfer/rclone -run '^TestSidecarOfficialBinary$'` | Passed; resolves one baseline skip |
| Repository root | `go vet ./...` | Passed; upstream deprecated GTK API compiler warnings only |
| Repository root | `go build -tags production -o <audit-scratch>/aeronautes .` | Passed; Linux production executable built; same upstream warnings |
| Repository root | `gofmt -l .` before audit documentation | No files reported |
| Repository root | `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build ./internal/...` for six targets | Passed for Linux/Windows/Darwin × amd64/arm64 |

The root package has no tests; the measured race/coverage run intentionally covers `./internal/...`.
No native app process was launched for this audit, and the six cross-builds are internal-core compilation only.
No Wails binding regeneration/drift command or complete package installer build was run locally.
The existing CI definitions and retrieved GitHub results were reviewed separately, as recorded below.

The [Go counts](evidence/go-test-counts.json) and [whole-source frontend coverage](evidence/frontend-coverage.json) are retained.
The latter includes zero-coverage production components rather than measuring only imported modules.
The data-safety, security and UI reports point to existing useful tests as well as missing cases.

## Real Swift attempt and scenario evidence

The baseline skipped 25 integration tests, 6 system tests, 1 oracle SAIO test and 1 explicit official-sidecar test.
The later official-sidecar command passed, leaving 32 cluster/matrix tests without execution evidence here.
The real-rclone compatibility matrix remains a separate gated test, not covered merely by placing the engine on PATH.

Attempted a disposable local SAIO with the repository's exact pinned image:

```sh
podman run --detach --name aeronautes-audit-20260905-saio \
  --publish 127.0.0.1:18080:8080 \
  docker.io/openstackswift/saio@sha256:18ddf9feb9d1f419751a66eace8d956ecdaf5a085dedcc54ae51c210447ea1fa
```

The image could not unpack: the environment had insufficient UID/GID namespace mappings for the image's ownership (`0:42` on `/etc/shadow`; `lchown: invalid argument`).
Podman exited 125 before a Swift service started.
Host mappings and image ownership were not altered to manufacture a passing run.
Use a properly configured disposable runner for the plan's mandatory two-account tests.

The actual local event stream was reconciled with the real producer exit 0 and exact `go list ./internal/...` package set:

```sh
go run ./internal/systemtest/cmd/scenariocov \
  -events <audit-scratch>/go-events.jsonl -test-exit 0 \
  -expected-packages <audit-scratch>/expected-packages.txt \
  -out <audit-scratch>/scenario-coverage.json
```

Result: `valid:true`, `coverageComplete:false`, `saioConfigured:false`, 0/102 cells, 47/47 actions with gaps.
The [complete report](evidence/local-scenario-coverage.json) and [47-action inventory](action-coverage.md) are preserved.
This is an honest incomplete baseline under ADR 0013; historical design counts and a valid event stream must not be turned into current system-safety evidence.

## Retrieved GitHub evidence

After repository access was supplied, authenticated read-only GitHub checks on 2026-09-05 confirmed master at `b16c9ffd3461a519489c24112a23503ac2725566`.
The compare API showed exactly one commit beyond the audited revision, changing only `.github/badges/coverage-go.svg`.
The audited product source is therefore the same as that master snapshot.

The [master CI run](https://github.com/csmart/aeronautes/actions/runs/33379497500), completed on 2026-08-31, reported success for Go, frontend, SAIO integration, headless app smoke and badge jobs.
The earlier run at the exact audited commit was cancelled when superseded by the badge commit.
The [job metadata](evidence/github-ci-evidence.json) is preserved; this is retrieved existing execution evidence, not a claim those jobs ran locally on the audit date.

The downloaded scenario artifact from the successful master run records Go 1.27.0, `valid:true`, `saioConfigured:true`, 8/102 cells, `coverageComplete:false`, and gaps for all 47 actions.
The [report](evidence/github-master-scenario.json) and [artifact provenance/checksum](evidence/github-artifact-provenance.json) are retained.
It corroborates the historical foundation count while the local Go 1.27.1 run remains 0/102 without SAIO.
The existing real-engine matrix and full native-platform coverage remain outside that successful CI contract.

GitHub reports master as protected, but the classic branch-protection endpoint returned 404, so exact required-rule enforcement was not established.
Default workflow permissions are read-only and workflows cannot approve pull requests.
Dependabot alerts are enabled and the complete open-alert response contained zero alerts.
Code-scanning alert retrieval returned 403; no code-scanning result is claimed.
The [repository/security responses](evidence/github-readonly.json) and [follow-up responses](evidence/github-followup.json) record these distinctions.
No credential values or signed artifact download URLs were retained in the audit.

## Targeted defect probes

The [preserved source and setup](probes/README.md) recreate 11 data-safety, 6 security/durability, 1 oracle and 12 UI tests.
At the audited revision, 29 fail at their intended safe-behaviour assertion and the PRAGMA observation passes.
Their nonzero exits are expected evidence and are recorded separately from the successful normal test suite.
Go probe servers and filesystem mutations stay in loopback/temporary test fixtures.
The security helper's original probe run used the race detector and reported no race-detector findings.
The portable combined preservation check was a separate run without `-race`.

After archiving the sources, the portable preparation script and both suites were rerun.
The [combined Go log](evidence/preserved-go.log) shows 17 intended assertion failures and 1 passing observation.
The [UI rerun log](evidence/preserved-ui.log) shows 12 intended assertion failures and no setup failures.
These demonstrate reproducibility of the recorded defects, not exhaustive coverage of all variants in the remediation acceptance criteria.

## Dependency and security evidence

The offline Go source scans reported no advisories for Linux amd64 internal packages, Darwin arm64 internal packages, and Windows amd64 `./...` including main/Wails under Go 1.27.1.
These are scoped source analyses against the recorded database snapshot, not blanket proof about native libraries or every runtime target.
The exact pinned rclone Linux amd64 binary scan reported 42 distinct symbol-level advisory IDs across nine modules plus the standard library.
The [inventory](evidence/rclone-advisories.json) preserves IDs, module/fix hints, scanner provenance and the checksum of the original full JSON trace.
Unused backends/server commands may explain many findings; enabled Swift/local reachability requires triage rather than treating 42 as an exploitable-vulnerability count.
See [SEC-11 and its primary advisory references](security-release.md#sec-11--the-engine-pin-has-a-substantial-untriaged-advisory-inventory).

The full npm advisory submission remains unperformed.
Automatic approval review rejected the network-enabled `npm audit` retry because it sends the dependency names/versions to npm without explicit user permission.
Permission was requested and was still pending when this audit was recorded.
Public maintainer advisory spot checks and the zero-open-alert GitHub Dependabot result are useful partial evidence, but do not replace a fresh complete npm advisory submission.

## What remains unverified

- Real two-account SAIO and representative SwiftStack semantics, including version archives, legacy/modern versioning and the full real-engine matrix.
- Native Linux GUI interaction, native macOS/Windows operation, all six packaged artifacts, screen readers, picker/keychain/portal integration, install/upgrade/rollback.
- Large-namespace memory/throughput/fairness measurements, long-duration token expiry/load/soak tests, broad fuzzing and a deployed-app penetration test.
- Actual child/parent kill and restart experiments across platforms, OS/power-loss experiments, and complete recovery/audit/local-filesystem snapshots.
- Full npm advisory graph, enabled-sidecar advisory reachability, all six sidecar binary scans, native system-library advisory inventory and legal licence compliance.

These limits constrain release readiness, not the validity of the narrowly reproduced defects.
The plan names concrete acceptance work for each gap.
