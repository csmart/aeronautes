# Download case-folded file and ancestor conflicts

## Scope and oracle

This is a bounded prerequisite under the existing portable namespace policy, [K-XFER-011](../REQUIREMENTS.md), [DESIGN §27.5 and §65.9 item 13](../DESIGN.md), with the admission boundaries preserved by [ADR 0034](../adr/0034-download-destination-admission.md).
A selected ordinary file `Foo` and a selected file `foo/child` require the same case-folded local identity to be both a file and a directory.
An ordinary file `Foo` and a zero-byte directory marker `foo/` have the same conflict.
The complete selection must be refused before service job/engine admission and before the direct executor's first item or effect, even if the current filesystem can represent the distinct raw spellings.

The scoped correction applies the existing comparison identity to planned files and every required ancestor at both boundaries.
Selected object names, local paths, diagnostic names and recorded identities keep their exact spelling.
No normalization, escaping, directory-only alias or reserved-name policy is added.

## Source finding and planned reproduction

The source inspection found that preflight lowercases complete names but stores and looks up file ancestors with raw case-sensitive keys.
Direct executor entry repeats the same division.
The inspected boundaries are [PreflightDownload](../../internal/transfer/local/local.go) and [Download](../../internal/transfer/local/download_execute.go) at the approved parent `dffe875e0cb5890d8c485085f787484ef4e0b597`.
For the pair above, the complete-name keys `foo` and `foo/child` differ, and the raw ancestor `foo` does not match the planned file `Foo`.
An existing-state filesystem check cannot discover a conflict between two still-absent planned entries.
This static finding preceded the maintained runtime reproduction recorded below.

Use dedicated owned selected, outside-sibling and unrelated local fixtures, both owned remote namespace scopes and full source payload bytes.
Add a safe first item `000-safe.bin` or marker `000-safe/` before the conflicting pair so per-item refusal or early materialization cannot satisfy the test.
Keep marker conflicts mixed with an ordinary blocking file; a pair of markers is not this file-versus-directory case.
Cover ordinary engine files, native files selected under the existing unsafe-prefix route and directory markers on mock Swift and real Swift.
Exercise immediate and deeper ancestors, nonadjacent names and direct input ordering in focused local tests.

## Required observations

Service cases call both PreflightDownload and StartDownload through an actual picker-issued token.
A refusal requires rejecting preflight output, an empty returned job id and refusal error, unchanged job/recovery/item/observation/transfer-audit counts, no selected content GETs, no local delta and unchanged owned remote namespaces and write trace.
Listings and marker-classification metadata reads remain permitted planning work.
When the baseline or a broken control unexpectedly returns a job id, wait for it and independently read public and durable job state, complete item pages and matching audit, preserving the failing safety assertion.
A truthful partial result or late collision is not successful preflight refusal.

Direct cases construct exact plans with real mock source payloads and the existing local engine harness, with and without recorders.
They require refusal before a non-nil report, admission, execution, verification or progress callbacks, source requests, Copy invocation and local effects.
The app's existing engine-construction spy separately proves service refusal before engine setup, with a safe control that reaches the configured sentinel.
The system stage callback runs after Copy returns; it remains a returned-Copy witness, not a direct child-start counter.
Remote snapshots cover the two owned namespace scopes, not entire accounts.

Passing controls preserve two distinct children under one exact parent, a compatible directory marker plus child and disjoint file/subtree paths.
For accepted controls, inspect complete file bytes and permitted local deltas independently of exact final job, item and audit facts.
Preserve the existing whole-leaf case-fold, exact-case ancestor and selected-root-symlink regressions.

## Verification and retained evidence

First run the maintained safety assertions against unchanged production and retain their actual failing exits alongside passing controls.
Record the exact source/test hashes, commands, full raw output and provider configuration without secrets.
After the bounded correction, require the same assertions to pass and narrowly broken preflight/direct ancestor guards to fail their relevant boundary checks.
Then run the relevant compatibility checks and complete inventory, scenario and coverage verification under the existing finite budgets.
Independent local review is required for every logical commit and after any authorised final-base rewrite, before protected PR delivery and all five resulting master checks.
This sequence was reviewed before implementation; the following sections record the observed results.

## Maintained reproduction and correction

The plan was committed as `232559869ee3ddc5d3bedb7e643ada7b78554de2` before implementation.
The before producers used its unchanged production, with only the dedicated local/service tests and the existing app construction spy extended.
Their 353 Go/SQL input hashes contain all 351 parent inputs; only the approved app test differs, and the two dedicated test files are new.
Every fixed and broken-overlay producer preserves the exact same three test files.
Only `local.go` and `download_execute.go` differ between the before and fixed production snapshots.

Preflight now stores the original file spelling under its existing lowercase key and looks up every ancestor using that key.
Its problem names and details identify the actual selected file, such as `scope/TOP/Foo`, without substituting the child's spelling.
Direct executor entry uses the same lowercase file and ancestor keys before destination inspection, recording or effects.
The function comment no longer claims that case-folded conflicts are merely warned on Linux.

The [local regressions](../../internal/transfer/local/download_path_case_test.go) cover immediate and deeper file parents, all-component case differences, nonadjacent names, directory markers, both routes, both input orders and recorded/unrecorded direct calls.
The [service regressions](../../internal/systemtest/download_path_case_test.go) run engine, native and mixed-marker selections through picker tokens on Mock and real Swift.
The [existing app spy](../../internal/app/download_root_admission_test.go) proves refusal before engine construction and preserves its construction-reaching safe control.
Every unsafe service selection starts with a harmless file or marker that must also remain untouched.

| Producer | Failing leaves | Passing leaves | Actual exit | Wall seconds |
| --- | ---: | ---: | ---: | ---: |
| Before local/app | 61 | 33 | 1 | 3.651 |
| Before Mock service | 6 | 9 | 1 | 10.728 |
| Before SAIO service | 6 | 9 | 1 | 16.759 |
| Fixed local/app | 0 | 94 | 0 | 3.670 |
| Fixed Mock service | 0 | 15 | 0 | 9.541 |
| Fixed SAIO service | 0 | 15 | 0 | 15.482 |
| Broken preflight comparison | 19 | 18 | 1 | 9.986 |
| Broken direct comparison | 48 | 24 | 1 | 1.721 |
| Broken complete-ancestor walk | 26 | 94 | 1 | 19.939 |

These are leaf counts derived from complete Go JSON terminal names, not counts inferred from table rows.
The fixed producers contain 97, 16 and 16 named passes respectively when their top-level parents are included.
No focused case skipped, and every recorded source hash remained unchanged during its producer.

All twelve unsafe baseline service cases actually accepted and completed a three-item job, with six item observations and two transfer audit entries.
They read the selected file content and created the conflicting local entries, including the safe first item.
The independently read local file sizes and full-byte SHA-256 digests match the captured remote source payloads.
The test kept its failing refusal assertion; completed transfers are evidence of the defect, not successful safety tests.
The two owned remote namespaces and unselected local sentinels were unchanged in both providers.

The same twelve fixed service cases return rejecting preflight problems and an empty Start job id.
They create no job, recovery, item, observation or transfer-audit rows, perform no selected content GET, invoke no returned-Copy callback and leave the complete owned local and remote snapshots unchanged.
Passing shared-parent, compatible marker/child and disjoint-path controls retain exact source/destination/staging identities, admitted item records, copied/verified observations, complete bytes and expected start/final audit.
The direct cases independently prove no admission, observation, progress, source request, Copy call, report or local effect before refusal.

The first two private Go overlays restore raw comparison keys at only one boundary each.
With only preflight broken, the service creates a failed job and two audit rows, but the still-correct direct guard prevents item rows, selected reads and local effects.
With only direct entry broken, constructed plans again write the conflicting entries, including both recorder modes and input orders.
The third overlay checks only each immediate parent at both boundaries; deeper file/marker conflicts fail on local, Mock and SAIO while the other controls continue passing.
The overlays and their exact replacements never edit the worktree.

The focused evidence reader reassembles Go output chunks before parsing the service's JSON log lines.
An initial reader incorrectly expected three planned item rows after the broken-preflight case; the retained raw result instead shows zero item rows because direct entry refuses before admission.
The corrected reader preserves that failed inspection, verifies the actual `[1, 0, 0, 0, 2]` job/recovery/item/observation/audit delta, and exits zero.

## Reproduction profile

Use Go 1.27.1, the pinned rclone 1.74.3 and the existing Mock/two-account SAIO fixture configuration described in [CI](../CI.md).
The maintained focused commands are:

```sh
go test -json -race -count=1 -parallel=3 -timeout=20m \
  -run 'TestDownloadPathCase|TestDownloadRootGuardBeforeEngineConstruction' \
  ./internal/transfer/local ./internal/app
go test -json -race -count=1 -parallel=3 -timeout=20m \
  -run '^TestDownloadPathCaseMock$' ./internal/systemtest
go test -json -race -count=1 -parallel=3 -timeout=20m \
  -run '^TestDownloadPathCaseRealSwift$' ./internal/systemtest
```

For a before replay, keep these maintained tests and use a Go overlay containing the two production files from `232559869ee3ddc5d3bedb7e643ada7b78554de2`.
Preserve the actual nonzero producer exit and all terminal names; do not invert the refusal assertions.
The private evidence bundle retains each exact command, tool/version and pinned-binary identity, raw output, actual exit, all Go/SQL snapshots, overlay replacements and validator results.
The focused source/test SHA-256 values are:

| File | SHA-256 |
| --- | --- |
| `internal/transfer/local/local.go` | `872769b173bc58823467d3f99976bf991a3acdb12952b5bf622521c693a476fb` |
| `internal/transfer/local/download_execute.go` | `0e16d1b1bc86af50793198837180c8d34b06f8dc666d3a44cce0ab85f41b4227` |
| `internal/transfer/local/download_path_case_test.go` | `de1ff59a55d7f8eec9790c24e623c3bf7844a11927c291e70e2e428e63cb1253` |
| `internal/systemtest/download_path_case_test.go` | `002f3a653d995806a778953ecfe9203d83fb10987b6125f9ee3e2771922ef387` |
| `internal/app/download_root_admission_test.go` | `0defcbac75a7bd5ee10bd30130c479cead782b64321e385dd10db091fccd411c` |

An initial broad `./...` invocation omitted the combined coverage option and failed desktop-root setup because this fresh worktree had no generated `frontend/dist` assets.
It completed with actual exit 1, 3,001 named passes, 22 passing packages and that one failed package in 679.292 seconds, with all Go/SQL inputs unchanged.
Its raw build failure remains retained, and it is not the maintained integration gate.
The integration result below uses the documented `./internal/...` package set and `-coverpkg=./internal/...` profile instead.

## Complete integration verification

The documented integration profile completed with actual exit 0: 2,976 named passes across the exact 20 packages returned by `go list ./internal/...`, no failures or skips, in 666.881 seconds.
All 353 Go/SQL inputs and the retained `go.mod`/`go.sum` snapshot match the final source.
The complete raw event-stream SHA-256 is `31223630057c4c265d8fa844024e51c3721fd30f65e3a9feda8519f6814bb180`.

The evidence validator reconciles every prior 2,850 terminal name from the approved destination source plus exactly 126 additions: 92 local names, 32 service names and two new app-spy cases.
It normalises only the existing generated one-hour absolute-expiry name, after checking the unchanged test source and both observed run timestamps against its Unix value.
No other missing or unexpected test name is discarded.
The complete stream resumes 38 top-level system groups, the previous 36 plus these two new service groups, under the unchanged `-parallel=3` budget.
Go releases a parallel slot before reporting its parent PASS, so open JSON event records are not an atomic active-work count.

Combined statement coverage is 87.3% (7,668 of 8,787 statements), independently recomputed from the atomic profile and checked against `go tool cover`.
The maintained reconciler consumed the actual producer exit and actual package list and returned schema 3, `valid: true`, SAIO configured, with 40 of 116 required cells and 38 actions still containing gaps.
The contract hash remains `6c1e4d947471b8b5`; this correction earns no new scenario credit or full-matrix clearance.
`go vet ./internal/...` also exited zero.
These results bind the recorded uncommitted source hashes; subsequent commits and final-base integration must preserve those inputs or receive appropriate new verification and independent review.

## Remaining limits

Portable namespace rejection and native filesystem acceptance are separate claims.
These fixtures need no symlink or concurrent actor and do not establish stable selected-root authority, race-safe placement or cleanup ownership.
Unicode equivalence, reserved names, forbidden characters, rclone encoding collisions and representative native target evidence remain wider P19 work.
The existing `strings.ToLower` key is not full Unicode case folding or exact APFS/NTFS comparison.
Directory-only aliases and a marker-only alias such as `Foo/` alongside `foo/child` remain outside this correction; no new refusal policy is asserted for them.
The runtime fixtures exercised Linux/amd64; this slice supplies no native macOS or Windows filesystem acceptance evidence.
P18, P19, P20, P30 and the valuable-data hold remain open.
